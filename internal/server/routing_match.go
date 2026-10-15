package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"gopkg.in/yaml.v3"
)

// 「测试一个网址」:控制面按同一份规则表模拟客户端的匹配。
//
// 域名后缀、关键词精确判定;内置规则组要看上游规则集的内容,控制面按 Clash 那份
// 上游拉取并缓存(和客户端各自下载的是同一个 URL,但客户端可能还没更新)。按 IP
// 匹配的规则(GEOIP、IP 段、ipcidr 规则集)需要先解析域名,这里不解析,只标出来。

const (
	ruleSetTTL     = 12 * time.Hour
	ruleSetMaxSize = 16 << 20
)

type ruleSetEntry struct {
	exact, suffix, keyword []string
	ipOnly                 bool
	at                     time.Time
	err                    error
}

type ruleSetCache struct {
	mu    sync.Mutex
	items map[string]*ruleSetEntry
	fetch func(ctx context.Context, url string) ([]byte, error) // 测试里替换
}

func defaultFetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, ruleSetMaxSize))
}

func (c *ruleSetCache) get(ctx context.Context, rs model.ClashRuleSet) *ruleSetEntry {
	c.mu.Lock()
	if c.items == nil {
		c.items = map[string]*ruleSetEntry{}
	}
	if e, ok := c.items[rs.URL]; ok && time.Since(e.at) < ruleSetTTL && e.err == nil {
		c.mu.Unlock()
		return e
	}
	fetch := c.fetch
	c.mu.Unlock()
	if fetch == nil {
		fetch = defaultFetch
	}
	e := &ruleSetEntry{at: time.Now(), ipOnly: rs.Behavior == "ipcidr"}
	if !e.ipOnly {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		body, err := fetch(ctx, rs.URL)
		cancel()
		if err != nil {
			e.err = err
		} else {
			parseRuleSet(e, rs.Behavior, body)
		}
	}
	c.mu.Lock()
	c.items[rs.URL] = e
	c.mu.Unlock()
	return e
}

// parseRuleSet 读 Clash 规则集:yaml 的 payload 列表,或每行一条的纯文本。
// domain 行为:"+.a.com" 是后缀,".a.com"/"*.a.com" 是子域名,其余是精确域名;
// classical 行为:只认 DOMAIN / DOMAIN-SUFFIX / DOMAIN-KEYWORD。
func parseRuleSet(e *ruleSetEntry, behavior string, body []byte) {
	var doc struct {
		Payload []string `yaml:"payload"`
	}
	lines := doc.Payload
	if yaml.Unmarshal(body, &doc) == nil && len(doc.Payload) > 0 {
		lines = doc.Payload
	} else {
		for _, l := range strings.Split(string(body), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				lines = append(lines, l)
			}
		}
	}
	for _, l := range lines {
		l = strings.ToLower(strings.Trim(strings.TrimSpace(l), `'"`))
		if behavior == "classical" {
			f := strings.Split(l, ",")
			if len(f) < 2 {
				continue
			}
			switch strings.ToUpper(f[0]) {
			case "DOMAIN":
				e.exact = append(e.exact, f[1])
			case "DOMAIN-SUFFIX":
				e.suffix = append(e.suffix, f[1])
			case "DOMAIN-KEYWORD":
				e.keyword = append(e.keyword, f[1])
			}
			continue
		}
		switch {
		case strings.HasPrefix(l, "+."):
			e.suffix = append(e.suffix, l[2:])
		case strings.HasPrefix(l, "*."), strings.HasPrefix(l, "."):
			e.suffix = append(e.suffix, strings.TrimLeft(l, "*."))
		case l != "":
			e.exact = append(e.exact, l)
		}
	}
}

func (e *ruleSetEntry) matches(d string) bool {
	for _, x := range e.exact {
		if d == x {
			return true
		}
	}
	for _, x := range e.suffix {
		if d == x || strings.HasSuffix(d, "."+x) {
			return true
		}
	}
	for _, x := range e.keyword {
		if strings.Contains(d, x) {
			return true
		}
	}
	return false
}

// matchOutcome 是一条规则对一个域名的判定。
type matchOutcome int

const (
	noMatch matchOutcome = iota
	matched
	needsIP   // 按 IP 匹配,这里不解析
	setFailed // 规则集拉不下来
)

