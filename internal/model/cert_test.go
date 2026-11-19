package model

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

// 签出来的东西要能直接被 x509 吃回去,且 CN、SAN、有效期、密钥类型都对上号
// —— 这些是客户端握手时唯一看的东西。
func TestGenerateSelfSignedCertificate(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCertificate("www.apple.com")
	if err != nil {
		t.Fatalf("签发: %v", err)
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		t.Fatal("证书或私钥为空")
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("证书不是 PEM CERTIFICATE 块: %v", block)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("证书解析: %v", err)
	}
	if cert.Subject.CommonName != "www.apple.com" {
		t.Errorf("CN = %q,应为 SNI", cert.Subject.CommonName)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "www.apple.com" {
		t.Errorf("SAN = %v,应为 [SNI]", cert.DNSNames)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || pub.N.BitLen() != 2048 {
		t.Errorf("公钥类型/位长不对: %T %d", cert.PublicKey, pub.N.BitLen())
	}
	if !cert.IsCA && cert.BasicConstraintsValid {
		// 非 CA 的端点证书:这正是服务端该用的形态。
	} else {
		t.Errorf("不应当是 CA 证书: IsCA=%v BasicConstraintsValid=%v", cert.IsCA, cert.BasicConstraintsValid)
	}
	days := time.Until(cert.NotAfter).Hours() / 24
	if days < 3600 || days > 3700 {
		t.Errorf("有效期 %d 天,应在 3650 天左右", int(days))
	}

	kb, _ := pem.Decode(keyPEM)
	if kb == nil || kb.Type != "RSA PRIVATE KEY" {
		t.Fatalf("私钥不是 PEM RSA PRIVATE KEY 块: %v", kb)
	}
	priv, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
	if err != nil {
		t.Fatalf("私钥解析: %v", err)
	}
	if priv.N.Cmp(pub.N) != 0 {
		t.Error("证书公钥与私钥不配对")
	}
}

// 同一 SNI 两次签出来的证书内容不同(序列号、私钥都是随机的),
// 调用方据此做「按 SNI 复用」的缓存才有意义。
func TestGenerateSelfSignedCertificateDistinct(t *testing.T) {
	c1, k1, err := GenerateSelfSignedCertificate("a.example.com")
	if err != nil {
		t.Fatal(err)
	}
	c2, k2, err := GenerateSelfSignedCertificate("a.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if string(c1) == string(c2) || string(k1) == string(k2) {
		t.Error("两次生成的证书/私钥完全相同")
	}
}

func TestGenerateSelfSignedCertificateRejectsEmptySNI(t *testing.T) {
	if _, _, err := GenerateSelfSignedCertificate(""); err == nil {
		t.Fatal("空 SNI 应当报错")
	}
}

func TestDefaultTLSCertPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Inbound
		want [2]string
	}{
		{"hy2 自动 tag", Inbound{Protocol: ProtocolHysteria2, Port: 8444},
			[2]string{"/etc/mpx/sing-box/hysteria2-8444-cert.pem", "/etc/mpx/sing-box/hysteria2-8444-key.pem"}},
		{"显式 tag", Inbound{Protocol: ProtocolTUIC, Port: 443, Tag: "我的 tuic"},
			[2]string{"/etc/mpx/sing-box/---tuic-cert.pem", "/etc/mpx/sing-box/---tuic-key.pem"}},
		{"tag 带斜杠", Inbound{Protocol: ProtocolHysteria2, Port: 443, Tag: "a/b"},
			[2]string{"/etc/mpx/sing-box/a-b-cert.pem", "/etc/mpx/sing-box/a-b-key.pem"}},
		{"tag 全是非法字符", Inbound{Protocol: ProtocolHysteria2, Port: 443, Tag: "///"},
			[2]string{"/etc/mpx/sing-box/----cert.pem", "/etc/mpx/sing-box/----key.pem"}},
		{"空 tag", Inbound{Protocol: ProtocolHysteria2, Port: 8443, Tag: ""},
			[2]string{"/etc/mpx/sing-box/hysteria2-8443-cert.pem", "/etc/mpx/sing-box/hysteria2-8443-key.pem"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert, key := tc.in.DefaultTLSCertPaths()
			if cert != tc.want[0] || key != tc.want[1] {
				t.Errorf("got (%q, %q), want %v", cert, key, tc.want)
			}
			if !strings.HasPrefix(cert, "/etc/mpx/sing-box/") || !strings.HasPrefix(key, "/etc/mpx/sing-box/") {
				t.Errorf("路径不在 /etc/mpx/sing-box 下: %q %q", cert, key)
			}
		})
	}
}
