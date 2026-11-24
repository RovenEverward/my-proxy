package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// requireAuth 校验管理接口 Bearer token（MPX_TOKEN）。
// token 为空时不校验（只用于本机开发，启动时已限制只能绑回环）。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != "" && tokenEqual(got, s.token) {
			next.ServeHTTP(w, r)
			return
		}
		writeErrorHint(w, http.StatusUnauthorized, "管理 token 不正确;请核对 MPX_TOKEN 或使用 mpx connect --ssh",
			"凭据无效")
	})
}

// tokenEqual 比较两枚 token 是否相同。先各自 SHA-256 再 ConstantTimeCompare,
// 避免两侧长度不同时 Go 的 ConstantTimeCompare 立刻返回而泄露管理 token 长度。
func tokenEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// newToken 生成一枚随机 token:32 字节随机数,URL 安全的 base64。
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
