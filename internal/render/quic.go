package render

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/netutil"
)

// quicEndpoint 校验客户端共同的 QUIC 约束,留空时按原生 UDP/TLS 处理。
func quicEndpoint(ep Endpoint) error {
	if effectiveNetwork(ep) != model.NetworkUDP || effectiveSecurity(ep) != model.SecurityTLS {
		return fmt.Errorf("QUIC 端点 %q 必须使用 UDP 和 TLS", ep.Remark)
	}
	if ep.Password == "" || (ep.Protocol == model.ProtocolTUIC && ep.UUID == "") {
		return fmt.Errorf("QUIC 端点 %q 缺少认证凭据", ep.Remark)
	}
	if ep.Protocol == model.ProtocolTUIC {
		_, err := quicCongestion(ep.CongestionControl)
		return err
	}
	return nil
}

func quicCongestion(value string) (string, error) {
	if value == "" {
		return "bbr", nil
	}
	switch value {
	case "bbr", "cubic", "new_reno":
		return value, nil
	default:
		return "", fmt.Errorf("TUIC 不支持的拥塞控制 %q", value)
	}
}

// singboxQUICNode 不混入 TCP transport、uTLS 或 multiplex 字段。
func singboxQUICNode(ep Endpoint, tag string) (map[string]any, error) {
	if err := quicEndpoint(ep); err != nil {
		return nil, err
	}
	tls := map[string]any{"enabled": true}
	if ep.SNI != "" {
		tls["server_name"] = ep.SNI
	}
	if len(ep.ALPN) > 0 {
		tls["alpn"] = ep.ALPN
	}
	if ep.AllowInsecure {
		tls["insecure"] = true
	}
	node := map[string]any{
		"type": string(ep.Protocol), "tag": tag, "server": ep.Address,
		"server_port": ep.Port, "password": ep.Password, "tls": tls,
	}
	if ep.Protocol == model.ProtocolTUIC {
		node["uuid"] = ep.UUID
		node["congestion_control"], _ = quicCongestion(ep.CongestionControl)
	}
	return node, nil
}

// ProbeClient 生成仅监听回环 mixed 入口、全部请求走待测端点的 sing-box 配置。
// 不带订阅模板的 TUN、规则下载和自动测速,便于独立验证认证与代理连接。
func ProbeClient(ep Endpoint, port int) ([]byte, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("探测端口 %d 超出范围", port)
	}
	cfg := map[string]any{
		"log":      map[string]any{"level": "warn"},
		"inbounds": []any{map[string]any{"type": "mixed", "tag": "probe-in", "listen": "127.0.0.1", "listen_port": port}},
		"route":    map[string]any{"final": "probe"},
	}
	if ep.Protocol == model.ProtocolWireguard {
		cfg["endpoints"] = []any{singboxWireguardEndpoint(ep, "probe")}
	} else {
		node, err := singboxNode(ep, "probe")
		if err != nil {
			return nil, err
		}
		cfg["outbounds"] = []any{node}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func clashQUICNode(ep Endpoint, meta bool) (clashProxy, error) {
	if !meta {
		return clashProxy{}, fmt.Errorf("render: 原版 Clash 不支持 QUIC 协议 %q,请使用 Clash.Meta", ep.Protocol)
	}
	if err := quicEndpoint(ep); err != nil {
		return clashProxy{}, err
	}
	p := clashProxy{Name: ep.Remark, Type: string(ep.Protocol), Server: ep.Address, Port: ep.Port,
		Password: ep.Password, UDP: true, SNI: ep.SNI, ALPN: ep.ALPN, SkipCertVerify: ep.AllowInsecure}
	if ep.Protocol == model.ProtocolTUIC {
		p.UUID = ep.UUID
		p.CongestionControl, _ = quicCongestion(ep.CongestionControl)
	}
	return p, nil
}

func quicLink(ep Endpoint) string {
	q := url.Values{}
	if ep.SNI != "" {
		q.Set("sni", ep.SNI)
	}
	if len(ep.ALPN) > 0 {
		q.Set("alpn", strings.Join(ep.ALPN, ","))
	}
	if ep.AllowInsecure {
		if ep.Protocol == model.ProtocolHysteria2 {
			q.Set("insecure", "1")
		} else {
			q.Set("allow_insecure", "1")
		}
	}
	auth := url.User(ep.Password)
	if ep.Protocol == model.ProtocolTUIC {
		auth = url.UserPassword(ep.UUID, ep.Password)
		cc, _ := quicCongestion(ep.CongestionControl)
		q.Set("congestion_control", cc)
	}
	return (&url.URL{Scheme: string(ep.Protocol), User: auth,
		Host: netutil.JoinHostPort(ep.Address, ep.Port), RawQuery: q.Encode(), Fragment: ep.Remark}).String()
}
