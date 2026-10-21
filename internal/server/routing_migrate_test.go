package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"gopkg.in/yaml.v3"
)

func migrateAPI(t *testing.T, e *env, body map[string]bool) MigrationReport {
	t.Helper()
	code, b := e.api("POST", "/api/routing/migrate", body)
	if code != 200 {
		t.Fatalf("migrate %d %s", code, b)
	}
	var r MigrationReport
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMigrateDefault(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("jp-1", "jp.example.com")
	in := e.createInbound(n.ID, "r", 443)
	e.createHost(in.ID, "", "jp.example.com")
	u := e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.InboundIDs = []int64{in.ID} })
	path := "/sub/" + u.SubToken + "/clash-meta"

	dry := migrateAPI(t, e, map[string]bool{"dry_run": true})
	if dry.Status != MigrateDryRun {
		t.Fatalf("%+v", dry)
	}
	if empty, _ := e.st.RoutingEmpty(); !empty {
		t.Fatal("dry run 不应写入")
	}
	_, _, before := e.sub(path, "")

	e.s.AutoMigrateRouting()
	st, _ := e.st.GetRoutingState()
	if st.Mode != model.RoutingRules || st.MigratedAt == nil {
		t.Fatalf("应已迁移: %+v", st)
	}
	rep := parseReport(st.Report)
	if rep == nil || rep.Status != MigrateDone {
		t.Fatalf("%+v", rep)
	}
	rules, _ := e.st.ListRules(0)
	seed, _ := render.DefaultRoutingSeed()
	if len(rules) != len(seed.Rules) {
		t.Fatalf("规则数 %d, 底稿 %d", len(rules), len(seed.Rules))
	}
	groups, _ := e.st.ListProxyGroups()
	if len(groups) == 0 {
		t.Fatalf("应生成策略组: %+v", groups)
	}

	// 新库的真实订阅应使用手动策略；不改写已有库的规则。
	var manualID int64
	for _, g := range groups {
		if g.Name == "手动切换" && g.Mode == model.GroupManual {
			manualID = g.ID
		}
	}
	scope, err := e.st.GetRouteScope(0)
	if err != nil {
		t.Fatal(err)
	}
	if manualID == 0 || scope.Final != model.GroupTarget(manualID) {
		t.Fatalf("默认兜底没有指向手动组：%+v", scope)
	}
	code, _, subscription := e.sub(path, "")
	if code != 200 {
		t.Fatalf("订阅 %d：%s", code, subscription)
	}
	var doc struct {
		Groups []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(subscription, &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range doc.Groups {
		if g.Name == render.FinalGroupName {
			found = true
			if g.Type != "select" || len(g.Proxies) == 0 || g.Proxies[0] != "手动切换" {
				t.Fatalf("实际订阅兜底 = %+v", g)
			}
		}
	}
	if !found {
		t.Fatal("实际订阅缺少兜底组")
	}

	// 再启动不会重复迁移
	e.s.AutoMigrateRouting()
	if r2, _ := e.st.ListRules(0); len(r2) != len(rules) {
		t.Fatal("不应重复迁移")
	}
	if r := migrateAPI(t, e, nil); r.Status != MigrateSkipped {
		t.Fatalf("已有规则表时应跳过: %+v", r)
	}

	// ?purpose= 已删除:查询参数被忽略,返回普通订阅。
	code, _, after := e.sub(path+"?purpose=media", "")
	_, _, plain := e.sub(path, "")
	if code != 200 || string(after) != string(plain) || string(after) == string(before) {
		t.Fatalf("purpose 兼容: %d", code)
	}

	if r := migrateAPI(t, e, map[string]bool{"replace": true}); r.Status != MigrateDone {
		t.Fatalf("replace: %+v", r)
	}
}

func TestMigrateCustomTemplates(t *testing.T) {
	e := newEnv(t)
	set := func(f model.ClientFormat, tpl string) {
		s, _ := e.st.GetSettings()
		if s.Templates == nil {
			s.Templates = map[string]string{}
		}
		s.Templates[string(f)] = tpl
		if err := e.st.SaveSettings(s); err != nil {
			t.Fatal(err)
		}
	}
	set(model.FormatSingBox, `{"outbounds":[]}`)
	e.s.AutoMigrateRouting()
	st, _ := e.st.GetRoutingState()
	if st.Mode != model.RoutingTemplate || st.MigratedAt != nil || parseReport(st.Report).Status != MigrateNeedsConfirm {
		t.Fatalf("自定义 sing-box 模板应等确认: %+v", st)
	}
	if r := migrateAPI(t, e, map[string]bool{"confirm": true}); r.Status != MigrateDone || !r.SingBoxCustom {
		t.Fatalf("%+v", r)
	}

	e2 := newEnv(t)
	e = e2
	set(model.FormatClashMeta, "proxy-groups:\n  - {name: PROXY, type: select, include-all-proxies: true}\nrules:\n  - PROCESS-NAME,curl,PROXY\n  - DOMAIN-SUFFIX,a.com,PROXY\n  - MATCH,DIRECT\n")
	r := migrateAPI(t, e, nil)
	if r.Status != MigrateBlocked || len(r.Unconvertible) != 1 || !strings.Contains(r.Unconvertible[0], "PROCESS-NAME") {
		t.Fatalf("%+v", r)
	}
	if st, _ := e.st.GetRoutingState(); st.Mode != model.RoutingTemplate {
		t.Fatal("转换不了时保持模板模式")
	}
	set(model.FormatClashMeta, "proxy-groups:\n  - {name: PROXY, type: select, include-all-proxies: true}\nrules:\n  - DOMAIN-SUFFIX,a.com,PROXY\n  - MATCH,DIRECT\n")
	if r := migrateAPI(t, e, nil); r.Status != MigrateDone || r.Source != "自定义 clash-meta 模板" || r.Rules != 1 || r.Final != "直连" {
		t.Fatalf("%+v", r)
	}
}
