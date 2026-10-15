package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestAutoLocateOnCreate(t *testing.T) {
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1.2.3.4", "/1.2.3.5":
			fmt.Fprint(w, `{"ip":"x","success":true,"country_code":"US","city":"Los Angeles"}`)
		default:
			fmt.Fprint(w, `{"success":false,"message":"reserved range"}`)
		}
	}))
	defer geo.Close()
	old := geoBaseURL
	geoBaseURL = geo.URL + "/"
	defer func() { geoBaseURL = old }()

	e := newEnv(t)
	create := func(name, addr string) (int, map[string]any) {
		code, raw := e.api("POST", "/api/nodes", model.Node{Name: name, Address: addr})
		var got map[string]any
		json.Unmarshal(raw, &got)
		return code, got
	}
	// 关着(New 的默认):不出网,不带提示。
	if code, got := create("off", "1.2.3.4"); code != 201 || got["display"] != nil || got["locate_note"] != nil {
		t.Fatalf("off: %d %v", code, got)
	}
	e.s.autoLocate = true
	if code, got := create("la-1", "1.2.3.5"); code != 201 || got["display"] != "🇺🇸 洛杉矶 A" {
		t.Fatalf("auto: %d %v", code, got)
	}
	// 失败不阻塞建节点,只给提示。
	if code, got := create("bad", "10.0.0.1"); code != 201 || !strings.Contains(fmt.Sprint(got["locate_note"]), "自动识别地区失败") {
		t.Fatalf("fail: %d %v", code, got)
	}
	if code, got := create("dom", "la.example.com"); code != 201 || !strings.Contains(fmt.Sprint(got["locate_note"]), "node locate") {
		t.Fatalf("domain: %d %v", code, got)
	}
	// 手动设过地区的不覆盖。
	_, raw := e.api("POST", "/api/nodes", model.Node{Name: "manual", Address: "203.0.113.9"})
	var n model.Node
	json.Unmarshal(raw, &n)
	e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", n.ID), map[string]any{"region": "JP", "city": "东京"})
	n.Address = "1.2.3.4"
	if d, _ := e.s.autoLocateNode(context.Background(), n); d != "🇯🇵 东京 A" {
		t.Fatalf("manual overwritten: %q", d)
	}
}
