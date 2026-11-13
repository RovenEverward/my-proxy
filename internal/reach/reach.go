// Package reach 是节点触达检查的核心:从某台机器出发,对目标(地址:端口)做一次
// TCP 握手和一次 UDP 探活,把结果分成 可达 / 不可达 / 未知 三档。
//
// 它只负责「发起探测的机器能看到什么」,不编排双来源、不碰数据库:
// 操作机(mpx node check、mpx ui 的 /local/reach)与控制面(POST /api/reach/probe)
// 跑的是同一份探针,各探各的、结果再合到一起。
//
// 判定口径(见 UC-06):
//   - TCP:握手成功=可达,拒绝=不可达(拒绝),超时=不可达(超时),其余错误=未知。
//   - UDP:收到应用回包=可达,ICMP 端口不可达=不可达,超时=未知 ——
//     UDP 服务端收到不认识的包可以合法地沉默,所以 UDP 超时只能算未知,不能算不可达。
package reach

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Cell 是一个(目标, 协议)的探测结果。
type Cell struct {
	Status    string `json:"status"`               // reachable / timeout / refused / unreachable / unknown
	LatencyMs int    `json:"latency_ms,omitempty"` // 仅可达时填
	Error     string `json:"error,omitempty"`      // 原始错误,诊断用,不进判定
}

const (
	CellReachable   = "reachable"   // 探到了
	CellTimeout     = "timeout"     // TCP 超时 / UDP 超时(UDP 只算未知)
	CellRefused     = "refused"     // TCP 拒绝 / UDP 端口不可达
	CellUnknown     = "unknown"     // 解析失败、网络错误、UDP 无回包
	CellUnreachable = "unreachable" // 保留码位:网络不可达类错误
)

// Target 是要探的一个入站端口:节点对外地址 + 入站端口。
type Target struct {
	InboundID int64  `json:"inbound_id"`
	NodeID    int64  `json:"node_id"`
	NodeName  string `json:"node_name"`
	Label     string `json:"label"`    // 订阅里的名字或 tag
	Address   string `json:"address"`  // 节点对外地址
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"` // vless / hysteria2 / …
	Network   string `json:"network"`  // tcp / udp / ws / …
}

// Result 是一个入站两个协议的探测结果。探针缺席表示该协议不适用:
// 纯 UDP 协议的 TCP 是 nil,纯 TCP 系入站的 UDP 也是 nil —— 每个入站仍各探一次
// 协议无关的端口探活没有意义,UDP 探测只对本来就走 UDP 的入站做。
type Result struct {
	InboundID int64 `json:"inbound_id"`
	TCP       *Cell `json:"tcp,omitempty"`
	UDP       *Cell `json:"udp,omitempty"`
}

// UDP-able:协议本身是 QUIC 系,或传输显式写着 udp。
func (t Target) udp() bool {
	if t.Network == "udp" {
		return true
	}
	switch t.Protocol {
	case "hysteria2", "tuic", "wireguard", "shadowsocks":
		return true
	}
	return false
}

// SourceSummary 是一个来源对一批入站的合并结论,供节点级判定。
type SourceSummary struct {
	Probed    bool `json:"probed"`              // 至少探过一个目标
	Reachable bool `json:"reachable"`           // 至少一个目标探到
	Failed    bool `json:"failed"`              // 探过、且确定不通(不是探不出来)
	LatencyMs int  `json:"latency_ms,omitempty"` // 最快一个可达的时延
}

// Verdict 值。
const (
	VerdictOK      = "ok"      // 本机与控制面都能触达
	VerdictBlocked = "blocked" // 本机不通、控制面可达:境内路径问题
	VerdictDown    = "down"    // 两边都不通:更像节点本身故障
	VerdictUnknown = "unknown" // 至少一侧探测没结果,不下结论
)

// Summarize 把一批目标探合并成一个来源结论,供节点级判定。
// 每个入站只由它自己的协议说话:TCP 系入站看 TCP 格,QUIC/UDP 系看 UDP 格;
// 另一协议的探测结果只留在明细里 —— 在 UDP 端口上探 TCP 必失败,混进判定
// 会把"该协议本来就不该通"误读成"节点不通"。
func Summarize(targets []Target, results map[int64]*Result) SourceSummary {
	s := SourceSummary{}
	for _, t := range targets {
		r := results[t.InboundID]
		if r == nil {
			continue
		}
		cell := r.TCP
		if t.udp() {
			cell = r.UDP
		}
		if cell == nil {
			continue
		}
		s.Probed = true
		switch cell.Status {
		case CellReachable:
			s.Reachable = true
			if s.LatencyMs == 0 || cell.LatencyMs < s.LatencyMs {
				s.LatencyMs = cell.LatencyMs
			}
		case CellTimeout:
			// UDP 的超时已经记成 CellUnknown,能走到这的都是 TCP 超时。
			s.Failed = true
		case CellRefused, CellUnreachable:
			s.Failed = true
		}
	}
	return s
}

