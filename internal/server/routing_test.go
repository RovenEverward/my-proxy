package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

func TestSubscriptionRoutingMode(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("la-1", "la.example.com")
	in := e.createInbound(n.ID, "r", 443)
	e.createHost(in.ID, "", "la.example.com")
	n2 := e.createNode("jp-1", "jp.example.com")
	in2 := e.createInbound(n2.ID, "r", 443)
	e.createHost(in2.ID, "", "jp.example.com")
	u := e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.InboundIDs = []int64{in.ID, in2.ID} })
	if code, b := e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", n2.ID), model.PurposeMetadata{Region: "JP"}); code != 200 {
		t.Fatalf("metadata %d %s", code, b)
	}
	path := "/sub/" + u.SubToken + "/clash-meta"
	_, _, before := e.sub(path, "")

	jp := &model.ProxyGroup{Name: "日本", Regions: []string{"JP"}, Mode: model.GroupManual}
	if err := e.st.CreateProxyGroup(jp); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.ReplaceRules(0, []model.RouteRule{{Type: model.RuleSuffix, Value: "example.jp", Target: model.GroupTarget(jp.ID), Enabled: true}, {Type: model.RuleSuffix, Value: "off.example", Target: model.TargetReject}}, -1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.ReplaceRules(u.ID, []model.RouteRule{{Type: model.RuleKeyword, Value: "bili", Target: model.TargetDirect, Enabled: true}}, -1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SetRouteFinal(0, model.GroupTarget(jp.ID)); err != nil {
		t.Fatal(err)
	}
	e.s.invalidateSubCache()
	if _, _, b := e.sub(path, ""); string(b) != string(before) {
		t.Fatal("template 模式下订阅不应变化")
	}
	if err := e.st.SaveRoutingState(&store.RoutingState{Mode: model.RoutingRules}); err != nil {
		t.Fatal(err)
	}
	e.s.invalidateSubCache()
	code, _, b := e.sub(path, "")
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	s := string(b)
	ki, si := strings.Index(s, "DOMAIN-KEYWORD,bili,DIRECT"), strings.Index(s, "DOMAIN-SUFFIX,example.jp,日本")
	if ki < 0 || si < 0 || ki > si {
		t.Fatalf("用户规则应在全局规则前:\n%s", s)
	}
	if strings.Contains(s, "off.example") {
		t.Fatal("停用的规则不应出现")
	}
	if !strings.Contains(s, "MATCH,"+"其他全部") || !strings.Contains(s, "jp.example.com") {
		t.Fatalf("兜底或节点缺失:\n%s", s)
	}
	if code, _, sb := e.sub("/sub/"+u.SubToken+"/sing-box", ""); code != 200 || !strings.Contains(string(sb), `"final": "其他全部"`) {
		t.Fatalf("sing-box %d %s", code, sb)
	}
	if code, _, lb := e.sub("/sub/"+u.SubToken+"/links", ""); code != 200 || strings.Contains(string(lb), "其他全部") {
		t.Fatal("links 不受影响")
	}
}
