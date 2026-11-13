package server

import (
	"log/slog"
	"net/http"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// ---- 对外入口 ----

// hostViewOf 给一个对外入口补上生效值。合并规则在 render 里,这里只负责把它的
// 两个输入(入站、节点)取出来。
func (s *Server) hostViewOf(h model.Host) (model.HostView, error) {
	in, err := s.store.GetInbound(h.InboundID)
	if err != nil {
		return model.HostView{}, err
	}
	n, err := s.store.GetNode(in.NodeID)
	if err != nil {
		return model.HostView{}, err
	}
	return model.HostView{Host: h, Effective: render.ResolveExposure(*n, *in, h)}, nil
}

// writeHostView 把一个对外入口连同它的生效值写回去。
func (s *Server) writeHostView(w http.ResponseWriter, code int, h model.Host) {
	view, err := s.hostViewOf(h)
	if err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	writeJSON(w, code, view)
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	hosts, err := s.store.ListHosts(id)
	if err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	// 显示用的名字算不出来时不影响列表本身:这两个字段是附加的,缺了就不带。
	names, err := s.subscriptionNames()
	if err != nil {
		slog.Warn("算订阅里的名字失败", "error", err)
	}
	views := make([]model.HostView, 0, len(hosts))
	for _, h := range hosts {
		v, err := s.hostViewOf(h)
		if err != nil {
			s.writeStoreError(w, err, "对外入口")
			return
		}
		n := names[subNameKey{h.InboundID, h.ID}]
		v.SubName, v.AutoName = n.name, n.auto
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	inboundID, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var h model.Host
	if !decodeBody(w, r, &h) {
		return
	}
	h.InboundID = inboundID
	if msg := validateHost(&h); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if _, err := s.store.GetInbound(inboundID); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if err := s.store.CreateHost(&h); err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	s.invalidateSubCache()
	s.writeHostView(w, http.StatusCreated, h)
}

func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var h model.Host
	if !decodeBody(w, r, &h) {
		return
	}
	h.ID = id
	if msg := validateHost(&h); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateHost(&h); err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	s.invalidateSubCache()
	s.writeHostView(w, http.StatusOK, h)
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteHost(id); err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, nil)
}
