package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// installKey 用密码登一次节点,把控制面的公钥追加进 authorized_keys。
//
// 这是唯一用密码的地方:它跑完一次就结束,之后控制面与该节点之间全是密钥。
// 密码不落库、不进日志、不进进程参数 —— 它走 SSH_ASKPASS 的 stdout 回给 ssh,
// 只有这两个进程看得见。
//
// pubkey 是控制面自己那对密钥的 .pub 内容(一行 ssh-ed25519 …),由调用方从
// 磁盘读出来;这个函数不碰文件系统里属于控制面的东西,只负责把它送过去。
func installKey(ctx context.Context, n model.Node, pubkey, password string) error {
	// askpass 脚本替 ssh 回答密码。SSH_ASKPASS_REQUIRE=force 让 ssh 连 TTY 都不看、
	// 只走这个脚本;脚本里密码用环境变量带进去,不出现在 ps 里。
	dir, err := os.MkdirTemp("", "mpx-askpass-*")
	if err != nil {
		return fmt.Errorf("准备 askpass 目录: %w", err)
	}
	defer os.RemoveAll(dir)

	askpass := filepath.Join(dir, "askpass.sh")
	// 引号处理:printf '%s' "$MPX_ASKPASS_PASSWORD" 原样吐密码,不换行不解释。
	script := "#!/bin/sh\nprintf '%s' \"$MPX_ASKPASS_PASSWORD\"\n"
	if err := os.WriteFile(askpass, []byte(script), 0o700); err != nil {
		return fmt.Errorf("写 askpass 脚本: %w", err)
	}

	// 远端脚本:把公钥追加进去,重复了不追加两遍。mkdir -p + chmod 保证 .ssh
	// 目录和文件的权限是 sshd 认的(0600/0700),错一点 sshd 会静默不收这把钥匙。
	remote := `
set -eu
mkdir -p ~/.ssh && chmod 700 ~/.ssh
touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys
grep -qxF "$PUBKEY" ~/.ssh/authorized_keys 2>/dev/null || echo "$PUBKEY" >> ~/.ssh/authorized_keys
`

	args := []string{
		// 不用 BatchMode:那会让 ssh 在需要密码时直接失败,而这里我们就是要它
		// 用密码。PasswordAuthentication 显式开,免得 ssh_config 里被关掉。
		"-o", "PasswordAuthentication=yes",
		"-o", "PubkeyAuthentication=no",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		// 不复用 ControlMaster:这次的认证方式是密码,别污染以后会话密钥的
		// 那条复用通道。
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
	}
	if p := n.SSHPortOrDefault(); p != 22 {
		args = append(args, "-p", strconv.Itoa(p))
	}
	args = append(args, n.SSHUserOrDefault()+"@"+n.Address, remote)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+askpass,
		"SSH_ASKPASS_REQUIRE=force",
		"MPX_ASKPASS_PASSWORD="+password,
		"PUBKEY="+pubkey,
		"DISPLAY=:0", // 有些 OpenSSH 版本要求 DISPLAY 非空才走 askpass
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return fmt.Errorf("节点 %s: 命令超时", n.Name)
			}
			return fmt.Errorf("节点 %s: 命令被取消", n.Name)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		// 密码错误、密码认证没开、连不上,都该让使用者看到 ssh 的原话。
		return fmt.Errorf("节点 %s: %s", n.Name, msg)
	}
	return nil
}

// EnsureSSHKey 保证控制面有一对 SSH 密钥,并返回它的公钥内容。
//
// 放在 node 包里而不是 control:它是「控制面怎么连节点」这件事的一部分,和
// ssh.go 里那套参数是同一层。keyPath 默认是 ~/.ssh/id_ed25519;为空时调用方
// 决定放哪(serve.go 把它定在数据目录里,跟 mpx.service 的 StateDirectory 对齐)。
func EnsureSSHKey(keyPath string) (pubkey string, err error) {
	if keyPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("找不到 home 目录放 SSH 密钥: %w", err)
		}
		keyPath = filepath.Join(home, ".ssh", "id_ed25519")
	}

	pubPath := keyPath + ".pub"
	if content, err := os.ReadFile(pubPath); err == nil {
		return strings.TrimSpace(string(content)), nil
	}

	// 没有现成的就生成一对;已有私钥但缺公钥时重新生成会覆盖,所以先看私钥
	// 在不在,在的话从它把公钥算出来。
	if _, err := os.Stat(keyPath); err == nil {
		out, err := exec.Command("ssh-keygen", "-y", "-f", keyPath).Output()
		if err != nil {
			return "", fmt.Errorf("从已有私钥 %s 算公钥: %w", keyPath, err)
		}
		pub := strings.TrimSpace(string(out))
		if err := os.WriteFile(pubPath, []byte(pub+"\n"), 0o644); err != nil {
			return "", fmt.Errorf("写公钥 %s: %w", pubPath, err)
		}
		return pub, nil
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", fmt.Errorf("建 .ssh 目录: %w", err)
	}
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "",
		"-C", "mpx 控制面", "-f", keyPath).CombinedOutput(); err != nil {
		return "", fmt.Errorf("生成 SSH 密钥: %s: %w", strings.TrimSpace(string(out)), err)
	}
	content, err := os.ReadFile(pubPath)
	if err != nil {
		return "", fmt.Errorf("读刚生成的公钥: %w", err)
	}
	return strings.TrimSpace(string(content)), nil
}

// BootstrapNode 是「只有密码时怎么把节点交进来」的全过程:
// 用密码登进去装上控制面的公钥,返回给调用方一份可以落库的节点。
//
// 失败时不落任何东西 —— 调用方据此决定要不要把这次 node add 整个回滚。
func BootstrapNode(ctx context.Context, n model.Node, pubkey, password string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return installKey(ctx, n, pubkey, password)
}
