package render

import (
	"encoding/json"
	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
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
