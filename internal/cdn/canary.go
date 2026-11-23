package cdn

import (
	"context"
	"net"
	"time"
)

// CanaryTargets 是境内探测端的金丝雀清单:境内直连不可能探通的目标。
// 它们能 TCP 拨通,说明本机出机被代理接管 —— 这时优选结果全是假的。
var CanaryTargets = []string{"www.google.com:443", "www.youtube.com:443"}

// TrustedPath 检查本机出机路径是不是干净的境内直连:逐个对 CanaryTargets
// 做 TCP 探测,全都不通才算干净。timeout<=0 用默认 2.5s。
func TrustedPath(ctx context.Context, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 2500 * time.Millisecond
	}
	for _, addr := range CanaryTargets {
		d := net.Dialer{Timeout: timeout}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return false
		}
	}
	return true
}
