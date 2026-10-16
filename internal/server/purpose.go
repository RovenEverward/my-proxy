package server

import (
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SetPurposeFilter connects capability detection. It must only remove endpoints.
// Install before serving; nil leaves purposes with capability requirements unavailable.
func (s *Server) SetPurposeFilter(f func(model.Purpose, []render.Endpoint) ([]render.Endpoint, error)) {
	s.purposeFilter = f
}

// setPurposeDeprecation 只服务订阅侧的 ?purpose= 兼容(见 subscribe.go),
// 那个入口保留到 v0.7.0,下一刀再随规则表迁移一起删。
func setPurposeDeprecation(w http.ResponseWriter) {
	w.Header().Set("Deprecation", "true")
	w.Header().Set("X-Mpx-Deprecated", "purposes are deprecated since v0.6.0 and will be removed in v0.7.0; use routing rules (/api/rules, /api/proxy-groups)")
}

func (s *Server) purposeRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/nodes/{id}/metadata", s.handleGetPurposeMetadata)
	api.HandleFunc("PUT /api/nodes/{id}/metadata", s.handlePutPurposeMetadata)
	api.HandleFunc("POST /api/nodes/{id}/locate", s.handleLocateNode)
	api.HandleFunc("GET /api/hosts/{id}/metadata", s.handleGetPurposeMetadata)
	api.HandleFunc("PUT /api/hosts/{id}/metadata", s.handlePutPurposeMetadata)
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
	// city / letter 用指针判断「有没有带」:只认识 region/line/purposes 的老客户端
	// 整份 PUT 时,不能把已分配的城市和字母冲掉。
	var in struct {
		model.PurposeMetadata
		City   *string `json:"city"`
		Letter *string `json:"letter"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	m := in.PurposeMetadata
	if in.City != nil {
		m.City = strings.TrimSpace(*in.City)
	}
	if in.Letter != nil {
		m.Letter = strings.ToUpper(strings.TrimSpace(*in.Letter))
	}
	if err := m.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	kind := purposeMetadataKind(r)
	metadataMu.Lock()
	defer metadataMu.Unlock()
	if kind == "hosts" {
		if m.City != "" || m.Letter != "" {
			writeError(w, 400, "city / letter 只用于节点")
			return
		}
	} else {
		code, msg := s.fillNodeLetter(id, &m, in.City != nil, in.Letter != nil)
		if code != 0 {
			writeError(w, code, msg)
			return
		}
	}
	if err := s.store.SavePurposeMetadata(kind, id, m); err != nil {
		s.writeStoreError(w, err, "元数据")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, 200, m)
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

// metadataMu 让「查已用字母 → 保存」成为一步,两个并发保存不会拿到同一个字母。
var metadataMu sync.Mutex

// fillNodeLetter 补齐节点元数据里省略的 city / letter,并按 (地区, 城市) 分配字母
// (docs/internal/node-naming-rfc.md):显式给的字母撞了就拒绝;沿用旧字母但换了分组
// 撞了就重新分配;没有字母就取该分组最小的空闲字母。删掉节点不让别人重排。
func (s *Server) fillNodeLetter(id int64, m *model.PurposeMetadata, citySet, letterSet bool) (int, string) {
	all, err := s.store.ListPurposeMetadata("nodes")
	if err != nil {
		return 500, err.Error()
	}
	old := all[id]
	if !citySet {
		m.City = old.City
	}
	if !letterSet {
		m.Letter = old.Letter
	}
	if strings.TrimSpace(m.Region) == "" {
		m.Letter = ""
		return 0, ""
	}
	used := map[string]int64{}
	usedSet := map[string]bool{}
	for other, om := range all {
		if other != id && om.Letter != "" && strings.TrimSpace(om.Region) != "" && model.LetterGroup(om) == model.LetterGroup(*m) {
			used[om.Letter] = other
			usedSet[om.Letter] = true
		}
	}
	if m.Letter != "" {
		if other, ok := used[m.Letter]; ok {
			if letterSet {
				return 409, fmt.Sprintf("字母 %s 已被节点 %d 占用", m.Letter, other)
			}
			m.Letter = ""
		}
	}
	if m.Letter == "" {
		if m.Letter = model.NextLetter(usedSet); m.Letter == "" {
			return 409, "该地区/城市的字母已用完"
		}
	}
	return 0, ""
}
