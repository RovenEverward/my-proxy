// Package model 定义控制面的领域对象。
//
// 这里只有数据与纯判断,不碰数据库、网络与渲染;持久化和渲染各自依赖本包。
package model

// Protocol 是入站协议。
type Protocol string

const (
	ProtocolVLESS  Protocol = "vless"
	ProtocolVMess  Protocol = "vmess"
	ProtocolTrojan Protocol = "trojan"
	// ProtocolShadowsocks 只覆盖 shadowsocks-2022:加密方式在渲染层写死
	// 2022-blake3-aes-128-gcm,不把 aes-*-gcm 那代旧加密放进来。
	ProtocolShadowsocks Protocol = "shadowsocks"
	// ProtocolWireguard 是一个 L3 协议,不套传输与安全层:wireguard 自己的
	// 握手已经是加密通道,外面再包 tls/reality 没有意义。
	ProtocolWireguard Protocol = "wireguard"
)

func (p Protocol) Valid() bool {
	switch p {
	case ProtocolVLESS, ProtocolVMess, ProtocolTrojan, ProtocolShadowsocks, ProtocolWireguard, ProtocolHysteria2, ProtocolTUIC:
		return true
	}
	return false
}

// Network 是传输方式。
type Network string

const (
	NetworkTCP  Network = "tcp"
	NetworkWS   Network = "ws"
	NetworkGRPC Network = "grpc"
	// NetworkXHTTP 是 xray 的新方向:splithttp 与 httpupgrade 都已停止演进,
	// xhttp 把两者的特性收进一个传输。这里仍然渲染 splithttp / httpupgrade,
	// 因为存量客户端(v2rayNG 旧版、部分 mihomo)还认这两个名字。
	NetworkXHTTP       Network = "xhttp"
	NetworkSplitHTTP   Network = "splithttp"
	NetworkHTTPUpgrade Network = "httpupgrade"
)

func (n Network) Valid() bool {
	switch n {
	case NetworkTCP, NetworkWS, NetworkGRPC, NetworkXHTTP, NetworkSplitHTTP, NetworkHTTPUpgrade, NetworkUDP:
		return true
	}
	return false
}

// Security 是传输安全层。
type Security string

const (
	SecurityNone    Security = "none"
	SecurityTLS     Security = "tls"
	SecurityReality Security = "reality"
)

func (s Security) Valid() bool {
	switch s {
	case SecurityNone, SecurityTLS, SecurityReality:
		return true
	}
	return false
}

// ClientFormat 是订阅能产出的客户端配置格式。
//
// 它同时用作订阅规则的匹配结果:见 SubscriptionRule.Result。
type ClientFormat string

const (
	FormatLinks     ClientFormat = "links"        // 分享链接,每行一条
	FormatBase64    ClientFormat = "base64-links" // 分享链接整体 base64
	FormatClash     ClientFormat = "clash"        // Clash 订阅 YAML
	FormatClashMeta ClientFormat = "clash-meta"   // Clash.Meta / mihomo 订阅 YAML
	FormatSingBox   ClientFormat = "sing-box"     // sing-box 客户端 config.json
	FormatXray      ClientFormat = "xray"         // xray 客户端 config.json(每个节点一份)
	FormatBlock     ClientFormat = "block"        // 拒绝,返回 404
)

func (f ClientFormat) Valid() bool {
	switch f {
	case FormatLinks, FormatBase64, FormatClash, FormatClashMeta, FormatSingBox, FormatXray, FormatBlock:
		return true
	}
	return false
}
