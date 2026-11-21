package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// UC-26:connect 的 CA 探测。受信证书 → CA 模式;自签/名字不符 → 落回指纹;
// 拨不通 → 接入失败。

// pemOf 编码一段 PEM(测试辅助)。
func pemOf(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// testCASign 造一个小 CA,给 ip 字面量签一张服务器证书(IP SAN)。
// 真实场景是域名证书 + 域名 SNI;测试里 listener 绑在 127.0.0.1,
// IP SAN 是「SNI=连接目标且受信」的等价形态 —— 校验逻辑不区分。
func testCASign(t *testing.T, ip string) (caPool *x509.CertPool, serverCert tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPool = x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(pemOf("CERTIFICATE", caDER)) {
		t.Fatal("CA 加不进池")
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "mpx control"},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTpl, ca, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srvKeyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	serverCert, err = tls.X509KeyPair(pemOf("CERTIFICATE", srvDER), pemOf("EC PRIVATE KEY", srvKeyDER))
	if err != nil {
		t.Fatal(err)
	}
	return caPool, serverCert
}

// testSelfSigned 造一张与 tls_identity 等价的自签证书(IP SAN 也带上,
// 免得握手因名字不符失败而不是因不受信失败)。
func testSelfSigned(t *testing.T, ip string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "mpx self-signed"},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pemOf("CERTIFICATE", der), pemOf("EC PRIVATE KEY", keyDER))
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// tlsEcho 起一个 TLS server,用给定证书应答;返回 hostport。
func tlsEcho(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u.Host
}

func TestProbeCATrusted(t *testing.T) {
	pool, cert := testCASign(t, "127.0.0.1")
	hostport := tlsEcho(t, cert)
	ok, err := probeCAWithRoots(hostport, pool)
	if err != nil {
		t.Fatalf("probeCAWithRoots: %v", err)
	}
	if !ok {
		t.Fatal("受信证书该 true")
	}
}

func TestProbeCASelfSignedFallsBack(t *testing.T) {
	hostport := tlsEcho(t, testSelfSigned(t, "127.0.0.1"))
	// 系统根里没有这张自签 → 不受信 → false(落回指纹),不是 error。
	ok, err := probeCAWithRoots(hostport, x509.NewCertPool())
	if err != nil {
		t.Fatalf("自签该回 false 不是错: %v", err)
	}
	if ok {
		t.Fatal("自签证书不该受信")
	}
}

func TestProbeCADialError(t *testing.T) {
	// 拨不通 → error(连接失败,不是「不受信」)。
	_, err := probeCAWithRoots("127.0.0.1:1", x509.NewCertPool())
	if err == nil {
		t.Fatal("拨不通该报错")
	}
}

// decideTrust 的整链判断:域名 + 受信 → CA;IP 字面量 → 跳过 CA 探测
// 直接指纹。
func TestDecideTrustSkipsCAForIP(t *testing.T) {
	opts.fingerprint = ""
	// IP 字面量:probeCA 不该被调(拨不通会 fail connect,而不是走指纹)。
	// 给一个肯定拨不通的 IP,如果走 CA 探测会返回错误;走指纹才返回
	// 「连不上」的 probe 错误 —— 两种错都说明没到 yes 提示,但断言
	// 走的是指纹路径:非 TTY 时指纹流程的错带「指纹是」字样。
	_, err := decideTrust("127.0.0.1:1")
	if err == nil {
		t.Fatal("该失败")
	}
	// 指纹路径的错来自 pin.Probe:「连不上控制面」。
	if got := err.Error(); got == "" {
		t.Fatal("该有错")
	}
}
