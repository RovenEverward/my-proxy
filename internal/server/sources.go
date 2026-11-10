package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/source"
)

func (s *Server) sourceRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/sources", s.handleListSources)
	api.HandleFunc("POST /api/sources", s.handleCreateSource)
	api.HandleFunc("GET /api/sources/{id}", s.handleGetSource)
	api.HandleFunc("PATCH /api/sources/{id}", s.handlePatchSource)
	api.HandleFunc("DELETE /api/sources/{id}", s.handleDeleteSource)
	api.HandleFunc("POST /api/sources/{id}/refresh", s.handleRefreshSource)
	api.HandleFunc("GET /api/sources/{id}/grants", s.handleSourceGrants)
	api.HandleFunc("PUT /api/sources/{id}/grants", s.handleReplaceSourceGrants)
	api.HandleFunc("PUT /api/sources/{id}/grants/{username}", s.handleSourceGrant)
	api.HandleFunc("DELETE /api/sources/{id}/grants/{username}", s.handleSourceGrant)
	api.HandleFunc("GET /api/proxy-groups/{id}/sources", s.handleGetGroupSources)
	api.HandleFunc("PUT /api/proxy-groups/{id}/sources", s.handleSetGroupSources)
	api.HandleFunc("GET /api/users/{username}/subscription-preview", s.handleSubscriptionPreview)
}

var sourceFormats = []model.ClientFormat{model.FormatClashMeta, model.FormatSingBox, model.FormatLinks, model.FormatBase64, model.FormatClash, model.FormatXray}

type sourceFormatView struct {
	Count  int            `json:"count"`
	Issues []source.Issue `json:"issues"`
}
type sourceView struct {
	*model.SubscriptionSource
	URLMasked     string                                  `json:"url_masked"`
	Refreshing    bool                                    `json:"refreshing"`
	SnapshotStale bool                                    `json:"snapshot_stale"`
	NodeCount     int                                     `json:"node_count"`
	InputFormat   string                                  `json:"input_format"`
	Issues        []source.Issue                          `json:"issues"`
	Formats       map[model.ClientFormat]sourceFormatView `json:"formats"`
}

