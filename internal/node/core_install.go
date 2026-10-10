package node

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

const SingBoxBinPath = "/usr/local/lib/mpx/sing-box"

var singBoxServiceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]*$`)

// InstallSingBox installs the separately managed QUIC core without starting it.
// Deploy writes the new configuration and restarts its own systemd service.
func InstallSingBox(ctx context.Context, r Runner, n model.Node, d model.CoreDeployment, binaryPath string) error {
	if d.Core != model.CoreSingBox {
		return fmt.Errorf("sing-box 安装不能使用核心 %q", d.Core)
	}
	cfg, service := d.ConfigPathOrDefault(), d.ServiceNameOrDefault()
	if !path.IsAbs(cfg) || strings.ContainsAny(cfg, "\x00\r\n%") || !singBoxServiceName.MatchString(service) || service == n.ServiceNameOrDefault() || path.Clean(cfg) == path.Clean(n.ConfigPathOrDefault()) {
		return fmt.Errorf("sing-box 配置路径和服务名必须独立于 Xray: %q / %q", cfg, service)
	}
	bin := SingBoxBinPath
	same := sameOnNode(ctx, r, n, []coreFile{{binaryPath, bin}})[bin]
	if !same {
		body, err := os.ReadFile(binaryPath)
		if err != nil {
			return err
		}
		bin += ".new"
		if err := PutFile(ctx, r, n, bin, body); err != nil {
			return fmt.Errorf("推送 sing-box 到节点 %s: %w", n.Name, err)
		}
	}
	if _, err := r.Run(ctx, n, "chmod 755 "+shellQuote(bin)); err != nil {
		return err
	}
	if err := verifySingBox(ctx, r, n, bin, cfg); err != nil {
		return err
	}
	if !same {
		if _, err := r.Run(ctx, n, "mv -f "+shellQuote(bin)+" "+shellQuote(SingBoxBinPath)); err != nil {
			return err
		}
	}
	unitPath := "/etc/systemd/system/" + service + ".service"
	unit := fmt.Sprintf(singBoxUnitTemplate, SingBoxBinPath, strconv.Quote(strings.ReplaceAll(cfg, "$", "$$")))
	if err := PutFile(ctx, r, n, unitPath, []byte(unit)); err != nil {
		return fmt.Errorf("写 sing-box systemd 单元: %w", err)
	}
	if _, err := r.Run(ctx, n, "chmod 644 "+shellQuote(unitPath)+" && systemctl daemon-reload && systemctl enable "+shellQuote(service)); err != nil {
		return fmt.Errorf("启用 sing-box systemd 单元: %w", err)
	}
	return nil
}

func verifySingBox(ctx context.Context, r Runner, n model.Node, bin, cfg string) error {
	out, err := r.Run(ctx, n, shellQuote(bin)+" version")
	if err != nil {
		return fmt.Errorf("节点 %s 上 sing-box 无法执行: %w: %s", n.Name, err, strings.TrimSpace(out))
	}
	tags := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Tags: ") {
			for _, tag := range strings.Split(strings.TrimPrefix(line, "Tags: "), ",") {
				tags[strings.TrimSpace(tag)] = true
			}
		}
	}
	for _, tag := range []string{"with_quic", "with_v2ray_api"} {
		if !tags[tag] {
			return fmt.Errorf("节点 %s 的 sing-box 缺少构建标签 %s,已放弃替换", n.Name, tag)
		}
	}
	out, err = r.Run(ctx, n, "if [ -f "+shellQuote(cfg)+" ]; then "+shellQuote(bin)+" check -c "+shellQuote(cfg)+"; fi")
	if err != nil {
		return fmt.Errorf("sing-box 通不过节点 %s 上现有配置的校验,已放弃替换: %w: %s", n.Name, err, strings.TrimSpace(out))
	}
	return nil
}

const singBoxUnitTemplate = `[Unit]
Description=mpx sing-box QUIC node
Documentation=https://github.com/RovenEverward/my-proxy
After=network.target nss-lookup.target

[Service]
Type=simple
ExecStart=%s run -c %s
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`
