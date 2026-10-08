package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// Runner 在一台节点上执行命令。
//
// 抽成接口是为了让上层(下发配置、增删用户、采流量)不依赖真实的 ssh:测试时可以
// 换成假实现,把「控制面到底对节点说了什么」变成可以直接断言的东西。
type Runner interface {
	// Run 在节点上执行一段脚本,返回它的标准输出。
	Run(ctx context.Context, n model.Node, script string) (string, error)
	// RunWithInput 同上,但把 stdin 喂给脚本。
	RunWithInput(ctx context.Context, n model.Node, script string, stdin []byte) (string, error)
}

// SSH 用系统自带的 ssh 客户端连节点。
//
// 刻意不自己实现 SSH 客户端:一台机器怎么连(跳板机、ssh-agent、~/.ssh/config、
// known_hosts)在用户机器上已经是既成事实,重写一份只会多出一处会漂移的真相,
// 而且要把这些能力补回来还得再写一遍。
type SSH struct {
	// Timeout 是单条命令的兜底上限,0 表示 DefaultTimeout。
	//
	// 调用方给的 ctx 自带 deadline 时它不起作用 —— 那条命令该给多久,调用方最
	// 清楚(采样、收敛、下发、安装各有各的预算)。它的作用是保证「没有一条命令
	// 能永远挂着」:漏了 deadline 的调用在这里被截断,而不是把整轮任务拖死。
	Timeout time.Duration
}

// XrayBinPath 是节点上 mpx 那份 xray 核心里可执行文件的位置。
//
// 不放在 /usr/local/bin:xray 的官方安装脚本装在那里,发行版包管理器也装在那里,
// 别人的 Marzban 之类也在那里。共用同一个路径意味着两件事同时成立 —— 我们可能
// 覆盖掉别人的核心,我们也可能用到不知道是谁、不知道哪个版本的核心。装和更新只
// 认这一个路径,别人的一个字节都不动。
const XrayBinPath = "/usr/local/lib/mpx/xray"

// xrayBin 是节点上每条 xray 命令的前缀。
//
// MPX_XRAY_BIN 是给本机测试留的口子(本机跑真实 xray 时它在别处,见 live_test.go);
// 真节点上没人会设它,于是永远解析成 XrayBinPath。
const xrayBin = `XRAY_BIN="${MPX_XRAY_BIN:-` + XrayBinPath + `}"; `

// DefaultTimeout 是单条远端命令的兜底上限。
//
// 取值来自最慢的那条合法命令:node install 要推 65MB(核心加两份 geodata),
// 实测约 40 秒。给到 15 分钟是照慢链路算的 —— 65MB 走 100KB/s 的出口要约 11
// 分钟,再留一点余量。正常情况下轮不到它:每个调用点都有自己的 ctx deadline
// (见 internal/control 的 sampleBudget 等),这里只是最后一道兜底。
const DefaultTimeout = 15 * time.Minute

func (s SSH) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

func (s SSH) Run(ctx context.Context, n model.Node, script string) (string, error) {
	return run(ctx, s.timeout(), n, script, nil)
}

func (s SSH) RunWithInput(ctx context.Context, n model.Node, script string, stdin []byte) (string, error) {
	return run(ctx, s.timeout(), n, script, stdin)
}

// run 跑一条远端命令,并保证它有上限。
//
// ctx 已经带了 deadline 就用它的预算,不覆盖:调用方知道这条命令要多久,兜底值
// 比它大得多,一旦覆盖就等于把某个节点的卡顿放大成整轮任务的卡顿。没有 deadline
// 时才补上 fallback。
func run(ctx context.Context, fallback time.Duration, n model.Node, script string, stdin []byte) (string, error) {
	budget := fallback
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fallback)
		defer cancel()
	}

	args := append(sshArgs(n), script)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		// 超时和「节点不可达」是两回事,日志里要能一眼分开:前者是连上了但命令
		// 不回来(节点卡住、重启中、命令自己挂住),后者连不上。ctx 结束会让
		// exec 杀掉 ssh 进程,那时 stderr 往往是空的、err 只写着 signal: killed,
		// 所以先看 ctx 的错。
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				// 毫秒取整:调用方给几十毫秒的小预算时,按秒取整会印出「0s 内
				// 没返回」,那句话什么也没说清。
				return stdout.String(), fmt.Errorf("节点 %s: 命令超时(%s 内没返回,节点卡住了)",
					n.Name, budget.Round(time.Millisecond))
			}
			return stdout.String(), fmt.Errorf("节点 %s: 命令被取消", n.Name)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("节点 %s: %s", n.Name, msg)
	}
	return stdout.String(), nil
}

func sshArgs(n model.Node) []string {
	// BatchMode 让 ssh 在需要密码/口令时直接失败而不是挂住 —— 控制面是常驻进程,
	// 不能停在一个永远等不到输入的提示上。
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if p := n.SSHPortOrDefault(); p != 22 {
		args = append(args, "-p", strconv.Itoa(p))
	}
	return append(args, n.SSHUserOrDefault()+"@"+n.Address)
}

// PutFile 把内容写到节点上的某个路径。
//
// 内容走标准输入而不是命令行:既避开转义,也不会出现在节点的进程列表里。
// 先写临时文件再改名,免得写到一半断线在节点上留下半份配置。
func PutFile(ctx context.Context, r Runner, n model.Node, p string, content []byte) error {
	script := fmt.Sprintf("umask 077 && mkdir -p %s && cat > %s && mv %s %s",
		shellQuote(path.Dir(p)), shellQuote(p+".tmp"), shellQuote(p+".tmp"), shellQuote(p))
	_, err := r.RunWithInput(ctx, n, script, content)
	return err
}

// shellQuote 把一个值安全地嵌进 shell 脚本。
//
// 入站 tag 和用户名会进命令行,不能假定它们只含安全字符。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
