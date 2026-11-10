package source_test

import (
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/source"
)

// 一份覆盖全部支持协议与典型跳过场景的 clash 正文。
const clashFixture = `
proxies:
  - name: vless-reality
    type: vless
    server: v.example.com
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    flow: xtls-rprx-vision
    network: tcp
    tls: true
    servername: www.microsoft.com
    client-fingerprint: chrome
    reality-opts:
      public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
      short-id: "0123abcd"
    udp: true
  - name: vmess-ws
    type: vmess
    server: vm.example.com
    port: 8443
    uuid: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
    alterId: 0
    cipher: auto
    network: ws
    tls: true
    servername: vm.example.com
    alpn: ["h2", "http/1.1"]
    ws-opts:
      path: /vm
      headers:
        Host: cdn.example.com
  - name: trojan-grpc
    type: trojan
    server: tj.example.com
    port: 443
    password: tj-pass
    network: grpc
    grpc-opts:
      grpc-service-name: svc1
  - name: ss-classic
    type: ss
    server: ss.example.com
    port: 8388
    cipher: chacha20-ietf-poly1305
    password: s3cret-pw
  - name: hy2
    type: hysteria2
    server: hy.example.com
    port: 443
    password: hy-pass
    sni: hy.example.com
    skip-cert-verify: true
    up: "100 Mbps"
    down: "200 Mbps"
  - name: tuic-node
    type: tuic
    server: tuic.example.com
    port: 4443
    uuid: "11111111-2222-3333-4444-555555555555"
    password: tuic-pw
    sni: tuic.example.com
    alpn: ["h3"]
    congestion-controller: cubic
    reduce-rtt: true
    heartbeat-interval: 8000
  - name: bad-ss-plugin
    type: ss
    server: bad.example.com
    port: 8388
    cipher: aes-256-gcm
    password: plugin-pw
    plugin: obfs
    plugin-opts: {mode: tls}
  - name: bad-ss-method
    type: ss
    server: bad2.example.com
    port: 8388
    cipher: aes-128-cfb
    password: s3cret-pw
  - name: bad-type
    type: ssr
    server: ssr.example.com
    port: 443
    cipher: none
    password: x
    protocol: origin
    obfs: plain
  - name: bad-network
    type: vless
    server: h2.example.com
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    network: h2
    tls: true
  - name: bad-detour
    type: vless
    server: chain.example.com
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    dialer-proxy: other-node
  - name: bad-vless-encryption
    type: vless
    server: enc.example.com
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    encryption: aes-128-gcm
`

// TestClashProtocols 六个协议各解一条,关键字段全部保真。
func TestClashProtocols(t *testing.T) {
	res, err := source.Parse([]byte(clashFixture))
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "clash" {
		t.Fatalf("Format = %q", res.Format)
	}
	if len(res.Endpoints) != 6 {
		t.Fatalf("Endpoints = %d: %+v", len(res.Endpoints), res.Endpoints)
	}

	v := byRemark(res.Endpoints, "vless-reality")
	if v == nil {
		t.Fatal("缺 vless-reality")
	}
	if v.Protocol != model.ProtocolVLESS || v.UUID != "11111111-2222-3333-4444-555555555555" ||
		v.Flow != "xtls-rprx-vision" || v.Network != model.NetworkTCP ||
		v.Security != model.SecurityReality || v.SNI != "www.microsoft.com" ||
		v.RealityPub != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" || v.RealitySID != "0123abcd" || v.Fingerprint != "chrome" {
		t.Errorf("vless ep = %+v", v)
	}
	if v.Port != 443 || v.Address != "v.example.com" {
		t.Errorf("vless 地址 = %+v", v)
	}

	vm := byRemark(res.Endpoints, "vmess-ws")
	if vm == nil {
		t.Fatal("缺 vmess-ws")
	}
	if vm.VMessSecurity != "auto" || vm.VMessAlterID != 0 ||
		vm.Network != model.NetworkWS || vm.Path != "/vm" || vm.Host != "cdn.example.com" ||
		vm.Security != model.SecurityTLS || vm.SNI != "vm.example.com" ||
		len(vm.ALPN) != 2 {
		t.Errorf("vmess ep = %+v", vm)
	}

	tj := byRemark(res.Endpoints, "trojan-grpc")
	if tj == nil {
		t.Fatal("缺 trojan-grpc")
	}
	// trojan 不写 tls 也是 TLS;grpc service-name 落 Path。
	if tj.Security != model.SecurityTLS || tj.Network != model.NetworkGRPC || tj.Path != "svc1" || tj.Password != "tj-pass" {
		t.Errorf("trojan ep = %+v", tj)
	}

	ss := byRemark(res.Endpoints, "ss-classic")
	if ss == nil || ss.SSMethod != "chacha20-ietf-poly1305" || ss.Password != "s3cret-pw" {
		t.Errorf("ss ep = %+v", ss)
	}

	hy := byRemark(res.Endpoints, "hy2")
	if hy == nil || hy.Protocol != model.ProtocolHysteria2 || hy.Password != "hy-pass" ||
		!hy.AllowInsecure || hy.Security != model.SecurityTLS || hy.Network != model.NetworkUDP {
		t.Errorf("hy2 ep = %+v", hy)
	}

	tu := byRemark(res.Endpoints, "tuic-node")
	if tu == nil || tu.Protocol != model.ProtocolTUIC || tu.CongestionControl != "cubic" ||
		tu.UUID == "" || tu.Password != "tuic-pw" {
		t.Errorf("tuic ep = %+v", tu)
	}
}

