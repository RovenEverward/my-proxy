// Package release 校验 mpx 发布件:SHA256SUMS 的 ed25519 签名,以及某个文件的哈希。
//
// 发布流程(.github/workflows/release.yml)对 SHA256SUMS 整个文件签一次名,得到
// 64 字节的原始签名 SHA256SUMS.sig。验证顺序固定为「先验签名、再对哈希、最后才解包」,
// 任何一步不过都不碰现有文件 —— 设计见 docs/internal/update-rfc.md §1。
//
// 公钥以 SPKI DER 的 base64(即 PEM 去掉首尾行)写在 PublicKeys 里,deploy/install.sh
// 与 install-cli.sh 里各有一份同样的字符串;TestScriptsCarrySameKeys 保证三处一致。
package release

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// PublicKeys 是可信的签名公钥。轮换时新旧两把同时在列,过一个发布周期再去掉旧的。
//
// 上线前要换成正式公钥(步骤见 docs/internal/update-rfc.md「M0 上线步骤」)。为空时
// Verify 一律报 ErrNoKey:没有公钥就是没验过,不能当作验过。
var PublicKeys = []string{}

// ErrNoKey 表示这份二进制里还没配置公钥。
var ErrNoKey = errors.New("没有配置发布签名公钥")

// Verify 用 keys 里任意一把公钥验证 sums 的签名。keys 为 nil 时用 PublicKeys。
func Verify(sums, sig []byte, keys []string) error {
	if keys == nil {
		keys = PublicKeys
	}
	if len(keys) == 0 {
		return ErrNoKey
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("签名长度是 %d 字节,应为 %d", len(sig), ed25519.SignatureSize)
	}
	for _, k := range keys {
		pub, err := ParseKey(k)
		if err != nil {
			return err
		}
		if ed25519.Verify(pub, sums, sig) {
			return nil
		}
	}
	return errors.New("SHA256SUMS 的签名不对:文件被改过,或不是本项目签的")
}

// ParseKey 解析 SPKI DER 的 base64 形式的 ed25519 公钥。
func ParseKey(s string) (ed25519.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("公钥不是合法的 base64: %w", err)
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("公钥解析失败: %w", err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("公钥不是 ed25519,而是 %T", k)
	}
	return pub, nil
}

// Lookup 在 SHA256SUMS(sha256sum 的输出格式)里找 name 的哈希。
func Lookup(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if _, err := hex.DecodeString(f[0]); err != nil || len(f[0]) != 64 {
				return "", fmt.Errorf("SHA256SUMS 里 %s 的哈希格式不对", name)
			}
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS 里没有 %s", name)
}

// Check 验签名后核对 data 是 SHA256SUMS 里 name 的那一份。
func Check(sums, sig []byte, keys []string, name string, data []byte) error {
	if err := Verify(sums, sig, keys); err != nil {
		return err
	}
	want, err := Lookup(sums, name)
	if err != nil {
		return err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s 的 sha256 与 SHA256SUMS 不符", name)
	}
	return nil
}
