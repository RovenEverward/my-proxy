package model

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// 用户只有一份 Key,各协议的身份都由它派生,因此改一次 Key 就能一次性换掉
// 该用户在所有协议上的凭证。
//
// 派生用 SHA-256 加域分隔,不引入额外依赖:我们不复用 Marzneshin 的 xxh128,
// 因为两边的 Key 体系本来就不通用。

func derive(domain, key string) [32]byte {
	return sha256.Sum256([]byte(domain + ":" + key))
}

// DeriveUUID 由 Key 派生 vless / vmess 的 UUID。
func DeriveUUID(key string) string {
	b := derive("uuid", key)
	b[6] = (b[6] & 0x0f) | 0x40 // 版本 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 变体
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// DerivePassword 由 Key 派生 trojan 的密码。
func DerivePassword(key string) string {
	b := derive("password", key)
	return hex.EncodeToString(b[:16])
}

// NewKey 生成一份新的密钥种子。
func NewKey() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// GenerateRealityKeypair 生成 REALITY 用的 X25519 密钥对。
// 返回的两个值都是无填充 base64,与 xray 配置里 privateKey / publicKey 的写法一致。
func GenerateRealityKeypair() (priv, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(k.Bytes()),
		base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// NewShortID 生成一个 REALITY shortId。
// xray 要求 shortId 是长度不超过 16 的偶数位十六进制串。
func NewShortID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// NewSubscriptionToken 生成订阅路径里那段不可猜的 token。
func NewSubscriptionToken() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
