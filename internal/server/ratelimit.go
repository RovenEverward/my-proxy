package server

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// 按客户端 IP 的令牌桶限流,只罩两类入口:/sub(公网、无鉴权、凭证在路径里)
// 和 /api(有鉴权,但仍有暴力撞 token 的价值)。每类一组常数,不做配置项。
//
// 不用 x/time/rate,是因为只需要「按 IP 一桶、满了拒掉」这一件事;x/time 的
// 等待与预约语义这里都用不上,一个 map + 每秒令牌数就够,也省掉一条依赖。
//
// 桶按 IP 分,且只在内存里:进程重启即重置,不需要持久化。map 无限增长的风险
// 用惰性淘汰兜住:take 时顺手把过期桶扔掉;理论上最坏也是“每个曾访问过的 IP
// 各留一个几十字节的桶”,比不限流时被扫的代价小得多。
//
// 上限按真实使用拍:/api 是单运维操作,几十 QPS 都嫌多;/sub 会被客户端定时
// 刷新,量稍大些。两个桶独立挂,因为两边的合理速率差着一个量级。

type ipBucket struct {
	tokens float64   // 当前令牌数,可到 burst
	last   time.Time // 上次补充令牌的时刻
	seen   time.Time // 上次命中时刻,惰性淘汰用
}

type ipRateLimiter struct {
	mu    sync.Mutex
	rate  float64 // 每秒令牌数
	burst float64 // 桶容量,决定能容忍多大的瞬时突发
	idle  time.Duration
	items map[string]*ipBucket
}

func newIPRateLimiter(rate, burst float64, idle time.Duration) *ipRateLimiter {
	return &ipRateLimiter{rate: rate, burst: burst, idle: idle, items: make(map[string]*ipBucket)}
}

// take 消耗一个令牌;false 表示该 IP 已超流。retryAfter 给调用方写 Retry-After。
func (l *ipRateLimiter) take(ip string, now time.Time) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.items[ip]
	if b == nil {
		b = &ipBucket{tokens: l.burst, last: now, seen: now}
		l.items[ip] = b
	}
	b.seen = now

	// 惰性淘汰:借着这次互斥把过期桶清掉,不用另起 goroutine 扫。
	for k, v := range l.items {
		if now.Sub(v.seen) > l.idle {
			delete(l.items, k)
		}
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		// 距下一个令牌还差多少,换算成整数秒给 Retry-After。
		return false, time.Duration((1-b.tokens)/l.rate*float64(time.Second)) + time.Second
	}
	b.tokens--
	return true, 0
}

// clientIP 取对端地址做限流键。不走 X-Forwarded-For:那个头可以伪造,而这台
// 服务前面没有可信的代理链把真实客户端地址传进来;连进来到的对端地址至少与
// 连接一致。被同一出口 NAT 的多人共享一个桶,是可接受的代价。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimit 把 next 罩在限流后面:超流直接 429 + Retry-After,不再进业务。
func (s *Server) rateLimit(l *ipRateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.take(clientIP(r), time.Now()); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
			writeError(w, http.StatusTooManyRequests, "请求太频繁,请稍后再试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// subLimit 与 apiLimit 是挂载点用的两个桶。路径前缀要精确匹配:/sub 只罩
// /sub/ 开头的路径(订阅本体与 rotate),/api 罩整个管理面。
const (
	subRate, subBurst = 10.0, 30.0
	// 桌面端 / mpx ui 的代理把所有请求都从一个 IP 进来,一次页面加载就有约 15 个请求
	// 同时打上来,桶要比「单运维手工操作」的口径宽出这 15 个的量。
	apiRate, apiBurst = 10.0, 60.0
	// 桶闲置多久可以扔。比客户端刷新间隔长一点,免得一轮重连就把桶删掉。
	bucketIdle = 10 * time.Minute
)

func newSubLimiter() *ipRateLimiter { return newIPRateLimiter(subRate, subBurst, bucketIdle) }

// newAPILimiter 的桶默认每秒 10、突发 60。浏览器测试可以让它宽一些:一个进程、一个 IP、
// 几十条用例连着跑,突发 60 一会就见底,429 会拖慢整套。只认正数,读不到或写错都按默认走。
func newAPILimiter() *ipRateLimiter {
	burst := apiBurst
	if v := os.Getenv("MPX_API_BURST"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			burst = n
		}
	}
	return newIPRateLimiter(apiRate, burst, bucketIdle)
}
