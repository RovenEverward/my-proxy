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

// 鉴权认两类凭据:
//
//   - 管理 token(/etc/mpx/env 的 MPX_TOKEN):主钥匙。不在设备列表里、不能吊销,
//     是忘了设备密码时的应急入口。
//   - 设备 token:每台接入的设备一枚,库里只存 SHA-256,可逐台吊销。
//
// 仍然没有角色和权限分级:谁接入了,谁就是这个控制面的主人。

type ctxKey int

const deviceIDKey ctxKey = 1

// probeIDKey 把探测点身份挂进请求上下文;值为 0 表示这条请求不是探测点发的。
const probeIDKey ctxKey = 2

// probeAllowed 是探测点凭据够得着的接口(UC-12):拉目标、回结果、让控制面
// 代探。管理面其余接口它一律不可见。
var probeAllowed = map[string]bool{
	"GET /api/reach/targets":  true,
	"POST /api/reach/results": true,
	"POST /api/reach/probe":   true,
}

// deviceTouchEvery 是记"最近使用"的最小间隔:每个请求都写一次库没有意义。
const deviceTouchEvery = time.Minute

// requireAuth 校验管理接口的 Bearer token。管理 token 为空时不校验(只用于本机
// 开发,启动时已保证这种情况只能绑回环)。
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
