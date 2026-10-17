package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/ports"
)

// 装控制面前先看端口:控制面(默认 8080)和第一个入站(默认 443)要用的端口
// 如果已经被别的程序占着,mpx 起来就会立刻退出(bind: address already in use),
// 入站则是 xray 起不来。以前要装到一半才在 systemd 日志里看到英文报错;现在在
// 「看清这台机器」时就问清楚,用中文说是哪个进程占的,能换的就换一个空闲端口。

// probeListenScript 列出 TCP 监听端口和占用它的进程;每行一条 ss= 记录。
// 没有 ss(极少见)时什么都不输出,下面按「看不出来」处理,不拦安装。
const probeListenScript = `if command -v ss >/dev/null 2>&1; then ss -Hltnp 2>/dev/null | while IFS= read -r l; do printf 'ss=%s\n' "$l"; done; fi
printf 'mpxdb=%s\n' "$(test -f /var/lib/mpx/mpx.db && echo 1)"`

// parseListeners 把 ss -Hltnp 的输出变成「端口 → 占用者」(见 internal/ports)。
func parseListeners(ss string) map[int]string { return ports.Parse(ss) }

// ownerIsMPX:占着端口的就是 mpx 自己(重装、重跑向导时),不算冲突。
func ownerIsMPX(owner string) bool { return strings.HasPrefix(owner, "mpx(") }

var (
	apiPortCandidates     = []int{8080, 18080, 28080, 8880, 9080, 10080, 38080}
	inboundPortCandidates = ports.InboundCandidates
)

// pickFreePort 在候选里挑第一个没被占的;mpx 自己占着的不算。
func pickFreePort(busy map[int]string, want int, candidates []int) int {
	return ports.Pick(busy, want, candidates, ownerIsMPX)
}

// portCheck 是一个端口的结论。
type portCheck struct {
	Port    int
	Owner   string // 非空 = 被别的程序占着
	Suggest int    // 被占时推荐的空闲端口
}

// checkPorts 根据探路结果判断控制面端口和入站端口。mpx 已经装过(重跑)时
// 入站端口由已有的数据决定,不再检查。
func checkPorts(info map[string]string, apiPort, inboundPort int) (api, inbound portCheck) {
	busy := parseListeners(info["ss"])
	api = portCheck{Port: apiPort}
	if o, ok := busy[apiPort]; ok && !ownerIsMPX(o) {
		api.Owner, api.Suggest = o, pickFreePort(busy, apiPort, apiPortCandidates)
	}
	inbound = portCheck{Port: inboundPort}
	if info["mpxdb"] != "1" {
		if o, ok := busy[inboundPort]; ok && !ownerIsMPX(o) {
			// 控制面要用的那个端口也算占用,免得推荐成同一个。
			b2 := map[int]string{}
			for k, v := range busy {
				b2[k] = v
			}
			b2[api.effective()] = "mpx 控制面"
			inbound.Owner, inbound.Suggest = o, pickFreePort(b2, inboundPort, inboundPortCandidates)
		}
	}
	return api, inbound
}

func (c portCheck) effective() int {
	if c.Owner != "" && c.Suggest > 0 {
		return c.Suggest
	}
	return c.Port
}

// portProbeFields 把结论写进向导「检查」一步返回的 probe(只加字段)。
func portProbeFields(info map[string]string, api, inbound portCheck) {
	put := func(prefix string, c portCheck) {
		info[prefix+"_port"] = strconv.Itoa(c.Port)
		info[prefix+"_busy"] = c.Owner
		if c.Suggest > 0 {
			info[prefix+"_suggest"] = strconv.Itoa(c.Suggest)
		} else {
			info[prefix+"_suggest"] = ""
		}
	}
	put("api", api)
	put("inbound", inbound)
}

// listenSummary 给日志看的一行:哪些端口在监听。
func listenSummary(info map[string]string) string {
	busy := parseListeners(info["ss"])
	ports := make([]int, 0, len(busy))
	for p := range busy {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	s := make([]string, 0, len(ports))
	for _, p := range ports {
		s = append(s, strconv.Itoa(p))
	}
	return strings.Join(s, " ")
}

// apiPortBusyError:控制面端口被占时的中文说明,带上占用的进程和一个能用的端口。
func apiPortBusyError(c portCheck, fromUI bool) error {
	alt := ""
	if c.Suggest > 0 {
		alt = fmt.Sprintf("(比如 %d)", c.Suggest)
	}
	how := fmt.Sprintf("在 mpx init 后面加 --addr 0.0.0.0:%d", c.Suggest)
	if fromUI {
		how = "在「设置 → 高级 → 监听地址」里改端口"
	}
	if c.Suggest == 0 {
		how = "先停掉占用它的程序"
	}
	return fmt.Errorf("控制面端口 %d 已被 %s 占用,mpx 起不来。换一个没被占用的端口%s再装:%s。还没有往这台机器装任何东西", c.Port, c.Owner, alt, how)
}
