package node

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"

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
type SSH struct{}

// xrayBin 在节点上定位 xray 可执行文件。
//
// 官方安装脚本放在 /usr/local/bin/xray,但用发行版包管理器装的会在别处,所以先按
// PATH 找、找不到再退回默认路径。节点上的一切 xray 调用都以它开头。
const xrayBin = `XRAY_BIN="$(command -v xray || echo /usr/local/bin/xray)"; `

func (SSH) Run(ctx context.Context, n model.Node, script string) (string, error) {
	return run(ctx, n, script, nil)
}

func (SSH) RunWithInput(ctx context.Context, n model.Node, script string, stdin []byte) (string, error) {
	return run(ctx, n, script, stdin)
}

func run(ctx context.Context, n model.Node, script string, stdin []byte) (string, error) {
	args := append(sshArgs(n), script)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
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
