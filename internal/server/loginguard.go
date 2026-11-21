package server

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// 无凭据领凭据的接口(POST /api/admin/login)单独挂、单独限流,三道闸:
//   - 每个 IP 每分钟 5 次;
//   - 全局每分钟 20 次 —— 防换着 IP 撞;
//   - 连续失败 20 次就整体锁 15 分钟。已有会话不受影响,它们走 token。
//
// 而且只在 HTTPS(或回环)上受理:密码不能明文过公网。
//
// 这套闸的前身是设备接入闸(UC-26 前 /api/connect);设备模型退役后同一套
// 机制只剩登录一个使用者,故类型改名 loginGuard,接口语义不变。

const (
	loginPerIPRate  = 5.0 / 60
	loginPerIPBurst = 5.0
	loginGlobalRate = 20.0 / 60
	loginGlobalBurs = 20.0
	loginLockAfter  = 20
	loginLockFor    = 15 * time.Minute
)

type loginGuard struct {
	perIP  *ipRateLimiter
	global *ipRateLimiter

	mu          sync.Mutex
	failures    int
	lockedUntil time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{
		perIP:  newIPRateLimiter(loginPerIPRate, loginPerIPBurst, bucketIdle),
		global: newIPRateLimiter(loginGlobalRate, loginGlobalBurs, bucketIdle),
	}
}

// locked 报告是否处在锁定期,以及还剩多久。
func (g *loginGuard) locked(now time.Time) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.lockedUntil) {
		return true, g.lockedUntil.Sub(now)
	}
	return false, 0
}

// record 记一次结果;返回这次失败是否触发了锁定。
func (g *loginGuard) record(ok bool, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ok {
		g.failures = 0
		return false
	}
	g.failures++
	if g.failures >= loginLockAfter {
		g.failures = 0
		g.lockedUntil = now.Add(loginLockFor)
		return true
	}
	return false
}

// LockedUntil 给 /api/status 用。
func (g *loginGuard) LockedUntil(now time.Time) *time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.lockedUntil) {
		t := g.lockedUntil
		return &t
	}
	return nil
}

func tooMany(w http.ResponseWriter, retry time.Duration, msg, uiHint string) {
	w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	writeErrorHint(w, http.StatusTooManyRequests, msg, uiHint)
}

// waitText 把还要等多久说成人话:不到一分钟按秒,其余按分钟向上取整。
func waitText(d time.Duration) string {
	if d < time.Minute {
		sec := int((d + time.Second - 1) / time.Second)
		if sec < 1 {
			sec = 1
		}
		return strconv.Itoa(sec) + " 秒"
	}
	return strconv.Itoa(int((d+time.Minute-1)/time.Minute)) + " 分钟"
}
