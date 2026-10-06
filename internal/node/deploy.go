package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// Deploy 把一份配置下发到节点并重启 xray。
//
// 顺序是「备份 → 写入 → 让 xray 自己校验 → 重启」。多花一次往返是有意的:配置写坏
// 会让整台节点上所有人断流,而 xray 的 -test 能在重启之前就把它拦住。
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
	return Restart(ctx, r, n)
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

// Version 读节点上 xray 的版本,用于确认用的是哪个核心。
func Version(ctx context.Context, r Runner, n model.Node) (string, error) {
	out, err := r.Run(ctx, n, xrayBin+"\"$XRAY_BIN\" version | head -1")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
