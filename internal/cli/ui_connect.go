package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/pin"
	"github.com/RovenEverward/my-proxy/internal/ui"
)

// connectRunner 把页面的「接入已有控制面」接到 mpx connect 的同一条路径上:
// 地址整理与取指纹共用 controlURL / pin.Probe,换 token 存配置档共用
// connectAndSaveQuiet,没有第二份实现。
type connectRunner struct{}

// Probe:整理地址、取回证书指纹。指纹只取来给人核对,这里不做任何判断 ——
// TOFU 的确认在页面上完成(勾选「指纹核对无误」)。
func (connectRunner) Probe(ctx context.Context, addr string) (string, string, error) {
	server, hostport, err := controlURL(addr)
	if err != nil {
		return "", "", err
	}
	fp, err := pin.Probe(ctx, hostport)
	if err != nil {
		return "", "", fmt.Errorf("连不上控制面 %s: %w", hostport, err)
	}
	return server, fp, nil
}

// Connect:用设备密码换 token,存进配置档并切为当前;返回页面切到正常模式要用的
// upstream/token/transport。name 为空时用主机名,与 mpx connect 一致。
func (connectRunner) Connect(ctx context.Context, server, fingerprint, password, name string) (*ui.SetupResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultDeviceName()
	}
	res, profile, err := connectAndSaveQuiet(server, fingerprint, password, name)
	if err != nil {
		return nil, err
	}
	up, err := url.Parse(server)
	if err != nil || up.Host == "" {
		return nil, fmt.Errorf("控制面地址 %q 看不懂", server)
	}
	return &ui.SetupResult{
		HostPort:    up.Host,
		Fingerprint: fingerprint,
		Connected:   true,
		Upstream:    up,
		Token:       res.Token,
		Transport:   pin.Transport(fingerprint),
		Profile:     profile,
	}, nil
}

// ConnectProfile:与 Connect 同一件事,但存进名为 profile 的**新**配置档 —— 页面
// 正常模式的「添加控制面」用它。不覆盖已有档:名字被占用直接报错,让页面提示换名
// (别把 default 里好好的 token 冲掉)。
func (connectRunner) ConnectProfile(ctx context.Context, server, fingerprint, password, name, profile string) (*ui.SetupResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultDeviceName()
	}
	res, err := connectAndSaveProfile(server, fingerprint, password, name, profile)
	if err != nil {
		return nil, err
	}
	up, err := url.Parse(server)
	if err != nil || up.Host == "" {
		return nil, fmt.Errorf("控制面地址 %q 看不懂", server)
	}
	return &ui.SetupResult{
		HostPort:    up.Host,
		Fingerprint: fingerprint,
		Connected:   true,
		Upstream:    up,
		Token:       res.Token,
		Transport:   pin.Transport(fingerprint),
		Profile:     profile,
	}, nil
}
