package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// 设备密码用 argon2id 哈希,存成 PHC 字符串:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
//
// 参数写进字符串里,以后调参不用迁移旧哈希。设备密码是人记的、熵低,所以要慢
// 哈希;设备 token 是 32 字节随机数,SHA-256 就够(见 device.go)。
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16

	// minDevicePasswordLen 是设备密码的最短长度(按字符数)。不设复杂度规则:
	// 长度比花样更管用,规则只会逼出 Passw0rd!。
	minDevicePasswordLen = 12
)

func hashDevicePassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func verifyDevicePassword(pw, phc string) (bool, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("设备密码哈希格式不认识")
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false, errors.New("设备密码哈希版本不认识")
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errors.New("设备密码哈希参数不认识")
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func checkDevicePasswordStrength(pw string) error {
	if n := len([]rune(pw)); n < minDevicePasswordLen {
		return fmt.Errorf("设备密码至少 %d 个字符,这个只有 %d 个", minDevicePasswordLen, n)
	}
	return nil
}
