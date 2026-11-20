// Package bwtest 实现 UC-20 的带宽测量:在 mpx 二进制里内嵌一个 sing-box
// 客户端,把待测端点当出站、本机 socks 入站做下载测速,把真实吞吐报回控制面。
//
// 为什么内嵌而不是起外部二进制:操作机上不该有第二份要管的核心;端点凭据
// 走 /api/bandwidth/endpoint 下发,本机进程内完成测量,凭据不落盘。
package bwtest

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/reach"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// DefaultTestURL 是测速下载的默认目标:字节数由调用方拼上,
// 默认按 ~16MiB 取(两次各取一半,单次上限 20s 内能下完的量)。
const DefaultTestURL = "https://speed.cloudflare.com/__down?bytes=16777216"

// Options 是一次测量的输入。
type Options struct {
	// TestURL 是下载测速的地址;空值取 DefaultTestURL。
	TestURL string
	// Rounds 是同一端点重复下载的次数,结果取最好一次。0 取 2。
	Rounds int
	// PerRoundBytes 是单次下载的目标字节数;URL 自带 bytes= 参数时忽略。
	PerRoundBytes int64
	// Timeout 是单次下载的上限,0 取 20s。
	Timeout time.Duration
	// VerifyPath 是是否先过金丝雀(默认 true):检测本机出机是否被代理
	// 接管。被接管时先试着绑物理接口绕行;绕不了就拒绝,不拿代理路径的
	// 数字当真值回存。
	VerifyPath *bool
}

// Result 是一次测量的输出。
type Result struct {
	Status      string    `json:"status"` // ok / failed / unsupported
	BytesPerSec int64     `json:"bytes_per_sec,omitempty"`
	DurationMs  int64     `json:"duration_ms,omitempty"`
	Bytes       int64     `json:"bytes,omitempty"`
	Protocol    string    `json:"protocol,omitempty"`
	Interface   string    `json:"interface,omitempty"` // 实际绑的接口;没绑是空
	PathTrusted bool      `json:"path_trusted"`
	Error       string    `json:"error,omitempty"`
	MeasuredAt  time.Time `json:"measured_at"`
}

// Measure 端到端测一批端点的带宽:逐个尝试,拿到最好一次 ok 即返回;
// 全部失败返回最后一条错误。wireguard / 不支持的协议记 unsupported。
//
// ctx 控制整体超时;测量本身也按单次 Timeout 截断。
func Measure(ctx context.Context, eps []render.EndpointConfig, opt Options) Result {
	if len(eps) == 0 {
		return Result{Status: "failed", Error: "对象没有可用端点", MeasuredAt: time.Now().UTC()}
	}
	opt = opt.normalize()
	res := Result{Status: "failed", MeasuredAt: time.Now().UTC()}

	// 金丝雀:确认走真实物理网而不是被本机代理接管。
	if opt.verifyPath() {
		if !reach.TrustedPath(ctx, 0) {
			iface, err := defaultPhysicalInterface()
			if err != nil || !boundCanary(ctx, iface, 0) {
				res.Error = "本机出机路径被代理接管,且无法绕行到物理接口 —— 请把节点地址加入 TUN/代理 白名单后再测"
				res.Status = "failed"
				return res
			}
			res.Interface = iface
		}
		res.PathTrusted = true
	}

	// 找一条能测的端点:先排除协议不支持的,其余逐个测,取最好的 ok。
	var lastErr string
	var best *Result
	for _, cfg := range eps {
		ep := cfg.Endpoint()
		if !measurable(ep) {
			continue // 先不急着记 unsupported,后面有能测的协议再试
		}
		r := measureEndpoint(ctx, ep, res.Interface, opt)
		if r.Status == "ok" {
			if best == nil || r.BytesPerSec > best.BytesPerSec {
				cp := r
				best = &cp
			}
			continue
		}
		lastErr = r.Error
	}
	if best != nil {
		best.Interface = res.Interface
		best.PathTrusted = res.PathTrusted
		return *best
	}

	// 全部端点都不可测或都失败:区分「协议不支持」与「真的失败」。
	if allUnsupported(eps) {
		res.Status = "unsupported"
		res.Error = "协议不支持带宽测量(wireguard 或仅支持 UDP 的协议走不了 HTTP 下载)"
	} else {
		res.Error = lastErr
	}
	return res
}

