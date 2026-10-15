package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestNodeDisplayLetters(t *testing.T) {
	e := newEnv(t)
	put := func(id int64, body any) (int, model.PurposeMetadata) {
		t.Helper()
		code, b := e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", id), body)
		var m model.PurposeMetadata
		json.Unmarshal(b, &m)
		return code, m
	}
	a := e.createNode("la-a", "a.example.com")
	b := e.createNode("la-b", "b.example.com")
	c := e.createNode("la-c", "c.example.com")
	if _, m := put(a.ID, map[string]any{"region": "US", "city": "洛杉矶"}); m.Letter != "A" {
		t.Fatalf("first letter %+v", m)
	}
	if _, m := put(b.ID, map[string]any{"region": "US", "city": "洛杉矶"}); m.Letter != "B" {
		t.Fatalf("second letter %+v", m)
	}
	// 显式撞字母要拒绝。
	if code, _ := put(c.ID, map[string]any{"region": "US", "city": "洛杉矶", "letter": "A"}); code != 409 {
		t.Fatalf("conflict code %d", code)
	}
	// 不同城市各自从 A 开始。
	if _, m := put(c.ID, map[string]any{"region": "US", "city": "圣何塞"}); m.Letter != "A" {
		t.Fatalf("other city %+v", m)
	}
	// 老客户端只发 region/line/purposes:城市与字母保留。
	if _, m := put(b.ID, model.PurposeMetadata{Region: "US", Line: "cn2"}); m.City != "洛杉矶" || m.Letter != "B" {
		t.Fatalf("legacy put lost fields %+v", m)
	}
	// 删掉 A 不让 B 重排;新节点补 A。
	if code, _ := e.api("DELETE", fmt.Sprintf("/api/nodes/%d", a.ID), nil); code != 204 && code != 200 {
		t.Fatalf("delete %d", code)
	}
	d := e.createNode("la-d", "d.example.com")
	if _, m := put(d.ID, map[string]any{"region": "US", "city": "洛杉矶"}); m.Letter != "A" {
		t.Fatalf("reuse letter %+v", m)
	}
	code, raw := e.api("GET", "/api/nodes", nil)
	if code != 200 {
		t.Fatal(code)
	}
	var views []model.NodeView
	json.Unmarshal(raw, &views)
	got := map[string]string{}
	for _, v := range views {
		got[v.Name] = v.Display
	}
	if got["la-b"] != "🇺🇸 洛杉矶 B" || got["la-c"] != "🇺🇸 圣何塞 A" || got["la-d"] != "🇺🇸 洛杉矶 A" {
		t.Fatalf("display %v", got)
	}
	// host 不收 city / letter。
	if code, _ := e.api("PUT", "/api/hosts/1/metadata", map[string]any{"city": "x"}); code != 400 && code != 404 {
		t.Fatalf("host city %d", code)
	}
	// 纯数字名:新建拒绝。
	if code, _ := e.api("POST", "/api/nodes", map[string]any{"name": "42", "address": "x.example.com"}); code != 400 {
		t.Fatalf("numeric name %d", code)
	}
}
