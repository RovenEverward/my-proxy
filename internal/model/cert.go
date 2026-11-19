package model

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

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
