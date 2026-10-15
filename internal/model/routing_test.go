package model

import "testing"

func TestRouteRuleValidate(t *testing.T) {
	ok := []RouteRule{
		{Type: RuleSuffix, Value: "Example.COM", Target: TargetDirect},
		{Type: RuleSuffix, Value: ".linux.do", Target: "group:3"},
		{Type: RuleKeyword, Value: "google", Target: TargetReject},
		{Type: RuleSetRef, Value: "ads", Target: TargetReject},
		{Type: RuleGeoIP, Value: "cn", Target: TargetDirect},
		{Type: RuleCIDR, Value: "91.108.4.1/22", Target: "group:1"},
		{Type: RuleCIDR, Value: "2001:db8::/32", Target: TargetDirect},
	}
	for _, r := range ok {
		r.Normalize()
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	r := RouteRule{Type: RuleCIDR, Value: "91.108.4.1/22"}
	r.Normalize()
	if r.Value != "91.108.4.0/22" {
		t.Errorf("CIDR 没有规范化: %s", r.Value)
	}
	bad := []RouteRule{
		{Type: RuleSuffix, Value: "a.com,DIRECT", Target: TargetDirect},
		{Type: RuleSuffix, Value: "localhost", Target: TargetDirect},
		{Type: RuleKeyword, Value: "", Target: TargetDirect},
		{Type: RuleSetRef, Value: "nope", Target: TargetDirect},
		{Type: RuleGeoIP, Value: "CHN", Target: TargetDirect},
		{Type: RuleCIDR, Value: "1.2.3.4", Target: TargetDirect},
		{Type: "process", Value: "x", Target: TargetDirect},
		{Type: RuleSuffix, Value: "a.com", Target: "la-1"},
		{Type: RuleSuffix, Value: "a.com", Target: "group:0"},
		{Type: RuleSuffix, Value: "a.com\nMATCH", Target: TargetDirect},
	}
	for _, r := range bad {
		r.Normalize()
		if r.Validate() == nil {
			t.Errorf("应当拒绝 %+v", r)
		}
	}
}

func TestProxyGroupValidateAndAdmits(t *testing.T) {
	g := ProxyGroup{Name: "美国", Regions: []string{"us"}, Protocols: []string{"Reality"}, Capabilities: []string{"netflix"}}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if g.Mode != GroupAuto || g.Interval != 300 || g.Regions[0] != "US" {
		t.Fatalf("默认值不对: %+v", g)
	}
	in := GroupCandidate{Region: "US", Protocol: "Reality", Capabilities: map[string]bool{"netflix": true}}
	if !g.Admits(in) {
		t.Fatal("应当收进")
	}
	for _, c := range []GroupCandidate{
		{Region: "JP", Protocol: "Reality", Capabilities: map[string]bool{"netflix": true}},
		{Region: "US", Protocol: "Hy2", Capabilities: map[string]bool{"netflix": true}},
		{Region: "US", Protocol: "Reality"},
	} {
		if g.Admits(c) {
			t.Errorf("不应收进 %+v", c)
		}
	}
	tagged := ProxyGroup{Tags: []string{"media"}}
	if tagged.Admits(GroupCandidate{Tags: []string{"game"}}) || !tagged.Admits(GroupCandidate{Tags: []string{"game", "media"}}) {
		t.Fatal("标签任一命中")
	}
	if (ProxyGroup{}).Admits(GroupCandidate{}) != true {
		t.Fatal("没有条件时全部收进")
	}
	for _, bad := range []ProxyGroup{
		{Name: ""}, {Name: "DIRECT"}, {Name: "流媒体"}, {Name: "a,b"},
		{Name: "x", Mode: "fast"}, {Name: "x", Protocols: []string{"reality"}},
		{Name: "x", Capabilities: []string{"ai"}}, {Name: "x", Regions: []string{"USA"}},
	} {
		bad.Normalize()
		if bad.Validate() == nil {
			t.Errorf("应当拒绝 %+v", bad)
		}
	}
}

func TestRuleCatalogShape(t *testing.T) {
	seen := map[string]bool{}
	for _, rs := range RuleCatalog() {
		if seen[rs.Key] {
			t.Errorf("重复键 %s", rs.Key)
		}
		seen[rs.Key] = true
		if rs.Name == "" || len(rs.Clash) == 0 || len(rs.SingBox) == 0 {
			t.Errorf("%s 不完整", rs.Key)
		}
		if rs.Default != TargetDirect && rs.Default != TargetReject && rs.Default != "PROXY" {
			t.Errorf("%s 默认去向 %q", rs.Key, rs.Default)
		}
	}
	for _, k := range []string{"lan", "ads", "ai", "media", "github", "cn"} {
		if !seen[k] {
			t.Errorf("缺 %s", k)
		}
	}
}
