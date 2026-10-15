package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
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

func TestMigrateDefaultAndPurposes(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("jp-1", "jp.example.com")
	in := e.createInbound(n.ID, "r", 443)
	e.createHost(in.ID, "", "jp.example.com")
	u := e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.InboundIDs = []int64{in.ID} })
	for _, p := range []model.Purpose{
		{Slug: "daily", Name: "日常", Kind: "daily", Strategy: "url-test", Regions: []string{"JP"}, Lines: []string{"cn2"}, TestURL: "https://www.gstatic.com/generate_204", Interval: 300},
		{Slug: "media", Name: "流媒体", Kind: "media", Strategy: "select", Regions: []string{"US"}, Domains: []string{"netflix.com", "nflxvideo.net"}, RequiredCapabilities: []string{"netflix"}, CapabilityRegion: "US", TestURL: "https://www.gstatic.com/generate_204", Interval: 300},
	} {
		p := p
		if err := e.st.CreatePurpose(&p); err != nil {
			t.Fatal(err)
		}
	}
	path := "/sub/" + u.SubToken + "/clash-meta"

	dry := migrateAPI(t, e, map[string]bool{"dry_run": true})
	if dry.Status != MigrateDryRun {
		t.Fatalf("%+v", dry)
	}
	if empty, _ := e.st.RoutingEmpty(); !empty {
		t.Fatal("dry run 不应写入")
	}
	if code, _ := e.api("PUT", "/api/routing", map[string]string{"mode": "rules"}); code != 409 {
		t.Fatal("规则表为空时不能切到 rules")
	}
	_, _, before := e.sub(path+"?purpose=media", "")

	e.s.AutoMigrateRouting()
	st, _ := e.st.GetRoutingState()
	if st.Mode != model.RoutingRules || st.MigratedAt == nil {
		t.Fatalf("应已迁移: %+v", st)
	}
	rep := parseReport(st.Report)
	if rep == nil || rep.Status != MigrateDone || rep.Final != "日常" || len(rep.Purposes) != 2 {
		t.Fatalf("%+v", rep)
	}
	drops := strings.Join(append(rep.Purposes[0].Dropped, rep.Purposes[1].Dropped...), "|")
	if !strings.Contains(drops, "cn2") || !strings.Contains(drops, "能力检测地区 US") {
		t.Fatalf("报告应列出丢掉的线路与能力地区: %s", drops)
	}
	rules, _ := e.st.ListRules(0)
	if rules[0].Value != "netflix.com" || rules[1].Value != "nflxvideo.net" {
		t.Fatalf("用途域名应在最前: %+v", rules[:2])
	}
	seed, _ := render.DefaultRoutingSeed()
	if len(rules) != len(seed.Rules)+2 {
		t.Fatalf("规则数 %d, 底稿 %d", len(rules), len(seed.Rules))
	}
	groups, _ := e.st.ListProxyGroups()
	var media *model.ProxyGroup
	for i := range groups {
		if groups[i].Name == "流媒体" || strings.HasPrefix(groups[i].Name, "流媒体(用途") {
			media = &groups[i]
		}
	}
	if media == nil || media.Capabilities[0] != "netflix" || media.Mode != model.GroupManual {
		t.Fatalf("用途组: %+v", groups)
	}

	// 再启动不会重复迁移
	e.s.AutoMigrateRouting()
	if r2, _ := e.st.ListRules(0); len(r2) != len(rules) {
		t.Fatal("不应重复迁移")
	}
	if r := migrateAPI(t, e, nil); r.Status != MigrateSkipped {
		t.Fatalf("已有规则表时应跳过: %+v", r)
	}

	// ?purpose= 返回普通订阅 + Deprecation
	code, h, after := e.sub(path+"?purpose=media", "")
	_, _, plain := e.sub(path, "")
	if code != 200 || h.Get("Deprecation") != "true" || string(after) != string(plain) || string(after) == string(before) {
		t.Fatalf("purpose 兼容: %d %v", code, h)
	}
	if code, _, _ := e.sub(path+"?purpose=gone", ""); code != 200 {
		t.Fatal("rules 模式下不存在的用途也返回普通订阅")
	}
	if c, b := e.api("GET", "/api/purposes", nil); c != 200 || !strings.Contains(string(b), "media") {
		t.Fatal("用途接口应照常可读")
	}
	if c, _ := e.api("POST", "/api/purposes", model.Purpose{Slug: "x", Name: "X", Kind: "daily", Strategy: "select", TestURL: "https://www.gstatic.com/generate_204", Interval: 300}); c != 201 {
		t.Fatalf("用途写接口不破坏兼容,得到 %d", c)
	}

	// 回滚开关
	if c, b := e.api("PUT", "/api/routing", map[string]string{"mode": "template"}); c != 200 || !strings.Contains(string(b), "template") {
		t.Fatalf("%d %s", c, b)
	}
	if _, h, b := e.sub(path+"?purpose=media", ""); h.Get("Deprecation") != "" || string(b) != string(before) {
		t.Fatal("切回 template 后用途订阅应恢复原样")
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
