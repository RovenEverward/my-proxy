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
	ConfigPath  string `json:"config_path"`  // 空表示 /etc/mpx/xray/config.json
	ServiceName string `json:"service_name"` // 空表示 mpx-xray

	// DeployedHash 是控制面最后一次推上去的 config.json 的 sha256,空串表示
	// 从没下发过。它是漂移判定的基准:节点上读回来的哈希和它不一样,就说明有人
	// 在机器上直接改过配置。
	//
	// 存在节点记录上而不是"一次检查"里,因为它描述的是我们这边做过什么,不会
	// 因为某次连不上节点而改变。
	DeployedHash string `json:"deployed_hash,omitempty"`
	// DeployedAt 是那次下发发生的时刻;没推过时为 nil。
	DeployedAt *time.Time `json:"deployed_at,omitempty"`

	// Draining 表示这台节点正在排水:订阅渲染不再把它的入口发给用户,让已连
	// 的用户随重连自然离开。它只是订阅层的开关,不摘 xray 上已有的用户 ——
	// 排水要快摘掉订阅里的入口,但不该把正在用的人当场断掉;真要把人摘掉由
	// 收敛或手动同步负责,那是有各自节奏的事。
	Draining bool `json:"draining"`

	// OutboundIP 是节点出站(直连)的 IP 版本偏好。
	//
	// "" 等同 "v4v6";取值见 ValidOutboundIP。它只影响节点上 xray 的 direct
	// 出站(settings.domainStrategy),不碰订阅渲染。
	OutboundIP string `json:"outbound_ip,omitempty"`
}

// ValidOutboundIP 报告取值是不是一个已知的出站 IP 偏好。
//
// "" 等同 "v4v6":留空表示用默认的 UseIPv4v6,不写进库也不影响判断。
func ValidOutboundIP(s string) bool {
	switch s {
	case "", "v4v6", "v4", "v6", "asis":
		return true
	}
	return false
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

	// HTTP 系传输(xhttp / splithttp / httpupgrade)的路径与 Host。
	//
	// 与 WSPath / GRPCService 分开存而不是复用一个 path 字段:同一条入站改传输
	// 方式时,各自的配置互不污染,改回去不用重填;也和 Host 的覆盖模型一致
	// (入站有自己的默认 path,Host 再覆盖)。
	XHTTPPath string `json:"xhttp_path,omitempty"`
	XHTTPHost string `json:"xhttp_host,omitempty"`
	// XHTTPMode 只对 xhttp 有效:auto / packet-up / stream-up / stream-one。
	// 空表示 auto,不写进配置。splithttp / httpupgrade 没有这个维度。
	XHTTPMode string `json:"xhttp_mode,omitempty"`

	// WireGuard 服务端私钥。生成逻辑与 RealityPriv 一致(控制面生成、改入站时
	// 原样留住),理由也一样:调用方单独给一个公钥只会给出一对配不上的密钥。
	// 不下发到客户端,客户端拿的是从它派生的公钥。
	WGSecretKey string `json:"-"`
	// WGAddress 是服务端在隧道里的地址(带前缀),如 10.0.0.1/24。
	WGAddress string `json:"wg_address,omitempty"`
	// WGMTU 缺省 1420(wireguard 经验值:1500 减去外层 IP/UDP 头还有富余)。
	WGMTU int `json:"wg_mtu,omitempty"`

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

	CongestionControl string `json:"congestion_control,omitempty"` // TUIC: cubic/new_reno/bbr;空为 bbr

	Flow string `json:"flow,omitempty"` // VLESS flow,如 xtls-rprx-vision

	// Mux / Fragment 是客户端侧特性,写进订阅而不写进节点配置:
	// 是否启用由客户端决定,节点端不需要知道。
	//
	// Mux 只在 xray / sing-box 客户端生效(mihomo 不认这个字段)。
	Mux bool `json:"mux,omitempty"`
	// Fragment 只在 xray 客户端生效:分片在 sockopt 层,Clash 与 sing-box
	// 都没有对应字段。
	Fragment bool `json:"fragment,omitempty"`

	// MaxUsers 是这个入站允许容纳的用户数上限,0 表示不限制。
	//
	// 只在订阅渲染层生效:满了的入站不进订阅,用户自然落到别的入站上;满了
	// 之后才被授权进来的用户由收敛负责摘掉。它不是硬性隔离 —— 已经在节点上
	// 的用户不会因为别人订阅里少了它而突然断线,断不断由收敛那一侧决定。
	MaxUsers int `json:"max_users,omitempty"`
}

