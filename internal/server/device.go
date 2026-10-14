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
// 仍然没有角色和权限分级:谁接入了,谁就是这个控制面的主人(decisions.md 第 6 条)。

type ctxKey int

const deviceIDKey ctxKey = 1

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
		if got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1 {
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
		}
		writeError(w, http.StatusUnauthorized, "token 不正确或这台设备已被吊销;重新 mpx connect")
	})
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
