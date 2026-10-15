package render

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

func routingEndpoints() []Endpoint {
	eps := purposeEndpoints()
	return eps
}

func seedPlan(t *testing.T, eps []Endpoint) RoutingPlan {
	t.Helper()
	seed, err := DefaultRoutingSeed()
	if err != nil {
		t.Fatal(err)
	}
	p := RoutingPlan{Rules: seed.Rules, Final: seed.Final}
	for _, g := range seed.Groups {
		p.Groups = append(p.Groups, RoutingGroup{ProxyGroup: g, Members: eps})
	}
	return p
}

type clashDoc struct {
	Groups []struct {
		Name    string   `yaml:"name"`
		Type    string   `yaml:"type"`
		Proxies []string `yaml:"proxies"`
	} `yaml:"proxy-groups"`
	Rules     []string       `yaml:"rules"`
	Providers map[string]any `yaml:"rule-providers"`
}

// terminal 沿 select 组的第一个选项走到底,得到客户端默认会用的出口。
func (d clashDoc) terminal(name string) string {
	for i := 0; i < 20; i++ {
		found := false
		for _, g := range d.Groups {
			if g.Name == name && g.Type == "select" && len(g.Proxies) > 0 {
				name, found = g.Proxies[0], true
				break
			}
		}
		if !found {
			return name
		}
	}
	return name
}

func (d clashDoc) flat() []string {
	out := []string{}
	for _, r := range d.Rules {
		f := strings.Split(r, ",")
		i := 2
		if f[0] == "MATCH" {
			i = 1
		}
		f[i] = d.terminal(f[i])
		out = append(out, strings.Join(f, ","))
	}
	return out
}

