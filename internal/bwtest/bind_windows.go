//go:build windows

package bwtest

import (
	"syscall"
)

// bindControl 在 Windows 上退化:没有 SO_BINDTODEVICE 等价物,绑接口靠
// 源地址 + 路由表,探测侧拿不到统一答案;返回 nil Control 让拨号走默认,
// 由上层在「绕行不成立」时给「加 TUN 白名单」的提示。
func bindControl(iface string) func(string, string, syscall.RawConn) error {
	return nil
}