// TestClashSkips 表达不了的节点全部跳过并给出原因,不影响其余节点。
func TestClashSkips(t *testing.T) {
	res, err := source.Parse([]byte(clashFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad-ss-plugin", "bad-ss-method", "bad-type", "bad-network", "bad-detour", "bad-vless-encryption"} {
		is := findIssue(res.Issues, name)
		if is == nil {
			t.Errorf("%s 没有进 Issues", name)
			continue
		}
		if is.Reason == "" {
			t.Errorf("%s 原因为空", name)
		}
		if byRemark(res.Endpoints, name) != nil {
			t.Errorf("%s 不应进入 Endpoints", name)
		}
	}
	assertNoSecrets(t, res, err, "s3cret-pw", "plugin-pw", fakeUUID, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
}

// TestClashIssueIndex Issue 带条目位置,便于在来源详情里指回原条目。
func TestClashIssueIndex(t *testing.T) {
	res, err := source.Parse([]byte(clashFixture))
	if err != nil {
		t.Fatal(err)
	}
	is := findIssue(res.Issues, "bad-ss-plugin")
	if is == nil {
		t.Fatal("缺 bad-ss-plugin")
	}
	// bad-ss-plugin 是 proxies 里第 7 条(Issue.Index 是 1-based 条目序号)。
	if is.Index != 7 {
		t.Errorf("Index = %d, want 7", is.Index)
	}
}

// TestClashTLSFidelity TLS 相关字段一个不能丢:servername/sni、alpn、
// skip-cert-verify、指纹、reality 公钥与 shortId。
func TestClashTLSFidelity(t *testing.T) {
	body := `proxies:
  - name: t
    type: trojan
    server: t.example.com
    port: 443
    password: pw
    sni: sni.example.com
    alpn: "h2"
    skip-cert-verify: true
    client-fingerprint: firefox
`
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	ep := res.Endpoints[0]
	if ep.SNI != "sni.example.com" || len(ep.ALPN) != 1 || ep.ALPN[0] != "h2" ||
		!ep.AllowInsecure || ep.Fingerprint != "firefox" || ep.Security != model.SecurityTLS {
		t.Errorf("ep = %+v", ep)
	}
}

// TestClashTrojanCertPin 证书固定等表达不了的 TLS 设置:跳过整节点而不是丢字段。
func TestClashTrojanCertPin(t *testing.T) {
	body := `proxies:
  - name: pinned
    type: trojan
    server: t.example.com
    port: 443
    password: pw
    fingerprint: "AA:BB:CC"
  - name: ok
    type: trojan
    server: ok.example.com
    port: 443
    password: pw
`
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 1 || res.Endpoints[0].Remark != "ok" {
		t.Fatalf("Endpoints = %+v", res.Endpoints)
	}
	if findIssue(res.Issues, "pinned") == nil {
		t.Error("证书固定的 trojan 应被跳过")
	}
}

// TestClashWSHeaders 只允许 Host 头;其余头的 ws 节点跳过。
func TestClashWSHeaders(t *testing.T) {
	body := `proxies:
  - name: ok
    type: vless
    server: a.example.com
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    network: ws
    ws-opts:
      path: /w
      headers: {Host: cdn.example.com}
  - name: extra-header
    type: vless
    server: b.example.com
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    network: ws
    ws-opts:
      headers: {X-Token: secret, Host: cdn.example.com}
`
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 1 {
		t.Fatalf("Endpoints = %+v", res.Endpoints)
	}
	if res.Endpoints[0].Host != "cdn.example.com" || res.Endpoints[0].Path != "/w" {
		t.Errorf("ep = %+v", res.Endpoints[0])
	}
	is := findIssue(res.Issues, "extra-header")
	if is == nil {
		t.Error("带额外头的 ws 应跳过")
	} else if strings.Contains(is.Reason, "secret") {
		t.Errorf("原因泄露头值: %q", is.Reason)
	}
}
