package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/store"
)

// 鉴权认两类凭据(UC-32 退役探测点后只剩管理面凭据):
//
//   - 管理会话:CLI Bearer(或兼容 cookie)携带同一枚 token,存
//     admin_sessions 表,30 天到期、可逐枚吊销;改密码时整表清空。
//   - 管理 token(/etc/mpx/env 的 MPX_TOKEN):主钥匙。不在会话表里、不能吊销,
//     SSH 通道(mpx api-relay)和忘了密码时的应急入口都走它。
//
// 没有角色和权限分级:谁登进来了,谁就是这个控制面的主人。

// authMasterKey 标记这次请求凭的是 env 主钥匙(MPX_TOKEN)。主钥匙不受任何
// 吊销管理,是「忘了密码/丢了会话」时的救援凭据;改管理员口令时它免验旧密码
// —— 那正是救援存在的意义(见 handleAdminPassword)。
const authMasterKey ctxKey = 3

type ctxKey int

// requireAuth 校验管理接口凭据。管理 token 为空时不校验(只用于本机
// 开发,启动时已保证这种情况只能绑回环)。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		if tok := adminSessionToken(r); tok != "" {
			if r.Header.Get("Authorization") == "" && !readMethod(r.Method) && !adminOriginOK(r) {
				writeAdminOriginRejected(w)
				return
			}
			if _, err := s.store.AdminSessionByTokenHash(hashToken(tok), time.Now()); err == nil {
				next.ServeHTTP(w, r)
				return
			} else if !errors.Is(err, store.ErrNotFound) {
				s.writeStoreError(w, err, "管理会话")
				return
			}
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != "" && tokenEqual(got, s.token) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authMasterKey, true)))
			return
		}
		writeErrorHint(w, http.StatusUnauthorized, "凭据不正确或会话已过期;请重新 mpx connect",
			"登录已失效或会话被吊销:请重新登录")
	})
}

// tokenEqual 比较两枚 token 是否相同。先各自 SHA-256 再 ConstantTimeCompare,
// 避免两侧长度不同时 Go 的 ConstantTimeCompare 立刻返回而泄露管理 token 长度。
func tokenEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// hashToken 是一枚 token 在库里的样子:只存 SHA-256,明文不落盘。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newToken 生成一枚随机 token:32 字节随机数,URL 安全的 base64。
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
