// Package server 是控制面的 HTTP 层:一半是给本机 CLI 用的管理接口,一半是
// 给客户端用的订阅接口。
//
// 控制面独占数据库;CLI 只通过这里的 /api 说话,不直接碰数据库文件。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/ruleset"
	"github.com/RovenEverward/my-proxy/internal/source"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// NodeOps 是控制面能对一台节点做的事。
//
// 它必须是控制面进程里的东西:能 SSH 到节点的是控制面,不是操作者手边那台
// 笔记本。所以 CLI 只负责说"该下发了",真正动手的在这里。
type NodeOps interface {
	DeployNode(ctx context.Context, n model.Node, now time.Time) error
	// CheckNode 现场问一台节点它现在怎么样。注意它"节点连不上"时返回的是一份
	// Reachable=false 的状态、error 为 nil —— 调用方判断节点死活要看状态里的
	// Reachable,而不是看 error 非不非空。
	CheckNode(ctx context.Context, n model.Node, now time.Time) (model.NodeStatus, error)
	// OnlineIPs 现场读出节点上每个用户此刻的在线 IP 列表(0 个或多个)。
	OnlineIPs(ctx context.Context, n model.Node) (map[string][]string, error)
}

// StatusSource 是控制面后台任务的自报状态,GET /api/status 靠它回答。
type StatusSource interface {
	// Snapshot 返回此刻的心跳与退避状态,不发任何 SSH。
	Snapshot(now time.Time) model.ControlStatus
}

// BackoffOps 是后台任务里退避状态的两个操作口:人工重试与确认可达后清退避。
type BackoffOps interface {
	// RetryNodeBackoff 不等退避窗口到点,立刻对一台节点跑一轮采样与收敛;
	// 试通后顺带刷新节点状态与告警。返回的错误就是要原样给操作者看的原因。
	RetryNodeBackoff(ctx context.Context, n model.Node) error
	// ClearNodeBackoff 在「确认这台节点能通」的地方清掉它的退避,不发 SSH。
	ClearNodeBackoff(nodeID int64)
}

// Server 持有控制面运行期需要的东西。
type Server struct {
	store    *store.Store
	sources  *source.Manager
	rulesets *ruleset.Manager
	token    string
	// tlsFingerprint 是控制面自签证书的公钥指纹;只有 Serve 起来的实例才有。
	tlsFingerprint string
	logger         *slog.Logger
	mux            *http.ServeMux
	nodeOps        NodeOps
	status         StatusSource
	backoff        BackoffOps
	dbPath         string

	// sshKeyPath 是控制面那对 SSH 密钥的私钥路径,密码引导新节点时用它算公钥。
	sshKeyPath string

	// subCache 缓存订阅渲染结果:同一个 token+format 在两次管理动作之间是
	// 同一份内容,重算只是白跑一遍数据库加渲染。任何写库成功的管理接口都
	// 全清它(见 invalidateSubCache),所以不会把过期内容发出去。
	subCache subCache

	// templateHashCache 缓存「输入指纹 → 渲染出的节点模板哈希」(ISS-04):
	// 指纹由节点+入站+镜像算出,输入变指纹变,不需要主动失效。
	templateHashMu    sync.Mutex
	templateHashCache map[string]string
	ruleSets          ruleSetCache // 「测试一个网址」用的上游规则集缓存,见 routing_match.go

	// subPulls 是订阅拉取记录的写库节流状态(按 user id 记上次写入时间),
	// 见 recordSubPull。只在内存里,重启即清空。
	subPulls subPullState

	// subLimiter、rulesetLimiter 与 apiLimiter 是三个独立的令牌桶:订阅公网
	// 可达且无鉴权,是扫描器与定时刷新的主战场;规则集镜像文件的突发形态
	// 与订阅本体差一个量级,单占一桶;管理面有鉴权,但仍值得防暴力撞 token。
	// 速率与突发见 ratelimit.go 的常数,按真实使用拍,不做配置项。
	subLimiter     *ipRateLimiter
	rulesetLimiter *ipRateLimiter
	apiLimiter     *ipRateLimiter
	// subHandler 是只含 /sub 路由的处理器,给订阅专用监听(--sub-addr)用:
	// 那个口上永远没有 /api,管理面不会随订阅端口外泄。
	subHandler http.Handler
	// subAddr 是订阅专用监听的地址(--sub-addr / MPX_SUB_ADDR),镜像默认
	// 上游推导靠它决定转发端口;空串表示没开。
	subAddr string
}

// SetSubAddr 告诉 server 订阅专用口开在哪;由 Serve 起监听前调一次。
func (s *Server) SetSubAddr(addr string) { s.subAddr = addr }

