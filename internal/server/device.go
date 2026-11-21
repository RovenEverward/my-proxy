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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/store"
)

// 鉴权认四类凭据:
//
//   - 管理 token(/etc/mpx/env 的 MPX_TOKEN):主钥匙。不在设备列表里、不能吊销,
//     是忘了设备密码时的应急入口,也是 UC-26 建号/救援的凭据。
//   - 设备 token:每台接入的设备一枚,库里只存 SHA-256,可逐台吊销。
//   - 探测点 join token:只放行探测接口,见 probeAllowed。
//   - 管理台会话(UC-26):浏览器 cookie 与 CLI Bearer 携带同一枚 token,存
//     admin_sessions 表,30 天到期、可逐枚吊销;改密码时整表清空。
//
// 仍然没有角色和权限分级:谁接入了,谁就是这个控制面的主人。

type ctxKey int

const deviceIDKey ctxKey = 1

// probeIDKey 把探测点身份挂进请求上下文;值为 0 表示这条请求不是探测点发的。
const probeIDKey ctxKey = 2

// authMasterKey 标记这次请求凭的是 env 主钥匙(MPX_TOKEN)。主钥匙不受任何
// 吊销管理,是「忘了密码/丢了会话」时的救援凭据;改管理员口令时它免验旧密码
// —— 那正是救援存在的意义(见 handleAdminPassword)。
const authMasterKey ctxKey = 3

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

// deviceTouchEvery 是记"最近使用"的最小间隔:每个请求都写一次库没有意义。
const deviceTouchEvery = time.Minute

// requireAuth 校验管理接口凭据。管理 token 为空时不校验(只用于本机
// 开发,启动时已保证这种情况只能绑回环)。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		// UC-26:管理台会话是第四类凭据。cookie(浏览器)与 Bearer(CLI)携带
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
			d, err := s.store.DeviceByTokenHash(hashToken(got))
			if err == nil {
				s.touchDevice(d.ID, r)
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceIDKey, d.ID)))
				return
			}
			if !errors.Is(err, store.ErrNotFound) {
				s.writeStoreError(w, err, "设备")
				return
			}
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
		writeErrorHint(w, http.StatusUnauthorized, "token 不正确或这台设备已被吊销;重新 mpx connect",
			"这台设备的登录已失效或被吊销:请在 MPX 里重新「接入已有控制面」")
	})
}

// tokenEqual 比较两枚 token 是否相同。先各自 SHA-256 再 ConstantTimeCompare,
// 避免两侧长度不同时 Go 的 ConstantTimeCompare 立刻返回而泄露管理 token 长度。
func tokenEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// hashToken 是设备 token 在库里的样子。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newDeviceToken 生成一枚设备 token:32 字节随机数,URL 安全的 base64。
func newDeviceToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var touchMu sync.Mutex

// probeTouched 与 deviceTouched 分两张表记节流:探测点 id 与设备 id 是两个
// 命名空间,混一张表会互相顶掉对方的节流窗口。
var probeTouched = map[int64]time.Time{}

func (s *Server) touchProbe(id int64, r *http.Request) {
	now := time.Now()
	touchMu.Lock()
	if now.Sub(probeTouched[id]) < deviceTouchEvery {
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

func (s *Server) touchDevice(id int64, r *http.Request) {
	now := time.Now()
	touchMu.Lock()
	if s.deviceTouched == nil {
		s.deviceTouched = map[int64]time.Time{}
	}
	if now.Sub(s.deviceTouched[id]) < deviceTouchEvery {
		touchMu.Unlock()
		return
	}
	s.deviceTouched[id] = now
	touchMu.Unlock()
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if err := s.store.TouchDevice(id, ip, now); err != nil {
		s.logger.Warn("记录设备最近使用失败", "device", id, "err", err)
	}
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.store.ListDevices()
	if err != nil {
		s.writeStoreError(w, err, "设备")
		return
	}
	cur, _ := r.Context().Value(deviceIDKey).(int64)
	for i := range ds {
		ds[i].Current = ds[i].ID == cur
	}
	writeJSON(w, http.StatusOK, ds)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "设备 id 不是整数")
		return
	}
	if err := s.store.DeleteDevice(id); err != nil {
		s.writeStoreError(w, err, "设备")
		return
	}
	touchMu.Lock()
	delete(s.deviceTouched, id)
	touchMu.Unlock()
	s.logger.Info("已吊销设备", "device", id)
	w.WriteHeader(http.StatusNoContent)
}
