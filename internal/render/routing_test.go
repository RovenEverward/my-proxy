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
	return []Endpoint{{NodeID: 1, InboundID: 1, HostID: 1, Remark: "Daily", Protocol: model.ProtocolVLESS, Address: "daily.example.com", Port: 443, UUID: "11111111-1111-4111-8111-111111111111", Network: model.NetworkTCP}, {NodeID: 2, InboundID: 2, HostID: 2, Remark: "Media", Protocol: model.ProtocolVLESS, Address: "media.example.com", Port: 443, UUID: "11111111-1111-4111-8111-111111111111", Network: model.NetworkTCP}}
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

// terminal 沿 select 组的首项解析默认去向；手动切换保留为用户选节点的边界。
func (d clashDoc) terminal(name string) string {
	for i := 0; i < 20; i++ {
		if name == "手动切换" {
			return name
		}
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

// 新库的默认种子与两条 Clash 渲染路径都以手动组作为代理默认去向。
func TestDefaultManualRouting(t *testing.T) {
	eps := routingEndpoints()
	p := seedPlan(t, eps)
	var manual string
	for _, g := range p.Groups {
		if g.Name == "手动切换" && g.Mode == model.GroupManual {
			manual = model.GroupTarget(g.ID)
		}
	}
	if manual == "" || p.Final != manual {
		t.Fatalf("默认兜底 = %q，手动组 = %q", p.Final, manual)
	}
	for _, r := range p.Rules {
		if _, ok := model.TargetGroupID(r.Target); ok && r.Target != manual {
			t.Errorf("默认规则 %s 指向 %s，想要 %s", r.Value, r.Target, manual)
		}
	}
	for _, rawTemplate := range []bool{true, false} {
		name := "实际订阅"
		if rawTemplate {
			name = "模板原样输出"
		}
		t.Run(name, func(t *testing.T) {
			var raw []byte
			var err error
			if rawTemplate {
				raw, err = Clash(eps, true, nil)
			} else {
				raw, err = ClashRouting(eps, p, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			var doc clashDoc
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			manualExists := false
			for _, g := range doc.Groups {
				if g.Name == "手动切换" && g.Type == "select" {
					manualExists = true
				}
				if g.Type != "select" {
					continue
				}
				got := doc.terminal(g.Name)
				if rawTemplate {
					want := map[string]string{"广告拦截": "REJECT", "本地直连": "DIRECT", "国内媒体": "DIRECT"}[g.Name]
					if want == "" {
						want = "手动切换"
					}
					if got != want {
						t.Errorf("%s 默认到 %s，想要 %s", g.Name, got, want)
					}
				} else if got != "手动切换" && got != "DIRECT" && got != "REJECT" {
					t.Errorf("%s 默认到了非手动代理组 %s", g.Name, got)
				}
			}
			if !manualExists {
				t.Fatal("缺少手动切换 selector")
			}
			if !rawTemplate && doc.terminal(FinalGroupName) != "手动切换" {
				t.Error("订阅兜底没有默认到手动切换")
			}
		})
	}
}

// 内置 DNS 跟随真实生成的兜底选择器；名字消歧后仍有效，自定义模板不改。
func TestSingBoxRoutingDefaultDNSDetour(t *testing.T) {
	cases := []struct {
		name      string
		template  []byte
		collision bool
	}{
		{name: "默认模板"},
		{name: "兜底名称冲突", collision: true},
		{name: "自定义模板", template: []byte(`{"dns":{"servers":[{"tag":"custom","type":"udp","server":"203.0.113.53","detour":"proxy"}]}}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eps := routingEndpoints()
			if c.collision {
				eps[0].Remark = FinalGroupName
			}
			raw, err := SingBoxRouting(eps, seedPlan(t, eps), c.template)
			if err != nil {
				t.Fatal(err)
			}
			cfg := decodeConfig(t, raw)
			final := cfg["route"].(map[string]any)["final"].(string)
			if c.collision && final == FinalGroupName {
				t.Fatal("夹具没有触发兜底名称消歧")
			}
			want := final
			if c.template != nil {
				want = singboxProxyTag
			}
			count := 0
			for _, item := range cfg["dns"].(map[string]any)["servers"].([]any) {
				server := item.(map[string]any)
				if detour, ok := server["detour"]; ok {
					count++
					if detour != want {
						t.Errorf("DNS 出站 = %v，想要 %s", detour, want)
					}
				}
			}
			if count != 1 {
				t.Fatalf("DNS 代理出站数量 = %d，想要 1", count)
			}
			finalGroup := findOutbound(t, cfg, final)
			if got := finalGroup["outbounds"].([]any)[0]; got != "手动切换" {
				t.Errorf("实际兜底首项 = %v，想要手动切换", got)
			}
		})
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
