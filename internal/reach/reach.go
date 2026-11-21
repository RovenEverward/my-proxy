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
//   - QUIC 栈(TUIC/Hysteria2):探测负载是不支持版本号的 QUIC Initial,
//     活着的监听器必须回 Version Negotiation —— 有回包=可达,读超时=不可达
//     (ISS-17),不再是「可能沉默」。裸 UDP 的 unknown 语义不变。
package reach

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
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
	CellTimeout     = "timeout"     // TCP 超时;QUIC 端点 UDP 无回包(协议要求必须回,不回即不通,ISS-17)
	CellRefused     = "refused"     // TCP 拒绝 / UDP 端口不可达
	CellUnknown     = "unknown"     // 解析失败、网络错误、裸 UDP 无回包(裸 UDP 允许合法沉默)
	CellUnreachable = "unreachable" // 保留码位:网络不可达类错误(ICMP 网络/主机不可达)
)

// Target 是要探的一个入站端口:对外地址 + 端口。自建节点按入站生成;
// 外部订阅端点(UC-12)也生成 Target,此时 Subject/TID 标记对象与端点身份,
// InboundID/NodeID 为零值,NodeName 用来署名。
type Target struct {
	InboundID int64  `json:"inbound_id"`
	NodeID    int64  `json:"node_id"`
	NodeName  string `json:"node_name"`
	Label     string `json:"label"`   // 订阅里的名字或 tag
	Address   string `json:"address"` // 节点对外地址
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"` // vless / hysteria2 / …
	Network   string `json:"network"`  // tcp / udp / ws / …

	// UC-12:对象身份与端点身份。自建目标 Subject 为 "node:<节点ID>"、
	// TID 为 "inbound:<入站ID>";外部目标 Subject 为 render.EndpointKey
	// ("s:<源ID>:<指纹>")、TID 与 Subject 相同(外部按端点判定)。
	Subject string `json:"subject,omitempty"`
	TID     string `json:"tid,omitempty"`
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
	case "hysteria2", "tuic", "wireguard":
		return true
	}
	return false
}

// quic 说该目标底层是 QUIC 栈(TUIC / Hysteria2):裸 UDP 探针探不出活
// ——服务端只回合法握手,扔随机字节过去永远静默,只有 QUIC 版本协商
// 能逼它开口(见 probeUDP)。
func (t Target) quic() bool {
	switch t.Protocol {
	case "tuic", "hysteria2":
		return true
	}
	return false
}

