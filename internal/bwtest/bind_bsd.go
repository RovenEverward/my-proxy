//go:build darwin || freebsd

package bwtest

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// bindControl 让 TCP 拨号绑到指定接口:macOS/BSD 用 IP_BOUND_IF + 接口索引。
func bindControl(iface string) func(string, string, syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var sockErr error
		err := c.Control(func(fd uintptr) {
			ifi, e := net.InterfaceByName(iface)
			if e != nil {
				sockErr = e
				return
			}
			sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, ifi.Index)
		})
		if err != nil {
			return err
		}
		return sockErr
	}
}