// New 组装一台控制面。token 为空表示不校验管理接口的鉴权,只用于本机开发。
//
// dbPath 是控制面用的库文件路径,备份/恢复接口靠它知道该碰哪个文件。传空串
// 时备份与恢复接口返回 500(正常从 Serve 进来说不清什么时候为空,但防御一下)。
func New(st *store.Store, token string, logger *slog.Logger, dbPath ...string) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		store:          st,
		token:          token,
		logger:         logger,
		mux:            http.NewServeMux(),
		subLimiter:     newSubLimiter(),
		rulesetLimiter: newRulesetLimiter(),
		apiLimiter:     newAPILimiter(),
	}
	if len(dbPath) > 0 {
		s.dbPath = dbPath[0]
	}
	s.sources = source.NewManager(st, s.invalidateSubCache)
	s.routes()
	return s
}

// SetNodeOps 接上跟节点打交道的通道。不接时下发接口返回 501,而不是回一句
// "已下发"让人以为节点真的变了。
func (s *Server) SetNodeOps(o NodeOps) { s.nodeOps = o }

// SetStatusSource 接上后台任务的心跳。不接时 /api/status 仍会回答说不出心跳,
// 但对象数量照给 —— CLI 的 status 不该因为后台任务没起就整个失败。
func (s *Server) SetStatusSource(src StatusSource) { s.status = src }

// SetBackoffOps 接上退避的人工操作口。不接时 POST /api/nodes/{id}/backoff/retry
// 返回 501,和没接下发通道时 sync 的做法一致。
func (s *Server) SetBackoffOps(ops BackoffOps) { s.backoff = ops }

// SetRulesets 接上规则集镜像拉取器(UC-05)。不接时 /sub/{token}/ruleset/* 一律
// 404,订阅渲染保持上游直连 —— 见 renderSubscription 的 RulesetURL 注入条件。
func (s *Server) SetRulesets(m *ruleset.Manager) { s.rulesets = m }

// listEnabledRules 给规则集拉取器的引用集合回填:全部作用域里启用中的规则。
func (s *Server) listEnabledRules() ([]model.RouteRule, error) {
	return s.store.ListEnabledRules()
}

