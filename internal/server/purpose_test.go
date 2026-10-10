package server

import (
	"encoding/json"
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestPurposeHTTPRoutingAndLifecycle(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("daily", "daily.example.com")
	in := e.createInbound(n.ID, "daily", 443)
	h := e.createHost(in.ID, "Daily", "daily.example.com")
	n2 := e.createNode("media", "media.example.com")
	in2 := e.createInbound(n2.ID, "media", 443)
	h2 := e.createHost(in2.ID, "Media", "media.example.com")
	secret := e.createNode("secret", "secret.example.com")
	e.createInbound(secret.ID, "secret", 443)
	u := e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.InboundIDs = []int64{in.ID, in2.ID} })
	p := model.Purpose{Slug: "media", Name: "流媒体", Kind: "media", Tags: []string{"media"}, Domains: []string{"netflix.com"}, Strategy: "select"}
	if code, b := e.request("POST", "/api/purposes", p, nil); code != 401 {
		t.Fatalf("auth %d %s", code, b)
	}
	if code, b := e.api("POST", "/api/purposes", p); code != 201 {
		t.Fatalf("create %d %s", code, b)
	}
	if code, _ := e.api("POST", "/api/purposes", p); code != 409 {
		t.Fatal(code)
	}
	if code, b := e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", n2.ID), model.PurposeMetadata{Region: "US", Line: "premium", Purposes: []string{"media"}}); code != 200 {
		t.Fatalf("metadata %d %s", code, b)
	}
	path := "/sub/" + u.SubToken
	for _, f := range []string{"clash-meta", "sing-box"} {
		code, _, b := e.sub(path+"/"+f+"?purpose=media", "")
		if code != 200 {
			t.Fatalf("%s %d %s", f, code, b)
		}
		if strings.Contains(string(b), "secret.example.com") {
			t.Fatal("authorization bypass")
		}
		if f == "clash-meta" {
			var cfg map[string]any
			if err := yaml.Unmarshal(b, &cfg); err != nil {
				t.Fatal(err)
			}
			rules := cfg["rules"].([]any)
			if rules[0] != "DOMAIN-SUFFIX,netflix.com,mpx-media" {
				t.Fatal(rules)
			}
			for _, g := range cfg["proxy-groups"].([]any) {
				m := g.(map[string]any)
				if m["name"] == "mpx-media" {
					if fmt.Sprint(m["proxies"]) != "[Media]" {
						t.Fatal(m)
					}
				}
			}
			if strings.Contains(fmt.Sprint(rules[len(rules)-1]), "mpx-media") {
				t.Fatal("ordinary traffic entered media")
			}
		} else {
			var cfg map[string]any
			if err := json.Unmarshal(b, &cfg); err != nil {
				t.Fatal(err)
			}
			r := cfg["route"].(map[string]any)
			first := r["rules"].([]any)[0].(map[string]any)
			if first["outbound"] != "mpx-media" || r["final"] == "mpx-media" {
				t.Fatal(r)
			}
			for _, o := range cfg["outbounds"].([]any) {
				m := o.(map[string]any)
				if m["tag"] == "mpx-media" && fmt.Sprint(m["outbounds"]) != "[Media]" {
					t.Fatal(m)
				}
			}
		}
	}
	// Explicit daily fallback differentiates normal and media traffic in one config.
	daily := model.Purpose{Slug: "daily", Name: "日常", Kind: "daily", Tags: []string{"daily"}, Strategy: "select"}
	if code, b := e.api("POST", "/api/purposes", daily); code != 201 {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ := e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", n.ID), model.PurposeMetadata{Purposes: []string{"daily"}}); code != 200 {
		t.Fatal(code)
	}
	p.DailyPurpose = "daily"
	if code, b := e.api("PUT", "/api/purposes/media", p); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	for _, f := range []string{"clash-meta", "sing-box"} {
		code, _, b := e.sub(path+"/"+f+"?purpose=media", "")
		if code != 200 {
			t.Fatalf("combined %d %s", code, b)
		}
		if f == "clash-meta" {
			var cfg map[string]any
			_ = yaml.Unmarshal(b, &cfg)
			rules := cfg["rules"].([]any)
			if rules[0] != "DOMAIN-SUFFIX,netflix.com,mpx-media" || rules[len(rules)-1] != "MATCH,mpx-daily" {
				t.Fatal(rules)
			}
		} else {
			var cfg map[string]any
			_ = json.Unmarshal(b, &cfg)
			route := cfg["route"].(map[string]any)
			if route["final"] != "mpx-daily" || route["rules"].([]any)[0].(map[string]any)["outbound"] != "mpx-media" {
				t.Fatal(route)
			}
		}
	}
	if code, _ := e.api("DELETE", "/api/purposes/daily", nil); code != 204 {
		t.Fatal(code)
	}
	if code, _, b := e.sub(path+"/sing-box?purpose=media", ""); code != 422 || !strings.Contains(string(b), "日常用途 daily 不存在或已删除") {
		t.Fatalf("missing fallback %d %s", code, b)
	}
	p.DailyPurpose = ""
	if code, _ := e.api("PUT", "/api/purposes/media", p); code != 200 {
		t.Fatal(code)
	}
	if code, b := e.api("GET", "/api/purposes/media/preview/alice/sing-box", nil); code != 200 || !strings.Contains(string(b), "mpx-media") {
		t.Fatalf("preview %d %s", code, b)
	}
	code, b := e.request("GET", path+"?purpose=media", nil, map[string]string{"Accept": "text/html"})
	if code != 200 || !strings.Contains(string(b), "选择用途") || !strings.Contains(string(b), "?purpose=media") || !strings.Contains(string(b), "HTTP 延迟") {
		t.Fatalf("page %d %s", code, b)
	}
	if code, _, _ := e.sub(path+"/links?purpose=media", ""); code != 422 {
		t.Fatal(code)
	}
	// Host override takes priority; no candidates must not silently fall back.
	if code, _ := e.api("PUT", fmt.Sprintf("/api/hosts/%d/metadata", h2.ID), model.PurposeMetadata{Purposes: []string{"daily"}}); code != 200 {
		t.Fatal(code)
	}
	if code, _, b := e.sub(path+"/clash-meta?purpose=media", ""); code != 422 || !strings.Contains(string(b), "默认订阅:") {
		t.Fatalf("empty %d %s", code, b)
	}
	if code, _ := e.api("PUT", fmt.Sprintf("/api/hosts/%d/metadata", h.ID), model.PurposeMetadata{Purposes: []string{"media"}}); code != 200 {
		t.Fatal(code)
	}
	if code, _, b := e.sub(path+"/clash-meta?purpose=media", ""); code != 200 || !strings.Contains(string(b), "- Daily") {
		t.Fatalf("metadata refresh %d %s", code, b)
	}
	p.Tags = []string{"absent"}
	if code, _ := e.api("PUT", "/api/purposes/media", p); code != 200 {
		t.Fatal(code)
	}
	if code, _, _ := e.sub(path+"/sing-box?purpose=media", ""); code != 422 {
		t.Fatal(code)
	}
	if code, _ := e.api("DELETE", "/api/purposes/media", nil); code != 204 {
		t.Fatal(code)
	}
	if code, _, b := e.sub(path+"/clash-meta?purpose=media", ""); code != 404 || !strings.Contains(string(b), "默认订阅:") {
		t.Fatalf("deleted %d %s", code, b)
	}
	if code, _, _ := e.sub(path+"/clash-meta", ""); code != 200 {
		t.Fatal(code)
	}
}
func TestPurposeCapabilitiesFailClosedAndRestrictFilter(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("daily", "daily.example.com")
	in := e.createInbound(n.ID, "daily", 443)
	u := e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.InboundIDs = []int64{in.ID} })
	p := model.Purpose{Slug: "daily", Name: "日常", Kind: "daily", RequiredCapabilities: []string{"youtube"}}
	if c, b := e.api("POST", "/api/purposes", p); c != 201 {
		t.Fatalf("%d %s", c, b)
	}
	path := "/sub/" + u.SubToken + "/sing-box?purpose=daily"
	if c, _, b := e.sub(path, ""); c != 422 || !strings.Contains(string(b), "尚未接入") {
		t.Fatalf("%d %s", c, b)
	}
	e.s.SetPurposeFilter(func(p model.Purpose, eps []render.Endpoint) ([]render.Endpoint, error) {
		return append(eps, render.Endpoint{NodeID: 999, InboundID: 999, Address: "leaked"}), nil
	})
	if c, _, b := e.sub(path, ""); c != 200 || strings.Contains(string(b), "leaked") {
		t.Fatalf("%d %s", c, b)
	}
	e.s.SetPurposeFilter(func(p model.Purpose, eps []render.Endpoint) ([]render.Endpoint, error) { return nil, nil })
	if c, _, _ := e.sub(path, ""); c != 422 {
		t.Fatalf("stale capability cache %d", c)
	}
	// Missing and invalid configurations have distinct errors.
	p.RequiredCapabilities = []string{"magic"}
	if c, _ := e.api("PUT", "/api/purposes/daily", p); c != 400 {
		t.Fatal(c)
	}
	if c, _ := e.api("PUT", "/api/nodes/9999/metadata", model.PurposeMetadata{}); c != 404 {
		t.Fatal(c)
	}
}
func TestPurposeDrainStatusAndCacheEncoding(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("one", "one.example.com")
	in := e.createInbound(n.ID, "one", 443)
	u := e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.InboundIDs = []int64{in.ID} })
	p := model.Purpose{Slug: "daily", Name: "日常", Kind: "daily"}
	if c, _ := e.api("POST", "/api/purposes", p); c != 201 {
		t.Fatal(c)
	}
	path := "/sub/" + u.SubToken + "/clash-meta"
	if c, _, _ := e.sub(path, ""); c != 200 {
		t.Fatal(c)
	}
	if c, _ := e.request("GET", path, nil, map[string]string{"Accept-Encoding": "gzip"}); c != 200 {
		t.Fatal(c)
	}
	if c, heads, b := e.sub(path, ""); c != 200 || heads.Get("Content-Encoding") != "" || !strings.Contains(string(b), "one.example.com") {
		t.Fatalf("encoding %d %v %s", c, heads, b)
	}
	if c, _, _ := e.sub(path+"?purpose=daily", ""); c != 200 {
		t.Fatal(c)
	}
	e.updateUser("alice", func(u *model.User) { u.Enabled = false })
	if c, _, b := e.sub(path+"?purpose=daily", ""); c != 422 || strings.Contains(string(b), "one.example.com") {
		t.Fatalf("disabled %d %s", c, b)
	}
	e.updateUser("alice", func(u *model.User) { u.Enabled = true })
	if c, b := e.api("POST", fmt.Sprintf("/api/nodes/%d/drain", n.ID), nil); c != 200 {
		t.Fatalf("drain %d %s", c, b)
	}
	if c, _, b := e.sub(path+"?purpose=daily", ""); c != 422 {
		t.Fatalf("drain output %d %s", c, b)
	}
}