// Verdict 合并两个来源的结论是节点级判定。
func Verdict(local, cp SourceSummary) string {
	switch {
	case local.Reachable && cp.Reachable:
		return VerdictOK
	case !local.Reachable && cp.Reachable:
		if local.Probed && local.Failed {
			return VerdictBlocked
		}
		return VerdictUnknown
	case local.Reachable && !cp.Reachable:
		return VerdictUnknown
	default:
		if local.Failed && cp.Failed {
			return VerdictDown
		}
		return VerdictUnknown
	}
}

// Options 是探测的上限。零值取默认值。
type Options struct {
	Timeout     time.Duration // 单个探针的上限,默认 4s
	Concurrency int           // 同时在飞的探针数,默认 32
	UDPWait     time.Duration // UDP 等回包的时间,默认 2.5s
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return 4 * time.Second
	}
	return o.Timeout
}
func (o Options) concurrency() int {
	if o.Concurrency <= 0 {
		return 32
	}
	return o.Concurrency
}
func (o Options) udpWait() time.Duration {
	if o.UDPWait <= 0 {
		return 2500 * time.Millisecond
	}
	return o.UDPWait
}

// udpPayload 是 UDP 探活发的负载。不冒充任何协议:探测的语义是「端口后面有没有
// 东西在收」,协议级握手属于体检不是触达检查。很多 UDP 服务端会丢不认识的包,
// 所以 UDP 没回包只能判 unknown(见包级注释)。
var udpPayload = []byte{0x01, 0x02, 0x03, 0x04}

// Probe 对一批目标并发探测,按 InboundID 收结果。一个目标探 TCP(总是)
// 与 UDP(仅该入站本来就走 UDP 时),两个探针独立计时、互不阻塞。
func Probe(ctx context.Context, targets []Target, opts Options) map[int64]*Result {
	results := make(map[int64]*Result, len(targets))
	var mu sync.Mutex
	sem := make(chan struct{}, opts.concurrency())
	var wg sync.WaitGroup
	for _, t := range targets {
		t := t
		res := &Result{InboundID: t.InboundID}
		mu.Lock()
		results[t.InboundID] = res
		mu.Unlock()
		probes := []func() *Cell{func() *Cell { return probeTCP(ctx, t, opts.timeout()) }}
		if t.udp() {
			probes = append(probes, func() *Cell { return probeUDP(ctx, t, opts.timeout(), opts.udpWait()) })
		}
		for i, p := range probes {
			i, p := i, p
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				cell := p()
				if i == 0 {
					res.TCP = cell
				} else {
					res.UDP = cell
				}
			}()
		}
	}
	wg.Wait()
	return results
}

func probeTCP(ctx context.Context, t Target, timeout time.Duration) *Cell {
	addr := net.JoinHostPort(t.Address, fmt.Sprint(t.Port))
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return &Cell{Status: classifyDial(err), Error: err.Error()}
	}
	_ = conn.Close()
	return &Cell{Status: CellReachable, LatencyMs: int(time.Since(start).Milliseconds())}
}

func probeUDP(ctx context.Context, t Target, timeout, wait time.Duration) *Cell {
	addr := net.JoinHostPort(t.Address, fmt.Sprint(t.Port))
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return &Cell{Status: classifyDial(err), Error: err.Error()}
	}
	defer conn.Close()
	start := time.Now()
	// 写两次:有的栈要等第二次写才报 ICMP 端口不可达。
	for i := 0; i < 2; i++ {
		if _, err = conn.Write(udpPayload); err != nil {
			return &Cell{Status: classifyDial(err), Error: err.Error()}
		}
		time.Sleep(30 * time.Millisecond)
	}
	deadline := start.Add(wait)
	if d := time.Until(deadline); d > 0 {
		_ = conn.SetReadDeadline(deadline)
		buf := make([]byte, 2048)
		_, err = conn.Read(buf)
	}
	switch {
	case err == nil:
		return &Cell{Status: CellReachable, LatencyMs: int(time.Since(start).Milliseconds())}
	case isTimeout(err):
		return &Cell{Status: CellUnknown} // UDP 超时不算不可达
	case isRefused(err):
		return &Cell{Status: CellRefused, Error: err.Error()}
	case isUnreachable(err):
		return &Cell{Status: CellUnreachable, Error: err.Error()}
	default:
		return &Cell{Status: CellUnknown, Error: err.Error()}
	}
}

// classifyDial 把 dial 错误归到判定档。
func classifyDial(err error) string {
	switch {
	case isRefused(err):
		return CellRefused
	case isUnreachable(err):
		return CellUnreachable
	case isTimeout(err):
		return CellTimeout
	default:
		return CellUnknown
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func isRefused(err error) bool {
	var oe *net.OpError
	return errors.As(err, &oe) && strings.Contains(oe.Err.Error(), "refused")
}

func isUnreachable(err error) bool {
	var oe *net.OpError
	return errors.As(err, &oe) && strings.Contains(oe.Err.Error(), "unreachable")
}
