package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/netutil"
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
	return runWithIdentity(ctx, fallback, n, script, stdin, "")
}

// runWithIdentity 与 run 相同,只是能额外指定一把私钥。
//
// 单独开一个入口是为了 mpx init:它连的机器还没有被纳管,控制面那把钥匙在不在
// 都还不知道,使用者手上可能只有自己的私钥(或什么都没有、只有密码)。
func runWithIdentity(ctx context.Context, fallback time.Duration, n model.Node, script string, stdin []byte, identity string) (string, error) {
	var stdout, stderr bytes.Buffer
	if err := runWithIdentityIO(ctx, fallback, n, script, stdin, identity, &stdout, &stderr); err != nil {
		return stdout.String(), err
	}
	return stdout.String(), nil
}

// runWithIdentityIO 是 runWithIdentity 的「输出往哪走」版本:攒着解析就给 buffer,
// 要边跑边看就给终端。
func runWithIdentityIO(ctx context.Context, fallback time.Duration, n model.Node, script string,
	stdin []byte, identity string, stdout, stderr io.Writer) error {
	budget := fallback
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fallback)
		defer cancel()
	}

	args := append(sshArgsWithIdentity(n, identity), script)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
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
				return fmt.Errorf("节点 %s: 命令超时(%s 内没返回,节点卡住了)",
					n.Name, budget.Round(time.Millisecond))
			}
			return fmt.Errorf("节点 %s: 命令被取消", n.Name)
		}
		return fmt.Errorf("节点 %s: %s", n.Name, sshErrText(err, stderr))
	}
	return nil
}

func sshArgs(n model.Node) []string {
	return sshArgsWithIdentity(n, "")
}

// sshControlPath 算出连接复用用的 socket 路径。
//
// 直接用 os.TempDir() 在 macOS 上会炸:那里的 TMPDIR 形如
// /var/folders/h5/…/T/,本身就有四五十个字符,再拼上散列键就超过了 unix socket
// 路径 104 字节的上限,ssh 会直接报 "path too long for Unix domain socket" 而不
// 是连不上 —— 一个看起来像网络问题的本地路径问题。mpx init 恰恰是在 macOS 上
// 跑的,所以这条路必须短。
//
// 优先用 /tmp(各平台的短路径),它不可写时才退回 TempDir 并接受风险。
func sshControlPath() string {
	const short = "/tmp"
	if f, err := os.CreateTemp(short, "mpx-probe-*"); err == nil {
		name := f.Name()
		f.Close()
		os.Remove(name)
		return short + "/mpx-ssh-%C"
	}
	return os.TempDir() + "/mpx-ssh-%C"
}

// sshArgsWithIdentity 是 sshArgs 加上一把可选的私钥。
//
// identity 非空时用 IdentitiesOnly=yes 把候选身份限死在这一把:init 面对的机器
// 上可能有一堆无关的密钥,让 ssh 挨个试既慢又可能因为「尝试次数过多」被 sshd
// 踢掉,而使用者说的就是「用这一把」。
func sshArgsWithIdentity(n model.Node, identity string) []string {
	// BatchMode 让 ssh 在需要密码/口令时直接失败而不是挂住 —— 控制面是常驻进程,
	// 不能停在一个永远等不到输入的提示上。
	args := []string{
		"-T", // machine protocols must never inherit RequestTTY=force
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		// ControlMaster 把几条相邻的 ssh 命令复用到同一条 TCP 连接上:收敛+采样+
		// 状态轮询一轮要往同一台节点发三四条命令,每次都握手+认证一次太重。
		// %C 是 OpenSSH 自己算的连接散列键(含目标主机/端口/用户),同一目标落
		// 同一个 socket 文件;不同节点互不干扰。
		// ControlPath 放临时目录而不是 ~/.ssh:控制面进程不一定有 HOME,也免得
		// 污染用户自己的 socket。进程结束时 socket 文件随进程清理。
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + sshControlPath(),
		"-o", "ControlPersist=60s",
	}
	if identity != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", identity)
	}
	if p := n.SSHPortOrDefault(); p != 22 {
		args = append(args, "-p", strconv.Itoa(p))
	}
	return append(args, sshTarget(n))
}

