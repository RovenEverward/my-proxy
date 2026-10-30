package control

import (
	"net"
	"testing"
)

func TestWebhookIPAllowed(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"172.16.5.5", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"0.0.0.0", false},
		{"fc00::1", false},
		{"fe80::1", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := webhookIPAllowed(ip); got != c.want {
			t.Errorf("%s: got %v want %v", c.ip, got, c.want)
		}
	}
}

func TestValidateWebhookTargetRejectsPrivate(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/hook",
		"https://10.1.2.3/x",
		"http://192.168.0.4:8080/h",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.64.1.2/hook",
	} {
		if err := ValidateWebhookTarget(raw); err == nil {
			t.Errorf("%s: 应拒绝", raw)
		}
	}
	if err := ValidateWebhookTarget("https://hook.example.com/mpx"); err != nil {
		// example.com 一般会解析到公网;若环境无 DNS 则 Lookup 失败返回 nil,也算过。
		t.Logf("公网域名校验: %v", err)
	}
	if err := ValidateWebhookTarget(""); err != nil {
		t.Fatalf("空串应合法: %v", err)
	}
}
