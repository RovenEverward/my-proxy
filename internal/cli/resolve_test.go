package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestPick(t *testing.T) {
	cs := []candidate{
		{ID: 1, Name: "la-1", Display: "🇺🇸 洛杉矶 A"},
		{ID: 2, Name: "jp-1", Display: "🇯🇵 东京 A"},
		{ID: 3, Name: "la-2", Display: "🇺🇸 洛杉矶 B"},
		{ID: 4, Name: "7"},
		{ID: 7, Name: "hk"},
		{ID: 8, Name: "hk-2"},
	}
	ok := []struct {
		arg    string
		want   int64
		prefix bool
	}{
		{"2", 2, false},        // 数字是 id
		{"7", 7, false},        // 数字优先当 id,即使有节点叫 "7"
		{"name:7", 4, false},   // 想按名字找就写 name:
		{"LA-1", 1, false},     // 不区分大小写
		{"洛杉矶A", 1, false},     // 展示名去国旗与空白
		{"🇺🇸 洛杉矶 b", 3, false}, // 原样展示名也行
		{"东京", 2, true},        // 唯一前缀(展示名)
		{"jp", 2, true},        // 唯一前缀(名字)
		{"hk", 7, false},       // 完全相等先于前缀:hk 不与 hk-2 歧义
	}
	for _, c := range ok {
		x, prefixed, err := pick("节点", c.arg, cs)
		if err != nil || x.ID != c.want || prefixed != c.prefix {
			t.Errorf("pick(%q) = %d prefixed=%v err=%v, want %d prefixed=%v", c.arg, x.ID, prefixed, err, c.want, c.prefix)
		}
	}
	_, _, err := pick("节点", "洛杉矶", cs)
	if err == nil || !strings.Contains(err.Error(), "匹配到 2 个节点") || !strings.Contains(err.Error(), "la-2(🇺🇸 洛杉矶 B)") {
		t.Errorf("歧义要列出候选,得到 %v", err)
	}
	for _, arg := range []string{"99", "sg", ""} {
		if _, _, err := pick("节点", arg, cs); err == nil {
			t.Errorf("pick(%q) 应当报错", arg)
		}
	}
}

func TestResolveAgainstServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/nodes":
			_ = json.NewEncoder(w).Encode([]model.NodeView{{Node: model.Node{ID: 1, Name: "la-1"}}, {Node: model.Node{ID: 3, Name: "la-2"}}, {Node: model.Node{ID: 5, Name: "jp-1"}}})
		case "/api/inbounds":
			_ = json.NewEncoder(w).Encode([]model.Inbound{
				{ID: 10, NodeID: 1, Tag: "reality"}, {ID: 11, NodeID: 3, Tag: "reality"},
				{ID: 12, NodeID: 5, Protocol: model.ProtocolVLESS, Port: 443},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "t")

	for arg, want := range map[string]int64{"la-2": 3, "JP": 5, "9": 9} {
		if id, err := resolveNodeID(arg, false); err != nil || id != want {
			t.Errorf("node %q = %d %v, want %d", arg, id, err, want)
		}
	}
	for arg, want := range map[string]int64{"la-1/reality": 10, "LA-2/real": 11, "vless-443": 12, "jp/vless": 12} {
		if id, err := resolveInboundID(arg, false); err != nil || id != want {
			t.Errorf("inbound %q = %d %v, want %d", arg, id, err, want)
		}
	}
	if _, err := resolveInboundID("reality", false); err == nil || !strings.Contains(err.Error(), "匹配到 2 个入站") {
		t.Errorf("两台节点都有 reality,只写 tag 应当歧义: %v", err)
	}
	if _, err := resolveNodeID("la", false); err == nil {
		t.Error("la 前缀匹配两台,应当歧义")
	}
}
