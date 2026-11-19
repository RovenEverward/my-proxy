// Package cdn 是 Cloudflare 边缘 IP 优选(UC-21):从境内探测端对公开 IP 段做
// 两段探测 —— TCP:443 粗扫按延迟收集候选,再对候选做 TLS+HTTP 验证要 CF-RAY
// 响应头。CF-RAY 是 Cloudflare 边缘必发的头(含 IATA 机房码),TCP 通但没有
// CF-RAY 的是「假墙 IP」:GFW 路径上的中间设备对所有 IP 都秒回,必须用 HTTP
// 层证据过滤,这是 CloudflareST 系工具趟出来的坑。
package cdn

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// Ranges 是 Cloudflare 公布的 IPv4 段(2024-11 版,https://www.cloudflare.com/ips-v4)。
// 内置一份兜底,控制面可经设置覆盖为在线拉取的清单。
var Ranges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
}

// Result 是一个 IP 的优选结论。
type Result struct {
	IP        string `json:"ip"`
	Reachable bool   `json:"reachable"`      // TCP 层能连上
	LatencyMs int    `json:"latency_ms"`     // TCP ping 最小延迟
	Edge      bool   `json:"edge"`           // HTTP 层确认是 Cloudflare 边缘(有 CF-RAY)
	Colo      string `json:"colo,omitempty"` // IATA 机房码,如 NRT/LAX/SJC
}

// ProbeTCP 对一批 IP 做 TCP:443 拨测,返回延迟合格的候选,按延迟升序。
// 64 并发 worker;任一连接在 timeout 内握手成功即记可达,失败不记。
// want<=0 表示不限;拿到 want 个合格候选后取消剩余探测(早停)。
func ProbeTCP(ctx context.Context, ips []string, want int, timeout time.Duration) []Result {
	type hit struct {
		ip  string
		lat int
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan string, 64)
	hits := make(chan hit, 128)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range ch {
				lat, ok := tcpPing(ctx, ip, timeout)
				if ok {
					select {
					case hits <- hit{ip, lat}:
					default:
					}
				}
			}
		}()
	}
	go func() {
		defer close(ch)
		for _, ip := range ips {
			select {
			case ch <- ip:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(hits)
	}()
	var out []Result
	for h := range hits {
		if want <= 0 || len(out) < want {
			out = append(out, Result{IP: h.ip, Reachable: true, LatencyMs: h.lat})
			if want > 0 && len(out) >= want {
				cancel()
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LatencyMs < out[j].LatencyMs })
	return out
}

func tcpPing(ctx context.Context, ip string, timeout time.Duration) (int, bool) {
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	if err != nil {
		return 0, false
	}
	_ = conn.Close()
	return int(time.Since(start).Milliseconds()), true
}

// Verify 对 TCP 合格的候选做 TLS+HTTP 验证:握手(InsecureSkipVerify —— 我们
// 不关心证书身份,只要证明后面是 Cloudflare 边缘)发 GET /,读响应头 CF-RAY。
// 有 CF-RAY 即真边缘,顺带解析尾部机房码。逐个串行探,凑够 want 个真边缘即停
// —— 「先按延迟快排再逐个验证」是用户早停语义的正确实现:先回来的最快。
func Verify(ctx context.Context, candidates []Result, want int, timeout time.Duration) []Result {
	var out []Result
	for _, c := range candidates {
		if want > 0 && len(out) >= want {
			break
		}
		select {
		case <-ctx.Done():
			return out
		default:
		}
		edge, colo, err := verifyEdge(ctx, c.IP, timeout)
		r := c
		if err == nil && edge {
			r.Edge, r.Colo = true, colo
			out = append(out, r)
		}
	}
	return out
}

func verifyEdge(ctx context.Context, ip string, timeout time.Duration) (bool, string, error) {
	d := net.Dialer{Timeout: timeout}
	tconn, err := tls.DialWithDialer(&d, "tcp", net.JoinHostPort(ip, "443"), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return false, "", err
	}
	defer tconn.Close()
	deadline := time.Now().Add(timeout)
	_ = tconn.SetDeadline(deadline)
	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nUser-Agent: mpx-cdn-probe\r\nConnection: close\r\n\r\n", ip)
	if _, err := tconn.Write([]byte(req)); err != nil {
		return false, "", err
	}
	buf := make([]byte, 8192)
	n, _ := tconn.Read(buf)
	head := string(buf[:n])
	return parseCFRay(head)
}

// parseCFRay 从响应头里找 CF-RAY;Cloudflare 边缘必发,格式 "CF-RAY: <id>-<COLO>"。
// 返回 (是不是边缘, 机房码, nil)。
func parseCFRay(head string) (bool, string, error) {
	for _, line := range []string{head} {
		_ = line
	}
	// 逐行找 CF-RAY,大小写不敏感。
	for _, l := range splitLines(head) {
		if len(l) >= 7 && (l[:7] == "CF-RAY:" || l[:7] == "cf-ray:") {
			val := trimSpace(l[7:])
			// 形如 "9b3f...-SJC";取最后一段。
			for i := len(val) - 1; i >= 0; i-- {
				if val[i] == '-' {
					return true, val[i+1:], nil
				}
			}
			return true, val, nil
		}
	}
	return false, "", fmt.Errorf("no cf-ray header")
}

func splitLines(s string) []string {
	var out []string
	for len(s) > 0 {
		i := 0
		for i < len(s) && s[i] != '\n' {
			i++
		}
		line := s[:i]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		out = append(out, line)
		if i < len(s) {
			s = s[i+1:]
		} else {
			break
		}
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// Expand 把 CIDR 段展开成全部 IP(跳过网络与广播地址);超 4096 个 IP 的段
// 仍全展开 —— Cloudflare 段最大 /13(约 52 万),全量扫描由调用方控节奏。
func Expand(ranges []string) ([]string, error) {
	var out []string
	for _, cidr := range ranges {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		for ip := ipnet.IP.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
			if skip(ip, ipnet) {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out, nil
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func skip(ip net.IP, ipnet *net.IPNet) bool {
	// 跳过网络号与广播号;小于 /31 的段都适用。
	ones, bits := ipnet.Mask.Size()
	if bits-ones < 2 {
		return false // /31、/32 不跳
	}
	bcast := ip.Mask(ipnet.Mask)
	for j := len(bcast) - 1; j >= 0; j-- {
		bcast[j] = ipnet.IP[j] | ^ipnet.Mask[j]
	}
	return ip.Equal(ipnet.IP) || ip.Equal(bcast)
}
