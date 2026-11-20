//go:build linux

package bwtest

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// bindControl 让 TCP 拨号绑到指定接口:Linux 用 SO_BINDTODEVICE。
func bindControl(iface string) func(string, string, syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var sockErr error
		err := c.Control(func(fd uintptr) {
			sockErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, iface)
		})
		if err != nil {
			return err
		}
		return sockErr
	}
}
