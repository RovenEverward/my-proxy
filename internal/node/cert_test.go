package node

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 用一只按脚本回答的 runner 模拟一台远端:文件存不存在由 test -f 的真值给出,
// 写下去的内容由 stdin 收到的东西给出。不真的起 ssh,只验「控制面说了什么」。
type certNode struct {
	t    *testing.T
	fn   func(script string) (string, error)
	file func(script string, stdin []byte) error
}

func (c *certNode) Run(_ context.Context, _ model.Node, script string) (string, error) {
	if c.fn != nil {
		return c.fn(script)
	}
	return "", nil
}

func (c *certNode) RunWithInput(_ context.Context, _ model.Node, script string, stdin []byte) (string, error) {
	if c.file != nil {
		return "", c.file(script, stdin)
	}
	return "", nil
}

func quicInbound(sni, cert, key string) model.Inbound {
	return model.Inbound{NodeID: 1, Tag: "hy2-8444", Protocol: model.ProtocolHysteria2,
		Network: model.NetworkUDP, Security: model.SecurityTLS, SNI: sni,
		CertFile: cert, KeyFile: key}
}

// 缺一对时:控制面签好一对,两个文件都按约定权限落位。
func TestEnsureTLSCertificatesWritesMissingPair(t *testing.T) {
	n := &certNode{t: t}
	var writes []struct {
		path   string
		body   string
		script string
	}
	n.fn = func(script string) (string, error) {
		if strings.Contains(script, "test -f") {
			return "missing:0\n", nil
		}
		return "", fmt.Errorf("unexpected: %s", script)
	}
	n.file = func(script string, stdin []byte) error {
		// PutFile 的脚本是 cat > x.tmp && mv x.tmp x && chmod M x:
		// 目标路径取 mv 后面那一段。
		mv := strings.Index(script, "&& mv")
		dest := script[strings.LastIndex(script, " ")+1:]
		_ = mv
		writes = append(writes, struct {
			path   string
			body   string
			script string
		}{strings.Trim(dest, "'"), string(stdin), script})
		return nil
	}

	_, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		quicInbound("www.apple.com", "/etc/mpx/sing-box/hy2-cert.pem", "/etc/mpx/sing-box/hy2-key.pem"),
	})
	if err != nil {
		t.Fatalf("缺文件时应当写下去: %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("应当写两个文件,实际 %d: %+v", len(writes), writes)
	}
	var certW, keyW struct {
		path   string
		body   string
		script string
	}
	for _, w := range writes {
		if strings.Contains(w.script, "chmod '644'") {
			certW = w
		} else if strings.Contains(w.script, "chmod '600'") {
			keyW = w
		}
	}
	if certW.path != "/etc/mpx/sing-box/hy2-cert.pem" {
		t.Errorf("cert 落到 %q", certW.path)
	}
	if !strings.Contains(certW.body, "BEGIN CERTIFICATE") {
		t.Errorf("cert 内容不是 PEM 证书: %q", certW.body)
	}
	if keyW.path != "/etc/mpx/sing-box/hy2-key.pem" {
		t.Errorf("key 落到 %q", keyW.path)
	}
	if !strings.Contains(keyW.body, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("key 内容不是 PEM 私钥: %q", keyW.body)
	}
}

// 同 SNI 的两个入站共用一对证书:只签一对,两个路径里的内容一致。
func TestEnsureTLSCertificatesReusesPairPerSNI(t *testing.T) {
	n := &certNode{t: t}
	writes := map[string]string{}
	n.fn = func(script string) (string, error) {
		if strings.Contains(script, "test -f") {
			return "missing:0\nmissing:1\n", nil
		}
		return "", nil
	}
	n.file = func(script string, stdin []byte) error {
		dest := strings.Trim(script[strings.LastIndex(script, " ")+1:], "'")
		writes[dest] = string(stdin)
		return nil
	}
	_, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		quicInbound("www.apple.com", "/a/cert.pem", "/a/key.pem"),
		{NodeID: 1, Tag: "tuic-8445", Protocol: model.ProtocolTUIC, Network: model.NetworkUDP,
			Security: model.SecurityTLS, SNI: "www.apple.com", CertFile: "/b/cert.pem", KeyFile: "/b/key.pem"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if writes["/a/cert.pem"] != writes["/b/cert.pem"] {
		t.Error("同 SNI 两个路径里的证书应当是同一份")
	}
	if writes["/a/key.pem"] != writes["/b/key.pem"] {
		t.Error("同 SNI 两个路径里的私钥应当是同一份")
	}
}

// 两文件都在时一字节都不写:节点上已有的证书不被覆盖。
func TestEnsureTLSCertificatesSkipsExisting(t *testing.T) {
	n := &certNode{t: t}
	n.fn = func(script string) (string, error) {
		if strings.Contains(script, "test -f") {
			return "ok:0\n", nil
		}
		return "", fmt.Errorf("unexpected: %s", script)
	}
	var got []byte
	n.file = func(_ string, stdin []byte) error {
		got = stdin
		return fmt.Errorf("不该写文件")
	}
	_, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		quicInbound("www.apple.com", "/a/cert.pem", "/a/key.pem"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("已存在的对被重写了")
	}
}

// 只有 cert 没有 key 算半个对:整对重签,而不是单独补一把配不上证书的 key。
func TestEnsureTLSCertificatesRepairsHalfPair(t *testing.T) {
	n := &certNode{t: t}
	var wroteKey bool
	n.fn = func(script string) (string, error) {
		if strings.Contains(script, "test -f") {
			return "missing:0\n", nil // test -f cert -a -f key 同时为真才 ok;一个缺就算缺
		}
		return "", nil
	}
	n.file = func(script string, stdin []byte) error {
		if strings.Contains(script, "key.pem") {
			wroteKey = true
		}
		return nil
	}
	_, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		quicInbound("www.apple.com", "/a/cert.pem", "/a/key.pem"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !wroteKey {
		t.Error("半对没补全")
	}
}

// 探活失败时别往下走:不知道节点上有什么,就什么也别动。
func TestEnsureTLSCertificatesProbeFailure(t *testing.T) {
	n := &certNode{t: t}
	n.fn = func(script string) (string, error) {
		return "", fmt.Errorf("ssh: connect failed")
	}
	var wrote bool
	n.file = func(_ string, _ []byte) error { wrote = true; return nil }
	_, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		quicInbound("www.apple.com", "/a/cert.pem", "/a/key.pem"),
	})
	if err == nil {
		t.Fatal("探活失败应当报错")
	}
	if wrote {
		t.Error("探活失败后还写了文件")
	}
}

