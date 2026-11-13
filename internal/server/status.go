package server

import (
	"net/http"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// ---- 状态 ----

// handleStatus 回答控制面此刻怎么样:后台任务的心跳、各类对象的数量、正在退避
// 的节点。
//
// 全部来自内存与本地库,不发 SSH —— 它要在节点不通的时候也能立刻回答,「节点
// 怎么样了」是 3b 里 node status 的事。后台任务没接上时心跳给零值,数量照给。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var out model.ControlStatus
	if s.status != nil {
		out = s.status.Snapshot(time.Now())
	} else {
		out.Backoffs = []model.NodeBackoff{}
	}

	nodes, err := s.store.ListNodes()
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	inbounds, err := s.store.ListInbounds(0)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	hosts, err := s.store.ListHosts(0)
	if err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	users, err := s.store.ListUsers()
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	out.Counts = model.Counts{
		Nodes:    len(nodes),
		Inbounds: len(inbounds),
		Hosts:    len(hosts),
		Users:    len(users),
	}
	out.TLSFingerprint = s.tlsFingerprint
	if ds, err := s.store.ListDevices(); err == nil {
		out.Devices = len(ds)
	}
	if h, err := s.store.DevicePasswordHash(); err == nil {
		out.DevicePasswordSet = h != ""
	}
	out.ConnectLockedUntil = s.connect.LockedUntil(time.Now())
	writeJSON(w, http.StatusOK, out)
}
