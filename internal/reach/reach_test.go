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

// TestQUICVNProbeElicitsReachable:模拟 QUIC 栈对带不支持版本的 Initial
// 回 Version Negotiation,探针应判 reachable —— 这是 TUIC/hy2 唯一的
// 探活信号,回包内容不解析,有包回就算。
func TestQUICVNProbeElicitsReachable(t *testing.T) {
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		buf := make([]byte, 2048)
		for i := 0; i < 2; i++ {
			n, addr, err := ln.ReadFrom(buf)
			if err != nil {
				return
			}
			if buf[0]&0x80 == 0 || len(buf) < 6 {
				continue // 不是 long header,不理
			}
			// Version Negotiation:long header + version 0 + 我们发的 DCID + 支持版本列表
			resp := append([]byte{0xC0, 0, 0, 0, 0}, buf[6:14]...)
			resp = append(resp, 0x01, 0x00, 0x00, 0x01)
			ln.WriteTo(resp, addr)
			_ = n
		}
	}()
	tgt := Target{Address: "127.0.0.1", Port: ln.LocalAddr().(*net.UDPAddr).Port, Protocol: "tuic"}
	c := probeUDP(context.Background(), tgt, 1500*time.Millisecond, 1500*time.Millisecond)
	if c.Status != CellReachable {
		t.Fatalf("QUIC VN 探活 = %q (%s), 想要 reachable", c.Status, c.Error)
	}
	if c.LatencyMs < 0 {
		t.Fatal("reachable 没带时延")
	}
}

// TestUDPProbeNonQUICStaysUnknown:非 QUIC 目标(如网络层 wireguard)不
// 冒充 QUIC 协议,照旧发通用负载,静默端照旧 unknown。
func TestUDPProbeNonQUICStaysUnknown(t *testing.T) {
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	tgt := Target{Address: "127.0.0.1", Port: ln.LocalAddr().(*net.UDPAddr).Port, Protocol: "wireguard"}
	c := probeUDP(context.Background(), tgt, 400*time.Millisecond, 300*time.Millisecond)
	if c.Status != CellUnknown {
		t.Fatalf("非 QUIC 静默端 = %q, 想要 unknown", c.Status)
	}
}

// TestSSNowTakesTCPCell:shadowsocks 从 UDP 类挪出,判定看 TCP 格——
// 真机 UDP 格必然 unknown(服务端不认随机负载),吃 UDP 格会让双不通
// 的死节点永远 unknown、永远剔除不了。
func TestSSNowTakesTCPCell(t *testing.T) {
	tgt := Target{Protocol: "shadowsocks", InboundID: 1}
	if tgt.udp() {
		t.Fatal("shadowsocks 不该走 UDP 判定格")
	}
}
