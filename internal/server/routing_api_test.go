package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestRoutingAPI(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("jp-1", "jp.example.com")
	in := e.createInbound(n.ID, "r", 443)
	e.createHost(in.ID, "", "jp.example.com")
	e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.InboundIDs = []int64{in.ID} })
	e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", n.ID), model.PurposeMetadata{Region: "JP"})

	code, b := e.api("POST", "/api/proxy-groups", model.ProxyGroup{Name: "日本", Regions: []string{"JP"}, Mode: model.GroupAuto})
	if code != 201 {
		t.Fatalf("create group %d %s", code, b)
	}
	var g model.ProxyGroup
	json.Unmarshal(b, &g)
	if code, _ := e.api("POST", "/api/proxy-groups", model.ProxyGroup{Name: "日本"}); code == 201 {
		t.Fatal("重名应被拒绝")
	}
	if code, b := e.api("POST", "/api/proxy-groups/preview", model.ProxyGroup{Name: "x", Regions: []string{"JP"}}); code != 200 || !strings.Contains(string(b), `"admitted": true`) {
		t.Fatalf("preview %d %s", code, b)
	}

	rules := []model.RouteRule{
		{Type: model.RuleSuffix, Value: "example.jp", Target: model.GroupTarget(g.ID), Enabled: true},
		{Type: model.RuleCIDR, Value: "10.0.0.0/8", Target: model.TargetDirect, Enabled: true},
		{Type: model.RuleSetRef, Value: "ai", Target: model.TargetReject, Enabled: true},
		{Type: model.RuleKeyword, Value: "ads", Target: model.TargetReject},
	}
	if code, b := e.api("PUT", "/api/rules", map[string]any{"rules": rules, "version": 0}); code != 200 {
		t.Fatalf("replace %d %s", code, b)
	}
	if code, _ := e.api("PUT", "/api/rules", map[string]any{"rules": rules, "version": 0}); code != 409 {
		t.Fatalf("旧版本应冲突,得到 %d", code)
	}
	if code, b := e.api("PUT", "/api/rules", map[string]any{"rules": []model.RouteRule{{Type: model.RuleSuffix, Value: "x.com", Target: "group:999", Enabled: true}}}); code != 400 {
		t.Fatalf("未知策略组应 400,得到 %d %s", code, b)
	}
	if code, _ := e.api("DELETE", fmt.Sprintf("/api/proxy-groups/%d", g.ID), nil); code != 409 {
		t.Fatalf("被引用的策略组不能删,得到 %d", code)
	}
	if code, b := e.api("PUT", "/api/rules?user=alice", map[string]any{"rules": []model.RouteRule{{Type: model.RuleKeyword, Value: "bili", Target: model.TargetDirect, Enabled: true}}}); code != 200 {
		t.Fatalf("user replace %d %s", code, b)
	}
	if code, b := e.api("PUT", "/api/rules/final", map[string]any{"target": model.GroupTarget(g.ID)}); code != 200 {
		t.Fatalf("final %d %s", code, b)
	}

	e.s.ruleSets.fetch = func(_ context.Context, url string) ([]byte, error) {
		return []byte("payload:\n  - DOMAIN-SUFFIX,openai.com\n  - '+.chatgpt.com'\n  - DOMAIN-KEYWORD,claude\n"), nil
	}
	test := func(body map[string]any) testResult {
		t.Helper()
		code, b := e.api("POST", "/api/rules/test", body)
		if code != 200 {
			t.Fatalf("test %d %s", code, b)
		}
		var r testResult
		json.Unmarshal(b, &r)
		return r
	}
	r := test(map[string]any{"domain": "https://www.Example.jp/path"})
	if r.Domain != "www.example.jp" || r.Matched == nil || r.Matched.Index != 1 || r.TargetName != "日本" || len(r.Members) != 1 {
		t.Fatalf("%+v", r)
	}
	r = test(map[string]any{"domain": "api.openai.com"})
	if r.Target != model.TargetReject || len(r.Uncertain) != 1 || r.Uncertain[0].Rule.Type != model.RuleCIDR {
		t.Fatalf("%+v", r)
	}
	r = test(map[string]any{"domain": "myads.net"})
	if r.Matched != nil || r.Target != model.GroupTarget(g.ID) || len(r.DisabledHits) != 1 {
		t.Fatalf("停用规则不该命中、应落到其他全部: %+v", r)
	}
	r = test(map[string]any{"domain": "bilibili.com", "user": "alice"})
	if r.Matched == nil || r.Matched.Scope != "user" || r.TargetName != "直连" {
		t.Fatalf("%+v", r)
	}
	if code, _ := e.api("POST", "/api/rules/test", map[string]any{"domain": "nodot"}); code != 400 {
		t.Fatal("非域名应 400")
	}
	// 模式只读:GET 恒为 rules,PUT /api/routing 已下线。
	if code, b := e.api("GET", "/api/routing", nil); code != 200 || !strings.Contains(string(b), `"mode": "rules"`) {
		t.Fatalf("routing %d %s", code, b)
	}
	if code, _ := e.api("PUT", "/api/routing", map[string]string{"mode": "template"}); code != 405 && code != 404 {
		t.Fatalf("PUT /api/routing 应已下线,得到 %d", code)
	}
	if code, b := e.api("GET", "/api/rule-catalog", nil); code != 200 || !strings.Contains(string(b), `"ai"`) {
		t.Fatalf("catalog %d", code)
	}
}

func TestParseRuleSet(t *testing.T) {
	e := &ruleSetEntry{}
	parseRuleSet(e, "domain", []byte("# c\n+.a.com\n.b.com\nc.com\n"))
	for d, want := range map[string]bool{"a.com": true, "x.a.com": true, "x.b.com": true, "c.com": true, "x.c.com": false, "d.com": false} {
		if e.matches(d) != want {
			t.Errorf("%s want %v", d, want)
		}
	}
}
