// xray 测量路径:碰到 sing-box 说不出的 xray 专有传输(xhttp/splithttp/
// httpupgrade)时,lazy 下载官方 xray 二进制到本机缓存,起临时进程测速。
// 复用 xrayrelease.Fetch —— 控制面装节点核心走同一条下载路径。
package bwtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/xrayrelease"
)

var xrayOnce sync.Once
var xrayBin string
var xrayErr error

// needsXray 说这个端点只有 xray 能渲 —— 主要是 xray 专有传输。
func needsXray(ep render.Endpoint) bool {
	switch ep.Network {
	case "xhttp", "splithttp", "httpupgrade":
		return true
	}
	return false
}

// ensureXray 取(或下载)本机平台的 xray 二进制;失败缓存错误,下次不再重复下载。
func ensureXray(ctx context.Context) (string, error) {
	xrayOnce.Do(func() {
		dir, err := os.UserConfigDir()
		if err != nil {
			xrayErr = fmt.Errorf("找不到用户配置目录: %w", err)
			return
		}
		files, err := xrayrelease.Fetch(ctx, xrayrelease.Request{
			Version: xrayrelease.RecommendedVersion,
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
			Dir:     filepath.Join(dir, "mpx", "xray"),
		})
		if err != nil {
			xrayErr = fmt.Errorf("下载 xray %s: %w", xrayrelease.RecommendedVersion, err)
			return
		}
		xrayBin = files.Xray
	})
	return xrayBin, xrayErr
}

// measureViaXray 起一个临时 xray 进程,经 socks 下载测速。
func measureViaXray(ctx context.Context, ep render.Endpoint, bindIface string, opt Options) Result {
	res := Result{Status: "failed", Protocol: string(ep.Protocol), MeasuredAt: time.Now().UTC()}
	out, err := render.XrayOutbound(ep, "bw")
	if err != nil {
		res.Error = fmt.Sprintf("渲染出站: %v", err)
		return res
	}
	bin, err := ensureXray(ctx)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	// xray 的 dialer 绑定是 outbound 层 sockopt.dialerProxy 不管用;BSD 用
	// IP_BOUND_IF,xray 的 streamSettings.sockopt.interface 正好是同一语义。
	if bindIface != "" {
		if ss, ok := out["streamSettings"].(map[string]any); ok {
			sockopt, _ := ss["sockopt"].(map[string]any)
			if sockopt == nil {
				sockopt = map[string]any{}
			}
			sockopt["interface"] = bindIface
			ss["sockopt"] = sockopt
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		res.Error = fmt.Sprintf("找端口: %v", err)
		return res
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"tag": "in", "protocol": "mixed", "listen": "127.0.0.1", "port": port,
			"settings": map[string]any{},
		}},
		"outbounds": []any{out},
	}
	dir, err := os.MkdirTemp("", "mpx-bwxray-")
	if err != nil {
		res.Error = fmt.Sprintf("建临时目录: %v", err)
		return res
	}
	defer os.RemoveAll(dir)
	cfgPath := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		res.Error = fmt.Sprintf("组配置: %v", err)
		return res
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		res.Error = fmt.Sprintf("写配置: %v", err)
		return res
	}
	cmd := exec.CommandContext(ctx, bin, "-c", cfgPath)
	// xray 子进程与测量同生共死;stderr 留着,失败时带出来定位。
	cmd.Stderr = os.Stderr
	cmd.Stdout = nil
	if err := cmd.Start(); err != nil {
		res.Error = fmt.Sprintf("起 xray: %v", err)
		return res
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	time.Sleep(500 * time.Millisecond)

	return downloadBest(ctx, res, port, opt)
}
