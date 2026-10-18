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
	"path/filepath"
	"strconv"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
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
	// InstallNode 装好节点上的 xray 核心并启动它,返回实际装上的版本。返回非空
	// 版本表示核心已经推上去了 —— 此时出错是配置没让服务起来,不是没装成。
	InstallNode(ctx context.Context, n model.Node, version string) (string, error)
	// OnlineIPs 现场读出节点上每个用户此刻的在线 IP 列表(0 个或多个)。
	// 「在线」是 xray 的 IP 粒度 —— 一个用户多个终端会贡献多个 IP。
	OnlineIPs(ctx context.Context, n model.Node) (map[string][]string, error)
}

// StatusSource 是控制面后台任务的自报状态,GET /api/status 靠它回答。
type StatusSource interface {
	// Snapshot 返回此刻的心跳与退避状态,不发任何 SSH。
	Snapshot(now time.Time) model.ControlStatus
}

// UpdateSource 是控制面查更新那条线的结论,GET /api/update 靠它回答。
//
// 单独一个接口而不是直接复用 StatusSource:两者只有版本信息这一小块重叠,
// 而 /api/update 想要的恰恰是「不查心跳、不碰节点」的那一份。
type UpdateSource interface {
	// ControlUpdate 返回控制面自己这一份版本结论,不发任何网络请求 —— 上游
	// 的检查由后台任务按自己的节奏做,接口只负责把手里的结论说出来。
	ControlUpdate() model.UpdatePart
}

// UpdateRefresher 是 UpdateSource 的可选扩展:显式要求控制面现在就去上游查一次,
// 不等后台任务的静默期。
//
// 单独一个接口而不是直接加进 UpdateSource:接口只新增不改,现有实现(测试里的
// 假更新源、别处接进来的实现)不该因为多了一个方法就编译不过。GET /api/update
// 在 handler 里做类型断言,接得上就支持 refresh=1,接不上就照旧只回缓存结论。
//
// 检查失败不能返回 error:结论里的 Error 字段会带上失败原因,其余字段是上一次
// 的结果。查询失败不该让整个接口失败。
type UpdateRefresher interface {
	RefreshControlUpdate(ctx context.Context) model.UpdatePart
}

// Server 持有控制面运行期需要的东西。
type Server struct {
	store *store.Store
	token string
	// tlsFingerprint 是控制面自签证书的公钥指纹;只有 Serve 起来的实例才有。
	tlsFingerprint string
	// deviceTouched 记每台设备上次写"最近使用"的时间,见 touchDevice。
	deviceTouched map[int64]time.Time
	connect       *connectGuard
	logger        *slog.Logger
	mux           *http.ServeMux
	nodeOps       NodeOps
	status        StatusSource
	updates       UpdateSource
	dbPath        string
	// updateDir 是控制面升级请求/状态文件的目录,默认是库旁边的 update/。
	// 为空表示没配库路径,控制面升级接口据此返回 503。
	updateDir string

	// sshKeyPath 是控制面那对 SSH 密钥的私钥路径,密码引导新节点时用它算公钥。
	// 由 serve.go 定在数据目录下(与 systemd StateDirectory 对齐);测试里没设的
	// 话 EnsureSSHKey 会回退到 ~/.ssh。
	sshKeyPath string

	// resolveXray 与 upgradeXray 是节点核心一键升级里 xray 那条路的两个实现点。
	// 默认分别走 CoreUpgrader 的 ResolveXray / UpgradeXray(控制面进程里的真实
	// 通道),测试替换成假实现,免得真去 ssh 节点或问 GitHub。
	resolveXray func(ctx context.Context, n model.Node, version string) (assetURL, sha256 string, err error)
	upgradeXray func(ctx context.Context, n model.Node, version, assetURL, sha256 string) (node.UpgradeResult, error)

	// nodeUpdate 是节点核心升级任务的内存状态。任务只跑在控制面进程里,重启即
	// 清空 —— 这是有意的:升级是操作者此刻发起的一件事,不是要落库的历史。
	nodeUpdate nodeUpdateState

	// autoLocate 为真时,新建节点后自动按 IP 问 ipwho.is 并写入显示名(见 autoLocateNode)。
	// New 默认关(测试不出网),Serve 按 ServeOptions.NoAutoLocate 打开。
	autoLocate bool

	// subCache 缓存订阅渲染结果:同一个 token+format 在两次管理动作之间是
	// 同一份内容,重算只是白跑一遍数据库加渲染。任何写库成功的管理接口都
	// 全清它(见 invalidateSubCache),所以不会把过期内容发出去。
	subCache subCache
	ruleSets ruleSetCache // 「测试一个网址」用的上游规则集缓存,见 routing_match.go

	// subPulls 是订阅拉取记录的写库节流状态(按 user id 记上次写入时间),
	// 见 recordSubPull。只在内存里,重启即清空。
	subPulls subPullState

	// subLimiter 与 apiLimiter 是两个独立的令牌桶:订阅公网可达且无鉴权,
	// 是扫描器与定时刷新的主战场;管理面有鉴权,但仍值得防暴力撞 token。
	// 速率与突发见 ratelimit.go 的常数,按真实使用拍,不做配置项。
	subLimiter *ipRateLimiter
	apiLimiter *ipRateLimiter
}