// SourceSummary 是一个来源对一批入站的合并结论,供节点级判定。
type SourceSummary struct {
	Probed    bool `json:"probed"`               // 至少探过一个目标
	Reachable bool `json:"reachable"`            // 至少一个目标探到
	Failed    bool `json:"failed"`               // 探过、且确定不通(不是探不出来)
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

// Key 返回目标的稳定身份:外部端点用 TID,自建入站用 InboundID 的字符串形。
// 同一目标在不同来源的结果按它归并。
func (t Target) Key() string {
	if t.TID != "" {
		return t.TID
	}
	return strconv.FormatInt(t.InboundID, 10)
}

// Probe 对一批目标并发探测,按 InboundID 收结果。一个目标探 TCP(总是)
// 与 UDP(仅该入站本来就走 UDP 时),两个探针独立计时、互不阻塞。
// UC-12 起 Target.Key() 是字符串身份("inbound:<id>"),键要先经目标表回译
// 成入站号,不能直接 ParseInt —— 那会把所有结果丢成空 map。
func Probe(ctx context.Context, targets []Target, opts Options) map[int64]*Result {
	keyed := ProbeKeyed(ctx, targets, opts)
	inboundOf := make(map[string]int64, len(targets))
	for _, t := range targets {
		inboundOf[t.Key()] = t.InboundID
	}
	results := make(map[int64]*Result, len(keyed))
	for k, r := range keyed {
		if id := inboundOf[k]; id != 0 {
			results[id] = r
		}
	}
	return results
}

// ProbeKeyed 与 Probe 同源,结果按 Target.Key() 归集:外部订阅端点的
// InboundID 全是零值,整数键会撞成一坨,按字符串身份收才对。
func ProbeKeyed(ctx context.Context, targets []Target, opts Options) map[string]*Result {
	results := make(map[string]*Result, len(targets))
	var mu sync.Mutex
	sem := make(chan struct{}, opts.concurrency())
	var wg sync.WaitGroup
	for _, t := range targets {
		t := t
		res := &Result{InboundID: t.InboundID}
		mu.Lock()
		results[t.Key()] = res
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

// CanaryTargets 是境内探测端的金丝雀清单:境内直连不可能探通的目标。
// 境内侧拨测若把其中任一目标探通,说明本机出机路径被代理接管,那一侧
// 的「可达」结果不可信,不该回存 —— ISS-14。
var CanaryTargets = []string{"www.google.com:443", "www.youtube.com:443"}

// TrustedPath 检查本机出机路径是不是干净的境内直连:逐个对 CanaryTargets
// 做 TCP 拨测,任一可达即不可信。只覆盖 TCP —— 接管代理对 UDP 的支持参差,
// TCP 足够暴露「全量被接管」这一态;一个都探不通才算可信。
func TrustedPath(ctx context.Context, timeout time.Duration) bool {
	return trustedPath(ctx, timeout, CanaryTargets)
}

// trustedPath 与 TrustedPath 同义,金丝雀清单由调用方给(测试用)。
func trustedPath(ctx context.Context, timeout time.Duration, addrs []string) bool {
	if timeout <= 0 {
		timeout = 2500 * time.Millisecond
	}
	for _, addr := range addrs {
		d := net.Dialer{Timeout: timeout}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return false
		}
	}
	return true
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
	// QUIC 栈(TUIC/hy2)探活:发一个带「不支持的版本号」的 QUIC Initial 头,
	// 协议栈必须回 Version Negotiation —— 一次回包就够证明监听器活着,
	// 不需要凭据、不引入 QUIC 依赖。random 负载它们只会静默丢,永远 unknown。
	payload := udpPayload
	if t.quic() {
		payload = quicVNProbe()
	}
	// 写两次:有的栈要等第二次写才报 ICMP 端口不可达。
	for i := 0; i < 2; i++ {
		if _, err = conn.Write(payload); err != nil {
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
		if t.quic() {
			// QUIC 栈必须回 Version Negotiation,读超时=协议层确定的「不通」,
			// 不是裸 UDP 的「可能沉默」(ISS-17)。
			return &Cell{Status: CellTimeout}
		}
		return &Cell{Status: CellUnknown} // 裸 UDP 允许合法沉默,超时不算不可达
	case isRefused(err):
		return &Cell{Status: CellRefused, Error: err.Error()}
	case isUnreachable(err):
		return &Cell{Status: CellUnreachable, Error: err.Error()}
	default:
		return &Cell{Status: CellUnknown, Error: err.Error()}
	}
}

// quicVNProbe 造一个 QUIC Initial:long header + 版本 0x0a0a0a01(保留位、
// 任何实现都不支持)+ 8 字节 DCID,补齐 1200 字节 —— RFC 9000 要求服务
// 端丢弃小于 1200 的 Initial,不补满根本走不到版本协商。收到的栈必须用
// Version Negotiation 答复:有监听器就有回包。
func quicVNProbe() []byte {
	buf := make([]byte, 0, 1200)
	buf = append(buf, 0xC0)                                           // long header + fixed bit + type=Initial
	buf = append(buf, 0x0a, 0x0a, 0x0a, 0x01)                         // unsupported version
	buf = append(buf, 0x08)                                           // DCID len
	buf = append(buf, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08) // DCID
	buf = append(buf, 0x00)                                           // SCID len
	buf = append(buf, 0x40, 0x40)                                     // packet number len (varint 0)
	buf = append(buf, make([]byte, 1200-len(buf))...)
	return buf
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