// Host 是一个入站对外暴露的一种方式。
//
// 一个入站默认自动带一个 Host(沿用节点地址与入站端口);需要同一入站经多个
// 地址(例如 CDN 域名 + 直连 IP)暴露时,再加 Host。留空的字段一律回落到
// 入站或节点上的值。
type Host struct {
	ID        int64 `json:"id"`
	InboundID int64 `json:"inbound_id"`
	// Remark 是客户端看到的节点名。可用变量:{SERVER_IP}、{NODE_NAME}、
	// {NODE_DISPLAY}(节点显示名,没有时同 {NODE_NAME})、{PROTOCOL}、{TRANSPORT}。
	// 留空时用节点显示名(再退回节点名),同一节点多个入口时自动加「 · 协议短名」。{USERNAME} 在这里被抹成空串 —— 节点名会被截图
	// 和转发,不该带上账号名;要按用户区分名字用订阅名那个字段。
	Remark  string   `json:"remark"`
	Address string   `json:"address"` // 空表示用 Node.Address
	Port    int      `json:"port"`    // 0 表示用 Inbound.Port
	SNI     string   `json:"sni,omitempty"`
	Host    string   `json:"host,omitempty"` // ws/grpc 的 Host 头
	Path    string   `json:"path,omitempty"`
	ALPN    []string `json:"alpn,omitempty"`
}

// Exposure 是 Host 合并节点与入站之后,真正会发布到订阅里的那一组值。
//
// 和 Host 分开是因为这两个问题的答案不一样:「我填了什么」和「最终发出去是什么」。
// Host 里留空的项在 Exposure 里一定已经回落成实际值。
type Exposure struct {
	Address string   `json:"address"`
	Port    int      `json:"port"`
	SNI     string   `json:"sni,omitempty"`
	Host    string   `json:"host,omitempty"` // ws/grpc 的 Host 头
	Path    string   `json:"path,omitempty"`
	ALPN    []string `json:"alpn,omitempty"`
}

// HostView 是 Host 对外的返回形态:填写过的字段原样返回(空就是空),另外附上
// 生效值。合并规则在 render 里,不在这一层。
type HostView struct {
	Host
	Effective Exposure `json:"effective"`
	// SubName 是这个入口在订阅里的名字(全部入站都能用的用户看到的那个,含重名
	// 时加的协议后缀与序号);AutoName 表示没起名、跟随节点显示名。只读、只增,
	// 只在列表里给。
	SubName  string `json:"sub_name,omitempty"`
	AutoName bool   `json:"auto_name,omitempty"`
}

