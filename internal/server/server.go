// Package server 是控制面的 HTTP 层:一半是给本机 CLI 用的管理接口,一半是
// 给客户端用的订阅接口。
//
// 控制面独占数据库;CLI 只通过这里的 /api 说话,不直接碰数据库文件。
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// NodeOps 是控制面能对一台节点做的事。
//
// 它必须是控制面进程里的东西:能 SSH 到节点的是控制面,不是操作者手边那台
// 笔记本。所以 CLI 只负责说"该下发了",真正动手的在这里。
type NodeOps interface {
	DeployNode(ctx context.Context, n model.Node, now time.Time) error
	// InstallNode 装好节点上的 xray 核心并启动它,返回实际装上的版本。返回非空
	// 版本表示核心已经推上去了 —— 此时出错是配置没让服务起来,不是没装成。
	InstallNode(ctx context.Context, n model.Node, version string) (string, error)
}

// Server 持有控制面运行期需要的东西。
type Server struct {
	store   *store.Store
	token   string
	logger  *slog.Logger
	mux     *http.ServeMux
	nodeOps NodeOps
}

// New 组装一台控制面。token 为空表示不校验管理接口的鉴权,只用于本机开发。
func New(st *store.Store, token string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{store: st, token: token, logger: logger, mux: http.NewServeMux()}
	s.routes()
	return s
}

// SetNodeOps 接上跟节点打交道的通道。不接时下发接口返回 501,而不是回一句
// "已下发"让人以为节点真的变了。
func (s *Server) SetNodeOps(o NodeOps) { s.nodeOps = o }

// Handler 返回可以直接交给 http.Server 的处理器。
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// 订阅:不挂 /api,和各家客户端约定俗成的路径一致。
	s.mux.HandleFunc("GET /sub/{token}", s.handleSubscribe)
	s.mux.HandleFunc("GET /sub/{token}/{format}", s.handleSubscribe)

	api := http.NewServeMux()
	api.HandleFunc("GET /api/nodes", s.handleListNodes)
	api.HandleFunc("POST /api/nodes", s.handleCreateNode)
	api.HandleFunc("GET /api/nodes/{id}", s.handleGetNode)
	api.HandleFunc("PUT /api/nodes/{id}", s.handleUpdateNode)
	api.HandleFunc("DELETE /api/nodes/{id}", s.handleDeleteNode)
	api.HandleFunc("GET /api/nodes/{id}/config", s.handlePreviewNodeConfig)
	api.HandleFunc("POST /api/nodes/{id}/sync", s.handleSyncNode)
	api.HandleFunc("POST /api/nodes/{id}/install", s.handleInstallNode)

	api.HandleFunc("GET /api/inbounds", s.handleListInbounds)
	api.HandleFunc("POST /api/inbounds", s.handleCreateInbound)
	api.HandleFunc("GET /api/inbounds/{id}", s.handleGetInbound)
	api.HandleFunc("PUT /api/inbounds/{id}", s.handleUpdateInbound)
	api.HandleFunc("DELETE /api/inbounds/{id}", s.handleDeleteInbound)
	api.HandleFunc("GET /api/inbounds/{id}/hosts", s.handleListHosts)
	api.HandleFunc("POST /api/inbounds/{id}/hosts", s.handleCreateHost)

	api.HandleFunc("PUT /api/hosts/{id}", s.handleUpdateHost)
	api.HandleFunc("DELETE /api/hosts/{id}", s.handleDeleteHost)

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

	s.mux.Handle("/api/", s.requireAuth(api))
}

// requireAuth 校验管理接口的 Bearer token。
//
// 只比对一个固定的管理 token,没有管理员表和权限矩阵:本工具的使用者就是
// 运维本人,区分角色在这里没有意义。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				writeError(w, http.StatusUnauthorized, "管理 token 不正确")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
