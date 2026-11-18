package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 订阅镜像(UC-10):在名单内一台节点上开一个公开端口,把到达的 TCP 转给
// 控制面。节点上不装任何 MPX 组件,用的是核心自带的 dokodemo-door /
// sing-box direct;订阅内容与鉴权仍以控制面为唯一真相。
//
// 加/删镜像是结构性变更:下发会重启节点核心,所以这两步都显式走
// DeployNode(同 handleSyncNode 的规矩),不走周期收敛。

// mirrorEndpoint 是一个对外的订阅入口:名字 + 地址前缀。
type mirrorEndpoint struct {
	Name     string
	Endpoint string
}

// mirrorEndpoints 列出当前所有镜像的对外入口,按节点名稳定排序。
// 订阅页与用户链接都靠它拼出"镜像 · 节点" 这一行。
func (s *Server) mirrorEndpoints() []mirrorEndpoint {
	mirrors, err := s.store.ListSubMirrors(0)
	if err != nil {
		return nil
	}
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil
	}
	addr := make(map[int64]string, len(nodes))
	name := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		addr[n.ID] = n.Address
		name[n.ID] = n.Name
	}
	out := make([]mirrorEndpoint, 0, len(mirrors))
	for _, m := range mirrors {
		if m.Remark != "" {
			out = append(out, mirrorEndpoint{Name: m.Remark, Endpoint: m.EndpointURL(addr[m.NodeID])})
			continue
		}
		out = append(out, mirrorEndpoint{Name: name[m.NodeID], Endpoint: m.EndpointURL(addr[m.NodeID])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// handleListMirrors 返回全部镜像,并带上每台的对外入口地址。
func (s *Server) handleListMirrors(w http.ResponseWriter, r *http.Request) {
	mirrors, err := s.store.ListSubMirrors(0)
	if err != nil {
		s.writeStoreError(w, err, "订阅镜像")
		return
	}
	nodes, err := s.store.ListNodes()
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	addr := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		addr[n.ID] = n.Address
	}
	type view struct {
		model.SubMirror
		Node    string `json:"node"`
		Address string `json:"address"`
		// Endpoint 是对外订阅前缀;把订阅 token 接上 /sub/<token> 就是入口。
		Endpoint string `json:"endpoint"`
	}
	out := make([]view, 0, len(mirrors))
	for _, m := range mirrors {
		out = append(out, view{
			SubMirror: m,
			Node:      nodeName(nodes, m.NodeID),
			Address:   addr[m.NodeID],
			Endpoint:  m.EndpointURL(addr[m.NodeID]),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func nodeName(nodes []model.Node, id int64) string {
	for _, n := range nodes {
		if n.ID == id {
			return n.Name
		}
	}
	return strconv.FormatInt(id, 10)
}

// mirrorCreateReq 是新建镜像的请求体。
type mirrorCreateReq struct {
	NodeID   int64  `json:"node_id"`
	Port     int    `json:"port"`
	Upstream string `json:"upstream"` // host:port;空则由控制面自己解析
	Remark   string `json:"remark"`
	// Sync 为真时建完立即下发到节点(默认)。CLI 加/删默认下发,传 false 只登记。
	Sync *bool `json:"sync,omitempty"`
}

// handleCreateMirror 建镜像并默认下发。端口与同节点入站、API 端口冲突时拒绝。
func (s *Server) handleCreateMirror(w http.ResponseWriter, r *http.Request) {
	var req mirrorCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "端口必须在 1-65535 之间")
		return
	}
	n, err := s.store.GetNode(req.NodeID)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}

	upstream, warn := s.resolveUpstream(req.Upstream, r)
	m := model.SubMirror{NodeID: n.ID, Port: req.Port, Upstream: upstream, Remark: req.Remark}
	if msg := s.mirrorConflict(&m, n); msg != "" {
		writeError(w, http.StatusConflict, msg)
		return
	}
	if err := s.store.CreateSubMirror(&m); err != nil {
		s.writeStoreError(w, err, "订阅镜像")
		return
	}

	synced := false
	if req.Sync == nil || *req.Sync {
		if s.nodeOps == nil {
			writeError(w, http.StatusNotImplemented, "控制面没有接上下发通道")
			return
		}
		// 不用 r.Context():客户端断连不该把已经发出的下发半途杀掉(同 handleSyncNode)。
		if err := s.nodeOps.DeployNode(context.Background(), *n, time.Now()); err != nil {
			s.logger.Error("镜像下发失败", "node", n.Name, "port", m.Port, "error", err)
			writeError(w, http.StatusBadGateway, "镜像已登记但下发失败: "+err.Error())
			return
		}
		synced = true
	}
	resp := map[string]any{"id": m.ID, "node": n.Name, "port": m.Port, "upstream": m.Upstream,
		"endpoint": m.EndpointURL(n.Address), "synced": synced}
	if warn != "" {
		resp["warn"] = warn
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUpdateMirror 改镜像的上游或备注。控制面迁移后用这把旧上游指到新地址;
// 改完默认下发(上游只在节点配置里,不重启不生效)。
func (s *Server) handleUpdateMirror(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Upstream *string `json:"upstream"`
		Remark   *string `json:"remark"`
		Sync     *bool   `json:"sync"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	m, err := s.store.GetSubMirror(id)
	if err != nil {
		s.writeStoreError(w, err, "订阅镜像")
		return
	}
	if req.Upstream != nil {
		if _, _, err := net.SplitHostPort(*req.Upstream); err != nil {
			writeError(w, http.StatusBadRequest, "upstream 必须是 host:port")
			return
		}
		if err := s.store.UpdateSubMirrorUpstream(id, *req.Upstream); err != nil {
			s.writeStoreError(w, err, "订阅镜像")
			return
		}
		m.Upstream = *req.Upstream
	}
	if req.Remark != nil {
		if err := s.store.UpdateSubMirrorRemark(id, *req.Remark); err != nil {
			s.writeStoreError(w, err, "订阅镜像")
			return
		}
		m.Remark = *req.Remark
	}
	synced := false
	if req.Sync == nil || *req.Sync {
		n, err := s.store.GetNode(m.NodeID)
		if err != nil {
			s.writeStoreError(w, err, "节点")
			return
		}
		if s.nodeOps == nil {
			writeError(w, http.StatusNotImplemented, "控制面没有接上下发通道")
			return
		}
		// 不用 r.Context():客户端断连不该把已经发出的下发半途杀掉(同 handleSyncNode)。
		if err := s.nodeOps.DeployNode(context.Background(), *n, time.Now()); err != nil {
			s.logger.Error("镜像更新下发失败", "node", n.Name, "error", err)
			writeError(w, http.StatusBadGateway, "镜像已更新但下发失败: "+err.Error())
			return
		}
		synced = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "upstream": m.Upstream, "remark": m.Remark, "synced": synced})
}

// handleDeleteMirror 删镜像并默认下发到节点。
func (s *Server) handleDeleteMirror(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetSubMirror(id)
	if err != nil {
		s.writeStoreError(w, err, "订阅镜像")
		return
	}
	n, nerr := s.store.GetNode(m.NodeID)
	if err := s.store.DeleteSubMirror(id); err != nil {
		s.writeStoreError(w, err, "订阅镜像")
		return
	}
	synced := false
	if nerr == nil && s.nodeOps != nil && r.URL.Query().Get("sync") != "0" {
		// 不用 r.Context():客户端断连不该把已经发出的下发半途杀掉(同 handleSyncNode)。
		if err := s.nodeOps.DeployNode(context.Background(), *n, time.Now()); err != nil {
			s.logger.Error("移除镜像下发失败", "node", n.Name, "error", err)
			writeError(w, http.StatusBadGateway, "镜像已删除但下发失败: "+err.Error())
			return
		}
		synced = true
	}
	name := ""
	if nerr == nil {
		name = n.Name
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "node": name, "removed": true, "synced": synced})
}

// mirrorConflict 检查镜像端口是否与该节点已有入站、API 端口或既有镜像冲突。
func (s *Server) mirrorConflict(m *model.SubMirror, n *model.Node) string {
	if m.Port == n.APIPortOrDefault() {
		return "端口与节点 API 端口冲突"
	}
	if inbounds, err := s.store.ListInbounds(n.ID); err == nil {
		for _, in := range inbounds {
			if in.Port == m.Port {
				return fmt.Sprintf("端口与入站 %s/%d 冲突", in.Protocol, in.Port)
			}
		}
	}
	if others, err := s.store.ListSubMirrors(n.ID); err == nil {
		for _, o := range others {
			if o.ID != m.ID && o.Port == m.Port {
				return "端口与既有订阅镜像冲突"
			}
		}
	}
	return ""
}

// resolveUpstream 解析镜像的上游(host:port)。显式给的优先,否则按顺序退到
// 订阅前缀、本次请求的 Host;推不出一个对外地址就回警告,让调用方决定要不要
// 继续(登记了指错地方的镜像只会让入口打不开,不会伤到节点本身)。
func (s *Server) resolveUpstream(given string, r *http.Request) (string, string) {
	if given != "" {
		if _, _, err := net.SplitHostPort(given); err != nil {
			if net.ParseIP(given) != nil {
				return net.JoinHostPort(given, "8080"), ""
			}
			return given, "上游不是 host:port 形状,下发前请确认"
		}
		return given, ""
	}
	// host 来源:本次请求的 Host 优先 —— 那是操作者正在调用的这台控制面,
	// 一定是活的;订阅前缀(SubURLPrefix)只是"客户端看到的地址",迁移后
	// 可能是台死机,转发指过去镜像就是空的。端口来源:控制面开了订阅专用口
	// (--sub-addr)时优先用它的端口 —— 镜像端口上只会有 /sub,/api 不跟着
	// 出去;没开则沿用 host 来源自带的端口(主监听,8080)。
	host := ""
	fallback := ""
	if r != nil && r.Host != "" {
		h, p, err := net.SplitHostPort(r.Host)
		if err != nil {
			h = r.Host
		}
		if !isLoopbackHost(h) {
			host = h
			fallback = p
		}
	}
	if host == "" {
		if settings, err := s.store.GetSettings(); err == nil && settings.SubURLPrefix != "" {
			host = hostOf(settings.SubURLPrefix)
			fallback = portOf(settings.SubURLPrefix, "8080")
		}
	}
	if host == "" {
		return "", "没给出 upstream,也推不出控制面对外地址;镜像会指到空上游,请补一个 host:port"
	}
	if _, port, err := net.SplitHostPort(s.subAddr); err == nil && port != "" {
		return net.JoinHostPort(host, port), ""
	}
	if fallback == "" {
		fallback = "8080"
	}
	return net.JoinHostPort(host, fallback), ""
}

// hostOf / portOf 把订阅前缀(如 https://177.5.75.195:8080)拆出 host 与端口。
func hostOf(prefix string) string {
	h := trimScheme(prefix)
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.TrimSuffix(strings.Trim(h, "[]"), "/")
}

func portOf(prefix, def string) string {
	if _, port, err := net.SplitHostPort(trimScheme(prefix)); err == nil {
		return port
	}
	return def
}

func trimScheme(s string) string {
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(s, p) {
			return strings.TrimSuffix(s[len(p):], "/")
		}
	}
	return strings.TrimSuffix(s, "/")
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "::1" || host == "127.0.0.1" || host == "[::1]"
}
