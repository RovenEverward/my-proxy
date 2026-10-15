package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 设备接入:新设备拿设备密码换一枚自己的 token(POST /api/connect)。
//
// 这是 /api 下唯一不要 token 的接口,所以单独挂、单独限流,三道闸:
//   - 每个 IP 每分钟 5 次;
//   - 全局每分钟 20 次 —— 防换着 IP 撞;
//   - 连续失败 20 次就整体锁 15 分钟。已经接入的设备不受影响,它们走 token。
//
// 而且只在 HTTPS(或回环)上受理:密码不能明文过公网。

const (
	connectPerIPRate  = 5.0 / 60
	connectPerIPBurst = 5.0
	connectGlobalRate = 20.0 / 60
	connectGlobalBurs = 20.0
	connectLockAfter  = 20
	connectLockFor    = 15 * time.Minute
	maxDeviceNameLen  = 64
)

type connectGuard struct {
	perIP  *ipRateLimiter
	global *ipRateLimiter

	mu          sync.Mutex
	failures    int
	lockedUntil time.Time
}

func newConnectGuard() *connectGuard {
	return &connectGuard{
		perIP:  newIPRateLimiter(connectPerIPRate, connectPerIPBurst, bucketIdle),
		global: newIPRateLimiter(connectGlobalRate, connectGlobalBurs, bucketIdle),
	}
}

// locked 报告是否处在锁定期,以及还剩多久。
func (g *connectGuard) locked(now time.Time) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.lockedUntil) {
		return true, g.lockedUntil.Sub(now)
	}
	return false, 0
}

// record 记一次结果;返回这次失败是否触发了锁定。
func (g *connectGuard) record(ok bool, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ok {
		g.failures = 0
		return false
	}
	g.failures++
	if g.failures >= connectLockAfter {
		g.failures = 0
		g.lockedUntil = now.Add(connectLockFor)
		return true
	}
	return false
}

// Locked 给 /api/status 用。
func (g *connectGuard) LockedUntil(now time.Time) *time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.lockedUntil) {
		t := g.lockedUntil
		return &t
	}
	return nil
}

type connectRequest struct {
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
}

type connectResponse struct {
	DeviceID int64  `json:"device_id"`
	Name     string `json:"name"`
	Token    string `json:"token"`
}

func tooMany(w http.ResponseWriter, retry time.Duration, msg string) {
	w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	writeError(w, http.StatusTooManyRequests, msg)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	g := s.connect
	if ok, retry := g.perIP.take(clientIP(r), now); !ok {
		tooMany(w, retry, "试得太频繁,请稍后再试")
		return
	}
	if ok, retry := g.global.take("*", now); !ok {
		tooMany(w, retry, "设备接入请求太多,请稍后再试")
		return
	}
	if locked, left := g.locked(now); locked {
		tooMany(w, left, "设备密码连续输错太多次,接入已锁定,"+left.Round(time.Minute).String()+"后再试")
		return
	}
	if r.TLS == nil && !fromLoopback(r) {
		writeError(w, http.StatusForbidden, "设备密码只能经 HTTPS 发送:用 mpx connect 接入(它走 HTTPS 并核对证书指纹)")
		return
	}

	var req connectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	name := strings.TrimSpace(req.DeviceName)
	if name == "" || len([]rune(name)) > maxDeviceNameLen {
		writeError(w, http.StatusBadRequest, "设备名不能为空,也不能超过 64 个字符")
		return
	}

	hash, err := s.store.DevicePasswordHash()
	if err != nil {
		s.writeStoreError(w, err, "设备密码")
		return
	}
	if hash == "" {
		writeError(w, http.StatusForbidden, "这个控制面还没设设备密码;在已接入的设备上跑 mpx device password")
		return
	}
	ok, err := verifyDevicePassword(req.Password, hash)
	if err != nil {
		s.logger.Error("校验设备密码失败", "err", err)
		writeError(w, http.StatusInternalServerError, "校验设备密码失败")
		return
	}
	if !ok {
		if g.record(false, now) {
			s.logger.Warn("设备密码连续输错,接入已锁定", "for", connectLockFor)
		}
		s.logger.Warn("设备接入:密码错误", "remote", r.RemoteAddr, "device", name)
		writeError(w, http.StatusUnauthorized, "设备密码不对")
		return
	}
	g.record(true, now)

	tok, err := newDeviceToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成设备 token 失败")
		return
	}
	d, err := s.store.CreateDevice(name, hashToken(tok), now)
	if err != nil {
		s.writeStoreError(w, err, "设备")
		return
	}
	s.logger.Info("新设备已接入", "device", d.ID, "name", name, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusCreated, connectResponse{DeviceID: d.ID, Name: d.Name, Token: tok})
}

type devicePasswordRequest struct {
	Password  string `json:"password"`
	RevokeAll bool   `json:"revoke_all"`
}

// handleSetDevicePassword 设置或修改设备密码(要已接入)。改密码不吊销已发出的
// 设备 token —— 密码只是"领钥匙"的凭据;要踢人用 revoke_all 或逐台吊销。
func (s *Server) handleSetDevicePassword(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil && !fromLoopback(r) {
		writeError(w, http.StatusForbidden, "设备密码只能经 HTTPS 发送")
		return
	}
	var req devicePasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if err := checkDevicePasswordStrength(req.Password); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	hash, err := hashDevicePassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "哈希设备密码失败")
		return
	}
	if err := s.store.SetDevicePasswordHash(hash); err != nil {
		s.writeStoreError(w, err, "设备密码")
		return
	}
	var revoked int64
	if req.RevokeAll {
		if revoked, err = s.store.DeleteAllDevices(); err != nil {
			s.writeStoreError(w, err, "设备")
			return
		}
	}
	s.logger.Info("设备密码已更新", "revoked", revoked)
	writeJSON(w, http.StatusOK, map[string]any{"set": true, "revoked": revoked})
}

// handleDisableDevicePassword 关闭设备密码接入。已接入的设备不受影响。
func (s *Server) handleDisableDevicePassword(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetDevicePasswordHash(""); err != nil {
		s.writeStoreError(w, err, "设备密码")
		return
	}
	s.logger.Info("设备密码接入已关闭")
	w.WriteHeader(http.StatusNoContent)
}
