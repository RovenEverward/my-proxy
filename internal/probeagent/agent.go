// Package probeagent 是探测点(mpx probe run)的常驻编排:探测点现在的唯一
// 职责是 CDN IP 优选 —— 每轮 tick 向控制面领名单,做完复检/扫描把结论
// 回传。境内活性探测与带宽补测已在 UC-30 退役。
package probeagent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/RovenEverward/my-proxy/internal/cdn"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// Caller 是发一次控制面调用的抽象:method、路径、可选 JSON body,回原始响应体。
type Caller func(method, path string, body any) ([]byte, error)

// tick 是调度粒度:一分钟看一次有没有 CDN 优选任务。
const tick = time.Minute

// cdnVerifyTimeout 是一次 TLS+HTTP 边缘验证(要 CF-RAY)的超时,与
// server 侧 cdnVerifyTimout 保持一致 —— 探测端自己也要超时,不能只靠
// 服务端假设。
const cdnVerifyTimeout = 5 * time.Second

// Tick 跑一轮:CDN 优选任务做一批。控制面按 cdn_optimize 开关与池状态决定
// 给不给名单,探测端不自己判定该不该跑。
// logf 用来报非致命警告(如金丝雀判定路径被代理),nil 时静默。
func Tick(ctx context.Context, call Caller, logf func(string, ...any)) error {
	return cdnOptimize(ctx, call, logf)
}

// Loop 是常驻轮转:先跑一轮,之后每 tick 一次,直到 ctx 结束。
// 单轮出错只记日志不退出 —— 探测端的价值在「一直在」,不在「一次都不能错」。
func Loop(ctx context.Context, call Caller, logf func(string, ...any), onError func(error)) {
	for {
		if err := Tick(ctx, call, logf); err != nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tick):
		}
	}
}

// cdnOptimize 是一轮 CDN IP 优选:向控制面领名单 —— recheck 是池内
// 全体成员(逐个复检,成败都回报;miss 只对实测结论计数),scan 是从
// IP 段新抽的样本(粗扫 TCP:443 拿够 sweep_want 个候选,再按延迟升序
// 逐个 TLS+HTTP 验证要 CF-RAY,凑够 verify_want 个真边缘回存)。
// 名单为空(开关关着)直接返回。金丝雀检查:境内路径被代理接管时这批
// 优选结果全是假的,整轮跳过不回存。
func cdnOptimize(ctx context.Context, call Caller, logf func(string, ...any)) error {
	raw, err := call("GET", "/api/cdn-optimize/targets", nil)
	if err != nil {
		return err
	}
	var rt struct {
		IPs           []string `json:"ips"`
		Recheck       []string `json:"recheck"`
		Scan          []string `json:"scan"`
		SweepWant     int      `json:"sweep_want"`
		VerifyWant    int      `json:"verify_want"`
		PingTimeoutMs int      `json:"ping_timeout_ms"`
	}
	if err := json.Unmarshal(raw, &rt); err != nil {
		return err
	}
	if len(rt.IPs) == 0 {
		return nil
	}
	// 金丝雀:境内路径被接管时 CF-RAY 都可能「通」得出来(代理出口就
	// 能连 Cloudflare),数据没有意义。
	if !cdn.TrustedPath(ctx, 0) {
		if logf != nil {
			logf("境内路径被代理接管(金丝雀可达),CDN 优选这一轮跳过不回存")
		}
		return nil
	}
	pingTimeout := 800 * time.Millisecond
	if rt.PingTimeoutMs > 0 {
		pingTimeout = time.Duration(rt.PingTimeoutMs) * time.Millisecond
	}
	var results []model.CDNResult
	appendResults := func(rs []cdn.Result) {
		for _, v := range rs {
			results = append(results, model.CDNResult{
				IP: v.IP, Reachable: v.Reachable, LatencyMs: v.LatencyMs,
				Edge: v.Edge, Colo: v.Colo,
			})
		}
	}
	if len(rt.Recheck) > 0 {
		// 池内成员无条件核完,失败的也要报 —— 控制面靠失败结论摘下入站。
		appendResults(cdn.Recheck(ctx, rt.Recheck, pingTimeout, cdnVerifyTimeout))
	}
	scan := rt.Scan
	if len(rt.Recheck) == 0 && len(scan) == 0 {
		// 旧版控制面没有 recheck/scan 字段,ips 就是全部候选(含池成员),
		// 沿用旧管道路由即可。
		scan = rt.IPs
	}
	if len(scan) > 0 {
		candidates := cdn.ProbeTCP(ctx, scan, rt.SweepWant, pingTimeout)
		if len(candidates) > 0 {
			appendResults(cdn.Verify(ctx, candidates, rt.VerifyWant, cdnVerifyTimeout))
		}
	}
	if len(results) == 0 {
		return nil
	}
	_, err = call("POST", "/api/cdn-optimize/results", map[string]any{"results": results})
	return err
}
