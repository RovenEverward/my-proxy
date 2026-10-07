package render

import (
	"reflect"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 对外入口留空的字段一律回落到节点或入站上的值 —— 这张表就是那份回落规则的依据。
func TestResolveExposure(t *testing.T) {
	node := model.Node{Name: "hk-1", Address: "203.0.113.9"}
	in := model.Inbound{
		Port:    8443,
		Network: model.NetworkWS,
		WSPath:  "/ws",
		SNI:     "node.example.com",
		ALPN:    []string{"h2", "http/1.1"},
	}

	got := ResolveExposure(node, in, model.Host{Remark: "只用备注"})
	want := model.Exposure{
		Address: "203.0.113.9",
		Port:    8443,
		SNI:     "node.example.com",
		Path:    "/ws",
		ALPN:    []string{"h2", "http/1.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("入口留空时每一项都该回落\n got %+v\nwant %+v", got, want)
	}

	got = ResolveExposure(node, in, model.Host{
		Address: "cdn.example.com",
		Port:    443,
		SNI:     "cdn.example.com",
		Host:    "cdn.example.com",
		Path:    "/cdn",
		ALPN:    []string{"h2"},
	})
	want = model.Exposure{
		Address: "cdn.example.com",
		Port:    443,
		SNI:     "cdn.example.com",
		Host:    "cdn.example.com",
		Path:    "/cdn",
		ALPN:    []string{"h2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("入口填了的该盖住入站上的值\n got %+v\nwant %+v", got, want)
	}
}

// Host 头没有可回落的地方:入站上没有这个概念,空就是不发。
func TestResolveExposureLeavesHostHeaderEmpty(t *testing.T) {
	got := ResolveExposure(
		model.Node{Address: "203.0.113.9"},
		model.Inbound{Port: 443, WSPath: "/ws"},
		model.Host{},
	)
	if got.Host != "" {
		t.Errorf("Host 头该是空,got %q", got.Host)
	}
}

// 路径来自入站上的哪个字段,取决于传输:grpc 用 serviceName,不是 path。
func TestResolveExposureGRPCUsesServiceName(t *testing.T) {
	in := model.Inbound{Network: model.NetworkGRPC, Port: 443, GRPCService: "grpc-svc", WSPath: "/ws"}

	if got := ResolveExposure(model.Node{}, in, model.Host{}).Path; got != "grpc-svc" {
		t.Errorf("grpc 的路径该用 serviceName,got %q", got)
	}
	if got := ResolveExposure(model.Node{}, in, model.Host{Path: "/override"}).Path; got != "/override" {
		t.Errorf("入口给了路径就该用它,got %q", got)
	}
}
