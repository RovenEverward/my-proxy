package render

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

func TestQUICPurposeMixedRendering(t *testing.T) {
	node, ins, users := quicFixture()
	hosts := map[int64][]model.Host{ins[0].ID: {{ID: 11, InboundID: ins[0].ID, Remark: "HY2", Address: "hy2.example.com"}}, ins[1].ID: {{ID: 12, InboundID: ins[1].ID, Remark: "TUIC", Address: "tuic.example.com"}}, ins[2].ID: {{ID: 13, InboundID: ins[2].ID, Remark: "Xray", Address: "vless.example.com"}}}
	all := DedupeRemarks(Resolve(node, ins, hosts, &users[1]))
	if len(all) != 3 {
		t.Fatal("mixed protocols lost")
	}
	for idx, ep := range all {
		if ep.NodeID != node.ID || ep.InboundID != ins[idx].ID || ep.HostID != int64(11+idx) {
			t.Fatalf("identity changed %+v", ep)
		}
	}
	if all[0].Password != model.DerivePassword(users[1].Key) || all[0].UUID != "" || all[1].Password != model.DerivePassword(users[1].Key) || all[1].UUID != model.DeriveUUID(users[1].Key) || all[2].UUID != model.DeriveUUID(users[1].Key) {
		t.Fatal("mixed credentials wrong")
	}
	original := append([]Endpoint(nil), all...)
	daily := model.Purpose{Slug: "quic-daily", Name: "Daily", Kind: "daily", Strategy: "select"}
	daily.Normalize()
	media := model.Purpose{Slug: "quic-media", Name: "Media", Kind: "media", Domains: []string{"media.test"}, Strategy: "select"}
	media.Normalize()
	for _, f := range []model.ClientFormat{model.FormatClashMeta, model.FormatSingBox} {
		for _, custom := range []bool{false, true} {
			name := string(f) + map[bool]string{false: "/default", true: "/custom"}[custom]
			t.Run(name, func(t *testing.T) {
				var template []byte
				if custom {
					if f == model.FormatClashMeta {
						template = []byte("rule-providers: {}\nproxy-groups:\n  - {name: PROXY, type: select, proxies: [Xray]}\nrules:\n  - DOMAIN,local.test,DIRECT\n  - MATCH,PROXY\n")
					} else {
						template = []byte(`{"outbounds":[],"route":{"rules":[{"action":"sniff"},{"ip_is_private":true,"outbound":"direct"}],"final":"proxy"}}`)
					}
				}
				base, err := PurposeSubscription(all, all[2:], daily, f, template)
				if err != nil {
					t.Fatal(err)
				}
				body, err := AddPurposeRouting(base, all, all[:2], media, f)
				if err != nil {
					t.Fatal(err)
				}
				var cfg map[string]any
				if f == model.FormatClashMeta {
					err = yaml.Unmarshal(body, &cfg)
				} else {
					err = json.Unmarshal(body, &cfg)
				}
				if err != nil {
					t.Fatal(err)
				}
				var nodes []any
				if f == model.FormatClashMeta {
					nodes = cfg["proxies"].([]any)
				} else {
					nodes = cfg["outbounds"].([]any)
				}
				byType := map[string]map[string]any{}
				for _, v := range nodes {
					m := v.(map[string]any)
					typ, _ := m["type"].(string)
					byType[typ] = m
				}
				for _, typ := range []string{"hysteria2", "tuic", "vless"} {
					if byType[typ] == nil {
						t.Fatalf("missing actual protocol %s", typ)
					}
				}
				if byType["hysteria2"]["password"] != all[0].Password || byType["tuic"]["password"] != all[1].Password || byType["tuic"]["uuid"] != all[1].UUID || byType["vless"]["uuid"] != all[2].UUID {
					t.Fatal("serialized credentials changed")
				}
				if byType["hysteria2"]["uuid"] != nil {
					t.Fatal("HY2 emitted UUID")
				}
				groupList, tagKey, membersKey := "proxy-groups", "name", "proxies"
				if f == model.FormatSingBox {
					groupList = "outbounds"
					tagKey = "tag"
					membersKey = "outbounds"
				}
				found := map[string][]string{}
				for _, v := range cfg[groupList].([]any) {
					m := v.(map[string]any)
					tag, _ := m[tagKey].(string)
					if tag == "mpx-quic-media" || tag == "mpx-quic-daily" {
						for _, member := range m[membersKey].([]any) {
							found[tag] = append(found[tag], member.(string))
						}
					}
				}
				if !reflect.DeepEqual(found["mpx-quic-media"], []string{"HY2", "TUIC"}) || !reflect.DeepEqual(found["mpx-quic-daily"], []string{"Xray"}) {
					t.Fatalf("groups %v", found)
				}
				if f == model.FormatSingBox {
					route := cfg["route"].(map[string]any)
					if route["final"] != "mpx-quic-daily" {
						t.Fatal(route)
					}
				} else {
					rules := cfg["rules"].([]any)
					if rules[0] != "DOMAIN-SUFFIX,media.test,mpx-quic-media" || rules[len(rules)-1] != "MATCH,mpx-quic-daily" {
						t.Fatal("wrong purpose routes")
					}
				}
				if !reflect.DeepEqual(all, original) {
					t.Fatal("render mutated endpoint identity/credentials")
				}
				// Each format runs assertions without binaries; official checks are opt-in.
				envName := "MPX_TEST_MIHOMO"
				if f == model.FormatSingBox {
					envName = "MPX_TEST_SINGBOX"
				}
				binary := os.Getenv(envName)
				if binary == "" {
					t.Log("official parser not configured: " + envName)
					return
				}
				dir := t.TempDir()
				file := filepath.Join(dir, "mixed-purpose.config")
				if err := os.WriteFile(file, body, 0600); err != nil {
					t.Fatal(err)
				}
				var cmd *exec.Cmd
				if f == model.FormatClashMeta {
					cmd = exec.Command(binary, "-t", "-d", dir, "-f", file)
				} else {
					cmd = exec.Command(binary, "check", "-c", file)
				}
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("official mixed-purpose parser %v\n%s", err, output)
				}
			})
		}
	}
	for _, f := range []model.ClientFormat{model.FormatClash, model.FormatXray, model.FormatLinks, model.FormatBase64} {
		if _, err := PurposeSubscription(all, all[:2], media, f, nil); err == nil {
			t.Fatalf("unsupported purpose format %s accepted", f)
		}
	}
}
