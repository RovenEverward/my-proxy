package reach

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestProbeMapsBackToInboundID 是 UC-12 加 TID 后出的回归:Target.Key() 是
// "inbound:<id>" 字符串身份,Probe 必须经目标表把键回译成入站号,而不是对
// 字符串身份 ParseInt —— 那一步永远失败,结果 map 全空,两侧都显成未探。
func TestProbeMapsBackToInboundID(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	targets := []Target{
		{InboundID: 7, NodeID: 1, Address: "127.0.0.1", Port: port,
			Protocol: "vless", Network: "tcp", Subject: "node:1", TID: "inbound:7"},
		{InboundID: 8, NodeID: 1, Address: "127.0.0.1", Port: port + 1,
			Protocol: "vless", Network: "tcp", Subject: "node:1", TID: "inbound:8"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := Probe(ctx, targets, Options{})
	if len(res) != 2 {
		t.Fatalf("结果数 = %d,想要 2(键回译丢了)", len(res))
	}
	if r := res[7]; r == nil || r.TCP == nil || r.TCP.Status != CellReachable {
		t.Errorf("入站 7 应是可达: %+v", r)
	}
	if r := res[8]; r == nil || r.TCP == nil || r.TCP.Status == CellReachable {
		t.Errorf("入站 8 应探测不通: %+v", r)
	}
}

// TestTrustedPathUsesCanary 验证金丝雀语义:清单里的目标能探通即路径不可信
// (被代理接管),一个都探不通才可信(ISS-14)。
func TestTrustedPathUsesCanary(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	live := ln.Addr().String()
	// 占一个本机没监听的高位端口当「不通」目标。
	dead := "127.0.0.1:1"
	ctx := context.Background()
	if trustedPath(ctx, time.Second, []string{dead, live}) {
		t.Fatal("金丝雀里有一个能探通,路径应判不可信")
	}
	if !trustedPath(ctx, time.Second, []string{dead}) {
		t.Fatal("金丝雀全探不通,路径应判可信")
	}
}
