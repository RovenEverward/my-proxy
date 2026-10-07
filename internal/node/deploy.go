package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// Deploy 把一份配置下发到节点并重启 xray。
//
// 顺序是「备份 → 写入 → 让 xray 自己校验 → 重启 → 确认真的起来了」。多花的两次往返
// 是有意的:配置写坏会让整台节点上所有人断流,而 xray 的 -test 只解析配置、不绑端口,
// 端口被占、核心和配置不匹配这类问题只有真启动才暴露 —— 不确认的话,节点已经断了而
// 控制面还在报「已下发」。
func Deploy(ctx context.Context, r Runner, n model.Node, cfg []byte) error {
	p := n.ConfigPathOrDefault()
	backup := p + ".bak"

	// 首次部署时原文件还不存在,备份失败不算错。
	if _, err := r.Run(ctx, n, fmt.Sprintf("cp -f %s %s 2>/dev/null || true", shellQuote(p), shellQuote(backup))); err != nil {
		return err
	}
	if err := PutFile(ctx, r, n, p, cfg); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if out, err := r.Run(ctx, n, xrayBin+"\"$XRAY_BIN\" -test -config "+shellQuote(p)); err != nil {
		// 校验没过就把备份放回去,别让节点停在起不来的状态。
		_, _ = r.Run(ctx, n, fmt.Sprintf("cp -f %s %s", shellQuote(backup), shellQuote(p)))
		return fmt.Errorf("配置没通过 xray 校验,已回滚: %w: %s", err, strings.TrimSpace(out))
	}
	if err := Restart(ctx, r, n); err != nil {
		return err
	}

	// Type=simple 的服务在 systemctl 返回时只是刚 fork 出来:进程随后就退掉(端口
	// 被占、核心不兼容)要过一瞬才反映到状态上,所以等一下再问。
	if _, err := r.Run(ctx, n, "sleep 1"); err != nil {
		return err
	}
	active, err := IsActive(ctx, r, n)
	if err != nil {
		return err
	}
	if active {
		return nil
	}

	// 起来了又倒下。有上一份就回滚再重启;首次部署没有上一份,把坏配置再启动一遍
	// 只会让它继续崩,不如停在这里、把日志交出去。
	rolled := false
	if out, _ := r.Run(ctx, n, "test -f "+shellQuote(backup)+" && echo yes"); strings.TrimSpace(out) == "yes" {
		if _, err := r.Run(ctx, n, fmt.Sprintf("cp -f %s %s", shellQuote(backup), shellQuote(p))); err == nil {
			_ = Restart(ctx, r, n)
			rolled = true
		}
	}
	tail := journalTail(ctx, r, n)
	if rolled {
		return fmt.Errorf("配置通过了校验但服务没起来,已回滚到上一份。节点日志:%s", tail)
	}
	return fmt.Errorf("配置通过了校验但服务没起来(首次部署,没有可回滚的配置)。节点日志:%s", tail)
}

// journalTail 取节点上服务最近的日志,用来解释「为什么没起来」。
//
// 日志取不到不算错:调用方要报的是服务没起来,日志只是附带的解释,不能让它把真正的
// 报错顶掉。所谓「最近」是就服务的生命周期而言 —— 重启是瞬断,上一次崩的原因往往就
// 在这几行里。
func journalTail(ctx context.Context, r Runner, n model.Node) string {
	out, err := r.Run(ctx, n, "journalctl -u "+shellQuote(n.ServiceNameOrDefault())+" -n 20 --no-pager 2>&1")
	if err != nil {
		return "取不到服务日志"
	}
	if s := strings.TrimSpace(out); s != "" {
		return "\n" + s
	}
	return "服务日志是空的"
}

func Restart(ctx context.Context, r Runner, n model.Node) error { return unit(ctx, r, n, "restart") }
func Start(ctx context.Context, r Runner, n model.Node) error   { return unit(ctx, r, n, "start") }
func Stop(ctx context.Context, r Runner, n model.Node) error    { return unit(ctx, r, n, "stop") }

func unit(ctx context.Context, r Runner, n model.Node, action string) error {
	_, err := r.Run(ctx, n, "systemctl "+action+" "+shellQuote(n.ServiceNameOrDefault()))
	return err
}

// IsActive 问节点上 xray 服务当前是不是在跑。
//
// systemctl is-active 在服务没跑时以非零退出,而「没跑」是一个正常答案而不是错误,
// 所以这里补一个 || true,让退出码不参与判断。
func IsActive(ctx context.Context, r Runner, n model.Node) (bool, error) {
	out, err := r.Run(ctx, n, "systemctl is-active "+shellQuote(n.ServiceNameOrDefault())+" || true")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "active", nil
}

// Version 读节点上跑着的 xray 是哪一版,用于确认换上去的核心真的起来了。
//
// 原始输出是 "Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 linux/amd64)",
// 这里只留下版本号那一段:调用方要把它打进给使用者看的一行里,整行读不下来。
func Version(ctx context.Context, r Runner, n model.Node) (string, error) {
	out, err := r.Run(ctx, n, xrayBin+"\"$XRAY_BIN\" version | head -1")
	if err != nil {
		return "", err
	}
	if fields := strings.Fields(out); len(fields) >= 2 {
		return strings.TrimPrefix(fields[1], "v"), nil
	}
	return strings.TrimSpace(out), nil
}
