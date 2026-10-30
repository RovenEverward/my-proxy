package netutil

import (
	"net"
	"testing"
)

func TestBracketHost(t *testing.T) {
	cases := map[string]string{
		"2001:db8::1":      "[2001:db8::1]",
		"[2001:db8::1]":    "[2001:db8::1]",
		"::ffff:192.0.2.1": "[::ffff:192.0.2.1]",
		"fe80::1%eth0":     "[fe80::1%eth0]",
		"192.0.2.1":        "192.0.2.1",
		"node.example.com": "node.example.com",
		"":                 "",
	}
	for in, want := range cases {
		if got := BracketHost(in); got != want {
			t.Errorf("BracketHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinHostPort(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"2001:db8::1", 443, "[2001:db8::1]:443"},
		{"[2001:db8::1]", 443, "[2001:db8::1]:443"},
		{"192.0.2.1", 8443, "192.0.2.1:8443"},
		{"node.example.com", 443, "node.example.com:443"},
	}
	for _, tc := range cases {
		got := JoinHostPort(tc.host, tc.port)
		if got != tc.want {
			t.Errorf("JoinHostPort(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}

	// 验收的硬条件:字面 IPv6 进 URI 后必须能被 net.SplitHostPort 拆回。
	host, port, err := net.SplitHostPort(JoinHostPort("2001:db8::1", 443))
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if host != "2001:db8::1" || port != "443" {
		t.Errorf("SplitHostPort = (%q, %q), want (2001:db8::1, 443)", host, port)
	}
}
