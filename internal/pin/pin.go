// Package pin 是"按证书指纹认控制面"的那一小块:算指纹、比指纹、造一个只认
// 某个指纹的 HTTP 客户端。
//
// 控制面用的是自己生成的自签证书(没有域名、不走 CA),所以浏览器式的"CA +
// 主机名"校验在这里没有意义;取而代之的是 SSH known_hosts 的做法:第一次接入
// 时把指纹记下,以后每次连接都只认这个指纹。指纹钉的是公钥(SPKI),不是整张
// 证书 —— 证书到期重签时只要密钥不换,已经接入的设备无感。
package pin

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Prefix 是指纹的书写前缀,与 ssh-keygen -l 的样子一致。
const Prefix = "SHA256:"

// Fingerprint 算一张证书公钥的指纹,写成 "SHA256:<无填充 base64>"。
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return Prefix + base64.RawStdEncoding.EncodeToString(sum[:])
}

// Normalize 把用户手敲或复制来的指纹整理成规范形状;认不出时返回错误。
//
// 前缀可省、大小写不敏感(前缀部分),尾部 '=' 填充可有可无 —— 这些都是复制
// 粘贴时常见的走样,不该让人因此接入失败。
func Normalize(fp string) (string, error) {
	s := strings.TrimSpace(fp)
	if len(s) >= len(Prefix) && strings.EqualFold(s[:len(Prefix)], Prefix) {
		s = s[len(Prefix):]
	}
	s = strings.TrimRight(s, "=")
	raw, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil || len(raw) != sha256.Size {
		return "", fmt.Errorf("指纹 %q 不是 SHA256:<base64> 的样子", fp)
	}
	return Prefix + s, nil
}

// ErrMismatch 表示对方出示的证书不是钉住的那一张。
var ErrMismatch = errors.New("证书指纹不符")

// MismatchError 带上双方的指纹,方便把"期望/实际"摆给人看。
type MismatchError struct{ Want, Got string }

func (e *MismatchError) Error() string {
	return fmt.Sprintf("控制面的证书指纹不符:期望 %s,实际 %s。可能是有人在中间冒充控制面,"+
		"也可能是控制面重装或换了证书;确认是后者再重新 mpx connect", e.Want, e.Got)
}
func (e *MismatchError) Is(t error) bool { return t == ErrMismatch }

// TLSConfig 返回一份只认指纹 fp 的 TLS 配置。fp 必须已经 Normalize 过。
//
// InsecureSkipVerify 关掉的是 CA 链与主机名校验 —— 自签证书本来就过不了那两样;
// 真正的校验在 VerifyConnection 里做,比对的是叶子证书公钥的指纹。
func TLSConfig(fp string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // 由 VerifyConnection 按指纹校验
		MinVersion:         tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("控制面没有出示证书")
			}
			if got := Fingerprint(cs.PeerCertificates[0]); got != fp {
				return &MismatchError{Want: fp, Got: got}
			}
			return nil
		},
	}
}

// Transport 返回一个只认指纹 fp 的 http.Transport;fp 为空时返回标准的默认
// 传输(用于 http:// 地址)。
func Transport(fp string) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if fp != "" {
		t.TLSClientConfig = TLSConfig(fp)
		// 控制面那头只说 HTTP/1.1(见 server 的嗅探监听);明说出来,免得 h2
		// 协商失败再退回的那一下。
		t.ForceAttemptHTTP2 = false
	}
	return t
}

// Probe 连一次 host:port,不做任何校验,只取回对方证书的指纹。它是"首次
// 接入时把指纹摆给人确认"(TOFU)的那一步,结果必须交给人判断,不能直接信。
func Probe(ctx context.Context, hostport string) (string, error) {
	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // 只取指纹给人确认
	}
	c, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return "", err
	}
	defer c.Close()
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("对方没有出示证书")
	}
	return Fingerprint(certs[0]), nil
}
