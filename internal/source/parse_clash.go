package source

// parse_clash.go 解 Clash/mihomo YAML 的 proxies 段。
//
// 每种协议有一份字段白名单:不在名单上的字段视为「无法完整保留」,整条
// 节点进 Issue。tls/reality-opts/ws-opts 等子对象同样按白名单核对。

import (
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// clashCommonKeys 是所有代理类型都可能有的字段。
var clashCommonKeys = map[string]bool{
	"name": true, "type": true, "server": true, "port": true,
	"udp": true, "tfo": true, "mptcp": true,
	"ip-version": true, "interface-name": true, "routing-mark": true,
	"dialer-proxy-chain": true,
	"tls":                true, "sni": true, "servername": true, "alpn": true,
	"skip-cert-verify": true, "client-fingerprint": true, "fingerprint": true,
	"network": true,
	"ws-opts": true, "grpc-opts": true, "xhttp-opts": true, "httpupgrade-opts": true,
	"reality-opts": true, "smux": true,
}

// clashProtocolKeys 是各协议自己的字段白名单。
var clashProtocolKeys = map[string]map[string]bool{
	"vless": {
		"uuid": true, "flow": true, "encryption": true,
	},
	"vmess": {
		"uuid": true, "alterId": true, "cipher": true, "security": true,
		"global-padding": true, "authenticated-length": true,
		"packet-encoding": true,
	},
	"trojan": {
		"password": true,
	},
	"ss": {
		"cipher": true, "password": true, "passwords": true,
	},
	"hysteria2": {
		"password": true, "auth": true,
		"up": true, "down": true, "up-mbps": true, "down-mbps": true,
		"ports": true, "obfs": true, "obfs-password": true,
		"ca": true, "ca-str": true,
	},
	"tuic": {
		"uuid": true, "password": true, "token": true,
		"congestion-controller": true, "udp-relay-mode": true,
		"reduce-rtt": true, "heartbeat-interval": true,
		"request-timeout": true, "max-udp-relay-packet-size": true,
		"fast-open": true, "disable-sni": true,
	},
}

// ssAllowedMethods 是本实现能完整表达并渲染的 SS method。
var ssAllowedMethods = map[string]bool{
	"aes-128-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305":  true,
	"2022-blake3-aes-128-gcm": true,
}

// vmessAllowedCiphers 是可保留的 vmess 用户加密取值。
var vmessAllowedCiphers = map[string]bool{
	"auto": true, "aes-128-gcm": true, "chacha20-poly1305": true,
	"none": true, "zero": true,
}

// parseClash 解 proxies 序列。
func parseClash(v any) (Result, error) {
	items, _, err := listOfMaps(v)
	res := Result{Format: FormatClash}
	if err != nil {
		return res, fmt.Errorf("source: clash 的 proxies 不是列表")
	}
	for i, item := range items {
		if item == nil {
			res.Issues = append(res.Issues, Issue{Index: i + 1, Name: "", Reason: "条目不是映射"})
			continue
		}
		ep, reason, name := clashNode(item)
		if reason != "" {
			res.Issues = append(res.Issues, Issue{Index: i + 1, Name: name, Reason: reason})
			continue
		}
		res.Endpoints = append(res.Endpoints, ep)
	}
	return res, nil
}

// clashNode 把一条 proxy 映射解成端点。reason 非空表示跳过。
func clashNode(m map[string]any) (render.Endpoint, string, string) {
	ep := render.Endpoint{}
	name := getString(m, "name")
	ep.Remark = name

	typ := strings.ToLower(getString(m, "type"))
	keys, ok := clashProtocolKeys[typ]
	if !ok {
		return ep, fmt.Sprintf("协议 %q 不在支持范围", typ), name
	}
	if extra := extraKeys(m, mergeKeys(clashCommonKeys, keys)); len(extra) > 0 {
		return ep, extraErr(extra).Error(), name
	}
	for _, fatal := range []struct {
		key, reason string
	}{
		{"dialer-proxy", "拨号链(dialer-proxy)无法保留"},
		{"plugin", "插件(plugin)无法保留"},
		{"plugin-opts", "插件参数(plugin-opts)无法保留"},
		{"obfs", "混淆(obfs)无法保留"},
		{"obfs-password", "混淆密码无法保留"},
		{"ports", "端口跳跃(ports)无法保留"},
		{"ca", "自定义 CA 无法保留"},
		{"ca-str", "自定义 CA 无法保留"},
		{"fingerprint", "证书固定(fingerprint)无法保留"},
		{"passwords", "多密码无法保留"},
		{"token", "tuic token 形态无法保留"},
		{"encryption", "无法保留的加密声明"},
		{"packet-encoding", "packet-encoding 无法保留"},
		{"authenticated-length", "authenticated-length 无法保留"},
		{"global-padding", "global-padding 无法保留"},
		{"dialer-proxy-chain", "拨号链(dialer-proxy-chain)无法保留"},
	} {
		if _, present := m[fatal.key]; present {
			if fatal.key == "encryption" && getString(m, "encryption") == "none" {
				continue
			}
			if fatal.key == "obfs" && strings.EqualFold(getString(m, "obfs"), "none") {
				continue
			}
			return ep, fatal.reason, name
		}
	}
	// 值条件:非默认取值才无法保留。
	if v := getString(m, "udp-relay-mode"); v != "" && v != "native" {
		return ep, fmt.Sprintf("udp-relay-mode %q 无法保留", v), name
	}
	if getBool(m, "disable-sni") {
		return ep, "disable-sni 无法保留", name
	}

	server := getString(m, "server")
	if server == "" {
		return ep, "缺少 server", name
	}
	port, err := parsePort(m["port"])
	if err != nil {
		return ep, err.Error(), name
	}
	ep.Address, ep.Port = server, port

	network, bad := mapNetwork(getString(m, "network"))
	if bad {
		return ep, fmt.Sprintf("传输 %q 无法保留", getString(m, "network")), name
	}
	ep.Network = network

	if reason := clashTLS(m, &ep); reason != "" {
		return ep, reason, name
	}
	if reason := clashTransport(m, &ep, typ); reason != "" {
		return ep, reason, name
	}
	if reason := clashSmux(m, &ep); reason != "" {
		return ep, reason, name
	}

	// REALITY 字段有效性与协议组合在协议分派前查:typ 还没落到 ep 上,
	// 这里用映射出的 Security 直接判。
	if ep.Security == model.SecurityReality {
		if typ != "vless" && typ != "vmess" {
			return ep, fmt.Sprintf("协议 %q 不支持 reality", typ), name
		}
		if reason := realityFieldCheck(&ep); reason != "" {
			return ep, reason, name
		}
	}

	switch typ {
	case "vless":
		ep.Protocol = model.ProtocolVLESS
		ep.UUID = getString(m, "uuid")
		ep.Flow = getString(m, "flow")
		if ep.UUID == "" {
			return ep, "缺少 uuid", name
		}
	case "vmess":
		ep.Protocol = model.ProtocolVMess
		ep.UUID = getString(m, "uuid")
		if ep.UUID == "" {
			return ep, "缺少 uuid", name
		}
		if aid, present := getInt(m, "alterId", "alter-id", "aid"); present {
			ep.VMessAlterID = aid
		}
		sec := getString(m, "cipher", "security")
		if sec == "" {
			sec = "auto"
		}
		if !vmessAllowedCiphers[sec] {
			return ep, fmt.Sprintf("vmess 加密 %q 不在支持范围", sec), name
		}
		if c, s := getString(m, "cipher"), getString(m, "security"); c != "" && s != "" && c != s {
			return ep, "vmess cipher 与 security 冲突", name
		}
		ep.VMessSecurity = sec
	case "trojan":
		ep.Protocol = model.ProtocolTrojan
		ep.Password = getString(m, "password")
		if ep.Password == "" {
			return ep, "缺少 password", name
		}
		// clash 的 trojan 无论 tls 写不写都是 TLS 握手。
		ep.Security = model.SecurityTLS
	case "ss":
		ep.Protocol = model.ProtocolShadowsocks
		cipher := getString(m, "cipher")
		if !ssAllowedMethods[cipher] {
			return ep, fmt.Sprintf("ss method %q 不在支持范围", cipher), name
		}
		ep.SSMethod = cipher
		ep.Password = getString(m, "password")
		if ep.Password == "" {
			return ep, "缺少 password", name
		}
		if ep.Network != model.NetworkTCP {
			return ep, "ss 不支持传输层", name
		}
		if reason := ssKeyCheck(cipher, ep.Password); reason != "" {
			return ep, reason, name
		}
	case "hysteria2":
		ep.Protocol = model.ProtocolHysteria2
		ep.Password = getString(m, "password", "auth")
		if ep.Password == "" {
			return ep, "缺少 password", name
		}
		ep.Network = model.NetworkUDP
		ep.Security = model.SecurityTLS
	case "tuic":
		ep.Protocol = model.ProtocolTUIC
		ep.UUID = getString(m, "uuid")
		ep.Password = getString(m, "password")
		if ep.UUID == "" || ep.Password == "" {
			return ep, "缺少 uuid 或 password", name
		}
		if reason := tuicUUIDCheck(ep.UUID); reason != "" {
			return ep, reason, name
		}
		ep.CongestionControl = getString(m, "congestion-controller")
		if ep.CongestionControl != "" {
			switch ep.CongestionControl {
			case "bbr", "cubic", "new_reno":
			default:
				return ep, fmt.Sprintf("tuic 拥塞控制 %q 不在支持范围", ep.CongestionControl), name
			}
		}
		ep.Network = model.NetworkUDP
		ep.Security = model.SecurityTLS
	}
	return ep, "", name
}

func mergeKeys(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// clashTLS 读 TLS 层字段:servername 优先于 sni,reality-opts 出现即
// SecurityReality,tls: true 是 SecurityTLS。hy2/tuic 由调用方随后强制
// SecurityTLS。
func clashTLS(m map[string]any, ep *render.Endpoint) string {
	ep.SNI = getString(m, "servername", "sni")
	ep.ALPN = getStringList(m, "alpn")
	ep.AllowInsecure = getBool(m, "skip-cert-verify")
	ep.Fingerprint = getString(m, "client-fingerprint")

	reality, err := requireMap(m, "reality-opts")
	if err != nil {
		return err.Error()
	}
	if reality != nil {
		if extra := extraKeys(reality, map[string]bool{"public-key": true, "short-id": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		ep.Security = model.SecurityReality
		ep.RealityPub = getString(reality, "public-key")
		ep.RealitySID = getString(reality, "short-id")
		if ep.RealityPub == "" {
			return "reality-opts 缺 public-key"
		}
		return ""
	}
	if getBool(m, "tls") {
		ep.Security = model.SecurityTLS
	}
	return ""
}

// clashTransport 读传输层:子对象同样按白名单核对,ws 只收 Host 头。
func clashTransport(m map[string]any, ep *render.Endpoint, typ string) string {
	switch ep.Network {
	case model.NetworkWS:
		opts, err := requireMap(m, "ws-opts")
		if err != nil {
			return err.Error()
		}
		if opts == nil {
			return ""
		}
		if getBool(opts, "v2ray-http-upgrade") {
			ep.Network = model.NetworkHTTPUpgrade
			return clashHTTPUpgrade(opts, ep)
		}
		if extra := extraKeys(opts, map[string]bool{"path": true, "headers": true, "max-early-data": true, "early-data-header-name": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		if _, bad := opts["max-early-data"]; bad {
			return "ws-opts.max-early-data 无法保留"
		}
		if _, bad := opts["early-data-header-name"]; bad {
			return "ws-opts.early-data-header-name 无法保留"
		}
		ep.Path = getString(opts, "path")
		headers, err := requireMap(opts, "headers")
		if err != nil {
			return "ws-opts.headers 无法保留"
		}
		for k, v := range headers {
			if !strings.EqualFold(k, "host") {
				return fmt.Sprintf("ws-opts.headers 的 %q 头无法保留", k)
			}
			if s, ok := v.(string); ok {
				ep.Host = s
			}
		}
	case model.NetworkGRPC:
		opts, err := requireMap(m, "grpc-opts")
		if err != nil {
			return err.Error()
		}
		if opts == nil {
			return ""
		}
		if extra := extraKeys(opts, map[string]bool{
			"grpc-service-name": true, "grpc-authority": true, "grpc-mode": true,
		}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		if mode := getString(opts, "grpc-mode"); mode != "" && mode != "gun" {
			return fmt.Sprintf("grpc-opts.grpc-mode %q 无法保留", mode)
		}
		ep.Path = getString(opts, "grpc-service-name")
		ep.Host = getString(opts, "grpc-authority")
	case model.NetworkXHTTP:
		opts, err := requireMap(m, "xhttp-opts")
		if err != nil {
			return err.Error()
		}
		if opts == nil {
			return ""
		}
		if extra := extraKeys(opts, map[string]bool{"path": true, "host": true, "mode": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		ep.Path = getString(opts, "path")
		ep.Host = getString(opts, "host")
		ep.Mode = getString(opts, "mode")
	case model.NetworkHTTPUpgrade:
		opts, err := requireMap(m, "httpupgrade-opts")
		if err != nil {
			return err.Error()
		}
		if opts == nil {
			return ""
		}
		return clashHTTPUpgrade(opts, ep)
	}
	return ""
}

// clashHTTPUpgrade 读 httpupgrade 形态的 path/host(ws-opts 里的
// v2ray-http-upgrade 也走这里,此时字段名是 host/path)。
func clashHTTPUpgrade(opts map[string]any, ep *render.Endpoint) string {
	if extra := extraKeys(opts, map[string]bool{"path": true, "host": true, "v2ray-http-upgrade": true, "headers": true}); len(extra) > 0 {
		return extraErr(extra).Error()
	}
	ep.Path = getString(opts, "path")
	ep.Host = getString(opts, "host")
	headers, err := requireMap(opts, "headers")
	if err != nil {
		return "headers 无法保留"
	}
	if len(headers) > 0 {
		// ws-opts 转过来的 headers 里只允许 Host。
		for k, v := range headers {
			if !strings.EqualFold(k, "host") {
				return fmt.Sprintf("headers 的 %q 头无法保留", k)
			}
			if s, ok := v.(string); ok {
				ep.Host = s
			}
		}
	}
	return ""
}

// clashSmux 读客户端侧 smux:enabled + smux 协议可以映射到 Mux,其余取值
// 或字段无法保留。
func clashSmux(m map[string]any, ep *render.Endpoint) string {
	smux, err := requireMap(m, "smux")
	if err != nil {
		return err.Error()
	}
	if smux == nil {
		return ""
	}
	if extra := extraKeys(smux, map[string]bool{"enabled": true, "protocol": true}); len(extra) > 0 {
		return extraErr(extra).Error()
	}
	if !getBool(smux, "enabled") {
		return ""
	}
	if proto := getString(smux, "protocol"); proto != "" && proto != "smux" {
		return fmt.Sprintf("smux 协议 %q 无法保留", proto)
	}
	ep.Mux = true
	return ""
}