// 非 TLS 入站不参与:探活脚本里不该出现它们。
func TestEnsureTLSCertificatesIgnoresNonTLS(t *testing.T) {
	n := &certNode{t: t}
	var probed string
	n.fn = func(script string) (string, error) {
		probed = script
		return "", nil
	}
	_, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		{NodeID: 1, Tag: "vless", Protocol: model.ProtocolVLESS, Network: model.NetworkWS,
			Security: model.SecurityNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	if probed != "" {
		t.Errorf("非 TLS 入站不该探活: %s", probed)
	}
}

// 挂 PEM 的辅助:给一对材料,节点上的状态由探活结果喂出来。
func managedInbound(certPEM, keyPEM string) model.Inbound {
	in := quicInbound("la1.shiziyi.top", "/a/cert.pem", "/a/key.pem")
	in.CertPEM, in.KeyPEM = certPEM, keyPEM
	return in
}

const fakeCertPEM = "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n"
const fakeKeyPEM = "-----BEGIN PRIVATE KEY-----\na2V5\n-----END PRIVATE KEY-----\n"

// 托管证书缺失:写的应当是库里的 PEM,而不是现签的自签。
func TestEnsureTLSCertificatesPushesManagedWhenMissing(t *testing.T) {
	n := &certNode{t: t}
	writes := map[string]string{}
	n.fn = func(script string) (string, error) { return "missing:0\n", nil }
	n.file = func(script string, stdin []byte) error {
		dest := strings.Trim(script[strings.LastIndex(script, " ")+1:], "'")
		writes[dest] = string(stdin)
		return nil
	}
	changed, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		managedInbound(fakeCertPEM, fakeKeyPEM),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("写了文件 changed 应当是 true")
	}
	if writes["/a/cert.pem"] != fakeCertPEM {
		t.Errorf("证书应写库里的 PEM,实际 %q", writes["/a/cert.pem"])
	}
	if writes["/a/key.pem"] != fakeKeyPEM {
		t.Errorf("私钥应写库里的 PEM,实际 %q", writes["/a/key.pem"])
	}
}

// 托管证书与库里哈希对不上(diff):覆盖成库里的内容,并告诉调用方文件动了。
func TestEnsureTLSCertificatesPushesManagedOnDiff(t *testing.T) {
	n := &certNode{t: t}
	writes := map[string]string{}
	n.fn = func(script string) (string, error) {
		if !strings.Contains(script, "sha256sum") {
			t.Errorf("托管证书的探活脚本应比对 sha256,实际: %s", script)
		}
		return "diff:0\n", nil
	}
	n.file = func(script string, stdin []byte) error {
		dest := strings.Trim(script[strings.LastIndex(script, " ")+1:], "'")
		writes[dest] = string(stdin)
		return nil
	}
	changed, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		managedInbound(fakeCertPEM, fakeKeyPEM),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("diff 写了文件 changed 应当是 true")
	}
	if writes["/a/cert.pem"] != fakeCertPEM || writes["/a/key.pem"] != fakeKeyPEM {
		t.Errorf("diff 应覆盖成库里的 PEM: %+v", writes)
	}
}

// 托管证书内容一致(ok):一字节不写,也不报 changed。
func TestEnsureTLSCertificatesManagedUnchanged(t *testing.T) {
	n := &certNode{t: t}
	n.fn = func(script string) (string, error) { return "ok:0\n", nil }
	n.file = func(_ string, _ []byte) error {
		t.Error("ok 状态不该写文件")
		return nil
	}
	changed, err := EnsureTLSCertificates(t.Context(), n, model.Node{}, []model.Inbound{
		managedInbound(fakeCertPEM, fakeKeyPEM),
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("ok 状态 changed 应当是 false")
	}
}
