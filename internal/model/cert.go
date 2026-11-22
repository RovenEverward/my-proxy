package model

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// InboundCertInfo 是入站所挂真实证书对外的展示信息。
//
// 内容从 CertPEM 现场解析:证书正文只有一份(库里),展示字段不存,
// 免得续期后两边对不上。
type InboundCertInfo struct {
	Subject  string     `json:"subject"`
	Issuer   string     `json:"issuer"`
	DNSNames []string   `json:"dns_names,omitempty"`
	NotAfter *time.Time `json:"not_after"`
	// DaysLeft 是距到期的天数(向下取整,过期为负)。
	DaysLeft int `json:"days_left"`
}

// ValidateCertPair 校验一对证书材料可用:PEM 合法、私钥与证书匹配。
func ValidateCertPair(certPEM, keyPEM []byte) error {
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("证书与私钥配对失败: %w", err)
	}
	return nil
}

// ParseInboundCert 解析证书 PEM 的首张证书,组装展示信息。
func ParseInboundCert(certPEM []byte, now time.Time) (*InboundCertInfo, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("不是 PEM 格式的证书")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析证书失败: %w", err)
	}
	notAfter := cert.NotAfter
	return &InboundCertInfo{
		Subject:  cert.Subject.String(),
		Issuer:   cert.Issuer.String(),
		DNSNames: cert.DNSNames,
		NotAfter: &notAfter,
		DaysLeft: int(cert.NotAfter.Sub(now).Hours() / 24),
	}, nil
}

// inboundCertValidity 是入站自签证书的有效期。
//
// QUIC 入站(hysteria2 / tuic)的客户端本来就 AllowInsecure、跳过校验,证书
// 只用来撑起 TLS 握手;十年远超运维周期,不做轮换。
const inboundCertValidity = 3650 * 24 * time.Hour

// GenerateSelfSignedCertificate 为一个 TLS 入站签发自签证书与私钥(PEM)。
//
// sni 同时写进 CN 与 DNS SAN:它既是握手时客户端声称要连的名字,也是证书对
// 外的身份 —— 两者一致,客户端在「不校验」模式下拿到的证书仍长得像这个站点的
// 正常证书。RSA 2048 与真实站点的主流证书形态一致。
//
// 不存库、不缓存:证书是节点上的运行时材料,丢了由下发管线重新签一张。
func GenerateSelfSignedCertificate(sni string) (certPEM, keyPEM []byte, err error) {
	if sni == "" {
		return nil, nil, fmt.Errorf("SNI 为空,签不了证书")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("生成私钥: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: sni},
		DNSNames:              []string{sni},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(inboundCertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("签发自签证书: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), nil
}
