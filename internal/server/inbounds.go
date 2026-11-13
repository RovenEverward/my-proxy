package server

import (
	"log/slog"
	"net/http"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// ---- 入站 ----

func (s *Server) handleListInbounds(w http.ResponseWriter, r *http.Request) {
	var nodeID int64
	if v := r.URL.Query().Get("node_id"); v != "" {
		id, ok := parseInt(w, v)
		if !ok {
			return
		}
		nodeID = id
	}
	inbounds, err := s.store.ListInbounds(nodeID)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	// 显示用的名字算不出来时不影响列表本身:这两个字段是附加的,缺了就不带。
	names, err := s.subscriptionNames()
	if err != nil {
		slog.Warn("算订阅里的名字失败", "error", err)
	}
	views := make([]model.InboundView, 0, len(inbounds))
	for _, in := range inbounds {
		n := names[subNameKey{in.ID, 0}]
		views = append(views, model.InboundView{Inbound: in, SubName: n.name, AutoName: n.auto})
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateInbound(w http.ResponseWriter, r *http.Request) {
	var in model.Inbound
	if !decodeBody(w, r, &in) {
		return
	}
	if err := ensureReality(&in, nil); err != nil {
		s.writeStoreError(w, err, "REALITY 密钥")
		return
	}
	if err := ensureWireguard(&in, nil); err != nil {
		s.writeStoreError(w, err, "wireguard 密钥")
		return
	}
	if msg := validateInbound(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// 默认 tag 落库,而不是每次用它的时候现算:节点上的增删用户、日志、渲染都按
	// tag 认入站,让它们各自去推同一个默认值迟早会推出分歧。
	in.Tag = in.TagOrDefault()
	if _, err := s.store.GetNode(in.NodeID); err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if err := s.store.CreateInbound(&in); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	s.markNodePending(in.NodeID)
	s.invalidateSubCache()
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) handleGetInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	in, err := s.store.GetInbound(id)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleUpdateInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var in model.Inbound
	if !decodeBody(w, r, &in) {
		return
	}
	in.ID = id
	old, err := s.store.GetInbound(id)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	in.NodeID = old.NodeID
	if err := ensureReality(&in, old); err != nil {
		s.writeStoreError(w, err, "REALITY 密钥")
		return
	}
	if err := ensureWireguard(&in, old); err != nil {
		s.writeStoreError(w, err, "wireguard 密钥")
		return
	}
	if msg := validateInbound(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// 没给 tag 就沿用原来的:换了 tag 等于在节点上换了一个入站,老的那个不会被删。
	if in.Tag == "" {
		in.Tag = old.TagOrDefault()
	}
	if err := s.store.UpdateInbound(&in); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if inboundNeedsApply(*old, in) {
		s.markNodePending(in.NodeID)
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleDeleteInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	gone, getErr := s.store.GetInbound(id)
	if err := s.store.DeleteInbound(id); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if getErr == nil {
		s.markNodePending(gone.NodeID)
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, nil)
}
