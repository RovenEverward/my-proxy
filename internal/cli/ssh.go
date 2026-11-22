package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// UC-28:本机 CLI 用 SSH 密钥向控制面领会话。
//
// SSH 在这里只做一件事:在控制面所在服务器上用 MPX_TOKEN 铸一枚普通会话
// token(POST /api/admin/session),回本机存起来。之后所有 /api 调用仍走
// HTTPS+Bearer —— SSH 是「铸会话的运输层」,不是 API 的传输层,也不是新的
// 凭据类型。会话照常在 admin_sessions 表里、30 天到期、可吊销。
//
// 只调系统的 ssh 命令:~/.ssh/config 的别名、ProxyJump、agent、known_hosts
// 都由它处理,行为与使用者手敲完全一致,不引第三方 SSH 库。

// sshConnectTimeout 是一次 ssh 铸会话的上限:连不上多半是地址或密钥不对,
// 等久了也没有意义。
const sshConnectTimeout = 30 * time.Second

// sshSessionCmd 是 profile 里没存自定义命令时用的远端铸会话脚本。
//
// 端口取控制面 API 的端口(serve --addr 的那个):远端 mpx 用它环回直连
// 控制面,不依赖 MPX_TOKEN 之外的任何配置。mpx 二进制按 command -v 找,
// PATH 里没有时退回 /usr/local/bin/mpx —— 那是 deploy/install.sh 的固定落点。
func sshSessionCmd(apiPort int) string {
	return `MPX="$(command -v mpx || echo /usr/local/bin/mpx)" && ` +
		`MPX_TOKEN="$(sed -n 's/^MPX_TOKEN=//p' /etc/mpx/env)" ` +
		`"$MPX" --server http://127.0.0.1:` + strconv.Itoa(apiPort) +
		` admin session --kind ssh`
}

// mintSessionViaSSH 经 ssh 在 target 上铸一枚会话 token。
//
// remoteCmd 为空时按控制面端口拼默认命令(见 sshSessionCmd);给了就原样跑。
// target 是 ssh 的目标串:user@host、~/.ssh/config 的别名,或连人带参数的
// 一串(如 "root@host -p 5522")—— 它按空白切开后接在 ssh 选项后面。
//
// 返回的 expires 是服务端给的到期时间;解析不出时返回零值,调用方照常存
// token(token 本身是凭据,过期时间只是展示)。
func mintSessionViaSSH(ctx context.Context, target, remoteCmd string, apiPort int) (token string, expires time.Time, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", time.Time{}, errors.New("SSH 目标为空")
	}
	script := strings.TrimSpace(remoteCmd)
	if script == "" {
		script = sshSessionCmd(apiPort)
	}
	args := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	args = append(args, strings.Fields(target)...)
	args = append(args, script)

	ctx, cancel := context.WithTimeout(ctx, sshConnectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", time.Time{}, fmt.Errorf("ssh %s 超时(%s 内没返回)", target, sshConnectTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		if strings.Contains(msg, "unknown command") {
			return "", time.Time{}, fmt.Errorf("ssh %s: %s(控制面 mpx 版本过旧,先在服务器上升级到含 admin session 的版本)", target, msg)
		}
		return "", time.Time{}, fmt.Errorf("ssh %s: %s", target, msg)
	}
	// admin session 默认只打 token 一行;保险起见仍按 JSON 先解一次,兼容
	// 远端跑的是 -o json 变体。
	out := strings.TrimSpace(stdout.String())
	if strings.HasPrefix(out, "{") {
		var res struct {
			Token     string    `json:"token"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		if json.Unmarshal([]byte(out), &res) == nil && res.Token != "" {
			return res.Token, res.ExpiresAt, nil
		}
	}
	// 纯文本:取最后一行的第一个字段 —— 远端万一在前面打了日志,token 仍
	// 是最后一行(脚本以 admin session 收尾,其后没有输出)。
	lines := strings.Split(out, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if fields := strings.Fields(last); len(fields) > 0 {
		token = fields[len(fields)-1]
	}
	if token == "" {
		return "", time.Time{}, fmt.Errorf("ssh %s: 远端没回会话 token(输出: %s)", target, out)
	}
	return token, time.Time{}, nil
}

// sshTargetFromServer 在 --ssh 没带值时用:拿 connect 地址的主机名当 ssh 目标。
// https:// 前缀与端口都剥掉;IPv6 剥方括号。host 是 IP 字面量时一样可行 —
// ssh root@IP 与 ssh 别名对系统 ssh 来说没有差别。
func sshTargetFromServer(server string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(server, "https://"), "/")
	host, _, err := net.SplitHostPort(s)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return s
}

// apiPortFromServer 取控制面地址里的端口,没写就用 8080(与 connect 一致)。
func apiPortFromServer(server string) int {
	s := strings.TrimSuffix(strings.TrimPrefix(server, "https://"), "/")
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return defaultControlPort
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return defaultControlPort
	}
	return p
}
