package render

import (
	"encoding/json"
	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func purposeEndpoints() []Endpoint {
	return []Endpoint{{NodeID: 1, InboundID: 1, HostID: 1, Remark: "Daily", Protocol: model.ProtocolVLESS, Address: "daily.example.com", Port: 443, UUID: "11111111-1111-4111-8111-111111111111", Network: model.NetworkTCP}, {NodeID: 2, InboundID: 2, HostID: 2, Remark: "Media", Protocol: model.ProtocolVLESS, Address: "media.example.com", Port: 443, UUID: "11111111-1111-4111-8111-111111111111", Network: model.NetworkTCP}}
}
func TestPurposeCustomTemplateDailyRouting(t *testing.T) {
	all := purposeEndpoints()
	p := model.Purpose{Slug: "daily", Name: "Daily", Kind: "daily"}
	p.Normalize()
	clash := []byte("rule-providers: {}\nproxy-groups:\n  - name: PINNED\n    type: select\n    proxies: [Media]\nrules:\n  - DOMAIN-SUFFIX,example.org,PINNED\n  - GEOIP,CN,DIRECT,no-resolve\n  - MATCH,PINNED\n")
	b, err := PurposeSubscription(all, all[1:], p, model.FormatClashMeta, clash)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	r := cfg["rules"].([]any)
	if r[0] != "DOMAIN-SUFFIX,example.org,mpx-daily" || r[1] != "GEOIP,CN,DIRECT,no-resolve" || r[2] != "MATCH,mpx-daily" {
		t.Fatal(r)
	}
	for _, g := range cfg["proxy-groups"].([]any) {
		m := g.(map[string]any)
		if m["name"] == "mpx-daily" {
			names := m["proxies"].([]any)
			if len(names) != 1 || names[0] != "Media" {
				t.Fatal(m)
			}
		}
	}
	sb := []byte(`{"route":{"final":"proxy","rules":[{"type":"logical","mode":"or","rules":[{"domain_suffix":["example.org"]}],"outbound":"proxy"},{"ip_is_private":true,"outbound":"direct"}]},"outbounds":[]}`)
	b, err = PurposeSubscription(all, all[1:], p, model.FormatSingBox, sb)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	route := cfg["route"].(map[string]any)
	rules := route["rules"].([]any)
	if route["final"] != "mpx-daily" || rules[0].(map[string]any)["outbound"] != "mpx-daily" || rules[1].(map[string]any)["outbound"] != "direct" {
		t.Fatal(route)
	}
}

func TestPurposePreservesIndirectDecisions(t *testing.T) {
	all := purposeEndpoints()
	p := model.Purpose{Slug: "daily", Name: "Daily", Kind: "daily"}
	p.Normalize()
	clash := []byte("rule-providers: {}\nproxy-groups:\n  - {name: LAN, type: select, proxies: [DIRECT, Daily]}\n  - {name: NestedLAN, type: select, proxies: [LAN, Media]}\n  - {name: Ads, type: select, proxies: [REJECT, DIRECT]}\n  - {name: PROXY, type: select, proxies: [Daily]}\n  - {name: CycleA, type: select, proxies: [CycleB]}\n  - {name: CycleB, type: select, proxies: [CycleA]}\nsub-rules:\n  reject-special:\n    - MATCH,Ads\n  proxy-special:\n    - MATCH,PROXY\nrules:\n  - DOMAIN,lan.test,NestedLAN\n  - DOMAIN,ads.test,Ads\n  - DOMAIN,cycle.test,CycleA\n  - SUB-RULE,(NETWORK,TCP),reject-special\n  - MATCH,PROXY\n")
	b, err := PurposeSubscription(all, all[:1], p, model.FormatClashMeta, clash)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	want := []string{"DOMAIN,lan.test,NestedLAN", "DOMAIN,ads.test,Ads", "DOMAIN,cycle.test,CycleA", "SUB-RULE,(NETWORK,TCP),reject-special", "MATCH,mpx-daily"}
	for i, r := range cfg["rules"].([]any) {
		if r != want[i] {
			t.Fatalf("rule %d %v", i, r)
		}
	}
	subs := cfg["sub-rules"].(map[string]any)
	if subs["reject-special"].([]any)[0] != "MATCH,Ads" || subs["proxy-special"].([]any)[0] != "MATCH,mpx-daily" {
		t.Fatal(subs)
	}
	sb := []byte(`{"outbounds":[{"type":"selector","tag":"LAN","outbounds":["proxy","direct"],"default":"direct"},{"type":"selector","tag":"NestedLAN","outbounds":["LAN","proxy"]},{"type":"selector","tag":"Ads","outbounds":["block","proxy"]}],"route":{"final":"NestedLAN","rules":[{"domain":["lan.test"],"outbound":"NestedLAN"},{"domain":["ads.test"],"outbound":"Ads"},{"domain":["ordinary.test"],"outbound":"proxy"}]}}`)
	b, err = PurposeSubscription(all, all[:1], p, model.FormatSingBox, sb)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	route := cfg["route"].(map[string]any)
	if route["final"] != "NestedLAN" {
		t.Fatal(route)
	}
	rules := route["rules"].([]any)
	for i, w := range []string{"NestedLAN", "Ads", "mpx-daily"} {
		if rules[i].(map[string]any)["outbound"] != w {
			t.Fatal(rules)
		}
	}
	for _, final := range []string{"direct", "block"} {
		raw := []byte(`{"outbounds":[],"route":{"final":"` + final + `"}}`)
		b, err = PurposeSubscription(all, all[:1], p, model.FormatSingBox, raw)
		if err != nil {
			t.Fatal(err)
		}
		json.Unmarshal(b, &cfg)
		if cfg["route"].(map[string]any)["final"] != final {
			t.Fatal(string(b))
		}
	}
}
func TestPurposeDefaultDirectAndSniffOrdering(t *testing.T) {
	all := purposeEndpoints()
	p := model.Purpose{Slug: "daily", Name: "Daily", Kind: "daily"}
	p.Normalize()
	for _, f := range []model.ClientFormat{model.FormatClashMeta, model.FormatSingBox} {
		b, err := PurposeSubscription(all, all[:1], p, f, nil)
		if err != nil {
			t.Fatal(err)
		}
		var c map[string]any
		if f == model.FormatClashMeta {
			yaml.Unmarshal(b, &c)
			foundLAN, foundAds := false, false
			for _, raw := range c["rules"].([]any) {
				r := raw.(string)
				if r == "GEOIP,CN,本地直连" {
					foundLAN = true
				}
				if strings.HasSuffix(r, ",广告拦截") {
					foundAds = true
				}
			}
			if !foundLAN || !foundAds {
				t.Fatal("default direct/block wrappers lost")
			}
		} else {
			json.Unmarshal(b, &c)
			route := c["route"].(map[string]any)
			direct := false
			for _, raw := range route["rules"].([]any) {
				if raw.(map[string]any)["outbound"] == "本地直连" {
					direct = true
				}
			}
			if !direct {
				t.Fatal("default direct wrapper lost")
			}
			media := model.Purpose{Slug: "media", Name: "Media", Kind: "media", Domains: []string{"media.test"}}
			media.Normalize()
			b, err = AddPurposeRouting(b, all, all[1:], media, f)
			if err != nil {
				t.Fatal(err)
			}
			json.Unmarshal(b, &c)
			rules := c["route"].(map[string]any)["rules"].([]any)
			purposeAt := -1
			preAt := -1
			for i, r := range rules {
				m := r.(map[string]any)
				if m["action"] == "sniff" || m["action"] == "hijack-dns" {
					preAt = i
				}
				if m["outbound"] == "mpx-media" {
					purposeAt = i
				}
			}
			if purposeAt <= preAt {
				t.Fatalf("purpose %d before preprocessing %d", purposeAt, preAt)
			}
		}
	}
}

func TestPurposePrecedesTerminalRuleWithLaterDNS(t *testing.T) {
	all := purposeEndpoints()
	p := model.Purpose{Slug: "media", Name: "Media", Kind: "media", Domains: []string{"media.test"}}
	p.Normalize()
	template := []byte(`{"outbounds":[],"route":{"rules":[{"action":"sniff"},{"domain":["media.test"],"outbound":"proxy"},{"protocol":"dns","action":"hijack-dns"}],"final":"proxy"}}`)
	b, err := PurposeSubscription(all, all[1:], p, model.FormatSingBox, template)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	rules := cfg["route"].(map[string]any)["rules"].([]any)
	if rules[0].(map[string]any)["action"] != "sniff" || rules[1].(map[string]any)["outbound"] != "mpx-media" || rules[2].(map[string]any)["outbound"] != "proxy" || rules[3].(map[string]any)["action"] != "hijack-dns" {
		t.Fatal(rules)
	}
}

func TestPurposeOfficialPreservationParsers(t *testing.T) {
	mihomo, singbox := os.Getenv("MPX_TEST_MIHOMO"), os.Getenv("MPX_TEST_SINGBOX")
	if mihomo == "" || singbox == "" {
		t.Skip("official binaries required")
	}
	all := purposeEndpoints()
	p := model.Purpose{Slug: "daily", Name: "Daily", Kind: "daily"}
	p.Normalize()
	for _, tc := range []struct {
		f        model.ClientFormat
		template string
	}{
		{model.FormatClashMeta, "rule-providers: {}\nproxy-groups:\n  - {name: LAN, type: select, proxies: [DIRECT, Daily]}\n  - {name: NestedLAN, type: select, proxies: [LAN, Media]}\n  - {name: Ads, type: select, proxies: [REJECT, DIRECT]}\n  - {name: PROXY, type: select, proxies: [Daily]}\nsub-rules:\n  reject-special:\n    - MATCH,Ads\n  proxy-special:\n    - MATCH,PROXY\nrules:\n  - DOMAIN,lan.test,NestedLAN\n  - DOMAIN,ads.test,Ads\n  - SUB-RULE,(NETWORK,TCP),reject-special\n  - MATCH,PROXY\n"},
		{model.FormatSingBox, `{"outbounds":[{"type":"selector","tag":"LAN","outbounds":["proxy","direct"],"default":"direct"},{"type":"selector","tag":"NestedLAN","outbounds":["LAN","proxy"]},{"type":"selector","tag":"Ads","outbounds":["block","proxy"]}],"route":{"final":"NestedLAN","rules":[{"domain":["lan.test"],"outbound":"NestedLAN"},{"domain":["ads.test"],"outbound":"Ads"},{"domain":["ordinary.test"],"outbound":"proxy"}]}}`},
		{model.FormatSingBox, `{"outbounds":[],"route":{"final":"direct"}}`},
	} {
		b, err := PurposeSubscription(all, all[:1], p, tc.f, []byte(tc.template))
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		file := filepath.Join(dir, "config")
		if err = os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
		var cmd *exec.Cmd
		if tc.f == model.FormatClashMeta {
			cmd = exec.Command(mihomo, "-t", "-d", dir, "-f", file)
		} else {
			cmd = exec.Command(singbox, "check", "-c", file)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", tc.f, err, out)
		}
	}
}

// Opt-in official parsers validate generated whole configs, including custom templates.
func TestPurposeOfficialParsers(t *testing.T) {
	mihomo, singbox := os.Getenv("MPX_TEST_MIHOMO"), os.Getenv("MPX_TEST_SINGBOX")
	if mihomo == "" || singbox == "" {
		t.Skip("set MPX_TEST_MIHOMO and MPX_TEST_SINGBOX to official binaries")
	}
	all := purposeEndpoints()
	for _, kind := range []string{"daily", "media", "download", "game"} {
		for _, custom := range []bool{false, true} {
			for _, f := range []model.ClientFormat{model.FormatClashMeta, model.FormatSingBox} {
				t.Run(kind+"/"+string(f)+"/"+map[bool]string{false: "default", true: "custom"}[custom], func(t *testing.T) {
					p := model.Purpose{Slug: kind, Name: kind, Kind: kind, Domains: []string{"netflix.com"}, Strategy: "url-test"}
					p.Normalize()
					var template []byte
					if custom {
						if f == model.FormatClashMeta {
							template = []byte("rule-providers: {}\nproxy-groups:\n  - name: PROXY\n    type: select\n    include-all-proxies: true\nrules:\n  - MATCH,PROXY\n")
						} else {
							template = []byte(`{"outbounds":[],"route":{"final":"proxy","rules":[]}}`)
						}
					}
					// Non-daily parser cases include a real distinct daily fallback layer.
					if kind != "daily" {
						daily := model.Purpose{Slug: "daily", Name: "Daily", Kind: "daily", Strategy: "select"}
						daily.Normalize()
						base, e := PurposeSubscription(all, all[:1], daily, f, template)
						if e != nil {
							t.Fatal(e)
						}
						template = base
					}
					var b []byte
					var err error
					if kind != "daily" {
						b, err = AddPurposeRouting(template, all, all[1:], p, f)
					} else {
						b, err = PurposeSubscription(all, all[1:], p, f, template)
					}
					if err != nil {
						t.Fatal(err)
					}
					dir := t.TempDir()
					file := filepath.Join(dir, "config")
					if err = os.WriteFile(file, b, 0600); err != nil {
						t.Fatal(err)
					}
					var cmd *exec.Cmd
					if f == model.FormatClashMeta {
						cmd = exec.Command(mihomo, "-t", "-d", dir, "-f", file)
					} else {
						cmd = exec.Command(singbox, "check", "-c", file)
					}
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("%v\n%s", err, output)
					}
				})
			}
		}
	}
}
