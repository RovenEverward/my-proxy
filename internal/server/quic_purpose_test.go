package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"gopkg.in/yaml.v3"
)

func TestQUICPurposeHTTPMixedEligibility(t *testing.T) {
	e := newEnv(t)
	dailyNode := e.createNode("daily-xray", "daily.example.com")
	dailyIn := e.createInbound(dailyNode.ID, "daily", 443)
	dailyHost := e.createHost(dailyIn.ID, "Daily-Xray", "daily.example.com")
	type entrance struct {
		node model.Node
		in   model.Inbound
		host model.Host
	}
	entries := []entrance{{dailyNode, dailyIn, dailyHost}}
	for idx, p := range []model.Protocol{model.ProtocolHysteria2, model.ProtocolTUIC} {
		n := e.createNode(string(p), string(p)+".example.com")
		code, b := e.api("POST", "/api/inbounds", model.Inbound{NodeID: n.ID, Tag: string(p), Protocol: p, Port: 8443 + idx, CertFile: "/cert.pem", KeyFile: "/key.pem", SNI: "quic.example.com", ALPN: []string{"h3"}})
		if code != 201 {
			t.Fatalf("QUIC create %d %s", code, b)
		}
		in := decodeJSON[model.Inbound](t, b)
		h := e.createHost(in.ID, string(p), n.Address)
		entries = append(entries, entrance{n, in, h})
	}
	// Same inbound, different host identity: no observations for this exposure.
	unknownHost := e.createHost(entries[1].in.ID, "Unobserved-HY2", "unobserved.example.com")
	denied := e.createNode("denied", "denied.example.com")
	deniedIn := e.createInbound(denied.ID, "denied", 443)
	e.createHost(deniedIn.ID, "Denied", denied.Address)
	u := e.createUser("quic-purpose")
	e.updateUser(u.Username, func(v *model.User) { v.InboundIDs = []int64{dailyIn.ID, entries[1].in.ID, entries[2].in.ID} })
	user, err := e.st.GetUser(u.Username)
	if err != nil {
		t.Fatal(err)
	}
	daily := model.Purpose{Slug: "mixed-daily", Name: "Daily", Kind: "daily", Tags: []string{"daily"}, RequiredCapabilities: []string{"egress"}, Strategy: "select"}
	media := model.Purpose{Slug: "mixed-media", Name: "Media", Kind: "media", Tags: []string{"media"}, Domains: []string{"media.test"}, DailyPurpose: daily.Slug, RequiredCapabilities: []string{"netflix"}, CapabilityRegion: "JP", Strategy: "select"}
	for _, p := range []model.Purpose{daily, media} {
		if c, b := e.api("POST", "/api/purposes", p); c != 201 {
			t.Fatalf("purpose %d %s", c, b)
		}
	}
	for idx, x := range entries {
		tag := "media"
		if idx == 0 {
			tag = "daily"
		}
		if c, b := e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", x.node.ID), model.PurposeMetadata{Purposes: []string{tag}}); c != 200 {
			t.Fatalf("metadata %d %s", c, b)
		}
	}
	results := make([]model.CapabilityResult, len(entries))
	for idx, x := range entries {
		n, _ := e.st.GetNode(x.node.ID)
		in, _ := e.st.GetInbound(x.in.ID)
		h, _ := e.st.GetHost(x.host.ID)
		target := "netflix"
		if idx == 0 {
			target = "egress"
		}
		results[idx] = model.CapabilityResult{NodeID: n.ID, InboundID: in.ID, HostID: h.ID, Target: target, Status: model.CapabilityAvailable, ObservedStatus: model.CapabilityAvailable, Consecutive: 2, Region: "JP", Fingerprint: control.CapabilityFingerprint(*n, *in, *h), CheckedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	}
	save := func() {
		t.Helper()
		if err := e.st.SaveCapabilityResults(results); err != nil {
			t.Fatal(err)
		}
	}
	save()
	seen := map[[3]int64]bool{}
	e.s.SetPurposeFilter(func(p model.Purpose, eps []render.Endpoint) ([]render.Endpoint, error) {
		for _, ep := range eps {
			seen[[3]int64{ep.NodeID, ep.InboundID, ep.HostID}] = true
			if ep.Protocol == model.ProtocolTUIC && (ep.UUID != model.DeriveUUID(user.Key) || ep.Password != model.DerivePassword(user.Key)) {
				t.Error("TUIC identity credentials changed")
			}
			if ep.Protocol == model.ProtocolHysteria2 && (ep.UUID != "" || ep.Password != model.DerivePassword(user.Key)) {
				t.Error("HY2 identity credentials changed")
			}
		}
		out, err := e.s.filterPurposeCapabilities(p, eps)
		// The server must retain original credentials when intersecting hook output.
		for idx := range out {
			out[idx].Password = "untrusted-filter-password"
			out[idx].UUID = "untrusted-filter-uuid"
		}
		return out, err
	})
	read := func(f, slug string, want int) []byte {
		t.Helper()
		code, _, b := e.sub("/sub/"+u.SubToken+"/"+f+"?purpose="+slug, "")
		if code != want {
			t.Fatalf("%s %s want%d got%d %s", f, slug, want, code, b)
		}
		if want == 422 && !strings.Contains(string(b), "/sub/"+u.SubToken) {
			t.Fatalf("missing default URL %s", b)
		}
		return b
	}
	check := func(want []string) {
		t.Helper()
		for _, f := range []string{"clash-meta", "sing-box"} {
			b := read(f, media.Slug, 200)
			if strings.Contains(string(b), denied.Address) || strings.Contains(string(b), "untrusted-filter-") {
				t.Fatalf("grant/credential leak %s", b)
			}
			cfg := quicPurposeHTTPConfig(t, f, b)
			nodesKey, tagKey, groupKey := "proxies", "name", "proxies"
			groupsKey := "proxy-groups"
			if f == "sing-box" {
				nodesKey = "outbounds"
				tagKey = "tag"
				groupKey = "outbounds"
				groupsKey = "outbounds"
			}
			found := false
			for _, raw := range cfg[groupsKey].([]any) {
				m := raw.(map[string]any)
				if m[tagKey] == "mpx-"+media.Slug {
					got := []string{}
					for _, x := range m[groupKey].([]any) {
						got = append(got, x.(string))
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s candidates got%v want%v", f, got, want)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("missing media group")
			}
			types := map[string]bool{}
			for _, raw := range cfg[nodesKey].([]any) {
				m := raw.(map[string]any)
				typ, _ := m["type"].(string)
				types[typ] = true
				if typ == "vless" && m["uuid"] != model.DeriveUUID(user.Key) {
					t.Fatal("Xray UUID changed")
				}
				if typ == "hysteria2" || typ == "tuic" {
					if m["password"] != model.DerivePassword(user.Key) {
						t.Fatalf("%s QUIC password changed", f)
					}
					if typ == "tuic" && m["uuid"] != model.DeriveUUID(user.Key) {
						t.Fatal("TUIC UUID changed")
					}
					if typ == "hysteria2" && m["uuid"] != nil {
						t.Fatal("HY2 UUID added")
					}
				}
			}
			if !types["vless"] {
				t.Fatal("daily Xray missing")
			}
			if f == "sing-box" {
				route := cfg["route"].(map[string]any)
				if route["final"] != "mpx-"+daily.Slug {
					t.Fatal(route)
				}
				rules := route["rules"].([]any)
				if rules[2].(map[string]any)["outbound"] != "mpx-"+media.Slug {
					t.Fatal("media rule missing")
				}
			} else {
				rules := cfg["rules"].([]any)
				if rules[0] != "DOMAIN-SUFFIX,media.test,mpx-"+media.Slug || !strings.Contains(rules[len(rules)-1].(string), "mpx-"+daily.Slug) {
					t.Fatal("daily/media route distinction lost")
				}
			}
		}
	}
	check([]string{"hysteria2", "tuic"})
	for _, f := range []string{"clash-meta", "sing-box"} {
		b := read(f, daily.Slug, 200)
		cfg := quicPurposeHTTPConfig(t, f, b)
		if f == "sing-box" {
			if cfg["route"].(map[string]any)["final"] != "mpx-"+daily.Slug {
				t.Fatal("direct daily subscription final lost")
			}
		} else {
			rules := cfg["rules"].([]any)
			if rules[len(rules)-1] != "MATCH,mpx-"+daily.Slug {
				t.Fatal("direct daily subscription final lost")
			}
		}
	}
	if !seen[[3]int64{entries[1].node.ID, entries[1].in.ID, entries[1].host.ID}] || !seen[[3]int64{entries[2].node.ID, entries[2].in.ID, entries[2].host.ID}] || !seen[[3]int64{entries[1].node.ID, entries[1].in.ID, unknownHost.ID}] {
		t.Fatal("endpoint identities missing")
	}
	for _, f := range []string{"clash", "xray", "links", "base64-links"} {
		b := read(f, media.Slug, 422)
		if !strings.Contains(string(b), "clash-meta / sing-box") {
			t.Fatalf("format silently lost QUIC %s", b)
		}
	}
	results[1].Status = model.CapabilityUnknown
	save()
	check([]string{"tuic"})
	results[1].Status = model.CapabilityAvailable
	results[1].ExpiresAt = time.Now().Add(-time.Second)
	save()
	check([]string{"tuic"})
	results[1].ExpiresAt = time.Now().Add(time.Hour)
	results[1].Region = "US"
	save()
	check([]string{"tuic"})
	results[1].Region = "JP"
	save()
	check([]string{"hysteria2", "tuic"})
	// Stored success becomes unknown after changing an exposure fingerprint.
	changed, _ := e.st.GetHost(entries[1].host.ID)
	changed.Address = "changed-hy2.example.com"
	if err := e.st.UpdateHost(changed); err != nil {
		t.Fatal(err)
	}
	check([]string{"tuic"})
	n, _ := e.st.GetNode(entries[1].node.ID)
	in, _ := e.st.GetInbound(entries[1].in.ID)
	results[1].Fingerprint = control.CapabilityFingerprint(*n, *in, *changed)
	save()
	check([]string{"hysteria2", "tuic"})
	e.updateUser(u.Username, func(v *model.User) { v.InboundIDs = []int64{dailyIn.ID, entries[1].in.ID} })
	check([]string{"hysteria2"})
	for _, f := range []string{"clash-meta", "sing-box"} {
		if b := read(f, media.Slug, 200); strings.Contains(string(b), entries[2].host.Address) {
			t.Fatal("revoked TUIC entrance still exported")
		}
	}
	e.updateUser(u.Username, func(v *model.User) { v.InboundIDs = []int64{dailyIn.ID, entries[1].in.ID, entries[2].in.ID} })
	if c, b := e.api("POST", fmt.Sprintf("/api/nodes/%d/drain", entries[2].node.ID), nil); c != 200 {
		t.Fatalf("drain %d %s", c, b)
	}
	check([]string{"hysteria2"})
	results[1].Status = model.CapabilityUnknown
	save()
	for _, f := range []string{"clash-meta", "sing-box"} {
		read(f, media.Slug, 422)
	}
	results[1].Status = model.CapabilityAvailable
	save()
	check([]string{"hysteria2"})
	// Daily dependency uses its own capability requirements, including live TTL.
	results[0].ExpiresAt = time.Now().Add(-time.Second)
	save()
	for _, f := range []string{"clash-meta", "sing-box"} {
		read(f, media.Slug, 422)
	}
	results[0].ExpiresAt = time.Now().Add(time.Hour)
	save()
	check([]string{"hysteria2"})
	past := time.Now().Add(-time.Hour)
	e.updateUser(u.Username, func(v *model.User) { v.ExpireAt = &past })
	for _, f := range []string{"clash-meta", "sing-box"} {
		read(f, media.Slug, 422)
	}
	e.updateUser(u.Username, func(v *model.User) { v.ExpireAt = nil; v.Enabled = false })
	read("sing-box", media.Slug, 422)
}

func quicPurposeHTTPConfig(t *testing.T, f string, b []byte) map[string]any {
	t.Helper()
	var cfg map[string]any
	var err error
	if f == "sing-box" {
		err = json.Unmarshal(b, &cfg)
	} else {
		err = yaml.Unmarshal(b, &cfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
