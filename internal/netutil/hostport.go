// Package netutil 收拢「地址怎么写进文本」这类跨模块共用的小规则。
//
// 分享链接、xray 的 wireguard endpoint、ssh 目标都把地址直接拼进字符串;
// 裸 IPv6 在这些位置必须加方括号,否则 net.SplitHostPort、URI 解析器和 ssh
// 都会把它当成多段冒号分隔的别的东西。规则只有这一份,避免每个调用点各写一遍。
package netutil

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// BracketHost 返回适合直接嵌入 URI、ssh 目标等文本的主机部分。
//
// 字面 IPv6 加方括号(含带 zone 的地址与 IPv4-mapped 地址);IPv4、域名和
// 已经带方括号的输入原样返回,重复调用不会叠出 [[...]]。
func BracketHost(host string) string {
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
		return "[" + host + "]"
	}
	return host
}

// JoinHostPort 把主机和端口拼成 host:port,字面 IPv6 自动带方括号。
//
// 结果可以直接交给 net.SplitHostPort;IPv4、域名与原来的 "%s:%d" 输出一致。
func JoinHostPort(host string, port int) string {
	return net.JoinHostPort(unbracket(host), strconv.Itoa(port))
}

// unbracket 去掉调用方可能已经加上的方括号,让 net.JoinHostPort 只加一次。
func unbracket(host string) string {
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		return host[1 : len(host)-1]
	}
	return host
}
