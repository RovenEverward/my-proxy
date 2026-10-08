// clash.go 把端点渲染成 Clash / Clash.Meta(mihomo)订阅 YAML。
//
// meta 为 false 时按原版 Clash 能接受的最小集合输出:proxies、proxy-groups、
// rules 三段,规则就三条(GEOIP,LAN / GEOIP,CN / MATCH),适合不想要完整规则表
// 的客户端。
//
// meta 为 true 时按 mihomo 渲染:在一份内置的完整底稿(templates/clash-default.yaml)
// 上注入 proxies,其余(rule-providers / proxy-groups / rules / dns)原样带出。
// 底稿里的规则源上百个、共几十万条,由客户端自己下载更新,订阅正文只是骨架。
//
// REALITY 目前只有 mihomo 支持。meta 为 false 时 reality-opts 仍会写出(结构
// 与 meta 版一致),但原版 Clash 不认这个字段,要用 REALITY 就必须走 mihomo。
package render

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

//go:embed templates/clash-default.yaml
var clashDefaultTemplate []byte

// Clash 渲染 Clash / Clash.Meta 的订阅 YAML。meta 为 true 时按 mihomo 的字段渲染,
// 并套用一份完整规则底稿;为 false 时只输出最小可用集合。
//
// templateYAML 是底稿(通常是控制面里的全局模板);为 nil 或空时 meta 版用内置
// 默认,meta=false 用最小集合。遇到本项目不支持的协议、传输或安全层直接报错,
// 不静默丢节点:订阅是用户唯一的连接入口,少一条比明确失败更难排查。
func Clash(eps []Endpoint, meta bool, templateYAML []byte) ([]byte, error) {
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

	if meta {
		return clashMeta(proxies, templateYAML)
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

// clashMeta 在完整底稿上注入 proxies 段,产出 meta 版订阅。
//
// 用 yaml.Node 而不是 map 往返:map 会把键排序并丢掉文件头的注释,而这份底稿是
// 手工维护的,注释和键序(rule-providers → proxy-groups → rules → dns)就是它
// 的文档,值得原样保留。proxies 插在 rule-providers 之后,因为 proxy-groups 里的
// include-all-proxies 依赖 proxies 先出现。
func clashMeta(proxies []clashProxy, templateYAML []byte) ([]byte, error) {
	base := templateYAML
	if len(bytes.TrimSpace(base)) == 0 {
		base = clashDefaultTemplate
	}

	var root yaml.Node
	if err := yaml.Unmarshal(base, &root); err != nil {
		return nil, fmt.Errorf("render: 解析 Clash 模板: %w", err)
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("render: Clash 模板顶层不是映射")
	}

	// 把 proxies 序列化成节点,插到 rule-providers 这一对键值之后。
	proxiesBytes, err := yaml.Marshal(proxies)
	if err != nil {
		return nil, fmt.Errorf("render: 序列化 proxies: %w", err)
	}
	var proxiesNode yaml.Node
	if err := yaml.Unmarshal(proxiesBytes, &proxiesNode); err != nil {
		return nil, fmt.Errorf("render: 解析 proxies: %w", err)
	}

	insertAt := -1
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "rule-providers" {
			insertAt = i + 2 // key+value 之后
			break
		}
	}
	if insertAt < 0 {
		return nil, fmt.Errorf("render: Clash 模板缺 rule-providers 段")
	}

	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "proxies"}
	val := proxiesNode.Content[0]
	doc.Content = append(doc.Content, nil, nil)
	copy(doc.Content[insertAt+2:], doc.Content[insertAt:])
	doc.Content[insertAt] = key
	doc.Content[insertAt+1] = val

	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("render: 序列化 Clash 模板: %w", err)
	}
	return out, nil
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
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Server   string `yaml:"server"`
	Port     int    `yaml:"port"`
	UUID     string `yaml:"uuid,omitempty"`
	Password string `yaml:"password,omitempty"`
	AlterID  *int   `yaml:"alterId,omitempty"`
	Cipher   string `yaml:"cipher,omitempty"`
	// wireguard 不写 network:它是 L3,这个字段只对代理类节点有意义。
	Network           string                `yaml:"network,omitempty"`
	TLS               bool                  `yaml:"tls,omitempty"`
	UDP               bool                  `yaml:"udp"`
	ServerName        string                `yaml:"servername,omitempty"`
	ALPN              []string              `yaml:"alpn,omitempty"`
	Flow              string                `yaml:"flow,omitempty"`
	ClientFingerprint string                `yaml:"client-fingerprint,omitempty"`
	SkipCertVerify    bool                  `yaml:"skip-cert-verify,omitempty"`
	WSOpts            *clashWSOpts          `yaml:"ws-opts,omitempty"`
	GRPCOpts          *clashGRPCOpts        `yaml:"grpc-opts,omitempty"`
	XHTTPOpts         *clashXHTTPOpts       `yaml:"xhttp-opts,omitempty"`
	HTTPUpgradeOpts   *clashHTTPUpgradeOpts `yaml:"httpupgrade-opts,omitempty"`
	RealityOpts       *clashRealityOpts     `yaml:"reality-opts,omitempty"`

	// ss-2022 没有 uuid/flow;wireguard 没有 server/port/网络层。
	PrivateKey string `yaml:"private-key,omitempty"` // wireguard / ss-2022
	PublicKey  string `yaml:"public-key,omitempty"`  // wireguard
	IP         string `yaml:"ip,omitempty"`          // wireguard 隧道地址
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

