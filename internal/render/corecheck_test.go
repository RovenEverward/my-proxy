package render

// corecheck_test.go 用真实核心验证渲染产物:sing-box check 与 mihomo -t。
// 二进制路径从环境变量读(MPX_TEST_SINGBOX / MPX_TEST_MIHOMO),不在 PATH 或环境
// 变量里就跳过——单测不依赖外部二进制。

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// coreEndpoints 是覆盖每种协议/传输/安全层的外部端点集。
func coreEndpoints() []Endpoint {
	key32 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	return []Endpoint{
		{Remark: "vless-tcp", Address: "v.example.com", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: "12345678-1234-1234-1234-123456789abc",
			Flow: "xtls-rprx-vision", Security: model.SecurityReality, SNI: "www.microsoft.com",
			RealityPub: key32, RealitySID: "ab", Fingerprint: "chrome"},
		{Remark: "vless-ws", Address: "v2.example.com", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: "12345678-1234-1234-1234-123456789abc",
			Network: model.NetworkWS, Path: "/v", Host: "cdn.example.com", Security: model.SecurityTLS,
			SNI: "v2.example.com", ALPN: []string{"h2"}},
		{Remark: "vmess-grpc", Address: "vm.example.com", Port: 8443,
			Protocol: model.ProtocolVMess, UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			Network: model.NetworkGRPC, Path: "svc", Security: model.SecurityNone},
		{Remark: "trojan", Address: "tj.example.com", Port: 443,
			Protocol: model.ProtocolTrojan, Password: "tj-pass",
			Security: model.SecurityTLS, SNI: "tj.example.com", AllowInsecure: true},
		{Remark: "ss-classic", Address: "ss.example.com", Port: 8388,
			Protocol: model.ProtocolShadowsocks, SSMethod: "aes-256-gcm", Password: "pw",
			Network: model.NetworkTCP},
		{Remark: "ss-2022", Address: "s2.example.com", Port: 8389,
			Protocol: model.ProtocolShadowsocks, SSMethod: "2022-blake3-aes-128-gcm",
			Password: "MTIzNDU2Nzg5MDEyMzQ1Ng==", Network: model.NetworkTCP},
		{Remark: "hy2", Address: "hy.example.com", Port: 443,
			Protocol: model.ProtocolHysteria2, Password: "pw",
			Network: model.NetworkUDP, Security: model.SecurityTLS, SNI: "hy.example.com"},
		{Remark: "tuic", Address: "tu.example.com", Port: 4443,
			Protocol: model.ProtocolTUIC, UUID: "11111111-2222-3333-4444-555555555555",
			Password: "pw", CongestionControl: "bbr",
			Network: model.NetworkUDP, Security: model.SecurityTLS, SNI: "tu.example.com"},
	}
}

func coreBinary(t *testing.T, env, name string) string {
	t.Helper()
	if p := os.Getenv(env); p != "" {
		return p
	}
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s 不可用(%s 未设置)", name, env)
	}
	return p
}

// TestSingBoxConfigPassCoreCheck 外部端点渲染出的 sing-box 配置能通过
// sing-box check。覆盖 AC04:合法节点不被误拒。
func TestSingBoxConfigPassCoreCheck(t *testing.T) {
	bin := coreBinary(t, "MPX_TEST_SINGBOX", "sing-box")
	eps := coreEndpoints()
	for _, ep := range eps {
		if err := Compatible(ep, model.FormatSingBox); err != nil {
			t.Fatalf("%s 应兼容 sing-box: %v", ep.Remark, err)
		}
	}
	cfg, err := SingBox(eps, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	if err := os.WriteFile(file, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "check", "-c", file).CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box check 失败: %v\n%s", err, out)
	}
}

// TestClashMetaConfigPassCoreCheck 外部端点渲染出的 Clash Meta 配置能通过
// mihomo -t。
func TestClashMetaConfigPassCoreCheck(t *testing.T) {
	bin := coreBinary(t, "MPX_TEST_MIHOMO", "mihomo")
	eps := coreEndpoints()
	for _, ep := range eps {
		if err := Compatible(ep, model.FormatClashMeta); err != nil {
			t.Fatalf("%s 应兼容 clash meta: %v", ep.Remark, err)
		}
	}
	yaml, err := Clash(eps, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, yaml, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-d", dir, "-t").CombinedOutput()
	if err != nil {
		t.Fatalf("mihomo -t 失败: %v\n%s", err, out)
	}
}
