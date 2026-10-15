package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// 分流规则的管理接口(全部是新增,见 docs/internal/routing-rules-rfc.md §5)。

func (s *Server) routingRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/routing", s.handleGetRouting)
	api.HandleFunc("PUT /api/routing", s.handleSetRoutingMode)
	api.HandleFunc("POST /api/routing/migrate", s.handleMigrateRouting)
	api.HandleFunc("GET /api/rules", s.handleListRules)
	api.HandleFunc("PUT /api/rules", s.handleReplaceRules)
	api.HandleFunc("PATCH /api/rules/{id}", s.handlePatchRule)
	api.HandleFunc("PUT /api/rules/final", s.handleSetFinal)
	api.HandleFunc("POST /api/rules/test", s.handleTestRule)
	api.HandleFunc("GET /api/rule-catalog", s.handleRuleCatalog)
	api.HandleFunc("GET /api/proxy-groups", s.handleListGroups)
	api.HandleFunc("POST /api/proxy-groups", s.handleCreateGroup)
	api.HandleFunc("POST /api/proxy-groups/preview", s.handlePreviewGroup)
	api.HandleFunc("GET /api/proxy-groups/{id}", s.handleGetGroup)
	api.HandleFunc("PUT /api/proxy-groups/{id}", s.handleUpdateGroup)
	api.HandleFunc("DELETE /api/proxy-groups/{id}", s.handleDeleteGroup)
}

// scopeUser 解析 ?user=<用户名>;不带表示全局(返回 nil)。
func (s *Server) scopeUser(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	name := strings.TrimSpace(r.URL.Query().Get("user"))
	if name == "" {
		return nil, true
	}
	u, err := s.store.GetUser(name)
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return nil, false
	}
	return u, true
}

func uid(u *model.User) int64 {
	if u == nil {
		return 0
	}
	return u.ID
}

// routingError 把规则相关的存储错误翻成状态码:版本冲突与组被引用是 409,
// 校验失败和指向不存在的组是 400。
func (s *Server) routingError(w http.ResponseWriter, err error, what string) {
	var inUse *store.GroupInUseError
	switch {
	case errors.Is(err, store.ErrVersionConflict), errors.Is(err, store.ErrDuplicate), errors.As(err, &inUse):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, what+"不存在")
	case errors.Is(err, store.ErrUnknownGroup):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		if isValidation(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeStoreError(w, err, what)
	}
}

// isValidation 区分校验错误(给调用方看原话)与数据库错误(只记日志)。
// model 与 store 的校验错误都是纯中文描述,数据库错误带驱动前缀。
func isValidation(err error) bool {
	m := err.Error()
	return !strings.Contains(m, "sqlite") && !strings.Contains(m, "SQL") && !strings.Contains(m, "database")
}

type routingView struct {
	Mode          model.RoutingMode `json:"mode"`
	MigratedAt    any               `json:"migrated_at,omitempty"`
	Report        string            `json:"report,omitempty"`
	Migration     *MigrationReport  `json:"migration,omitempty"` // report 解析后的结构
	Warnings      []string          `json:"warnings"`
	GroupsMembers map[int64]int     `json:"group_members"`
}

// handleGetRouting 返回分流模式、迁移报告与警告(空策略组、失效的规则组)。
func (s *Server) handleGetRouting(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetRoutingState()
	if err != nil {
		s.writeStoreError(w, err, "分流设置")
		return
	}
	warn, members, err := s.routingWarnings()
	if err != nil {
		s.writeStoreError(w, err, "分流设置")
		return
	}
	v := routingView{Mode: st.Mode, Report: st.Report, Migration: parseReport(st.Report), Warnings: warn, GroupsMembers: members}
	if st.MigratedAt != nil {
		v.MigratedAt = st.MigratedAt
	}
	writeJSON(w, http.StatusOK, v)
}

// previewEndpoints 是「一个能用全部入站的用户」会拿到的端点,用来算策略组成员与警告。
// 只用于展示,不返回凭证。
func (s *Server) previewEndpoints() ([]render.Endpoint, error) {
	return s.endpointsFor(&model.User{Username: "preview", Key: "preview", Enabled: true})
}