// 默认底稿转换成规则表后再生成,每条规则的匹配内容与默认出口都和原订阅一致。
func TestDefaultSeedEquivalentClash(t *testing.T) {
	eps := routingEndpoints()
	orig, err := Clash(eps, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := ClashRouting(eps, seedPlan(t, eps), nil)
	if err != nil {
		t.Fatal(err)
	}
	var a, b clashDoc
	if err := yaml.Unmarshal(orig, &a); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(gen, &b); err != nil {
		t.Fatal(err)
	}
	fa, fb := a.flat(), b.flat()
	if len(fa) != len(fb) {
		t.Fatalf("规则数不同: 原 %d 生成 %d", len(fa), len(fb))
	}
	for i := range fa {
		if fa[i] != fb[i] {
			t.Fatalf("第 %d 条不同:\n 原 %s\n 新 %s", i+1, fa[i], fb[i])
		}
	}
	if len(a.Providers) != len(b.Providers) {
		t.Fatalf("规则集数量不同: %d vs %d", len(a.Providers), len(b.Providers))
	}
	if !strings.Contains(string(gen), "dns:") || !strings.Contains(string(gen), "sniffer:") {
		t.Fatal("模板里与分流无关的段应保留")
	}
}

func TestClashRoutingChoicesAndEmptyGroup(t *testing.T) {
	eps := routingEndpoints()
	p := RoutingPlan{
		Groups: []RoutingGroup{{ProxyGroup: model.ProxyGroup{ID: 1, Name: "自动选择", Mode: model.GroupAuto, TestURL: "https://x/204", Interval: 300}, Members: eps},
			{ProxyGroup: model.ProxyGroup{ID: 2, Name: "日本", Mode: model.GroupManual}}},
		Rules: []model.RouteRule{
			{Type: model.RuleSetRef, Value: "ads", Target: model.TargetReject},
			{Type: model.RuleSetRef, Value: "media", Target: "group:2"},
			{Type: model.RuleCIDR, Value: "2001:db8::/32", Target: model.TargetDirect},
			{Type: model.RuleSuffix, Value: "a.com", Target: "group:99"},
		},
		Final: "group:1",
	}
	b, err := ClashRouting(eps, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	var d clashDoc
	_ = yaml.Unmarshal(b, &d)
	want := map[string]string{"广告拦截": "REJECT,DIRECT,自动选择,日本", "流媒体": "日本,自动选择,DIRECT", "日本": "DIRECT", FinalGroupName: "自动选择,日本,DIRECT"}
	for _, g := range d.Groups {
		if w, ok := want[g.Name]; ok {
			if got := strings.Join(g.Proxies, ","); got != w {
				t.Errorf("%s: %s, want %s", g.Name, got, w)
			}
			delete(want, g.Name)
		}
	}
	if len(want) > 0 {
		t.Errorf("缺组 %v", want)
	}
	s := strings.Join(d.Rules, "\n")
	for _, w := range []string{"IP-CIDR6,2001:db8::/32,DIRECT,no-resolve", "DOMAIN-SUFFIX,a.com,DIRECT", "MATCH," + FinalGroupName} {
		if !strings.Contains(s, w) {
			t.Errorf("缺规则 %s", w)
		}
	}
}

func TestConvertClashTemplateRejectsUnknown(t *testing.T) {
	tpl := "proxy-groups:\n  - {name: P, type: select, include-all-proxies: true}\n  - {name: F, type: select, proxies: [la-1]}\nrules:\n  - DOMAIN,exact.com,P\n  - DOMAIN-SUFFIX,a.com,F\n  - RULE-SET,mine,P\n  - SUB-RULE,(NETWORK,TCP),x\n  - DOMAIN-SUFFIX,ok.com,P\n  - MATCH,P\n"
	seed, bad, err := ConvertClashTemplate([]byte(tpl))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 4 {
		t.Fatalf("应有 4 条无法转换: %v", bad)
	}
	if len(seed.Rules) != 1 || seed.Final != "group:1" || seed.Groups[0].Mode != model.GroupManual {
		t.Fatalf("可转换部分不对: %+v", seed)
	}
}

func TestSingBoxRoutingShape(t *testing.T) {
	eps := routingEndpoints()
	b, err := SingBoxRouting(eps, seedPlan(t, eps), nil)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
		Route     struct {
			Rules   []map[string]any `json:"rules"`
			RuleSet []map[string]any `json:"rule_set"`
			Final   string           `json:"final"`
		} `json:"route"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Route.Final != FinalGroupName || cfg.Route.Rules[0]["action"] != "sniff" || cfg.Route.Rules[1]["action"] != "hijack-dns" {
		t.Fatalf("前置规则或兜底不对: %+v", cfg.Route.Rules[:2])
	}
	tags := map[string]bool{}
	for _, o := range cfg.Outbounds {
		tags[o["tag"].(string)] = true
	}
	for _, r := range cfg.Route.Rules[2:] {
		if o, ok := r["outbound"].(string); ok && !tags[o] {
			t.Errorf("规则指向不存在的出站 %s", o)
		}
	}
	defined := map[string]bool{}
	for _, s := range cfg.Route.RuleSet {
		defined[s["tag"].(string)] = true
	}
	for _, r := range cfg.Route.Rules {
		for _, s := range toAnySlice(r["rule_set"]) {
			if !defined[s.(string)] {
				t.Errorf("规则集 %v 没定义", s)
			}
		}
	}
	for _, old := range []string{"全球代理", "漏网之鱼"} {
		if tags[old] {
			t.Errorf("模板里的旧分类组 %s 应被去掉", old)
		}
	}
}

// 用官方客户端解析生成的整份配置(设置 MPX_TEST_MIHOMO / MPX_TEST_SINGBOX 才跑)。
func TestRoutingOfficialParsers(t *testing.T) {
	mihomo, singbox := os.Getenv("MPX_TEST_MIHOMO"), os.Getenv("MPX_TEST_SINGBOX")
	if mihomo == "" || singbox == "" {
		t.Skip("set MPX_TEST_MIHOMO and MPX_TEST_SINGBOX to official binaries")
	}
	eps := routingEndpoints()
	p := seedPlan(t, eps)
	p.Rules = append([]model.RouteRule{{Type: model.RuleKeyword, Value: "steam", Target: model.TargetReject}, {Type: model.RuleCIDR, Value: "91.108.4.0/22", Target: "group:1"}, {Type: model.RuleGeoIP, Value: "JP", Target: model.TargetDirect}}, p.Rules...)
	p.Groups = append(p.Groups, RoutingGroup{ProxyGroup: model.ProxyGroup{ID: 9, Name: "空组", Mode: model.GroupAuto, TestURL: "https://www.gstatic.com/generate_204", Interval: 300}})
	for _, f := range []model.ClientFormat{model.FormatClashMeta, model.FormatSingBox} {
		var b []byte
		var err error
		if f == model.FormatClashMeta {
			b, err = ClashRouting(eps, p, nil)
		} else {
			b, err = SingBoxRouting(eps, p, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		file := filepath.Join(dir, "config")
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(singbox, "check", "-c", file)
		if f == model.FormatClashMeta {
			cmd = exec.Command(mihomo, "-t", "-d", dir, "-f", file)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", f, err, out)
		}
	}
}
