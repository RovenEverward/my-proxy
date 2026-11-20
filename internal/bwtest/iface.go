package bwtest

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/reach"
)

// 本机出机被代理接管时,测量要绑到物理接口绕开 TUN(UC-20)。
//
// defaultPhysicalInterface 挑出「最像物理网卡」的接口名:非回环、是 up
// 状态、有全局 IPv4 地址、名字不像隧道/虚拟。拿不到确定性答案时返回错误,
// 由上层决定是否提示「手动加白名单」。

var virtualPrefixes = []string{
	"lo", "utun", "tun", "tap", "gif", "stf", "awdl", "llw", "bridge",
	"vnet", "vmnet", "docker", "veth", "br-", "zt", "tailscale", "wg",
	"ipsec", "ppp", "anpi", "en0-",
}

func defaultPhysicalInterface() (string, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, ifi := range ifs {
		name := ifi.Name
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		lname := strings.ToLower(name)
		virtual := false
		for _, p := range virtualPrefixes {
			if strings.HasPrefix(lname, p) {
				virtual = true
				break
			}
		}
		if virtual {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err == nil && ip.IsGlobalUnicast() && ip.To4() != nil {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("没找到物理网卡(平台 %s)", runtime.GOOS)
}

// boundCanary 对金丝雀做一次绑定物理接口的 TCP 拨测:探不通说明绑定后
// 走的是真实直连,绕行成立;探得通说明物理口出去的也还是被接管,
// 绕行不成立。
func boundCanary(ctx context.Context, iface string, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 2500 * time.Millisecond
	}
	for _, addr := range reach.CanaryTargets {
		d := net.Dialer{Timeout: timeout, Control: bindControl(iface)}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return false
		}
	}
	return true
}
