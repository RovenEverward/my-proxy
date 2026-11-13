package model

import "time"

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
	// DeployedTemplateHash 是那次下发时「配置模板」的哈希:同一份节点与入站、不带
	// 任何用户渲染出来的配置的 sha256。DeployedHash 里含着用户名单,用户增删都会
	// 让它变,所以它比不出「新版本换了写法」;这一份只随模板与结构变化。
	// 空串表示还没在装着这一版的控制面上下发过。
	DeployedTemplateHash string `json:"deployed_template_hash,omitempty"`
	// TemplateDrift 表示按现在这版控制面的写法渲染,得到的模板哈希与下发时记下的
	// 不一样:新版本改了节点配置的样子,应用一次才生效。它和「有改动未应用」
	// (节点上的文件与下发的不一致)是两回事,只在读节点时现算,不落库。
	TemplateDrift bool `json:"template_drift,omitempty"`

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
