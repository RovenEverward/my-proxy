package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/RovenEverward/my-proxy/internal/pin"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// certValidity 是自签证书的有效期。客户端按公钥指纹认人、不看有效期
// (见 internal/pin),这个数字只是让证书本身不至于显得过期;十年内不用管它。
const certValidity = 10 * 365 * 24 * time.Hour

// loadOrCreateIdentity 取出控制面的 TLS 身份;库里还没有就当场生成一份存进去。
//
// 生成在控制面进程里做,而不是 install.sh 里调 openssl:少一条外部依赖,也让
// "从源码直接 mpx serve" 的开发场景和正式安装走同一条路。
func loadOrCreateIdentity(st *store.Store) (tls.Certificate, string, error) {
	certPEM, keyPEM, err := st.GetTLSIdentity()
	if errors.Is(err, store.ErrNotFound) {
		certPEM, keyPEM, err = newSelfSigned()
		if err != nil {
			return tls.Certificate{}, "", err
		}
		if err := st.PutTLSIdentity(certPEM, keyPEM); err != nil {
			return tls.Certificate{}, "", err
		}
	} else if err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("库里的 TLS 身份坏了: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("解析证书: %w", err)
	}
	cert.Leaf = leaf
	return cert, pin.Fingerprint(leaf), nil
}

// newSelfSigned 生成一张 ECDSA P-256 自签证书。主题与 SAN 只写主机名,纯粹
// 是给人看的:客户端不校验它们。
func newSelfSigned() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("生成私钥: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "mpx"
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "mpx control plane " + host},
		DNSNames:              []string{host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("签发自签证书: %w", err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), nil
}
