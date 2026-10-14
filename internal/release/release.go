// Package release 按发布里的 SHA256SUMS 核对 mpx 发布件。
//
// 只做哈希,不做签名:防的是下载损坏、截断和拿错包;来源可信靠 HTTPS 与 GitHub。
// 决定与理由见 docs/internal/update-rfc.md。deploy/install.sh 与 install-cli.sh 里有
// 同一套逻辑的 shell 版本(curl | bash 跑的脚本没法引用这个包)。
package release

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

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

// Check 核对 data 正是 SHA256SUMS 里 name 的那一份。
func Check(sums []byte, name string, data []byte) error {
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
