package server

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 订阅里默认名换成显示名,多入口加协议后缀;host 手写的 remark 不变。
func TestSubscriptionUsesNodeDisplay(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("la-1", "la.example.com")
	e.createInbound(n.ID, "ws-a", 8443)
	e.createInbound(n.ID, "ws-b", 8444)
	in3 := e.createInbound(n.ID, "ws-c", 8445)
	e.createHost(in3.ID, "{NODE_DISPLAY} 手写", "la.example.com")
	user := e.createUser("alice")
	if code, b := e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", n.ID), model.PurposeMetadata{Region: "US", City: "洛杉矶"}); code != 200 {
		t.Fatalf("metadata %d %s", code, b)
	}
	code, _, body := e.sub("/sub/"+user.SubToken+"/links", "v2rayng/1.8")
	if code != 200 {
		t.Fatalf("sub %d %s", code, body)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if i := strings.LastIndex(line, "#"); i >= 0 {
			s, _ := url.PathUnescape(line[i+1:])
			names = append(names, s)
		}
	}
	want := []string{"🇺🇸 洛杉矶 A · WS", "🇺🇸 洛杉矶 A · WS 2", "🇺🇸 洛杉矶 A 手写"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("names %q, want %q", names, want)
	}
}