// New 组装一台控制面。token 为空表示不校验管理接口的鉴权,只用于本机开发。
//
// dbPath 是控制面用的库文件路径,备份/恢复接口靠它知道该碰哪个文件。传空串
// 时备份与恢复接口返回 500(正常从 Serve 进来说不清什么时候为空,但防御一下)。
func New(st *store.Store, token string, logger *slog.Logger, dbPath ...string) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		store:      st,
		token:      token,
		logger:     logger,
		mux:        http.NewServeMux(),
		subLimiter: newSubLimiter(),
		apiLimiter: newAPILimiter(),
		connect:    newConnectGuard(),
	}
	if len(dbPath) > 0 {
		s.dbPath = dbPath[0]
	}
	if s.dbPath != "" {
		s.updateDir = filepath.Join(filepath.Dir(s.dbPath), "update")
	}
	s.resolveXray = s.defaultResolveXray
	s.upgradeXray = s.defaultUpgradeXray
	s.routes()
	return s
}

// SetNodeOps 接上跟节点打交道的通道。不接时下发接口返回 501,而不是回一句
// "已下发"让人以为节点真的变了。
func (s *Server) SetNodeOps(o NodeOps) { s.nodeOps = o }

// SetStatusSource 接上后台任务的心跳。不接时 /api/status 仍会回答说不出心跳,
// 但对象数量照给 —— CLI 的 status 不该因为后台任务没起就整个失败。
func (s *Server) SetStatusSource(src StatusSource) { s.status = src }

// SetUpdateSource 接上控制面的更新检查结论,GET /api/update 靠它回答。
//
// 不接时那一行只有本机版本、说不了有没有新版 —— 这也是 /api/update 该有的
// 退化样子:接口本身照常回 200,不因为后台任务没起来就整个失败。
func (s *Server) SetUpdateSource(src UpdateSource) { s.updates = src }

// SetUpdateDir 换掉控制面升级请求/状态文件的目录,供测试指定可写位置。
// 传空串等于禁用控制面升级接口。
func (s *Server) SetUpdateDir(dir string) { s.updateDir = dir }

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
	// 订阅页的内置字体。挂在 /sub/ 下、用四段路径,是为了让只转发 /sub/ 的
	// 反向代理也能取到字体,而且永远不会撞上三段式的 /sub/{token}/{format}。
	sub.HandleFunc("GET /sub/-/fonts/{name}", s.handleSubFont)
	s.mux.Handle("/sub/", s.rateLimit(s.subLimiter, sub))

	api := http.NewServeMux()
	s.metadataRoutes(api)
	s.routingRoutes(api)
	s.planRoutes(api)
	s.diagnosticRoutes(api)
	api.HandleFunc("GET /api/status", s.handleStatus)
	api.HandleFunc("GET /api/update", s.handleUpdate)
	// 控制面远程升级:只往 update 目录落一个请求文件,真正的换二进制由 root 的
	// 一次性单元跑 `mpx update run control` 完成,见 selfupdate.ControlRunner。
	api.HandleFunc("POST /api/update/control", s.handleUpdateControl)
	// 节点核心一键升级:POST 起一个后台任务,GET 带 since 增量拉进度。
	// 任务状态只在内存里,不落库。
	api.HandleFunc("POST /api/update/nodes", s.handleUpdateNodesStart)
	api.HandleFunc("GET /api/update/nodes", s.handleUpdateNodesProgress)
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
	api.HandleFunc("POST /api/nodes/{id}/install", s.handleInstallNode)
	api.HandleFunc("GET /api/nodes/{id}/ports", s.handleNodePorts)
	api.HandleFunc("POST /api/nodes/{id}/drain", s.handleDrainNode)
	api.HandleFunc("POST /api/nodes/{id}/undrain", s.handleUndrainNode)
	api.HandleFunc("GET /api/nodes/{id}/online", s.handleNodeOnline)
	api.HandleFunc("GET /api/nodes/{id}/capabilities", s.handleGetCapabilities)
	api.HandleFunc("PUT /api/nodes/{id}/capabilities", s.handlePutCapabilities)
	api.HandleFunc("POST /api/nodes/{id}/capabilities/probe", s.handleProbeCapabilities)

	api.HandleFunc("GET /api/inbounds", s.handleListInbounds)
	api.HandleFunc("POST /api/inbounds", s.handleCreateInbound)
	api.HandleFunc("GET /api/inbounds/{id}", s.handleGetInbound)
	api.HandleFunc("PUT /api/inbounds/{id}", s.handleUpdateInbound)
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

	api.HandleFunc("GET /api/devices", s.handleListDevices)
	api.HandleFunc("DELETE /api/devices/{id}", s.handleDeleteDevice)
	api.HandleFunc("PUT /api/device-password", s.handleSetDevicePassword)
	api.HandleFunc("DELETE /api/device-password", s.handleDisableDevicePassword)

	// 限流在外、鉴权在内:反过来的话,拿错 token 的请求在进桶之前就被 401 打回,
	// 撞 token 等于不受限流。
	s.mux.Handle("/api/", s.rateLimit(s.apiLimiter, s.requireAuth(api)))
	// 设备接入不要 token(它就是来领 token 的),自带三道闸,见 connect.go。
	s.mux.HandleFunc("POST /api/connect", s.handleConnect)
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