// sshTarget 返回 ssh 的 user@host 目标。字面 IPv6 必须带方括号,否则 ssh 会把
// 冒号后的部分当成端口或直接判成非法目标。端口仍由 -p 单独给。
func sshTarget(n model.Node) string {
	return n.SSHUserOrDefault() + "@" + netutil.BracketHost(n.Address)
}

// RunAs 用指定的认证方式在一台「还没纳管」的机器上跑一条命令。
//
// 它是给 mpx init 用的:那台机器上还没有控制面的公钥,私钥在不在也不一定,
// 所以要把「用哪把钥匙」或「用密码」显式说出来。纳管之后的机器一律走 SSH.Run,
// 用控制面自己那把钥匙。
type Auth struct {
	// Identity 是私钥路径;空表示用 ssh 自己的默认身份。
	Identity string
	// Password 非空时改用密码认证(走 SSH_ASKPASS,不落进程参数)。
	Password string
}

// Run 在 n 上跑一条命令。stdout 原样返回;失败时的错误带上远端或 ssh 的原话。
func RunAs(ctx context.Context, n model.Node, a Auth, script string) (string, error) {
	if a.Password != "" {
		args := append(passwordSSHArgs(n), script)
		return runPasswordSSH(ctx, n, args, a.Password)
	}
	return runWithIdentity(ctx, DefaultTimeout, n, script, nil, a.Identity)
}

// RunAsIO 同 RunAs,但把远端的输出直接写到给定的 writer。
//
// 给 mpx init 用:那条命令要跑好几分钟(装控制面、下核心),使用者得看着它动,
// 攒到最后一次性吐出来等于几分钟没有任何反馈。
func RunAsIO(ctx context.Context, n model.Node, a Auth, script string, stdout, stderr io.Writer) error {
	if a.Password != "" {
		args := append(passwordSSHArgs(n), script)
		return execPasswordSSH(ctx, n, args, a.Password, nil, stdout, stderr)
	}
	return runWithIdentityIO(ctx, DefaultTimeout, n, script, nil, a.Identity, stdout, stderr)
}

// RunWithInputAs 同 RunAs,但把 stdin 喂给远端命令。
func RunWithInputAs(ctx context.Context, n model.Node, a Auth, script string, stdin []byte) (string, error) {
	if a.Password != "" {
		// 密码这一路没有 stdin 通道(askpass 占着),调用方要传内容进远端时
		// 得先把公钥装上、换成密钥认证再来。
		return "", fmt.Errorf("节点 %s: 密码认证下不能同时给远端喂标准输入", n.Name)
	}
	return runWithIdentity(ctx, DefaultTimeout, n, script, stdin, a.Identity)
}

// PutFileAs 同 PutFile,但可以指定认证方式。
func PutFileAs(ctx context.Context, n model.Node, a Auth, p string, content []byte) error {
	_, err := RunWithInputAs(ctx, n, a, putFileScript(p), content)
	return err
}

// PutFile 把内容写到节点上的某个路径。
//
// 内容走标准输入而不是命令行:既避开转义,也不会出现在节点的进程列表里。
// 先写临时文件再改名,免得写到一半断线在节点上留下半份配置。
func PutFile(ctx context.Context, r Runner, n model.Node, p string, content []byte) error {
	_, err := r.RunWithInput(ctx, n, putFileScript(p), content)
	return err
}

// putFileScript 是写远端文件用的那段脚本,PutFile 与 PutFileAs 共用一份。
func putFileScript(p string) string {
	return fmt.Sprintf("umask 077 && mkdir -p %s && cat > %s && mv %s %s",
		shellQuote(path.Dir(p)), shellQuote(p+".tmp"), shellQuote(p+".tmp"), shellQuote(p))
}

// shellQuote 把一个值安全地嵌进 shell 脚本。
//
// 入站 tag 和用户名会进命令行,不能假定它们只含安全字符。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellQuote 是 shellQuote 的导出形式,给包外的调用方拼远端脚本用。
func ShellQuote(s string) string { return shellQuote(s) }