func (s *Server) matchRule(ctx context.Context, r model.RouteRule, d string) (matchOutcome, string) {
	switch r.Type {
	case model.RuleSuffix:
		if d == r.Value || strings.HasSuffix(d, "."+r.Value) {
			return matched, ""
		}
	case model.RuleKeyword:
		if strings.Contains(d, r.Value) {
			return matched, ""
		}
	case model.RuleGeoIP, model.RuleCIDR:
		return needsIP, "按 IP 匹配,测试时不解析域名"
	case model.RuleSetRef:
		rs, ok := model.RuleSetByKey(r.Value)
		if !ok {
			return noMatch, ""
		}
		var why []string
		for _, c := range rs.Clash {
			e := s.ruleSets.get(ctx, c)
			switch {
			case e.ipOnly:
				why = append(why, c.Name+" 按 IP 匹配")
			case e.err != nil:
				why = append(why, c.Name+" 拉取失败")
			case e.matches(d):
				return matched, ""
			}
		}
		for _, w := range why {
			if strings.HasSuffix(w, "拉取失败") {
				return setFailed, strings.Join(why, ";")
			}
		}
		if len(why) > 0 {
			return needsIP, strings.Join(why, ";")
		}
	}
	return noMatch, ""
}

type testStep struct {
	Scope  string          `json:"scope"` // global / user
	Index  int             `json:"index"` // 在该作用域里的序号,从 1 开始
	Rule   model.RouteRule `json:"rule"`
	Reason string          `json:"reason,omitempty"`
}

type testResult struct {
	Domain       string     `json:"domain"`
	User         string     `json:"user,omitempty"`
	Matched      *testStep  `json:"matched,omitempty"` // 为空表示走「其他全部」
	Target       string     `json:"target"`
	TargetName   string     `json:"target_name"`
	Members      []string   `json:"members,omitempty"` // 去向是策略组时,当前会收进的入口
	Mode         string     `json:"mode,omitempty"`
	Uncertain    []testStep `json:"uncertain,omitempty"`     // 在命中之前、无法判定的规则
	DisabledHits []testStep `json:"disabled_hits,omitempty"` // 停用了、但本来会命中的规则
}

func normDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/:?#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSuffix(s, ".")
}

func (s *Server) handleTestRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain string `json:"domain"`
		User   string `json:"user"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	d := normDomain(body.Domain)
	if d == "" || !strings.Contains(d, ".") && d != "localhost" {
		writeError(w, http.StatusBadRequest, "填一个域名,比如 chatgpt.com")
		return
	}
	res := testResult{Domain: d}
	scopes := []struct {
		name string
		id   int64
	}{}
	var user *model.User
	if body.User != "" {
		u, err := s.store.GetUser(body.User)
		if err != nil {
			s.writeStoreError(w, err, "用户")
			return
		}
		user, res.User = u, u.Username
		scopes = append(scopes, struct {
			name string
			id   int64
		}{"user", u.ID})
	}
	scopes = append(scopes, struct {
		name string
		id   int64
	}{"global", 0})
	final := ""
	for _, sc := range scopes {
		rules, err := s.store.ListRules(sc.id)
		if err != nil {
			s.writeStoreError(w, err, "规则")
			return
		}
		if final == "" {
			rs, err := s.store.GetRouteScope(sc.id)
			if err != nil {
				s.writeStoreError(w, err, "规则")
				return
			}
			final = rs.Final
		}
		if res.Matched != nil {
			continue
		}
		for i, rule := range rules {
			if !rule.Enabled && rule.Type == model.RuleSetRef {
				continue // 停用的内置规则组不去拉上游
			}
			out, why := s.matchRule(r.Context(), rule, d)
			step := testStep{Scope: sc.name, Index: i + 1, Rule: rule, Reason: why}
			if !rule.Enabled {
				if out == matched {
					res.DisabledHits = append(res.DisabledHits, step)
				}
				continue
			}
			if out == matched {
				res.Matched = &step
				break
			}
			if out == needsIP || out == setFailed {
				res.Uncertain = append(res.Uncertain, step)
			}
		}
	}
	groups, err := s.store.ListProxyGroups()
	if err != nil {
		s.writeStoreError(w, err, "策略组")
		return
	}
	if final == "" {
		final = model.TargetDirect
		if len(groups) > 0 {
			final = model.GroupTarget(groups[0].ID)
		}
	}
	res.Target = final
	if res.Matched != nil {
		res.Target = res.Matched.Rule.Target
	}
	res.TargetName = map[string]string{model.TargetDirect: "直连", model.TargetReject: "拦截"}[res.Target]
	if id, ok := model.TargetGroupID(res.Target); ok {
		var eps = []string{}
		for _, g := range groups {
			if g.ID != id {
				continue
			}
			res.TargetName, res.Mode = g.Name, string(g.Mode)
			var ep []string
			list, err := s.endpointsForTest(user)
			if err == nil {
				cands, err := s.groupCandidates(list, []model.ProxyGroup{g})
				if err == nil {
					for i, e := range list {
						if g.Admits(cands[i]) {
							ep = append(ep, e.Remark)
						}
					}
				}
			}
			eps = ep
		}
		res.Members = eps
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) endpointsForTest(u *model.User) ([]render.Endpoint, error) {
	if u == nil {
		return s.previewEndpoints()
	}
	return s.endpointsFor(u)
}