func (s *Server) routingWarnings() ([]string, map[int64]int, error) {
	groups, err := s.store.ListProxyGroups()
	if err != nil {
		return nil, nil, err
	}
	eps, err := s.previewEndpoints()
	if err != nil {
		return nil, nil, err
	}
	cands, err := s.groupCandidates(eps, groups)
	if err != nil {
		return nil, nil, err
	}
	warn := []string{}
	members := map[int64]int{}
	for _, g := range groups {
		n := 0
		for _, c := range cands {
			if g.Admits(c) {
				n++
			}
		}
		members[g.ID] = n
		if n == 0 {
			warn = append(warn, "策略组「"+g.Name+"」没有符合条件的入口,订阅里会退成直连")
		}
	}
	rules, err := s.store.ListRules(0)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range rules {
		if r.Type == model.RuleSetRef {
			if _, ok := model.RuleSetByKey(r.Value); !ok {
				warn = append(warn, "规则组 "+r.Value+" 已不在内置目录里,订阅会跳过这条规则")
			}
		}
	}
	return warn, members, nil
}

type rulesView struct {
	Global        []model.RouteRule `json:"global"`
	GlobalVersion int64             `json:"global_version"`
	Final         string            `json:"final"`
	User          string            `json:"user,omitempty"`
	UserRules     []model.RouteRule `json:"user_rules,omitempty"`
	UserVersion   int64             `json:"user_version,omitempty"`
	UserFinal     string            `json:"user_final,omitempty"`
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	u, ok := s.scopeUser(w, r)
	if !ok {
		return
	}
	var v rulesView
	var err error
	if v.Global, err = s.store.ListRules(0); err != nil {
		s.writeStoreError(w, err, "规则")
		return
	}
	sc, err := s.store.GetRouteScope(0)
	if err != nil {
		s.writeStoreError(w, err, "规则")
		return
	}
	v.GlobalVersion, v.Final = sc.Version, sc.Final
	if u != nil {
		v.User = u.Username
		if v.UserRules, err = s.store.ListRules(u.ID); err != nil {
			s.writeStoreError(w, err, "规则")
			return
		}
		us, err := s.store.GetRouteScope(u.ID)
		if err != nil {
			s.writeStoreError(w, err, "规则")
			return
		}
		v.UserVersion, v.UserFinal = us.Version, us.Final
		if v.UserRules == nil {
			v.UserRules = []model.RouteRule{}
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// handleReplaceRules 整表替换一个作用域;version 必须是读到的版本(省略表示不检查)。
func (s *Server) handleReplaceRules(w http.ResponseWriter, r *http.Request) {
	u, ok := s.scopeUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Rules   []model.RouteRule `json:"rules"`
		Version *int64            `json:"version"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	expect := int64(-1)
	if body.Version != nil {
		expect = *body.Version
	}
	v, err := s.store.ReplaceRules(uid(u), body.Rules, expect)
	if err != nil {
		s.routingError(w, err, "规则")
		return
	}
	s.invalidateSubCache()
	rules, _ := s.store.ListRules(uid(u))
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules, "version": v})
}

// handlePatchRule 改一条规则:只改请求里给了的字段。
func (s *Server) handlePatchRule(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	cur, err := s.store.GetRule(id)
	if err != nil {
		s.routingError(w, err, "规则")
		return
	}
	var p struct {
		Type    *model.RuleType `json:"type"`
		Value   *string         `json:"value"`
		Target  *string         `json:"target"`
		Enabled *bool           `json:"enabled"`
		Note    *string         `json:"note"`
	}
	if !decodeBody(w, r, &p) {
		return
	}
	if p.Type != nil {
		cur.Type = *p.Type
	}
	if p.Value != nil {
		cur.Value = *p.Value
	}
	if p.Target != nil {
		cur.Target = *p.Target
	}
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	}
	if p.Note != nil {
		cur.Note = *p.Note
	}
	if err := s.store.UpdateRule(cur); err != nil {
		s.routingError(w, err, "规则")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, cur)
}

// handleSetFinal 改「其他全部」;对用户传 null 或空串表示跟随全局。
func (s *Server) handleSetFinal(w http.ResponseWriter, r *http.Request) {
	u, ok := s.scopeUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Target *string `json:"target"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t := ""
	if body.Target != nil {
		t = *body.Target
	}
	v, err := s.store.SetRouteFinal(uid(u), t)
	if err != nil {
		s.routingError(w, err, "规则")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, map[string]any{"target": t, "version": v})
}

func (s *Server) handleRuleCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.RuleCatalog())
}

// ---- 策略组 ----

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.ListProxyGroups()
	if err != nil {
		s.writeStoreError(w, err, "策略组")
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	g, err := s.store.GetProxyGroup(id)
	if err != nil {
		s.routingError(w, err, "策略组")
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var g model.ProxyGroup
	if !decodeBody(w, r, &g) {
		return
	}
	g.ID = 0
	if err := s.store.CreateProxyGroup(&g); err != nil {
		s.routingError(w, err, "策略组")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var g model.ProxyGroup
	if !decodeBody(w, r, &g) {
		return
	}
	g.ID = id
	if err := s.store.UpdateProxyGroup(&g); err != nil {
		s.routingError(w, err, "策略组")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteProxyGroup(id); err != nil {
		s.routingError(w, err, "策略组")
		return
	}
	s.invalidateSubCache()
	w.WriteHeader(http.StatusNoContent)
}

type previewEntry struct {
	NodeID       int64    `json:"node_id"`
	InboundID    int64    `json:"inbound_id"`
	HostID       int64    `json:"host_id"`
	Name         string   `json:"name"`
	Protocol     string   `json:"protocol"`
	Region       string   `json:"region,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Admitted     bool     `json:"admitted"`
	Reasons      []string `json:"reasons,omitempty"`
}

// handlePreviewGroup 给一个草稿策略组,返回全部入口各自进不进组、为什么。
func (s *Server) handlePreviewGroup(w http.ResponseWriter, r *http.Request) {
	var g model.ProxyGroup
	if !decodeBody(w, r, &g) {
		return
	}
	g.Normalize()
	eps, err := s.previewEndpoints()
	if err != nil {
		s.writeStoreError(w, err, "入口")
		return
	}
	// 能力结果只在有能力条件时才查;预览总是带上,方便界面显示。
	probe := g
	probe.Capabilities = model.CapabilityTargets
	cands, err := s.groupCandidates(eps, []model.ProxyGroup{probe})
	if err != nil {
		s.writeStoreError(w, err, "入口")
		return
	}
	out := []previewEntry{}
	for i, ep := range eps {
		c := cands[i]
		e := previewEntry{NodeID: ep.NodeID, InboundID: ep.InboundID, HostID: ep.HostID, Name: ep.Remark, Protocol: c.Protocol, Region: c.Region, Tags: c.Tags, Admitted: g.Admits(c)}
		for _, t := range model.CapabilityTargets {
			if c.Capabilities[t] {
				e.Capabilities = append(e.Capabilities, t)
			}
		}
		if len(g.Regions) > 0 && !contains(g.Regions, c.Region) {
			e.Reasons = append(e.Reasons, "地区不符")
		}
		if len(g.Protocols) > 0 && !contains(g.Protocols, c.Protocol) {
			e.Reasons = append(e.Reasons, "协议不符")
		}
		for _, cap := range g.Capabilities {
			if !c.Capabilities[cap] {
				e.Reasons = append(e.Reasons, cap+" 检测未通过")
			}
		}
		if len(g.Tags) > 0 && !anyIn(g.Tags, c.Tags) {
			e.Reasons = append(e.Reasons, "没有所选标签")
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func anyIn(want, have []string) bool {
	for _, h := range have {
		if contains(want, h) {
			return true
		}
	}
	return false
}