// clashXHTTPOpts 是 xhttp 的传输参数,只有 mihomo 认。
type clashXHTTPOpts struct {
	Path string `yaml:"path,omitempty"`
	Host string `yaml:"host,omitempty"`
	Mode string `yaml:"mode,omitempty"`
}

// clashHTTPOpts 是 httpupgrade 的传输参数,只有 mihomo 认。
type clashHTTPUpgradeOpts struct {
	Path string `yaml:"path,omitempty"`
	Host string `yaml:"host,omitempty"`
}

// clashRealityOpts 是 REALITY 参数,只有 mihomo 认。
type clashRealityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id,omitempty"`
}

// clashNode 把一个端点转成 Clash 节点。
func clashNode(ep Endpoint, meta bool) (clashProxy, error) {
	switch ep.Protocol {
	case model.ProtocolVLESS, model.ProtocolVMess, model.ProtocolTrojan,
		model.ProtocolShadowsocks, model.ProtocolWireguard:
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
		Name:   ep.Remark,
		Server: ep.Address,
		Port:   ep.Port,
		UDP:    true,
	}

	switch ep.Protocol {
	case model.ProtocolVLESS:
		p.Type = "vless"
		p.UUID = ep.UUID
		p.Flow = ep.Flow
		p.Network = string(transport)
	case model.ProtocolVMess:
		p.Type = "vmess"
		p.UUID = ep.UUID
		alterID := 0
		p.AlterID = &alterID
		p.Cipher = "auto"
		p.Network = string(transport)
	case model.ProtocolTrojan:
		p.Type = "trojan"
		p.Password = ep.Password
		p.Network = string(transport)
	case model.ProtocolShadowsocks:
		p.Type = "ss"
		p.Cipher = "2022-blake3-aes-128-gcm"
		p.Password = ep.Password
		p.Network = string(transport)
	case model.ProtocolWireguard:
		p.Type = "wireguard"
		// xray 侧的密钥是无填充 RawURL base64,mihomo 只认带填充的标准 base64。
		p.PrivateKey = paddedStdB64(ep.WGPrivateKey)
		p.PublicKey = paddedStdB64(ep.WGPublicKey)
		p.IP = ep.WGAddress
		// wireguard 是 L3,mihomo 的 network 字段对它无意义,不写。
	}

	// wireguard 是 L3,不走 TLS 层、不走路径/Host 传输参数。
	if ep.Protocol != model.ProtocolWireguard {
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
		case model.NetworkXHTTP:
			opts := &clashXHTTPOpts{Path: ep.Path, Host: ep.Host}
			if ep.Mode != "" && ep.Mode != "auto" {
				opts.Mode = ep.Mode
			}
			p.XHTTPOpts = opts
		case model.NetworkHTTPUpgrade:
			p.HTTPUpgradeOpts = &clashHTTPUpgradeOpts{Path: ep.Path, Host: ep.Host}
			// splithttp 在 mihomo 里没有对应类型,xray 客户端侧也不建议再用——
			// 订阅里遇到 splithttp 端点就在 clash/sing-box 端跳过,而不是给错类型。
		}
	}

	if sec == model.SecurityReality && ep.Protocol != model.ProtocolWireguard {
		opts := &clashRealityOpts{PublicKey: ep.RealityPub}
		if ep.RealitySID != "" {
			opts.ShortID = ep.RealitySID
		}
		p.RealityOpts = opts
	}

	return p, nil
}
