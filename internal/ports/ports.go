// Package ports 解析 ss 的监听列表,回答「某个端口是不是被别的程序占着、是谁」。
// 控制面装机(mpx init / 首次安装向导)和节点装核心前都用它先看一眼端口,
// 免得装到一半才因为 address already in use 起不来。
package ports

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Unknown 是 ss 看不到进程信息时的占用者说法。
const Unknown = "另一个程序"

var ownerRe = regexp.MustCompile(`\(\("([^"]+)",pid=(\d+)`)

// Parse 把 ss -Hl[tu]np 的输出变成「端口 → 占用者」,占用者写成「nginx(进程 1234)」。
func Parse(ss string) map[int]string {
	out := map[int]string{}
	for _, line := range strings.Split(ss, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		// ss -H 的列:State Recv-Q Send-Q Local Peer [Process];UDP 的 State 是 UNCONN。
		local := f[3]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(local[i+1:])
		if err != nil || port <= 0 {
			continue
		}
		owner := Unknown
		if m := ownerRe.FindStringSubmatch(line); m != nil {
			owner = fmt.Sprintf("%s(进程 %s)", m[1], m[2])
		}
		if _, seen := out[port]; !seen || owner != Unknown {
			out[port] = owner
		}
	}
	return out
}

// Program 取出占用者里的程序名(「nginx(进程 1234)」→ nginx)。
func Program(owner string) string {
	if i := strings.Index(owner, "(进程"); i > 0 {
		return owner[:i]
	}
	return ""
}

// Pick 在候选里挑第一个空着的端口(跳过 want);都被占了就从 20000 往上找。
// ours 判断某个占用者是不是自己人(自己人占着不算冲突)。
func Pick(busy map[int]string, want int, candidates []int, ours func(string) bool) int {
	free := func(p int) bool {
		o, ok := busy[p]
		return !ok || (ours != nil && ours(o))
	}
	for _, p := range candidates {
		if p != want && free(p) {
			return p
		}
	}
	for p := 20000; p < 65000; p++ {
		if p != want && free(p) {
			return p
		}
	}
	return 0
}

// InboundCandidates 是入站端口被占时依次尝试的备选。
var InboundCandidates = []int{443, 2087, 2053, 2083, 2096, 8443, 9443}

// ErrNoSS:机器上没有 ss,看不出端口占用。调用方按「不知道」处理,不拦操作。
var ErrNoSS = errors.New("机器上没有 ss 命令,看不出端口占用")
