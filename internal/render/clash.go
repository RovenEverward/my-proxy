// clash.go 把端点渲染成 Clash / Clash.Meta(mihomo)订阅 YAML。
//
// 产出的是完整可用的订阅:顶层含 proxies、proxy-groups、rules 三段。
// meta 为 true 时按 mihomo 的字段渲染,额外写出 client-fingerprint。
//
// REALITY 目前只有 mihomo 支持。meta 为 false 时 reality-opts 仍会写出(结构
// 与 meta 版一致),但原版 Clash 不认这个字段,要用 REALITY 就必须走 mihomo。
package render

import (
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

// Clash 渲染 Clash / Clash.Meta 的订阅 YAML。meta 为 true 时按 mihomo 的字段渲染。
//
// 遇到本项目不支持的协议、传输或安全层直接报错,不静默丢节点:订阅是用户唯一
// 的连接入口,少一条比明确失败更难排查。
func Clash(eps []Endpoint, meta bool) ([]byte, error) {
	proxies := make([]clashProxy, 0, len(eps))
	names := make([]string, 0, len(eps))
	for _, ep := range eps {
		p, err := clashNode(ep, meta)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, p)
		names = append(names, ep.Remark)
	}

	// PROXY 是给用户手选的总入口,AUTO 是按延迟自动选。
	selectProxies := append([]string{"AUTO", "DIRECT"}, names...)

	cfg := clashConfig{
		Proxies: proxies,
		ProxyGroups: []clashGroup{
			{Name: "PROXY", Type: "select", Proxies: selectProxies},
			{
				Name:     "AUTO",
				Type:     "url-test",
				Proxies:  names,
				URL:      "http://www.gstatic.com/generate_204",
				Interval: 300,
			},
		},
		Rules: []string{
			"GEOIP,LAN,DIRECT",
			"GEOIP,CN,DIRECT",
			"MATCH,PROXY",
		},
	}

	return yaml.Marshal(cfg)
}

// clashConfig 是订阅 YAML 的顶层结构。
type clashConfig struct {
	Proxies     []clashProxy `yaml:"proxies"`
	ProxyGroups []clashGroup `yaml:"proxy-groups"`
	Rules       []string     `yaml:"rules"`
}

// clashGroup 是一个策略组。URL 与 Interval 只有 url-test 组才用,故可省略。
type clashGroup struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Proxies  []string `yaml:"proxies"`
	URL      string   `yaml:"url,omitempty"`
	Interval int      `yaml:"interval,omitempty"`
}

// clashProxy 是一个节点。字段顺序即 YAML 里的输出顺序。
//
// TLS 与 SkipCertVerify 用 bool + omitempty:false 表示"不写这个字段",正是
// security=none 想要的效果。AlterID 必须是指针,否则 vmess 的 alterId: 0 会被
// omitempty 吃掉。
type clashProxy struct {
	Name              string            `yaml:"name"`
	Type              string            `yaml:"type"`
	Server            string            `yaml:"server"`
	Port              int               `yaml:"port"`
	UUID              string            `yaml:"uuid,omitempty"`
	Password          string            `yaml:"password,omitempty"`
	AlterID           *int              `yaml:"alterId,omitempty"`
	Cipher            string            `yaml:"cipher,omitempty"`
	Network           string            `yaml:"network"`
	TLS               bool              `yaml:"tls,omitempty"`
	UDP               bool              `yaml:"udp"`
	ServerName        string            `yaml:"servername,omitempty"`
	ALPN              []string          `yaml:"alpn,omitempty"`
	Flow              string            `yaml:"flow,omitempty"`
	ClientFingerprint string            `yaml:"client-fingerprint,omitempty"`
	SkipCertVerify    bool              `yaml:"skip-cert-verify,omitempty"`
	WSOpts            *clashWSOpts      `yaml:"ws-opts,omitempty"`
	GRPCOpts          *clashGRPCOpts    `yaml:"grpc-opts,omitempty"`
	RealityOpts       *clashRealityOpts `yaml:"reality-opts,omitempty"`
}

// clashWSOpts 是 ws 的传输参数。
type clashWSOpts struct {
	Path    string            `yaml:"path,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// clashGRPCOpts 是 grpc 的传输参数。
type clashGRPCOpts struct {
	GRPCServiceName string `yaml:"grpc-service-name,omitempty"`
}

// clashRealityOpts 是 REALITY 参数,只有 mihomo 认。
type clashRealityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id,omitempty"`
}

// clashNode 把一个端点转成 Clash 节点。
func clashNode(ep Endpoint, meta bool) (clashProxy, error) {
	switch ep.Protocol {
	case model.ProtocolVLESS, model.ProtocolVMess, model.ProtocolTrojan:
	default:
		return clashProxy{}, fmt.Errorf("render: Clash 不支持的协议 %q", ep.Protocol)
	}

	transport := effectiveNetwork(ep)
	if !transport.Valid() {
		return clashProxy{}, fmt.Errorf("render: Clash 不支持的传输 %q", ep.Network)
	}
	sec := effectiveSecurity(ep)
	if !sec.Valid() {
		return clashProxy{}, fmt.Errorf("render: Clash 不支持的安全层 %q", ep.Security)
	}

	p := clashProxy{
		Name:    ep.Remark,
		Type:    string(ep.Protocol),
		Server:  ep.Address,
		Port:    ep.Port,
		Network: string(transport),
		UDP:     true,
	}

	switch ep.Protocol {
	case model.ProtocolVLESS:
		p.UUID = ep.UUID
		p.Flow = ep.Flow
	case model.ProtocolVMess:
		p.UUID = ep.UUID
		alterID := 0
		p.AlterID = &alterID
		p.Cipher = "auto"
	case model.ProtocolTrojan:
		p.Password = ep.Password
	}

	if sec == model.SecurityTLS || sec == model.SecurityReality {
		// REALITY 也走 TLS:握手仍然是 TLS,只是证书校验换成了 REALITY。
		p.TLS = true
		p.ServerName = ep.SNI
		p.ALPN = ep.ALPN
		p.SkipCertVerify = ep.AllowInsecure
	}
	// client-fingerprint 是 mihomo 独有字段,原版 Clash 会拒绝未知字段。
	if meta {
		p.ClientFingerprint = ep.Fingerprint
	}

	switch transport {
	case model.NetworkWS:
		opts := &clashWSOpts{Path: ep.Path}
		if ep.Host != "" {
			opts.Headers = map[string]string{"Host": ep.Host}
		}
		p.WSOpts = opts
	case model.NetworkGRPC:
		p.GRPCOpts = &clashGRPCOpts{GRPCServiceName: ep.Path}
	}

	if sec == model.SecurityReality {
		opts := &clashRealityOpts{PublicKey: ep.RealityPub}
		if ep.RealitySID != "" {
			opts.ShortID = ep.RealitySID
		}
		p.RealityOpts = opts
	}

	return p, nil
}
