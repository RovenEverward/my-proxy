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
func installKey(ctx context.Context, n model.Node, keyPath, pubkey, password string) error {
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

	// 公钥直接拼进远端脚本 —— 不要用 ssh 的环境变量。
	//
	// 这里踩过一次:原先把公钥放在客户端的 PUBKEY 环境变量里,远端脚本 echo "$PUBKEY"。
	// 但 OpenSSH 默认不转发任意环境变量(要客户端 SendEnv 与 sshd 的 AcceptEnv 两边
	// 同时放行),远端看到的是空串,于是往 authorized_keys 里追加了一个空行,而 ssh
	// 退出码仍然是 0 —— 一次记成成功的空操作,直到后来密钥登录失败才暴露。公钥是
	// 公开信息,拼进脚本没有泄露问题。
	if strings.ContainsAny(pubkey, "'\n\r") {
		return fmt.Errorf("公钥内容异常,拒绝写入")
	}

	// mkdir -p + chmod 保证 .ssh 目录和文件的权限是 sshd 认的(0600/0700),
	// 错一点 sshd 会静默不收这把钥匙。
	remote := `
mkdir -p ~/.ssh && chmod 700 ~/.ssh
touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys
PK='` + pubkey + `'
grep -qxF "$PK" ~/.ssh/authorized_keys 2>/dev/null || echo "$PK" >> ~/.ssh/authorized_keys
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

	// 装完当场用密钥再登一次,确认这把钥匙真的能用。
	//
	// 上面那条 ssh 的退出码不足以证明钥匙生效:脚本可能没跑成、sshd 可能拒收
	// 权限不对的文件、节点的 sshd 可能压根关着 PubkeyAuthentication(实测碰到过
	// 一台写死 PubkeyAuthentication no 的)。没有这一步,这些情况全都会被记成
	// 「已装上公钥」,错误推迟到第一次下发配置时才以别的面目出现。
	if err := verifyKey(ctx, n, keyPath); err != nil {
		return fmt.Errorf("公钥已写入但密钥登录不通(检查节点 sshd 是否允许 PubkeyAuthentication): %w", err)
	}
	return nil
}

// verifyKey 只用密钥连一次节点,确认控制面这把钥匙真的被节点接受。
//
// 故意不复用 ControlMaster:上一条命令的复用通道是密码认证建起来的,拿它来
// "验证"等于什么都没验证。keyPath 必须显式传:控制面那把私钥不在 ~/.ssh,
// 而在数据目录里(见 serve.go),靠默认路径会验成另一把钥匙。
func verifyKey(ctx context.Context, n model.Node, keyPath string) error {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "PubkeyAuthentication=yes",
		"-o", "PasswordAuthentication=no",
		"-o", "IdentitiesOnly=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
	}
	if keyPath != "" {
		args = append(args, "-i", keyPath)
	}
	if p := n.SSHPortOrDefault(); p != 22 {
		args = append(args, "-p", strconv.Itoa(p))
	}
	args = append(args, n.SSHUserOrDefault()+"@"+n.Address, "echo MPX_KEY_OK")

	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("节点 %s: %s", n.Name, msg)
	}
	if !strings.Contains(stdout.String(), "MPX_KEY_OK") {
		return fmt.Errorf("节点 %s: 密钥登录没有回显", n.Name)
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
// 用密码登进去装上控制面的公钥,再验证密钥确实能登,返回给调用方一份可以落库的节点。
//
// keyPath 是控制面私钥的位置;验证那一步必须用它,不能靠 ssh 的默认身份。
// 失败时不落任何东西 —— 调用方据此决定要不要把这次 node add 整个回滚。
func BootstrapNode(ctx context.Context, n model.Node, keyPath, pubkey, password string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return installKey(ctx, n, keyPath, pubkey, password)
}
