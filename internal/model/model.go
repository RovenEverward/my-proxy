// Package model 定义控制面的领域对象。
//
// 这里只有数据与纯判断,不碰数据库、网络与渲染;持久化和渲染各自依赖本包。
package model

import (
	"regexp"
	"time"
)

// Protocol 是入站协议。
type Protocol string

const (
	ProtocolVLESS  Protocol = "vless"
	ProtocolVMess  Protocol = "vmess"
	ProtocolTrojan Protocol = "trojan"
)

func (p Protocol) Valid() bool {
	switch p {
	case ProtocolVLESS, ProtocolVMess, ProtocolTrojan:
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
)

func (n Network) Valid() bool {
	switch n {
	case NetworkTCP, NetworkWS, NetworkGRPC:
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

// Node 是一台跑 xray 的服务器。
//
// 控制面通过 SSH 下发配置、启停服务,并通过 SSH 隧道访问该节点上 xray 的 API 端口。
type Node struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"` // 对外地址(IP 或域名),Host 未覆盖时用它

	SSHPort int    `json:"ssh_port"` // 0 表示 22
	SSHUser string `json:"ssh_user"` // 空表示 root

	APIPort     int    `json:"api_port"`     // xray API 入站端口,仅监听回环
	ConfigPath  string `json:"config_path"`  // 空表示 /usr/local/etc/xray/config.json
	ServiceName string `json:"service_name"` // 空表示 xray
}

// Inbound 是某个节点上 xray 的一个入站。
type Inbound struct {
	ID       int64    `json:"id"`
	NodeID   int64    `json:"node_id"`
	Tag      string   `json:"tag"`
	Protocol Protocol `json:"protocol"`
	Listen   string   `json:"listen"` // 空表示 0.0.0.0
	Port     int      `json:"port"`
	Network  Network  `json:"network"`
	Security Security `json:"security"`

	// 传输参数
	WSPath      string `json:"ws_path,omitempty"`
	GRPCService string `json:"grpc_service,omitempty"`

	// TLS / REALITY
	SNI         string   `json:"sni,omitempty"`
	ALPN        []string `json:"alpn,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"` // 客户端 uTLS 指纹,默认 chrome
	CertFile    string   `json:"cert_file,omitempty"`   // TLS:节点上的证书路径
	KeyFile     string   `json:"key_file,omitempty"`
	RealityDest string   `json:"reality_dest,omitempty"` // REALITY 回落目标,如 www.microsoft.com:443
	RealityPriv string   `json:"-"`                      // REALITY 私钥,不下发到客户端
	RealityPub  string   `json:"reality_pub,omitempty"`
	ShortIDs    []string `json:"short_ids,omitempty"`

	Flow string `json:"flow,omitempty"` // VLESS flow,如 xtls-rprx-vision
}

// Host 是一个入站对外暴露的一种方式。
//
// 一个入站默认自动带一个 Host(沿用节点地址与入站端口);需要同一入站经多个
// 地址(例如 CDN 域名 + 直连 IP)暴露时,再加 Host。留空的字段一律回落到
// 入站或节点上的值。
type Host struct {
	ID        int64    `json:"id"`
	InboundID int64    `json:"inbound_id"`
	Remark    string   `json:"remark"`  // 客户端看到的节点名,支持 {USERNAME} 等变量
	Address   string   `json:"address"` // 空表示用 Node.Address
	Port      int      `json:"port"`    // 0 表示用 Inbound.Port
	SNI       string   `json:"sni,omitempty"`
	Host      string   `json:"host,omitempty"` // ws/grpc 的 Host 头
	Path      string   `json:"path,omitempty"`
	ALPN      []string `json:"alpn,omitempty"`
}

// ResetStrategy 是流量周期重置策略。
type ResetStrategy string

const (
	ResetNone    ResetStrategy = "none"
	ResetDaily   ResetStrategy = "daily"
	ResetWeekly  ResetStrategy = "weekly"
	ResetMonthly ResetStrategy = "monthly"
)

// Status 是用户对外的可用状态。
type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusExpired  Status = "expired"
	StatusLimited  Status = "limited" // 流量已超限
)

// User 是一个订阅主体。
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	// Key 是派生 UUID / 密码的种子,不直接下发。
	Key string `json:"key,omitempty"`
	// SubToken 是订阅链接里那段不可猜的路径。与 Key 分开是为了让订阅链接泄
	// 露后可以单独换掉,而不用连带换掉用户在各个协议上的凭证。
	SubToken string `json:"sub_token,omitempty"`

	Enabled bool `json:"enabled"`

	DataLimit int64 `json:"data_limit"` // 字节;0 表示不限
	// UsedTraffic 是本周期内已用的字节数;周期重置会把它归零。
	UsedTraffic int64 `json:"used_traffic"`
	// LifetimeUsedTraffic 是从创建至今的累计用量,周期重置不动它。
	LifetimeUsedTraffic int64      `json:"lifetime_used_traffic"`
	ExpireAt            *time.Time `json:"expire_at,omitempty"` // nil 表示不过期

	ResetStrategy ResetStrategy `json:"reset_strategy"`
	// TrafficResetAt 是上一次周期重置的时刻,也是周期的起算点;nil 表示从未
	// 重置过(以 CreatedAt 起算)。只存这一个事实,下次重置时间由它现算。
	TrafficResetAt *time.Time `json:"traffic_reset_at,omitempty"`

	// InboundIDs 限定该用户可用的入站;为空表示全部入站。
	InboundIDs []int64 `json:"inbound_ids,omitempty"`

	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Status 按固定优先级判定用户状态:显式停用 > 过期 > 超限 > 可用。
func (u *User) Status(now time.Time) Status {
	switch {
	case !u.Enabled:
		return StatusDisabled
	case u.ExpireAt != nil && now.After(*u.ExpireAt):
		return StatusExpired
	case u.DataLimit > 0 && u.UsedTraffic >= u.DataLimit:
		return StatusLimited
	default:
		return StatusActive
	}
}

// AllowsInbound 报告该用户是否被授权使用某个入站。
func (u *User) AllowsInbound(inboundID int64) bool {
	if len(u.InboundIDs) == 0 {
		return true
	}
	for _, id := range u.InboundIDs {
		if id == inboundID {
			return true
		}
	}
	return false
}

// SubscriptionRule 把 User-Agent 正则映射到一种订阅输出格式。
type SubscriptionRule struct {
	Pattern string       `json:"pattern"`
	Result  ClientFormat `json:"result"`
}

// Settings 是控制面的全局设置。
type Settings struct {
	// SubURLPrefix 是订阅链接前缀,CLI 用它拼出完整订阅地址。
	SubURLPrefix string `json:"sub_url_prefix"`
	// ProfileTitle 是客户端里显示的订阅名,支持 {USERNAME} 等变量。
	ProfileTitle string `json:"profile_title"`
	SupportLink  string `json:"support_link"`
	// UpdateInterval 是客户端自动更新间隔,单位小时。
	UpdateInterval int `json:"update_interval"`

	Rules []SubscriptionRule `json:"rules"`
}

// MatchFormat 返回第一条匹配该 User-Agent 的规则结果;没有命中时用 FormatLinks。
//
// 规则只有个位数,每次请求重新编译的开销可以忽略,因此不缓存编译结果。
// 写入口负责拒绝编译不过的正则;这里遇到坏正则跳过而不是报错,免得一条坏
// 规则让整个订阅不可用。
func (s *Settings) MatchFormat(userAgent string) ClientFormat {
	for _, r := range s.Rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			continue
		}
		if re.MatchString(userAgent) {
			return r.Result
		}
	}
	return FormatLinks
}
