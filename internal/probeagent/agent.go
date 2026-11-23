// Package probeagent 是探测点(mpx probe run)的常驻编排:探测点现在的唯一
// 职责是 CDN IP 优选 —— 每轮 tick 向控制面领名单,做完复检/扫描把结论
// 回传。境内活性探测与带宽补测已在 UC-30 退役;UC-31 给优选补了丢包与
// 吞吐两个度量维度。
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
// UC-31:每 IP 拨 ping_count 次出丢包率;边缘通过的按延迟升序取前
// speed_count 个向 speed_url 做限时下载测速,速度进结果。丢包超
// loss_max 的直接不验证不入选。测速地址整体不可达(前两个都失败)时
// 整轮测速跳过,结果照常回存 —— 地址挂不是 IP 挂。
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
		PingCount     int      `json:"ping_count"`
		LossMax       float64  `json:"loss_max"`
		SpeedURL      string   `json:"speed_url"`
		SpeedCount    int      `json:"speed_count"`
		SpeedSeconds  int      `json:"speed_seconds"`
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
	pings := rt.PingCount
	if pings < 1 {
		pings = 1
	}
	var results []model.CDNResult
	appendResults := func(rs []cdn.Result) {
		for _, v := range rs {
			results = append(results, model.CDNResult{
				IP: v.IP, Reachable: v.Reachable, LatencyMs: v.LatencyMs, Loss: v.Loss,
				Edge: v.Edge, Colo: v.Colo, SpeedMbps: v.SpeedMbps,
			})
		}
	}
	var edge []cdn.Result // 本轮全部边缘通过项,测速从这里挑
	if len(rt.Recheck) > 0 {
		// 池内成员无条件核完,失败的也要报 —— 控制面靠失败结论摘下入站。
		rs := cdn.Recheck(ctx, rt.Recheck, pings, pingTimeout, cdnVerifyTimeout)
		for _, r := range rs {
			if r.Edge {
				edge = append(edge, r)
			}
		}
		appendResults(rs)
	}
	scan := rt.Scan
	if len(rt.Recheck) == 0 && len(scan) == 0 {
		// 旧版控制面没有 recheck/scan 字段,ips 就是全部候选(含池成员),
		// 沿用旧管道路由即可。
		scan = rt.IPs
	}
	if len(scan) > 0 {
		candidates := cdn.ProbeTCP(ctx, scan, rt.SweepWant, pings, pingTimeout)
		// UC-31:丢包超阈的不进验证段 —— 高丢包 IP 即使验证过也上不了
		// 入口,省下验证开销。
		var qualified []cdn.Result
		for _, c := range candidates {
			if c.Loss <= rt.LossMax {
				qualified = append(qualified, c)
			}
		}
		if len(qualified) > 0 {
			rs := cdn.Verify(ctx, qualified, rt.VerifyWant, cdnVerifyTimeout)
			edge = append(edge, rs...)
			appendResults(rs)
		}
	}
	// UC-31:边缘通过后测速。speed_url 空表示不测(退化 UC-21);测速
	// 地址整体不可达时前两发全失败,后面不再浪费 —— 判地址挂不判 IP 挂。
	if rt.SpeedURL != "" && rt.SpeedCount > 0 && len(edge) > 0 {
		speedSecs := rt.SpeedSeconds
		if speedSecs < 1 {
			speedSecs = 8
		}
		speedTimeout := time.Duration(speedSecs) * time.Second
		tested, aborted := 0, false
		for i := range results {
			if tested >= rt.SpeedCount || aborted {
				break
			}
			if !results[i].Edge {
				continue
			}
			mbps, colo, err := cdn.MeasureSpeed(ctx, results[i].IP, rt.SpeedURL, speedTimeout)
			tested++
			if err != nil {
				if tested >= 2 && results[i].SpeedMbps == 0 {
					// 连续两个边缘测不出:测速地址挂了,整轮测速停手。
					// 结果里速度字段全为 0,控制面按丢包+延迟兜底排序。
					aborted = true
					if logf != nil {
						logf("测速地址疑似不可达(%s),本轮测速中止", rt.SpeedURL)
					}
					break
				}
				continue
			}
			results[i].SpeedMbps = mbps
			if colo != "" {
				results[i].Colo = colo
			}
		}
	}
	if len(results) == 0 {
		return nil
	}
	_, err = call("POST", "/api/cdn-optimize/results", map[string]any{"results": results})
	return err
}