// Handler 返回可以直接交给 http.Server 的处理器。
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// 订阅:不挂 /api,和各家客户端约定俗成的路径一致。三条都裹上订阅限流桶:
	// /sub 公网可达、无鉴权、是扫描器与定时刷新的主战场,速率见 ratelimit.go。
	sub := http.NewServeMux()
	sub.HandleFunc("GET /sub/{token}", s.handleSubscribe)
	sub.HandleFunc("GET /sub/{token}/{format}", s.handleSubscribe)
	// 订阅页上的"换个订阅地址":鉴权就是这个 token 本身(见 handleRotateSub)。
	sub.HandleFunc("POST /sub/{token}/rotate", s.handleRotateSub)
	// UC-05:镜像出来的规则集文件。四段路径不会撞上 /sub/{token}/{format}
	// ({format} 是单段);fmt/name 的白名单在处理器里校验。
	sub.HandleFunc("GET /sub/{token}/ruleset/{fmt}/{name}", s.handleRulesetFile)
	s.mux.Handle("/sub/", s.subRateLimit(sub))
	s.subHandler = sub

	api := http.NewServeMux()
	s.metadataRoutes(api)
	s.routingRoutes(api)
	s.rulesetRoutes(api)
	s.sourceRoutes(api)
	api.HandleFunc("GET /api/mirrors", s.handleListMirrors)
	api.HandleFunc("POST /api/mirrors", s.handleCreateMirror)
	api.HandleFunc("PUT /api/mirrors/{id}", s.handleUpdateMirror)
	api.HandleFunc("DELETE /api/mirrors/{id}", s.handleDeleteMirror)
	api.HandleFunc("GET /api/status", s.handleStatus)
	api.HandleFunc("GET /api/nodes", s.handleListNodes)
	api.HandleFunc("POST /api/nodes", s.handleCreateNode)
	// GET /api/nodes/{id} 认一个 ?fresh=1:不带就读库里缓存的状态,带了就现场
	// ssh 查一次、落库,再连同节点一起回答。
	//
	// 让"读"和"绕开缓存地读"共用一条路径,是因为它们回答的是同一个问题(这台
	// 节点现在怎么样),差别只在新鲜程度 —— 那本来就是查询参数该管的事。缓存
	// 状态因此只有这一个出口,不会再出现"哪一份是刚查的"要人去猜。
	api.HandleFunc("GET /api/nodes/pending", s.handleListNodePending)
	api.HandleFunc("GET /api/nodes/{id}", s.handleGetNode)
	api.HandleFunc("GET /api/nodes/{id}/cores/{core}", s.handleGetCoreDeployment)
	api.HandleFunc("PUT /api/nodes/{id}/cores/{core}", s.handleSetCoreDeployment)

	api.HandleFunc("GET /api/doctor", s.handleDoctor)
	api.HandleFunc("PUT /api/nodes/{id}", s.handleUpdateNode)
	api.HandleFunc("DELETE /api/nodes/{id}", s.handleDeleteNode)
	api.HandleFunc("GET /api/nodes/{id}/config", s.handlePreviewNodeConfig)
	api.HandleFunc("POST /api/nodes/{id}/sync", s.handleSyncNode)
	api.HandleFunc("GET /api/nodes/{id}/ports", s.handleNodePorts)
	api.HandleFunc("POST /api/nodes/{id}/drain", s.handleDrainNode)
	api.HandleFunc("POST /api/nodes/{id}/undrain", s.handleUndrainNode)
	// 人工重试退避中的节点:清掉跳过窗口,当场跑一轮采样与收敛。
	api.HandleFunc("POST /api/nodes/{id}/backoff/retry", s.handleRetryNodeBackoff)
	api.HandleFunc("GET /api/nodes/{id}/online", s.handleNodeOnline)

	api.HandleFunc("GET /api/inbounds", s.handleListInbounds)
	api.HandleFunc("POST /api/inbounds", s.handleCreateInbound)
	api.HandleFunc("GET /api/inbounds/{id}", s.handleGetInbound)
	api.HandleFunc("PUT /api/inbounds/{id}", s.handleUpdateInbound)
	api.HandleFunc("PUT /api/inbounds/{id}/cert", s.handleUpdateInboundCert)
	api.HandleFunc("DELETE /api/inbounds/{id}", s.handleDeleteInbound)
	api.HandleFunc("GET /api/inbounds/{id}/hosts", s.handleListHosts)
	api.HandleFunc("POST /api/inbounds/{id}/hosts", s.handleCreateHost)

	api.HandleFunc("PUT /api/hosts/{id}", s.handleUpdateHost)
	api.HandleFunc("DELETE /api/hosts/{id}", s.handleDeleteHost)

	api.HandleFunc("GET /api/sub-pulls", s.handleListSubPulls)
	api.HandleFunc("GET /api/users", s.handleListUsers)
	api.HandleFunc("POST /api/users", s.handleCreateUser)
	api.HandleFunc("GET /api/users/{username}", s.handleGetUser)
	api.HandleFunc("PUT /api/users/{username}", s.handleUpdateUser)
	api.HandleFunc("DELETE /api/users/{username}", s.handleDeleteUser)
	api.HandleFunc("POST /api/users/{username}/reset-traffic", s.handleResetUserTraffic)
	api.HandleFunc("POST /api/users/{username}/revoke-sub", s.handleRevokeSub)
	api.HandleFunc("GET /api/users/{username}/links", s.handleUserLinks)
	api.HandleFunc("GET /api/users/{username}/activity", s.handleUserActivity)

	api.HandleFunc("GET /api/settings", s.handleGetSettings)
	api.HandleFunc("PUT /api/settings", s.handleSaveSettings)
	// 内置 UA 识别规则(带展示名),给界面的「恢复内置」用。只读。
	api.HandleFunc("GET /api/settings/builtin-rules", s.handleBuiltinRules)
	api.HandleFunc("GET /api/settings/templates/{format}", s.handleGetTemplate)
	api.HandleFunc("PUT /api/settings/templates/{format}", s.handlePutTemplate)
	api.HandleFunc("DELETE /api/settings/templates/{format}", s.handleDeleteTemplate)
	api.HandleFunc("POST /api/settings/test-webhook", s.handleTestWebhook)

	// 备份与恢复:GET 流式返回 tar.gz,POST 校验并暂存归档(重启后生效)。
	api.HandleFunc("GET /api/backup", s.handleBackup)
	api.HandleFunc("POST /api/restore", s.handleRestore)

	// 限流在外、鉴权在内:反过来的话,拿错 token 的请求在进桶之前就被 401 打回,
	// 撞 token 等于不受限流。
	s.mux.Handle("/api/", s.rateLimit(s.apiLimiter, s.requireAuth(apiCompression(api))))
}

// ---- 请求/响应工具 ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeErrorHint 在 writeError 的回答里多带一个 ui_hint:同一件事的界面版说法,
// 界面拿它替换 error 里的命令行提示;uiHint 为空时回答与 writeError 完全一样。
func writeErrorHint(w http.ResponseWriter, code int, msg, uiHint string) {
	if uiHint == "" {
		writeError(w, code, msg)
		return
	}
	writeJSON(w, code, map[string]string{"error": msg, "ui_hint": uiHint})
}

// writeErrorField 在 writeError 的回答里多带一个 field:出错的是哪个字段(JSON
// 里的名字),界面据此把红字放到那一栏下面。error 本身照旧,旧读者不受影响。
func writeErrorField(w http.ResponseWriter, code int, msg, field string) {
	writeJSON(w, code, map[string]string{"error": msg, "field": field})
}

// writeStoreError 把存储层的错误翻成合适的状态码。
// 找不到是 404,其余是 500 并把真实原因记进日志、只给调用方一句概括。
func (s *Server) writeStoreError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, what+"不存在")
		return
	}
	s.logger.Error("存储操作失败", "what", what, "error", err)
	writeError(w, http.StatusInternalServerError, what+"操作失败")
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Server) pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "路径里的 id 不是整数")
		return 0, false
	}
	return id, true
}
