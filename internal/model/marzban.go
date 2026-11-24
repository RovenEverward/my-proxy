package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// ParseMarzbanSubToken 按 Marzban 的铸法验一枚订阅 token,通过时返回里面的
// 用户名。
//
// Marzban 的 token 是无状态签名:数据段 base64url(用户名,unix秒) 拼上签名
// sha256(数据段 + secret) 的前 10 个 base64 字符。验证方不看时间戳,只看
// 签名 —— 所以已发出去的任意一枚旧链接都能在这一步认出人,迁库时不用逐
// 个抄 token(它们本来就不落库)。
//
// 任何一步对不上(太短、解码失败、签名不符、格式不是「名,秒」)都返回
// false,调用方按普通 404 处理。
func ParseMarzbanSubToken(token, secret string) (string, bool) {
	if secret == "" || len(token) < 15 {
		return "", false
	}
	data, sig := token[:len(token)-10], token[len(token)-10:]

	sum := sha256.Sum256([]byte(data + secret))
	expected := base64.URLEncoding.EncodeToString(sum[:])[:10]
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", false
	}

	// Marzban 用的是去填充 urlsafe b64,补回 '=' 再解。
	raw, err := base64.URLEncoding.DecodeString(data + strings.Repeat("=", (4-len(data)%4)%4))
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(raw), ",", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}