func (o *Options) normalize() Options {
	out := *o
	if out.TestURL == "" {
		out.TestURL = DefaultTestURL
	}
	if out.Rounds <= 0 {
		out.Rounds = 2
	}
	if out.Timeout <= 0 {
		out.Timeout = 20 * time.Second
	}
	return out
}

func (o *Options) verifyPath() bool { return o.VerifyPath == nil || *o.VerifyPath }

// measurable 说这个端点能不能走 HTTP 下载测带宽:UDP-only 的协议
// (hysteria2/tuic/wireguard)与 bare TCP 不行之外的都按 sing-box outbound 试。
func measurable(ep render.Endpoint) bool {
	switch ep.Protocol {
	case "wireguard":
		return false
	}
	// sing-box outbound 里能承载 TCP 的协议都能测;quic 系(hysteria2/tuic)
	// 也是 TCP over QUIC 之上的 TCP,照样能走 mixed inbound 的 socks。
	return ep.Protocol.Valid()
}

func allUnsupported(eps []render.EndpointConfig) bool {
	for _, c := range eps {
		if measurable(c.Endpoint()) {
			return false
		}
	}
	return true
}

// measureEndpoint 起一个临时 sing-box,经 socks 下载测速。
func measureEndpoint(ctx context.Context, ep render.Endpoint, bindIface string, opt Options) Result {
	res := Result{Status: "failed", Protocol: string(ep.Protocol), MeasuredAt: time.Now().UTC()}
	out, err := render.SingBoxOutbound(ep, "bw")
	if err != nil {
		res.Error = fmt.Sprintf("渲染出站: %v", err)
		return res
	}
	if bindIface != "" {
		out["bind_interface"] = bindIface
	}

	// 组一个最小 sing-box 配置:local dns + mixed inbound + 这个出站。
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		res.Error = fmt.Sprintf("找端口: %v", err)
		return res
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	cfg := map[string]any{
		"log": map[string]any{"level": "error"},
		"dns": map[string]any{
			"servers": []any{map[string]any{"type": "local", "tag": "dns-local"}},
			"final":   "dns-local",
		},
		"inbounds": []any{map[string]any{
			"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": port,
		}},
		"outbounds": []any{out},
		"route":     map[string]any{"final": "bw"},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		res.Error = fmt.Sprintf("组配置: %v", err)
		return res
	}
	b, err := startBox(raw)
	if err != nil {
		res.Error = fmt.Sprintf("起 sing-box: %v", err)
		return res
	}
	defer b.Close()
	time.Sleep(300 * time.Millisecond) // 等 listener 起来

	// 下载测速:经本机 socks,按 Rounds 重复取最好一次。
	socks := &url.URL{Scheme: "socks5", Host: "127.0.0.1:" + itoa(port)}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(socks),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 测速只看吞吐不看证书
		},
	}
	var bestBps int64
	var bestBytes, bestMs int64
	for i := 0; i < opt.Rounds; i++ {
		bps, n, ms, err := downloadOnce(ctx, client, opt.TestURL, opt.Timeout)
		if err != nil {
			res.Error = err.Error()
			continue
		}
		if bps > bestBps {
			bestBps, bestBytes, bestMs = bps, n, ms
		}
	}
	if bestBps <= 0 {
		if res.Error == "" {
			res.Error = "下载测速没有拿到字节"
		}
		return res
	}
	res.Status = "ok"
	res.BytesPerSec = bestBps
	res.Bytes = bestBytes
	res.DurationMs = bestMs
	return res
}

// downloadOnce 拉一轮下载,按收到的字节数 / 实际耗时算字节/秒。
// 到上限时间或目标字节提前截断都算有效样本。
func downloadOnce(ctx context.Context, c *http.Client, testURL string, timeout time.Duration) (bps int64, n int64, ms int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("组请求: %w", err)
	}
	start := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("下载: %w", err)
	}
	defer resp.Body.Close()
	n, err = io.Copy(io.Discard, resp.Body)
	ms = time.Since(start).Milliseconds()
	if err != nil && !errorsIsDeadline(err) {
		return 0, n, ms, fmt.Errorf("下载: %w", err)
	}
	if ms <= 0 {
		ms = 1
	}
	if n <= 0 {
		return 0, 0, ms, fmt.Errorf("下载没有拿到字节")
	}
	return n * 1000 / ms, n, ms, nil
}

func errorsIsDeadline(err error) bool {
	return err != nil && strings.Contains(err.Error(), "context deadline exceeded")
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
