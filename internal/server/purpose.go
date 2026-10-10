package server

import (
	"errors"
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
	"net/http"
	"strings"
	"time"
)

// SetPurposeFilter connects capability detection. It must only remove endpoints.
// Install before serving; nil leaves purposes with capability requirements unavailable.
func (s *Server) SetPurposeFilter(f func(model.Purpose, []render.Endpoint) ([]render.Endpoint, error)) {
	s.purposeFilter = f
}
func (s *Server) purposeRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/purposes", s.handleListPurposes)
	api.HandleFunc("POST /api/purposes", s.handleCreatePurpose)
	api.HandleFunc("GET /api/purposes/{slug}", s.handleGetPurpose)
	api.HandleFunc("PUT /api/purposes/{slug}", s.handleUpdatePurpose)
	api.HandleFunc("DELETE /api/purposes/{slug}", s.handleDeletePurpose)
	api.HandleFunc("GET /api/purposes/{slug}/preview/{username}/{format}", s.handlePurposePreview)
	api.HandleFunc("GET /api/nodes/{id}/metadata", s.handleGetPurposeMetadata)
	api.HandleFunc("PUT /api/nodes/{id}/metadata", s.handlePutPurposeMetadata)
	api.HandleFunc("GET /api/hosts/{id}/metadata", s.handleGetPurposeMetadata)
	api.HandleFunc("PUT /api/hosts/{id}/metadata", s.handlePutPurposeMetadata)
}
func (s *Server) handleListPurposes(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.ListPurposes()
	if err != nil {
		s.writeStoreError(w, err, "用途")
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) handleGetPurpose(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPurpose(r.PathValue("slug"))
	if err != nil {
		s.writeStoreError(w, err, "用途")
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) decodePurpose(w http.ResponseWriter, r *http.Request) (*model.Purpose, bool) {
	var p model.Purpose
	if !decodeBody(w, r, &p) {
		return nil, false
	}
	if slug := r.PathValue("slug"); slug != "" {
		if p.Slug != "" && p.Slug != slug {
			writeError(w, 400, "slug 与路径不一致")
			return nil, false
		}
		p.Slug = slug
	}
	p.Normalize()
	if err := p.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return nil, false
	}
	for f, t := range p.Templates {
		if err := validateTemplate(model.ClientFormat(f), []byte(t)); err != nil {
			writeError(w, 400, err.Error())
			return nil, false
		}
		if f != "clash-meta" && f != "sing-box" {
			writeError(w, 400, "用途模板只支持 clash-meta / sing-box")
			return nil, false
		}
	}
	return &p, true
}
func (s *Server) handleCreatePurpose(w http.ResponseWriter, r *http.Request) {
	p, ok := s.decodePurpose(w, r)
	if !ok {
		return
	}
	if _, err := s.store.GetPurpose(p.Slug); err == nil {
		writeError(w, 409, "用途已存在")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, err, "用途")
		return
	}
	if err := s.store.CreatePurpose(p); err != nil {
		s.writeStoreError(w, err, "用途")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, 201, p)
}
func (s *Server) handleUpdatePurpose(w http.ResponseWriter, r *http.Request) {
	p, ok := s.decodePurpose(w, r)
	if !ok {
		return
	}
	if err := s.store.UpdatePurpose(p); err != nil {
		s.writeStoreError(w, err, "用途")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, 200, p)
}
func (s *Server) handleDeletePurpose(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeletePurpose(r.PathValue("slug")); err != nil {
		s.writeStoreError(w, err, "用途")
		return
	}
	s.invalidateSubCache()
	w.WriteHeader(204)
}
func purposeMetadataKind(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/nodes/") {
		return "nodes"
	}
	return "hosts"
}
func (s *Server) handleGetPurposeMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetPurposeMetadata(purposeMetadataKind(r), id)
	if err != nil {
		s.writeStoreError(w, err, "元数据")
		return
	}
	writeJSON(w, 200, m)
}
func (s *Server) handlePutPurposeMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var m model.PurposeMetadata
	if !decodeBody(w, r, &m) {
		return
	}
	if err := m.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.SavePurposeMetadata(purposeMetadataKind(r), id, m); err != nil {
		s.writeStoreError(w, err, "元数据")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, 200, m)
}
func (s *Server) handlePurposePreview(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPurpose(r.PathValue("slug"))
	if err != nil {
		s.writeStoreError(w, err, "用途")
		return
	}
	u, err := s.store.GetUser(r.PathValue("username"))
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	f := model.ClientFormat(r.PathValue("format"))
	body, err := s.renderPurposeSubscription(u, *p, f)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	w.Header().Set("Content-Type", contentTypeOf(f))
	w.Write(body)
}
func (s *Server) renderPurposeSubscription(u *model.User, p model.Purpose, f model.ClientFormat) ([]byte, error) {
	if f != model.FormatClashMeta && f != model.FormatSingBox {
		return nil, fmt.Errorf("用途路由只支持 clash-meta / sing-box；请使用默认订阅获取其他格式")
	}
	if status := u.Status(time.Now()); status != model.StatusActive {
		return nil, fmt.Errorf("%s", statusRemark(status))
	}
	all, err := s.endpointsFor(u)
	if err != nil {
		return nil, err
	}
	nodes, err := s.store.ListPurposeMetadata("nodes")
	if err != nil {
		return nil, err
	}
	hosts, err := s.store.ListPurposeMetadata("hosts")
	if err != nil {
		return nil, err
	}
	candidates := []render.Endpoint{}
	for _, ep := range all {
		if p.Matches(model.EffectivePurposeMetadata(nodes[ep.NodeID], hosts[ep.HostID])) {
			candidates = append(candidates, ep)
		}
	}
	if len(p.RequiredCapabilities) > 0 && s.purposeFilter == nil {
		return nil, fmt.Errorf("用途要求能力检测，但检测尚未接入")
	}
	if s.purposeFilter != nil {
		filtered, e := s.purposeFilter(p, candidates)
		if e != nil {
			return nil, e
		}
		// Enforce the contract even if a future filter returns additional endpoints.
		allowed := map[[3]int64]render.Endpoint{}
		for _, ep := range candidates {
			allowed[[3]int64{ep.NodeID, ep.InboundID, ep.HostID}] = ep
		}
		candidates = nil
		for _, ep := range filtered {
			if original, ok := allowed[[3]int64{ep.NodeID, ep.InboundID, ep.HostID}]; ok {
				candidates = append(candidates, original)
				delete(allowed, [3]int64{ep.NodeID, ep.InboundID, ep.HostID})
			}
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("用途 %s 没有可用候选，请检查授权、排水、标签与能力结果", p.Name)
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		return nil, err
	}
	template := settings.TemplateFor(f)
	if t := p.Templates[string(f)]; t != "" {
		template = t
	}
	if p.DailyPurpose != "" {
		daily, err := s.store.GetPurpose(p.DailyPurpose)
		if err != nil {
			return nil, fmt.Errorf("日常用途 %s 不存在或已删除", p.DailyPurpose)
		}
		if daily.Kind != "daily" || daily.DailyPurpose != "" {
			return nil, fmt.Errorf("daily_purpose 必须引用无递归的日常用途")
		}
		// The selected purpose template is the base for both layers.
		copyDaily := *daily
		copyDaily.Templates = map[string]string{string(f): template}
		base, err := s.renderPurposeSubscription(u, copyDaily, f)
		if err != nil {
			return nil, fmt.Errorf("日常用途 %s: %w", daily.Slug, err)
		}
		return render.AddPurposeRouting(base, all, candidates, p, f)
	}
	return render.PurposeSubscription(all, candidates, p, f, []byte(template))
}