// InboundView 是入站列表的返回形态:入站本身,另附没有对外入口时订阅里用的名字
// (SubName / AutoName 同 HostView)。只增字段,入站自己的 JSON 不变。
type InboundView struct {
	Inbound
	SubName  string `json:"sub_name,omitempty"`
	AutoName bool   `json:"auto_name,omitempty"`
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

// UserStatuses 按 User.Status 的判定优先级列出全部状态。
//
// 判定逻辑只有 Status 方法那一份,这里只列取值 —— 校验查询参数、拼错误提示、
// 写测试都从这一处取,新加状态时不会漏掉某个列表。
var UserStatuses = []Status{StatusDisabled, StatusExpired, StatusLimited, StatusActive}

// Valid 报告取值是不是一个已知状态。空串不算:它在这里表示"不限",由调用方处理。
func (s Status) Valid() bool {
	for _, v := range UserStatuses {
		if s == v {
			return true
		}
	}
	return false
}

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

	// InboundIDs 限定该用户可用的入站;为空表示全部入站(没挂套餐时)。
	// 挂了套餐时,它是套餐之外的「额外授权」,与套餐取并集。
	InboundIDs []int64 `json:"inbound_ids,omitempty"`

	// PlanID 是用户挂的套餐 id;nil 表示没挂套餐,完全沿用上面的旧语义。
	// 用指针是为了在整体 PUT 里区分「没提这个字段」和「摘掉套餐」。
	PlanID *int64 `json:"plan_id,omitempty"`

	// planAllNodes / planInbounds 是挂套餐时解析出来的授权,只在内存里,不序列化。
	// 判定入口只有 AllowsInbound 一处,别的地方不再各自解释套餐。
	//
	// planResolved 表示这份授权是否已经按套餐解析过,只有 SetPlanAuth 会置位。
	// 从 JSON 直接解码出来的用户即使带着 plan_id,这份授权也是空的;不把
	// 「已解析」与「plan_id 非空」分开,这种对象会被误判成什么入站都不能用。
	planResolved bool
	planAllNodes bool
	planInbounds map[int64]bool

	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SetPlanAuth 设置解析后的套餐授权(仅内存,不落库),并把这份授权标记为已解析。
// allNodes 为真表示全部入站,此时 inboundIDs 被忽略。存储层读用户时按套餐表解析
// 后调用它;没挂套餐的用户也要调用(传 allNodes=false、inboundIDs=nil),这样
// 「已解析」与「有没有套餐」是两件独立的事实。
func (u *User) SetPlanAuth(allNodes bool, inboundIDs []int64) {
	u.planResolved = true
	u.planAllNodes = allNodes
	if len(inboundIDs) == 0 {
		u.planInbounds = nil
		return
	}
	m := make(map[int64]bool, len(inboundIDs))
	for _, id := range inboundIDs {
		m[id] = true
	}
	u.planInbounds = m
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
//
// 判定只有这一份,订阅、xray 下发、收敛、能力检测、用户筛选都走这里。
//
//   - 套餐授权没解析(从没调用过 SetPlanAuth,例如从 JSON 解码出来的用户):
//     按旧语义判定,InboundIDs 为空表示全部入站 —— 不能因为带着 plan_id 却
//     没有解析结果就判成什么都不能用。
//   - 已解析且没挂套餐(PlanID 为空):同样按旧语义,InboundIDs 为空表示全部。
//   - 已解析且挂了套餐:all_nodes 套餐就是全部;否则可用范围 = 套餐入站 ∪ 用户
//     自己的 InboundIDs(额外授权)。套餐为空且没有额外授权时什么都不能用 ——
//     空套餐不等于全开,这是有意的(见 docs/internal/plans-rfc.md)。
func (u *User) AllowsInbound(inboundID int64) bool {
	if u.planResolved && u.PlanID != nil {
		if u.planAllNodes || u.planInbounds[inboundID] {
			return true
		}
	} else if len(u.InboundIDs) == 0 {
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

	// WebhookURL 是通知的投递地址,POST 一个 JSON 事件过去;为空表示不通知。
	WebhookURL string `json:"webhook_url"`
	// NotifyDaysLeft 是用户到期前多少天开始提醒(用户侧通知)。
	NotifyDaysLeft int `json:"notify_days_left"`
	// NotifyUsagePercent 是用户用量达到多少百分比开始提醒(用户侧通知)。
	NotifyUsagePercent int `json:"notify_usage_percent"`

	Rules []SubscriptionRule `json:"rules"`

	// Templates 是各格式的客户端模板,键是 ClientFormat 的字符串
	// (clash-meta / sing-box / xray)。缺键或空串表示用内置默认模板。
	Templates map[string]string `json:"templates"`
}

// 用户侧通知的默认阈值,写入设置里的缺省值。0 有特殊含义(见 DaysLeft 与
// UsagePercent),所以默认值用正数,和"关掉某项通知"的写法分开。
const (
	DefaultNotifyDaysLeft     = 3
	DefaultNotifyUsagePercent = 80
)

// DaysLeft 返回用户到期的剩余天数;不过期返回 0 与 false。
func (u *User) DaysLeft(now time.Time) (int, bool) {
	if u.ExpireAt == nil {
		return 0, false
	}
	days := int(u.ExpireAt.Sub(now).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return days, true
}

// UsagePercent 返回本周期已用量的百分比;不限量返回 0。
func (u *User) UsagePercent() int {
	if u.DataLimit <= 0 {
		return 0
	}
	pct := int(u.UsedTraffic * 100 / u.DataLimit)
	if pct > 100 {
		pct = 100
	}
	return pct
}

// TemplateFor 返回该格式的模板文本;没存过或存了空串时返回空串,
// 调用方据此回落内置默认模板。
func (s *Settings) TemplateFor(format ClientFormat) string {
	if s == nil || s.Templates == nil {
		return ""
	}
	return s.Templates[string(format)]
}

// BuiltinRules 是开箱即用的 UA 识别规则,按顺序匹配。
//
// 它的存在是「订阅链接只有一条、客户端各有各的格式」这件事的另一半:光有
// 三大格式的模板,没有规则把请求认到对应模板上,Clash 客户端拿到的还是纯
// 分享链接。所以规则表不是可选配置,而是默认体验的一部分 —— 用户把订阅
// 地址填进客户端就该拿到对的配置,不该先来读文档写正则。
//
// 顺序即优先级,短的/具体的放前面:clash 系要先认 mihomo 再认 clash,否则
// 「clash-verge/v1.7.0 mihomo/v1.18.0」这种同时报多个名字的 UA 会被宽的那条
// 先接住,拿到原版 Clash 的最小集合而不是完整的 meta 底稿。
//
// 返回新的切片,调用方改动不会波及其他调用者。
func BuiltinRules() []SubscriptionRule {
	return []SubscriptionRule{
		// Karing 会同时声称自己是好几种客户端,排在最前,免得更宽的那条抢走它。
		{Pattern: `(?i)karing`, Result: FormatClashMeta},
		// mihomo 系(Clash Verge Rev / Mihomo Party / Stash / FlClash)。
		{Pattern: `(?i)mihomo|clash-verge|clash\.meta|clashmeta|flclash|stash`, Result: FormatClashMeta},
		// 原版 Clash 家族排在 meta 之后:它们的 UA 里通常只有 "clash"。
		{Pattern: `(?i)clash`, Result: FormatClash},
		{Pattern: `(?i)sing-box|sfi/|sfa/|sfm/|hiddify|nekobox`, Result: FormatSingBox},
		{Pattern: `(?i)xray`, Result: FormatXray},
		// 这几个都只认 base64 链接列表,给不了 YAML/JSON。
		{Pattern: `(?i)v2rayn|v2rayng`, Result: FormatBase64},
		{Pattern: `(?i)shadowrocket`, Result: FormatBase64},
		{Pattern: `(?i)quantumult`, Result: FormatBase64},
		{Pattern: `(?i)surge|loon`, Result: FormatBase64},
	}
}

// RuleList 返回生效的规则表。没配过规则(空表)时回落内置默认。
//
// 空表算「没配过」而不是「用户特意关掉了分流」:后者可以自己写一条
// `(?i).*` → links 的规则来表达,而前者是绝大多数人的真实状态 —— 他们
// 装完控制面就直接把订阅填进客户端了。老库升级上来时 rules 列是 "[]",
// 走的也是这条路,不然修好的默认规则对新装的库生效、对已有的库不生效,
// 同一版本两种行为。
func (s *Settings) RuleList() []SubscriptionRule {
	if s == nil || len(s.Rules) == 0 {
		return BuiltinRules()
	}
	return s.Rules
}

// MatchFormat 返回第一条匹配该 User-Agent 的规则结果与是否命中;没有命中时
// 返回 FormatLinks 与 false。
//
// 第二个返回值区分「规则真的命中了」和「谁都没匹配上、落的默认值」:同一个
// FormatLinks,前者是用户在规则里点名要的,后者只是没人认领。订阅入口靠它决定
// 浏览器要不要拿订阅页 —— 只有走到「没人认领的默认」这一步,页面才顶上来。
//
// 规则只有个位数,每次请求重新编译的开销可以忽略,因此不缓存编译结果。
// 写入口负责拒绝编译不过的正则;这里遇到坏正则跳过而不是报错,免得一条坏
// 规则让整个订阅不可用。
func (s *Settings) MatchFormat(userAgent string) (ClientFormat, bool) {
	for _, r := range s.RuleList() {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			continue
		}
		if re.MatchString(userAgent) {
			return r.Result, true
		}
	}
	return FormatLinks, false
}

// NodeStatus 是一台节点「刚才被问过之后」的样子。
//
// 它是对一次提问的回答,所以字段都是观察值而不是期望值:里面没有我们希望节点
// 是什么样,只有它现在是什么样。观察失败也是这个结构的一部分 —— 连不上时
// Reachable 为 false、Error 写着原因,而不是"没有状态"。丢了那一次观察,使用者
// 就分不清"节点上配置被改了"和"这台机器根本没理我"。
//
// 与之相对,「我们最后一次推上去的是什么」属于节点自己的属性,存在 nodes 表上
// (DeployedHash),不在这里 —— 否则每次检查都会把它覆盖成观察值,漂移就永远
// 看不出来了。
type NodeStatus struct {
	NodeID int64 `json:"node_id"`
	// CheckedAt 是这次观察发生的时刻。
	CheckedAt time.Time `json:"checked_at"`
	// Reachable 是 ssh 有没有连上。false 时下面的观察值都不可信。
	Reachable bool `json:"reachable"`
	// Active 是节点上跑 xray 的那个 systemd 服务在不在跑。
	Active bool `json:"active"`
	// Version 是节点上核心的版本号,只留版本那一段(如 "26.3.27")。
	Version string `json:"version"`
	// ObservedHash 是节点上那份 config.json 的 sha256。拿它和 nodes 表里的
	// DeployedHash 比,就能看出有人是否在机器上直接改过配置。
	ObservedHash string       `json:"observed_hash"`
	Cores        []CoreStatus `json:"cores,omitempty"`
	// Certificates 是这台节点上所有用证书的入站的证书到期情况,一个入站一条。
	Certificates []CertificateInfo `json:"certificates"`
	// Error 是观察失败的原因(ssh 连不上、超时)。为空表示这次问到了。
	Error string `json:"error,omitempty"`
}

// CertificateInfo 是一个入站用的证书的到期情况。
//
// 一个入站一条(而不是一个证书文件一条):使用者关心的是"哪个入站要到期了",
// 多个入站共用一张证书时按入站分行,反而更容易看清要动哪一处。
type CertificateInfo struct {
	InboundID   int64  `json:"inbound_id"`
	InboundName string `json:"inbound_name"`
	// Path 是证书在节点上的路径。
	Path string `json:"path"`
	// NotAfter 是证书的到期时间;读不到时为 nil,原因在 Error 里。
	NotAfter *time.Time `json:"not_after"`
	// DaysLeft 是距到期还剩多少天(向下取整,已过期是负数)。NotAfter 为空时
	// 这个值没有意义,看 Error。
	DaysLeft int `json:"days_left"`
	// Error 是这张证书没读成的原因:文件不在、打不开、不是 PEM。留空表示读到了。
	Error string `json:"error,omitempty"`
}

// NodeView 是节点对外的返回形态:节点记录本身,外加最近一次观察到的状态。
//
// 与 HostView 同样的理由:服务端与 CLI 看的是同一份形状。Status 为 nil 表示
// 这台节点还从没被检查过(刚加进来、巡检还没轮到),不是"检查失败"—— 检查
// 失败是一次有结果的观察,Reachable=false 会写在 Status 里。
type NodeView struct {
	Node
	Status       *NodeStatus        `json:"status"`
	Capabilities []CapabilityResult `json:"capabilities"`
	// Display 是只读的显示名(旗帜 + 城市/国家 + 字母),由节点元数据拼出;没有地区时为空。
	Display string `json:"display,omitempty"`
}

// 证书临期的两个阈值。doctor 与巡检告警共用,免得两处各写一个数字。
const (
	// CertWarnDays 以内要提醒:该准备换了。
	CertWarnDays = 14
	// CertFailDays 以内是故障:随时可能中断。
	CertFailDays = 3
)

// Kind 是告警的种类,一个 Kind 对应一类可恢复的状况。
type Kind string

const (
	KindNodeUnreachable Kind = "node_unreachable"
	KindNodeInactive    Kind = "node_inactive"
	KindNodeDrift       Kind = "node_drift"
	KindCertExpiring    Kind = "cert_expiring"
)

// 用户侧通知的种类,用于 user_notices 表记"哪一类提醒已经发过"。
const (
	NoticeKindExpiring  = "expiring"   // 快到期
	NoticeKindOverUsage = "over_usage" // 快超限
)

// UserNotice 是"这一条用户通知已经发过了"的记录。
//
// 它存在的意义是让用户侧提醒精确地只发一次:巡检每轮都会看一遍用户,如果只看
// 窗口(比如"三天内到期")而不记账,一轮漏跑或窗口横跨多轮就会重复发。记下
// (user, kind) 之后,同一用户同一类提醒只在条件消失(续费/重置)后才会再发。
type UserNotice struct {
	UserID int64     `json:"user_id"`
	Kind   string    `json:"kind"`
	SentAt time.Time `json:"sent_at"`
}

// Alert 是一条告警。
//
// Key 是这条告警的身份:同一个 Key 在冷却期内只报一次,条件消失时用同一个 Key
// 去 Resolve。所以 Key 必须由「哪台节点的哪一类问题」拼出来(NodeID + Kind,
// 证书再加路径),不能带上时间戳或剩余天数 —— 那样每次算出来都是新的 Key,
// 去重就失效了。
type Alert struct {
	Key      string    `json:"key"`
	Kind     Kind      `json:"kind"`
	NodeID   int64     `json:"node_id"`
	NodeName string    `json:"node_name"`
	Message  string    `json:"message"`
	At       time.Time `json:"at"`
}

// DoctorCheck 是自检的一项结论。
type DoctorCheck struct {
	// Name 是这一项的名字,给人看也用来定位(如 "节点 hk-1")。
	Name string `json:"name"`
	// Status 取 ok / warn / fail。
	Status string `json:"status"`
	// Detail 是结论的依据,带具体数字。
	Detail string `json:"detail"`
	// Hint 是"接下来该做什么",没有可做的就为空。
	Hint string `json:"hint,omitempty"`
	// UIHint 是同一个"接下来该做什么"的界面版:界面里该怎么做,只描述界面
	// 操作、不出现命令;为空时界面退回 Hint。
	UIHint string `json:"ui_hint,omitempty"`
}

// DoctorReport 是 GET /api/doctor 的回答,也是 mpx doctor 打印的东西。
//
// 与 ControlStatus / NodeView 同样的理由:服务端与 CLI 共用一份形状。
type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
}

// 自检结论的三个取值。
const (
	DoctorOK   = "ok"
	DoctorWarn = "warn"
	DoctorFail = "fail"
)

// HasFailure 报告这份报告里有没有 fail。CLI 拿它决定退出码:有一项 fail 就是
// 非零,脚本可以直接拿 doctor 当健康检查。
func (r DoctorReport) HasFailure() bool {
	for _, c := range r.Checks {
		if c.Status == DoctorFail {
			return true
		}
	}
	return false
}

// ControlStatus 是控制面自报的「现在怎么样」,GET /api/status 与 mpx status 共用。
//
// 与 HostView 同样的理由:服务端和 CLI 看的是同一份形状,分叉只会让两边各自
// 漂移。这些字段全在内存里,控制面一重启就从零再算 —— 库里的账不受影响。
type ControlStatus struct {
	// StartedAt 是控制面进程启动的时间。
	StartedAt time.Time `json:"started_at"`
	// UptimeSeconds 是到这次查询为止已运行的秒数。
	UptimeSeconds float64 `json:"uptime_seconds"`
	// IntervalSeconds 是后台任务的周期。
	IntervalSeconds float64 `json:"interval_seconds"`
	// Ticks 是已跑完的后台轮数。
	Ticks uint64 `json:"ticks"`
	// LastTickAt 是上一轮开始的时间;一次都没跑过时为 nil。
	LastTickAt *time.Time `json:"last_tick_at"`
	// LastTickSeconds 是上一轮花掉的秒数。
	LastTickSeconds float64 `json:"last_tick_seconds"`

	// Version 是这份控制面的版本。从源码直接编出来、又没打 tag 时为空。
	Version string `json:"version,omitempty"`
	// Update 是更新检查的结论。没查过、或者查不成时字段基本是空的。
	Update UpdateInfo `json:"update"`
	// TLSFingerprint 是控制面自签证书的公钥指纹(SHA256:...),CLI 按它认人。
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
	// Devices 是接入的设备数;DevicePasswordSet 表示设备密码接入是否开着。
	Devices           int  `json:"devices"`
	DevicePasswordSet bool `json:"device_password_set"`
	// ConnectLockedUntil 非空表示设备密码连续输错、接入锁定到这个时刻。
	ConnectLockedUntil *time.Time `json:"connect_locked_until,omitempty"`

	Counts   Counts        `json:"counts"`
	Backoffs []NodeBackoff `json:"backoffs"`
}

// UpdateInfo 是「有没有比现在更新的发布」这个问题的答案。
//
// 它只带消息,不带动作:升级永远是一次显式的人工决定。控制面换掉的正是自己,
// 自动换出一个起不来的二进制,连"查一下现在什么状态"这条路都断了。
//
// 单独定义一份而不是直接复用 updatecheck.Result,是为了让 API 的形状不跟着那个
// 包的内部实现走 —— 它多存了几个字段(比如未经截断的正文),没必要都发出去。
type UpdateInfo struct {
	// Current 是当前版本;Latest 是上游最新发布的版本。
	Current string `json:"current,omitempty"`
	Latest  string `json:"latest,omitempty"`
	// Behind 是落后了几次发布;0 表示已是最新。
	Behind int `json:"behind"`
	// URL 是最新发布的网页地址,去看具体改了什么。
	URL string `json:"url,omitempty"`
	// Notes 是最新发布正文的开头一段。
	Notes string `json:"notes,omitempty"`
	// PublishedAt 是最新发布的发布时间。
	PublishedAt *time.Time `json:"published_at,omitempty"`
	// CheckedAt 是这份结论是什么时候问出来的。
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	// Error 非空表示这次没问成,其余字段是上一次的结论。
	Error string `json:"error,omitempty"`
}

// Newer 报告确实有一份更新的发布。
//
// 当前版本或最新版本缺一个时一律返回 false:不知道自己在哪一版,就不能说别人
// 更新。这比猜一个要诚实,也免得从源码编出来的开发版天天提示"有新版本"。
func (u UpdateInfo) Newer() bool {
	return u.Latest != "" && u.Current != "" && u.Behind > 0
}

// Counts 是各类对象的数量,给状态页一眼看规模用。
type Counts struct {
	Nodes    int `json:"nodes"`
	Inbounds int `json:"inbounds"`
	Hosts    int `json:"hosts"`
	Users    int `json:"users"`
}

// NodeBackoff 是一台正处于失败退避中的节点。
type NodeBackoff struct {
	NodeID   int64  `json:"node_id"`
	NodeName string `json:"node_name"`
	// Failures 是连续失败次数,成功一次即清零。
	Failures int `json:"failures"`
	// SkipUntil 是这个窗口的结束时间,到了就重新试。
	SkipUntil time.Time `json:"skip_until"`
}
