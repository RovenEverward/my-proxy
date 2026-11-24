package server

import (
	"encoding/json"
	"fmt"
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
	api.HandleFunc("PATCH /api/sources/{id}/endpoints/{key}", s.handlePatchExternalMetadata)
	api.HandleFunc("GET /api/sources/{id}/grants", s.handleSourceGrants)
	api.HandleFunc("PUT /api/sources/{id}/grants", s.handleReplaceSourceGrants)
	api.HandleFunc("PUT /api/sources/{id}/grants/{username}", s.handleSourceGrant)
	api.HandleFunc("DELETE /api/sources/{id}/grants/{username}", s.handleSourceGrant)
	api.HandleFunc("GET /api/proxy-groups/{id}/sources", s.handleGetGroupSources)
	api.HandleFunc("GET /api/pool", s.handlePool)
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
	URLMasked     string `json:"url_masked"`
	Refreshing    bool   `json:"refreshing"`
	SnapshotStale bool   `json:"snapshot_stale"`
	NodeCount     int    `json:"node_count"`
	// Nodes 是快照里的节点名单:只带显示名与协议,不带地址和凭证,
	// 让节点页这类清单界面可以直接展示成员而不用读订阅预览。
	Nodes       []sourceNodeRef `json:"nodes"`
	InputFormat string          `json:"input_format"`
	Issues      []source.Issue                         `json:"issues"`
	Grants      []string                               `json:"grants"`
	Formats     map[model.ClientFormat]sourceFormatView `json:"formats"`
}

// sourceNodeRef 是外部节点的最小展示字段。Key 是端点指纹身份,
// Name 是清洗后的订阅名(UC-17),RawName 保留上游原名,供界面展开与回溯。
type sourceNodeRef struct {
	Name     string `json:"name"`
	RawName  string `json:"raw_name,omitempty"`
	Protocol string `json:"protocol"`
	Key      string `json:"key,omitempty"`
}

func (s *Server) viewSource(src *model.SubscriptionSource) sourceView {
	v := sourceView{SubscriptionSource: src, Refreshing: s.sources.Running(src.ID), SnapshotStale: len(src.Snapshot) > 0 && src.SnapshotURL != src.URL, Issues: []source.Issue{}, Grants: []string{}, Formats: map[model.ClientFormat]sourceFormatView{}}
	if grants, err := s.store.SourceGrants(src.ID); err == nil && grants != nil {
		v.Grants = grants
	}
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
	v.Nodes = make([]sourceNodeRef, 0, len(eps))
	extMeta, _ := s.store.ListExternalMetadata(src.ID)
	for _, ep := range eps {
		ep.SourceID = src.ID
		ref := sourceNodeRef{
			Name:     resolveExternalRemark(src.Name, extMeta, ep),
			RawName:  ep.Remark,
			Protocol: render.ProtocolShort(ep),
			Key:      string(ep.Key()),
		}
		v.Nodes = append(v.Nodes, ref)
	}
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
	// GrantAll 是「全员授权」开关:为真时这来源对所有用户(含新建)可见,
	// 按人授权表不再决定可见性,但保留记录,关掉后恢复生效。
	GrantAll *bool  `json:"grant_all"`
	Revision *int64 `json:"revision"`
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
	if p.GrantAll != nil {
		src.GrantAll = *p.GrantAll
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
		Users *[]string `json:"users"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Users != nil {
		if err := s.store.ReplaceSourceGrants(id, *body.Users); err != nil {
			s.writeStoreError(w, err, "来源或用户")
			return
		}
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
	// Excluded 为真时这个节点不出现在订阅里,只在预览里说明原因(排水)。
	Excluded bool `json:"excluded,omitempty"`
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
		// UC-19:剔除行直接读清洗池 —— 池里带着每个对象为什么不在订阅(排水)
		// 里的原因(目前只有排水),预览不再自己重算一遍。
		if pool, err := s.poolEndpointsFor(u); err == nil {
			for _, pe := range pool.Pool {
				if !pe.Excluded {
					continue
				}
				nodes = append(nodes, subscriptionPreviewNode{
					Name: pe.Remark, Protocol: render.ProtocolShort(pe.Endpoint),
					SourceID: pe.SourceID, SourceName: names[pe.SourceID],
					Reasons: []string{pe.ExcludeReason}, Excluded: true,
					Groups: []string{},
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "issues": set.Issues, "status": u.Status(time.Now()), "format": f})
}

func bareExternalKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.PathValue("key")
	if strings.HasPrefix(key, "s:") {
		writeError(w, http.StatusBadRequest,
			"端点 key 不能带 \"s:<source_id>:\" 前缀,请用 nodes[].key 里的指纹部分")
		return "", false
	}
	return key, true
}

func snapshotEndpoint(src *model.SubscriptionSource, key string) (render.Endpoint, bool) {
	var eps []render.Endpoint
	if err := json.Unmarshal(src.Snapshot, &eps); err != nil {
		return render.Endpoint{}, false
	}
	for _, ep := range eps {
		if ep.SourceKey == key {
			ep.SourceID = src.ID
			return ep, true
		}
	}
	return render.Endpoint{}, false
}

// handlePatchExternalMetadata 人工写/清一个外部端点的地区与城市。
func (s *Server) handlePatchExternalMetadata(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sourceFromPath(w, r)
	if !ok {
		return
	}
	key, ok := bareExternalKey(w, r)
	if !ok {
		return
	}
	ep, found := snapshotEndpoint(src, key)
	if !found {
		writeError(w, http.StatusNotFound, "这个端点不在当前快照里")
		return
	}
	var in struct {
		Region *string `json:"region"`
		City   *string `json:"city"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	region, err := normalizeExternalRegion(in.Region)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	metadataMu.Lock()
	defer metadataMu.Unlock()
	if region == "" {
		if err := s.store.DeleteExternalMetadata(src.ID, key); err != nil {
			s.writeStoreError(w, err, "外部端点元数据")
			return
		}
		s.invalidateSubCache()
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		return
	}
	existingAll, err := s.store.ListExternalMetadata(src.ID)
	if err != nil {
		s.writeStoreError(w, err, "外部端点元数据")
		return
	}
	existing := map[string]model.ExternalMetadata{}
	for _, m := range existingAll {
		existing[m.SourceKey] = m
	}
	m := existing[key]
	m.SourceID, m.SourceKey = src.ID, key
	m.Region = region
	if in.City != nil {
		m.City = strings.TrimSpace(*in.City)
	}
	if m.Letter == "" {
		existing[key] = m
		assigned := model.AssignExternalLetters(src.ID, []string{key}, map[string]string{key: m.Region}, existing)
		m = assigned[key]
	}
	m.Kind = "graded"
	if err := s.store.SaveExternalMetadataBatch([]model.ExternalMetadata{m}); err != nil {
		s.writeStoreError(w, err, "外部端点元数据")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "name": ep.Remark, "metadata": m})
}

func normalizeExternalRegion(v *string) (string, error) {
	if v == nil {
		return "", nil
	}
	raw := strings.TrimSpace(*v)
	if raw == "" {
		return "", nil
	}
	up := strings.ToUpper(raw)
	if model.RegionFlag(up) != "" {
		return up, nil
	}
	if r := model.RegionFromRemark(raw); r != "" {
		return r, nil
	}
	return "", fmt.Errorf("地区 %q 认不出来:两位国家码(如 US)或常见写法(美国/japan)都行", raw)
}
