package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/store"
)

// 鉴权认三类凭据(UC-26 起,设备模型已退役):
//
//   - 管理台会话:浏览器 cookie 与 CLI Bearer 携带同一枚 token,存
//     admin_sessions 表,30 天到期、可逐枚吊销;改密码时整表清空。
//   - 管理 token(/etc/mpx/env 的 MPX_TOKEN):主钥匙。不在会话表里、不能吊销,
//     是忘了密码时的应急入口(服务器上 mpx admin password 走的就是它)。
//   - 探测点 join token:只放行探测接口,见 probeAllowed。
//
// 仍然没有角色和权限分级:谁登进来了,谁就是这个控制面的主人。

// probeIDKey 把探测点身份挂进请求上下文;值为 0 表示这条请求不是探测点发的。
const probeIDKey ctxKey = 2

// authMasterKey 标记这次请求凭的是 env 主钥匙(MPX_TOKEN)。主钥匙不受任何
// 吊销管理,是「忘了密码/丢了会话」时的救援凭据;改管理员口令时它免验旧密码
// —— 那正是救援存在的意义(见 handleAdminPassword)。
const authMasterKey ctxKey = 3

type ctxKey int

// probeAllowed 是探测点凭据够得着的接口(UC-12):拉目标、回结果、让控制面
// 代探。管理面其余接口它一律不可见。
var probeAllowed = map[string]bool{
	"GET /api/reach/targets":  true,
	"POST /api/reach/results": true,
	"POST /api/reach/probe":   true,
	// UC-21:CDN 优选,探测点领名单与回结果走同一对凭据可达接口。
	"GET /api/cdn-optimize/targets":  true,
	"POST /api/cdn-optimize/results": true,
	// UC-23:CDN 入口自动测速。targets 返回端点凭据 —— 探测点本就需要
	// 靠它起 sing-box 实测,权界与 reach targets 下发目标凭据一致(探
	// 测点是操作者自有资产,凭据丢了本身就意味着内网失守)。
	"GET /api/bandwidth/targets":  true,
	"POST /api/bandwidth/results": true,
}

// probeTouchEvery 是记"最近上报"的最小间隔:每个请求都写一次库没有意义。
const probeTouchEvery = time.Minute

// requireAuth 校验管理接口凭据。管理 token 为空时不校验(只用于本机
// 开发,启动时已保证这种情况只能绑回环)。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		// UC-26:管理台会话是第一类凭据。cookie(浏览器)与 Bearer(CLI)携带
		// 同一枚 token,查同一张 admin_sessions 表;命中未过期行即放行。
		// 凭 cookie 进来的写请求(没带 Authorization)先过一次同源校验:
		// SameSite=Lax 挡住了大多数跨站 POST,这里再把 Origin/Fetch-Site
		// 也查掉,作为纵深;Bearer 形态(CLI)没有 Origin 一说,不查。
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
		if got != "" {
			// 探测点凭据是第三类(UC-12):只放行它够得着的探测接口。
			p, perr := s.store.ProbeByToken(hashToken(got))
			if perr == nil {
				if !probeAllowed[r.Method+" "+r.URL.Path] {
					writeError(w, http.StatusForbidden, "探测点凭据不能访问管理接口")
					return
				}
				s.touchProbe(p.ID, r)
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), probeIDKey, p.ID)))
				return
			}
			if !errors.Is(perr, store.ErrNotFound) {
				s.writeStoreError(w, perr, "探测点")
				return
			}
		}
		writeErrorHint(w, http.StatusUnauthorized, "凭据不正确或会话已过期;重新登录(浏览器)或重新 mpx connect(CLI)",
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
// 管理台会话与探测点 join token 都用它。
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var touchMu sync.Mutex

var probeTouched = map[int64]time.Time{}

func (s *Server) touchProbe(id int64, r *http.Request) {
	now := time.Now()
	touchMu.Lock()
	if now.Sub(probeTouched[id]) < probeTouchEvery {
		touchMu.Unlock()
		return
	}
	probeTouched[id] = now
	touchMu.Unlock()
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if err := s.store.TouchProbe(id, ip); err != nil {
		s.logger.Warn("记录探测点最近上报失败", "probe", id, "err", err)
	}
}