func (s *Server) viewSource(src *model.SubscriptionSource) sourceView {
	v := sourceView{SubscriptionSource: src, Refreshing: s.sources.Running(src.ID), SnapshotStale: len(src.Snapshot) > 0 && src.SnapshotURL != src.URL, Issues: []source.Issue{}, Formats: map[model.ClientFormat]sourceFormatView{}}
	if u, err := url.Parse(src.URL); err == nil {
		v.URLMasked = u.Scheme + "://" + u.Host + "/••••"
	}
	var report source.Result
	_ = json.Unmarshal(src.Report, &report)
	v.InputFormat = report.Format
	if report.Issues != nil {
		v.Issues = report.Issues
	}
	var eps []render.Endpoint
	_ = json.Unmarshal(src.Snapshot, &eps)
	v.NodeCount = len(eps)
	for _, f := range sourceFormats {
		fv := sourceFormatView{Issues: []source.Issue{}}
		for i, ep := range eps {
			if err := render.Compatible(ep, f); err != nil {
				fv.Issues = append(fv.Issues, source.Issue{Index: i + 1, Name: ep.Remark, Reason: err.Error()})
			} else {
				fv.Count++
			}
		}
		v.Formats[f] = fv
	}
	return v
}

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSources()
	if err != nil {
		s.writeStoreError(w, err, "订阅源")
		return
	}
	out := []sourceView{}
	for i := range list {
		out = append(out, s.viewSource(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) sourceFromPath(w http.ResponseWriter, r *http.Request) (*model.SubscriptionSource, bool) {
	id, ok := s.pathID(w, r)
	if !ok {
		return nil, false
	}
	src, err := s.store.GetSource(id)
	if err != nil {
		s.writeStoreError(w, err, "订阅源")
		return nil, false
	}
	return src, true
}

func (s *Server) handleGetSource(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sourceFromPath(w, r)
	if ok {
		writeJSON(w, http.StatusOK, s.viewSource(src))
	}
}

type sourceWrite struct {
	Name            *string `json:"name"`
	URL             *string `json:"url"`
	Enabled         *bool   `json:"enabled"`
	IntervalSeconds *int64  `json:"interval_seconds"`
	Region          *string `json:"region"`
	Revision        *int64  `json:"revision"`
}

func (p sourceWrite) apply(src *model.SubscriptionSource) {
	if p.Name != nil {
		src.Name = *p.Name
	}
	if p.URL != nil {
		src.URL = *p.URL
	}
	if p.Enabled != nil {
		src.Enabled = *p.Enabled
	}
	if p.IntervalSeconds != nil {
		src.IntervalSeconds = *p.IntervalSeconds
	}
	if p.Region != nil {
		src.Region = *p.Region
	}
}

func (s *Server) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	var p sourceWrite
	if !decodeBody(w, r, &p) {
		return
	}
	src := &model.SubscriptionSource{Enabled: true, IntervalSeconds: model.DefaultSourceInterval}
	p.apply(src)
	if err := s.store.CreateSource(src); err != nil {
		s.routingError(w, err, "订阅源")
		return
	}
	s.invalidateSubCache()
	if src.Enabled {
		_ = s.sources.StartRefresh(src.ID)
	}
	if current, err := s.store.GetSource(src.ID); err == nil {
		src = current
	}
	writeJSON(w, http.StatusCreated, s.viewSource(src))
}

func (s *Server) handlePatchSource(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sourceFromPath(w, r)
	if !ok {
		return
	}
	var p sourceWrite
	if !decodeBody(w, r, &p) {
		return
	}
	if p.Revision != nil && *p.Revision != src.Revision {
		writeError(w, http.StatusConflict, "来源已被修改，请刷新后重试")
		return
	}
	oldURL, wasEnabled := src.URL, src.Enabled
	p.apply(src)
	if err := s.store.UpdateSource(src); err != nil {
		s.routingError(w, err, "订阅源")
		return
	}
	s.invalidateSubCache()
	if src.Enabled && (oldURL != src.URL || !wasEnabled) {
		_ = s.sources.StartRefresh(src.ID)
	}
	writeJSON(w, http.StatusOK, s.viewSource(src))
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteSource(id); err != nil {
		s.writeStoreError(w, err, "订阅源")
		return
	}
	s.invalidateSubCache()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRefreshSource(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sourceFromPath(w, r)
	if !ok {
		return
	}
	if err := s.sources.StartRefresh(src.ID); err != nil {
		s.routingError(w, err, "订阅源")
		return
	}
	writeJSON(w, http.StatusAccepted, s.viewSource(src))
}

func (s *Server) handleSourceGrants(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	users, err := s.store.SourceGrants(id)
	if err != nil {
		s.writeStoreError(w, err, "来源或用户")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleReplaceSourceGrants(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Users []string `json:"users"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.ReplaceSourceGrants(id, body.Users); err != nil {
		s.writeStoreError(w, err, "来源或用户")
		return
	}
	s.invalidateSubCache()
	s.handleSourceGrants(w, r)
}

func (s *Server) handleSourceGrant(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.SetSourceGrant(id, r.PathValue("username"), r.Method == http.MethodPut); err != nil {
		s.writeStoreError(w, err, "来源或用户")
		return
	}
	s.invalidateSubCache()
	s.handleSourceGrants(w, r)
}

func (s *Server) handleGetGroupSources(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	values, err := s.store.GetGroupSources(id)
	if err != nil {
		s.writeStoreError(w, err, "策略组")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": values})
}

func (s *Server) handleSetGroupSources(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Sources []string `json:"sources"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.SetGroupSources(id, body.Sources); err != nil {
		s.routingError(w, err, "策略组")
		return
	}
	s.invalidateSubCache()
	s.handleGetGroupSources(w, r)
}

type endpointSet struct {
	Endpoints []render.Endpoint
	Issues    []source.Issue
}

// subscriptionEndpointsFor is the sole merge boundary. It never fetches upstream.
func (s *Server) subscriptionEndpointsFor(user *model.User, format model.ClientFormat) (endpointSet, error) {
	managed, err := s.endpointsFor(user)
	if err != nil {
		return endpointSet{}, err
	}
	set := endpointSet{Endpoints: managed, Issues: []source.Issue{}}
	if user.Status(time.Now()) != model.StatusActive {
		return set, nil
	}
	sources, err := s.store.ListSourcesForUser(user.ID)
	if err != nil {
		return set, err
	}
	for _, src := range sources {
		var eps []render.Endpoint
		if len(src.Snapshot) == 0 {
			continue
		}
		if err := json.Unmarshal(src.Snapshot, &eps); err != nil {
			set.Issues = append(set.Issues, source.Issue{Name: src.Name, Reason: "来源快照无法读取，请刷新该来源"})
			continue
		}
		for i, ep := range eps {
			ep.SourceID, ep.SourceRegion = src.ID, src.Region
			ep.Remark = src.Name + " · " + ep.Remark
			ep.AutoRemark = false
			if err := render.Compatible(ep, format); err != nil {
				set.Issues = append(set.Issues, source.Issue{Index: i + 1, Name: ep.Remark, Reason: err.Error()})
				continue
			}
			set.Endpoints = append(set.Endpoints, ep)
		}
	}
	set.Endpoints = render.DedupeRemarks(set.Endpoints)
	return set, nil
}

func previewFormat(w http.ResponseWriter, r *http.Request) (model.ClientFormat, bool) {
	f := model.ClientFormat(r.URL.Query().Get("format"))
	if f == "" {
		f = model.FormatClashMeta
	}
	if !f.Valid() || f == model.FormatBlock {
		writeError(w, http.StatusBadRequest, "预览格式无效")
		return f, false
	}
	return f, true
}

type subscriptionPreviewNode struct {
	Name       string   `json:"name"`
	Protocol   string   `json:"protocol"`
	SourceID   int64    `json:"source_id"`
	SourceName string   `json:"source_name"`
	Groups     []string `json:"groups"`
	Reasons    []string `json:"reasons"`
}

func (s *Server) handleSubscriptionPreview(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUser(r.PathValue("username"))
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	f, ok := previewFormat(w, r)
	if !ok {
		return
	}
	set, err := s.subscriptionEndpointsFor(u, f)
	if err != nil {
		s.writeStoreError(w, err, "订阅")
		return
	}
	plan, err := s.routingPlan(u, set.Endpoints)
	if err != nil {
		s.writeStoreError(w, err, "分流")
		return
	}
	list, err := s.store.ListSources()
	if err != nil {
		s.writeStoreError(w, err, "来源")
		return
	}
	names := map[int64]string{0: "自建节点"}
	for _, v := range list {
		names[v.ID] = v.Name
	}
	origins, err := s.store.ListGroupSources()
	if err != nil {
		s.writeStoreError(w, err, "策略组来源")
		return
	}
	nodes := []subscriptionPreviewNode{}
	if u.Status(time.Now()) == model.StatusActive {
		for _, ep := range set.Endpoints {
			n := subscriptionPreviewNode{Name: ep.Remark, Protocol: render.ProtocolShort(ep), SourceID: ep.SourceID, SourceName: names[ep.SourceID], Groups: []string{}, Reasons: []string{}}
			for _, g := range plan.Groups {
				for _, member := range g.Members {
					if member.Key() == ep.Key() {
						n.Groups = append(n.Groups, g.Name)
						break
					}
				}
			}
			if (f == model.FormatClashMeta || f == model.FormatSingBox) && len(n.Groups) == 0 {
				n.Reasons = append(n.Reasons, "节点已收录，但未进入策略组")
				if len(plan.Groups) == 0 {
					n.Reasons = append(n.Reasons, "尚未创建策略组")
				}
				if ep.SourceID > 0 {
					for _, group := range plan.Groups {
						reasons := []string{}
						if !model.OriginAllowed(origins[group.ID], ep.SourceID) {
							reasons = append(reasons, "来源不符")
						}
						if len(group.Regions) > 0 && !contains(group.Regions, ep.SourceRegion) {
							reasons = append(reasons, "地区不符或未知")
						}
						if len(group.Protocols) > 0 && !contains(group.Protocols, n.Protocol) {
							reasons = append(reasons, "协议不符")
						}
						if len(group.Capabilities) > 0 {
							reasons = append(reasons, "没有所需能力检测结果")
						}
						if len(group.Tags) > 0 {
							reasons = append(reasons, "旧标签条件不匹配")
						}
						if len(reasons) > 0 {
							n.Reasons = append(n.Reasons, group.Name+"："+strings.Join(reasons, "、"))
						}
					}
				}
			}
			nodes = append(nodes, n)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "issues": set.Issues, "status": u.Status(time.Now()), "format": f})
}
