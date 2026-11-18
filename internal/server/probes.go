package server

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// probeRoutes 挂探测点管理的接口(UC-12,只增):
//
//	POST   /api/probes             登记探测点:返回一次性接入 token 与接入命令。
//	GET    /api/probes             探测点列表(名字、最近上线、在线状态)。
//	PATCH  /api/probes/{id}        改名。
//	DELETE /api/probes/{id}        删除;它留下的来源判定一并清除。
//	POST   /api/probes/{id}/rotate 吊销现有凭据、重发一次性接入 token。
//
// 接入本身走 POST /api/probes/hello:不挂在鉴权链里,join token 即凭据,
// 与 POST /api/connect 同一模式。
func (s *Server) probeRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/probes", s.handleCreateProbe)
	api.HandleFunc("GET /api/probes", s.handleListProbes)
	api.HandleFunc("PATCH /api/probes/{id}", s.handleRenameProbe)
	api.HandleFunc("DELETE /api/probes/{id}", s.handleDeleteProbe)
	api.HandleFunc("POST /api/probes/{id}/rotate", s.handleRotateProbe)
}

// probeOnlineWindow 是探测点算「在线」的口径:最近三小时有上报。
const probeOnlineWindow = 3 * time.Hour

type probeCreateRequest struct {
	Name string `json:"name"`
}

// probeJoinCommand 组装一行接入命令给操作者抄到探测点上跑。
func probeJoinCommand(base, token string) string {
	return "mpx probe run --server " + base + " --join " + token
}

func (s *Server) handleCreateProbe(w http.ResponseWriter, r *http.Request) {
	var req probeCreateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 64 {
		writeErrorField(w, http.StatusBadRequest, "探测点名称须为 1–64 个字", "name")
		return
	}
	join, err := newDeviceToken()
	if err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	p, err := s.store.CreateProbePoint(req.Name, hashToken(join))
	if err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	p.JoinToken = join
	writeJSON(w, http.StatusCreated, struct {
		*model.ProbePoint
		JoinCommand string `json:"join_command"`
	}{p, probeJoinCommand(controlPlaneBase(r), join)})
	s.logger.Info("探测点已登记", "probe", p.ID, "name", p.Name)
}

// controlPlaneBase 用请求的 Host 与协议推一个可贴的接入地址;有订阅前缀时
// 前缀更准(经过反代的部署 Host 头可能是内网名)。
func controlPlaneBase(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

func (s *Server) handleListProbes(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.ListProbePoints()
	if err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	if ps == nil {
		ps = []*model.ProbePoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"probes": ps})
}

func (s *Server) handleRenameProbe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var req probeCreateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 64 {
		writeErrorField(w, http.StatusBadRequest, "探测点名称须为 1–64 个字", "name")
		return
	}
	if err := s.store.RenameProbePoint(id, req.Name); err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteProbe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteProbePoint(id); err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	s.logger.Info("探测点已删除", "probe", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleRotateProbe 吊销现有凭据、重发一次性接入 token:探测点丢了凭据或被
// 怀疑泄漏时用,原凭据即刻失效。
func (s *Server) handleRotateProbe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	join, err := newDeviceToken()
	if err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	if err := s.store.RotateProbeToken(id, hashToken(join)); err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"join_token":   join,
		"join_command": probeJoinCommand(controlPlaneBase(r), join),
	})
	s.logger.Info("探测点凭据已换发", "probe", id)
}

// handleProbeHello 是探测点的唯一接入点(UC-12):一次性 join token 换正式
// token。它在 /api/ 之下但不走 requireAuth —— join token 本身就是凭据,
// 和 POST /api/connect 同模式。
func (s *Server) handleProbeHello(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JoinToken string `json:"join_token"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := s.store.ProbeByJoinToken(hashToken(req.JoinToken))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "接入 token 不正确或已用过;在控制面上重发接入命令")
		return
	}
	token, err := newDeviceToken()
	if err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if err := s.store.ActivateProbe(p.ID, hashToken(token), ip); err != nil {
		s.writeStoreError(w, err, "探测点")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"probe_id": p.ID,
		"name":     p.Name,
		"token":    token,
	})
	s.logger.Info("探测点已接入", "probe", p.ID, "name", p.Name, "remote", ip)
}
