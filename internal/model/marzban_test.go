package model

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// marzbanToken 按 Marzban 的铸法造一枚测试 token,与 app/utils/jwt.py 对齐。
func marzbanToken(t *testing.T, username string, secret string) string {
	t.Helper()
	data := base64.URLEncoding.EncodeToString([]byte(username + ",1795504056"))
	sum := sha256.Sum256([]byte(data + secret))
	sig := base64.URLEncoding.EncodeToString(sum[:])[:10]
	return data + sig
}

func TestParseMarzbanSubToken(t *testing.T) {
	secret := "test-secret-key-0123456789abcdef"

	t.Run("合法 token 认出人", func(t *testing.T) {
		for _, name := range []string{"alice", "Cyber_one", "zhuliangjie", "a1b2c3"} {
			token := marzbanToken(t, name, secret)
			got, ok := ParseMarzbanSubToken(token, secret)
			if !ok || got != name {
				t.Fatalf("%q -> (%q,%v), 想要 (%q,true)", token, got, ok, name)
			}
		}
	})

	t.Run("签名错了不认", func(t *testing.T) {
		token := marzbanToken(t, "alice", secret)
		bad := token[:len(token)-10] + "AAAAAAAAAA"
		if got, ok := ParseMarzbanSubToken(bad, secret); ok {
			t.Fatalf("改过的签名也过了: %q", got)
		}
	})

	t.Run("空 secret 直接拒绝", func(t *testing.T) {
		if got, ok := ParseMarzbanSubToken(marzbanToken(t, "alice", secret), ""); ok {
			t.Fatalf("secret 没配也过了: %q", got)
		}
	})

	t.Run("太短的 token 不解析", func(t *testing.T) {
		if _, ok := ParseMarzbanSubToken("short", secret); ok {
			t.Fatal("短 token 也过了")
		}
	})

	t.Run("数据不是 名,秒 的形状不认", func(t *testing.T) {
		raw := base64.URLEncoding.EncodeToString([]byte("only-username"))
		sum := sha256.Sum256([]byte(raw + secret))
		token := raw + base64.URLEncoding.EncodeToString(sum[:])[:10]
		if _, ok := ParseMarzbanSubToken(token, secret); ok {
			t.Fatal("无逗号数据也过了")
		}
	})
}
