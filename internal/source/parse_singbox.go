package source

// parse_singbox.go 解 sing-box 配置的 outbounds 段:只收独立代理节点,
// selector/urltest/direct/block/dns 等结构性 outbound 静默忽略,不导入上游的
// 分流与 DNS。字段同样按白名单核对,表达不了的整条进 Issue。

import (
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// singboxCommonKeys 是独立节点都可能有的字段。detour 及其同族是拨号链,
// 在白名单里但值非空即无法保留。
var singboxCommonKeys = map[string]bool{
	"type": true, "tag": true, "server": true, "server_port": true,
	"server_ports": true, "hop_interval": true,
	"tls": true, "transport": true, "multiplex": true, "network": true,
	"detour":         true,
	"bind_interface": true, "inet4_bind_address": true, "inet6_bind_address": true,
	"routing_mark": true, "reuse_addr": true, "connect_timeout": true,
	"tcp_fast_open": true, "tcp_multi_path": true, "udp_fragment": true,
	"domain_strategy": true, "fallback_delay": true,
}

// singboxProtocolKeys 是各协议自己的字段白名单。
var singboxProtocolKeys = map[string]map[string]bool{
	"vless": {
		"uuid": true, "flow": true, "packet_encoding": true, "udp_over_tcp": true,
	},
	"vmess": {
		"uuid": true, "security": true, "alter_id": true,
		"global_padding": true, "authenticated_length": true,
		"packet_encoding": true, "udp_over_tcp": true,
	},
	"trojan": {
		"password": true,
	},
	"shadowsocks": {
		"method": true, "password": true, "plugin": true, "plugin_opts": true,
		"udp_over_tcp": true,
	},
	"hysteria2": {
		"password": true, "up_mbps": true, "down_mbps": true,
		"obfs": true, "brutal_debug": true,
	},
	"tuic": {
		"uuid": true, "password": true, "congestion_control": true,
		"udp_relay_mode": true, "udp_over_stream": true,
		"zero_rtt_handshake": true, "heartbeat": true,
	},
}

// singboxIgnoredTypes 是结构性 outbound:它们不是节点,静默忽略。
var singboxIgnoredTypes = map[string]bool{
	"selector": true, "urltest": true, "direct": true, "block": true,
	"dns": true, "group": true,
}

// singboxTLSKeys 是 tls 对象的白名单;其余键(证书、ech、分片、复用等)
// 都无法保留。
var singboxTLSKeys = map[string]bool{
	"enabled": true, "server_name": true, "insecure": true, "alpn": true,
	"disable_sni": true, "min_version": true, "max_version": true,
	"cipher_suites": true, "certificate": true, "certificate_path": true,
	"ech": true, "utls": true, "reality": true,
	"fragment": true, "record_fragment": true, "tls_fragment": true,
	"kernel_tx": true, "kernel_rx": true,
}

// parseSingBox 解 outbounds 序列。
func parseSingBox(v any) (Result, error) {
	items, _, err := listOfMaps(v)
	res := Result{Format: FormatSingBox}
	if err != nil {
		return res, fmt.Errorf("source: sing-box 的 outbounds 不是列表")
	}
	for i, item := range items {
		if item == nil {
			res.Issues = append(res.Issues, Issue{Index: i + 1, Name: "", Reason: "条目不是映射"})
			continue
		}
		ep, reason, name, structural := singboxNode(item)
		if structural {
			continue // selector / urltest / direct 等:不是节点,静默忽略。
		}
		if reason != "" {
			res.Issues = append(res.Issues, Issue{Index: i + 1, Name: name, Reason: reason})
			continue
		}
		res.Endpoints = append(res.Endpoints, ep)
	}
	return res, nil
}

// singboxNode 把一条 outbound 解成端点。reason 非空表示跳过并报原因;
// structural 为真表示该 outbound 是结构件,静默忽略。
func singboxNode(m map[string]any) (render.Endpoint, string, string, bool) {
	ep := render.Endpoint{}
	name := getString(m, "tag")
	ep.Remark = name

	typ := strings.ToLower(getString(m, "type"))
	if singboxIgnoredTypes[typ] {
		return ep, "", name, true
	}
	keys, ok := singboxProtocolKeys[typ]
	if !ok {
		return ep, fmt.Sprintf("协议 %q 不在支持范围", typ), name, false
	}
	if extra := extraKeys(m, mergeKeys(singboxCommonKeys, keys)); len(extra) > 0 {
		return ep, extraErr(extra).Error(), name, false
	}

	server := getString(m, "server")
	if server == "" {
		return ep, "缺少 server", name, false
	}
	port, err := parsePort(m["server_port"])
	if err != nil {
		return ep, err.Error(), name, false
	}
	// 端口跳跃表达不了。
	if _, multi := m["server_ports"]; multi {
		return ep, "server_ports 端口跳跃无法保留", name, false
	}
	if v := getString(m, "detour"); v != "" {
		return ep, "拨号链(detour)无法保留", name, false
	}
	ep.Address, ep.Port = server, port

	if reason := singboxTLS(m, &ep); reason != "" {
		return ep, reason, name, false
	}
	if reason := singboxTransport(m, &ep); reason != "" {
		return ep, reason, name, false
	}
	if reason := singboxMultiplex(m, &ep); reason != "" {
		return ep, reason, name, false
	}

	if ep.Security == model.SecurityReality {
		if typ != "vless" && typ != "vmess" {
			return ep, fmt.Sprintf("协议 %q 不支持 reality", typ), name, false
		}
		if reason := realityFieldCheck(&ep); reason != "" {
			return ep, reason, name, false
		}
	}

	switch typ {
	case "vless":
		ep.Protocol = model.ProtocolVLESS
		ep.UUID = getString(m, "uuid")
		ep.Flow = getString(m, "flow")
		if ep.UUID == "" {
			return ep, "缺少 uuid", name, false
		}
		if _, bad := m["packet_encoding"]; bad {
			return ep, "packet_encoding 无法保留", name, false
		}
	case "vmess":
		ep.Protocol = model.ProtocolVMess
		ep.UUID = getString(m, "uuid")
		if ep.UUID == "" {
			return ep, "缺少 uuid", name, false
		}
		if aid, present := getInt(m, "alter_id"); present {
			ep.VMessAlterID = aid
		}
		sec := getString(m, "security")
		if sec == "" {
			sec = "auto"
		}
		if !vmessAllowedCiphers[sec] {
			return ep, fmt.Sprintf("vmess 加密 %q 不在支持范围", sec), name, false
		}
		ep.VMessSecurity = sec
		for _, bad := range []string{"packet_encoding", "global_padding", "authenticated_length"} {
			if _, present := m[bad]; present {
				return ep, bad + " 无法保留", name, false
			}
		}
	case "trojan":
		ep.Protocol = model.ProtocolTrojan
		ep.Password = getString(m, "password")
		if ep.Password == "" {
			return ep, "缺少 password", name, false
		}
		// sing-box 的 trojan 不写 tls 就是明文;与 clash 的默认 TLS 不同。
	case "shadowsocks":
		ep.Protocol = model.ProtocolShadowsocks
		method := getString(m, "method")
		if !ssAllowedMethods[method] {
			return ep, fmt.Sprintf("ss method %q 不在支持范围", method), name, false
		}
		ep.SSMethod = method
		ep.Password = getString(m, "password")
		if ep.Password == "" {
			return ep, "缺少 password", name, false
		}
		if reason := ssKeyCheck(method, ep.Password); reason != "" {
			return ep, reason, name, false
		}
		if _, bad := m["plugin"]; bad {
			return ep, "plugin 无法保留", name, false
		}
		if _, bad := m["plugin_opts"]; bad {
			return ep, "plugin_opts 无法保留", name, false
		}
		if reason := singboxUDPOverTCP(m); reason != "" {
			return ep, reason, name, false
		}
		// shadowsocks 的 network 字段只能是 tcp_and_udp;udp 形态表达不了。
		if nw := getString(m, "network"); nw != "" && nw != "tcp_and_udp" {
			return ep, fmt.Sprintf("shadowsocks network %q 无法保留", nw), name, false
		}
	case "hysteria2":
		ep.Protocol = model.ProtocolHysteria2
		ep.Password = getString(m, "password")
		if ep.Password == "" {
			return ep, "缺少 password", name, false
		}
		if _, bad := m["obfs"]; bad {
			return ep, "混淆(obfs)无法保留", name, false
		}
		if nw := getString(m, "network"); nw != "" && nw != "udp" {
			return ep, fmt.Sprintf("network %q 无法保留", nw), name, false
		}
		ep.Network = model.NetworkUDP
		ep.Security = model.SecurityTLS
	case "tuic":
		ep.Protocol = model.ProtocolTUIC
		ep.UUID = getString(m, "uuid")
		ep.Password = getString(m, "password")
		if ep.UUID == "" || ep.Password == "" {
			return ep, "缺少 uuid 或 password", name, false
		}
		if reason := tuicUUIDCheck(ep.UUID); reason != "" {
			return ep, reason, name, false
		}
		ep.CongestionControl = getString(m, "congestion_control")
		if ep.CongestionControl != "" {
			switch ep.CongestionControl {
			case "bbr", "cubic", "new_reno":
			default:
				return ep, fmt.Sprintf("tuic 拥塞控制 %q 不在支持范围", ep.CongestionControl), name, false
			}
		}
		for _, bad := range []string{"udp_relay_mode", "udp_over_stream", "zero_rtt_handshake", "heartbeat"} {
			if v, present := m[bad]; present {
				switch bad {
				case "udp_relay_mode":
					if s, ok := v.(string); ok && s == "native" {
						continue
					}
				case "udp_over_stream", "zero_rtt_handshake":
					if b, ok := v.(bool); ok && !b {
						continue
					}
				case "heartbeat":
					continue // 客户端侧心跳间隔,不影响连接参数。
				}
				return ep, bad + " 无法保留", name, false
			}
		}
		if nw := getString(m, "network"); nw != "" && nw != "udp" {
			return ep, fmt.Sprintf("network %q 无法保留", nw), name, false
		}
		ep.Network = model.NetworkUDP
		ep.Security = model.SecurityTLS
	}
	return ep, "", name, false
}

// singboxUDPOverTCP 检查 udp_over_tcp:为真(或对象 enabled)时无法保留,
// 缺省/关闭视同默认。
func singboxUDPOverTCP(m map[string]any) string {
	v, present := m["udp_over_tcp"]
	if !present {
		return ""
	}
	switch t := v.(type) {
	case bool:
		if t {
			return "udp_over_tcp 无法保留"
		}
	case map[string]any:
		if getBool(t, "enabled") {
			return "udp_over_tcp 无法保留"
		}
	}
	return ""
}

// singboxTLS 解 tls 对象:enabled=false 或无 tls 即 none;reality.enabled
// 时才算 REALITY。白名单之外的键无法保留。
func singboxTLS(m map[string]any, ep *render.Endpoint) string {
	tls, err := requireMap(m, "tls")
	if err != nil {
		return err.Error()
	}
	if tls == nil || !getBool(tls, "enabled") {
		return ""
	}
	if extra := extraKeys(tls, singboxTLSKeys); len(extra) > 0 {
		return extraErr(extra).Error()
	}
	for _, fatal := range []struct {
		key, reason string
	}{
		{"ech", "ECH 无法保留"},
		{"certificate", "客户端证书无法保留"},
		{"certificate_path", "客户端证书无法保留"},
		{"fragment", "TLS 分片无法保留"},
		{"record_fragment", "TLS 分片无法保留"},
		{"tls_fragment", "TLS 分片无法保留"},
		{"kernel_tx", "kernel_tx 无法保留"},
		{"kernel_rx", "kernel_rx 无法保留"},
	} {
		if _, present := tls[fatal.key]; present {
			return fatal.reason
		}
	}
	if getBool(tls, "disable_sni") {
		return "disable_sni 无法保留"
	}
	ep.Security = model.SecurityTLS
	ep.SNI = getString(tls, "server_name")
	ep.ALPN = getStringList(tls, "alpn")
	ep.AllowInsecure = getBool(tls, "insecure")

	utls, err := requireMap(tls, "utls")
	if err != nil {
		return err.Error()
	}
	if utls != nil {
		if extra := extraKeys(utls, map[string]bool{"enabled": true, "fingerprint": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		if getBool(utls, "enabled") {
			ep.Fingerprint = getString(utls, "fingerprint")
		}
	}
	reality, err2 := requireMap(tls, "reality")
	if err2 != nil {
		return err2.Error()
	}
	if reality != nil && getBool(reality, "enabled") {
		if extra := extraKeys(reality, map[string]bool{"enabled": true, "public_key": true, "short_id": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		ep.Security = model.SecurityReality
		ep.RealityPub = getString(reality, "public_key")
		ep.RealitySID = getString(reality, "short_id")
		if ep.RealityPub == "" {
			return "reality.public_key 缺失"
		}
	} else if reality != nil {
		if extra := extraKeys(reality, map[string]bool{"enabled": true, "public_key": true, "short_id": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
	}
	return ""
}

// singboxTransport 解 transport 对象:ws/grpc/httpupgrade 可映射,其余
// transport 类型(http / quic / http3…)无法保留。
func singboxTransport(m map[string]any, ep *render.Endpoint) string {
	tr, err := requireMap(m, "transport")
	if err != nil {
		return err.Error()
	}
	if tr == nil {
		ep.Network = model.NetworkTCP
		return ""
	}
	ttype := strings.ToLower(getString(tr, "type"))
	switch ttype {
	case "ws":
		if extra := extraKeys(tr, map[string]bool{"type": true, "path": true, "headers": true, "max_early_data": true, "early_data_header_name": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		for _, bad := range []string{"max_early_data", "early_data_header_name"} {
			if v, present := tr[bad]; present {
				if n, ok := v.(float64); ok && n == 0 {
					continue
				}
				return bad + " 无法保留"
			}
		}
		ep.Network = model.NetworkWS
		ep.Path = getString(tr, "path")
		headers, err := requireMap(tr, "headers")
		if err != nil {
			return "transport.headers 无法保留"
		}
		for k, v := range headers {
			if !strings.EqualFold(k, "host") {
				return fmt.Sprintf("transport.headers 的 %q 头无法保留", k)
			}
			if s, ok := v.(string); ok {
				ep.Host = s
			}
		}
	case "grpc":
		if extra := extraKeys(tr, map[string]bool{"type": true, "service_name": true, "idle_timeout": true, "ping_timeout": true, "permit_without_stream": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		if getBool(tr, "permit_without_stream") {
			return "grpc permit_without_stream 无法保留"
		}
		ep.Network = model.NetworkGRPC
		ep.Path = getString(tr, "service_name")
	case "httpupgrade":
		if extra := extraKeys(tr, map[string]bool{"type": true, "host": true, "path": true, "headers": true}); len(extra) > 0 {
			return extraErr(extra).Error()
		}
		h, err := requireMap(tr, "headers")
		if err != nil {
			return "httpupgrade headers 无法保留"
		}
		if len(h) > 0 {
			return "httpupgrade headers 无法保留"
		}
		ep.Network = model.NetworkHTTPUpgrade
		ep.Host = getString(tr, "host")
		ep.Path = getString(tr, "path")
	default:
		return fmt.Sprintf("transport 类型 %q 无法保留", ttype)
	}
	return ""
}

// singboxMultiplex 解 multiplex:仅 smux 协议映射到 Mux。
func singboxMultiplex(m map[string]any, ep *render.Endpoint) string {
	mux, err := requireMap(m, "multiplex")
	if err != nil {
		return err.Error()
	}
	if mux == nil {
		return ""
	}
	if extra := extraKeys(mux, map[string]bool{
		"enabled": true, "protocol": true, "max_connections": true,
		"min_streams": true, "max_streams": true, "padding": true,
	}); len(extra) > 0 {
		return extraErr(extra).Error()
	}
	if !getBool(mux, "enabled") {
		return ""
	}
	if proto := getString(mux, "protocol"); proto != "" && proto != "smux" {
		return fmt.Sprintf("multiplex 协议 %q 无法保留", proto)
	}
	ep.Mux = true
	return ""
}
