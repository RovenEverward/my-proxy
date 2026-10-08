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

// DeriveSS2022Key 由 Key 派生 shadowsocks-2022 的用户密钥。
//
// 服务端密钥在入站上另存(见 serverconfig),这里只产出每个用户的 psk。
// 加密方式固定为 2022-blake3-aes-128-gcm,密钥因此是 16 字节;
// 写成 base64 与 xray 配置里 clients[].password 的写法一致。
func DeriveSS2022Key(key string) string {
	b := derive("ss2022", key)
	return base64.StdEncoding.EncodeToString(b[:16])
}

// DeriveWGKeypair 由 Key 确定性派生一对 wireguard X25519 密钥。
//
// 不随机生成:同一用户每次订阅都要拿到同一把私钥,否则订阅刷新一次,客户端
// 手里的 key 就和服务端 peers 里的 publicKey 对不上了。SHA-256 的输出本身
// 就是合法的 X25519 私钥种子(X25519 对私钥做 clamp,任意 32 字节都可用)。
//
// 返回无填充 URL-safe base64,与 xray 配置里 secretKey / publicKey 的写法
// 一致(实测 xray 认这种;渲染给 sing-box / mihomo 时再换成带填充的标准
// base64,它们只认那种)。
func DeriveWGKeypair(key string) (priv, pub string, err error) {
	b := derive("wireguard", key)
	privKey, err := ecdh.X25519().NewPrivateKey(b[:])
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(privKey.Bytes()),
		base64.RawURLEncoding.EncodeToString(privKey.PublicKey().Bytes()), nil
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
