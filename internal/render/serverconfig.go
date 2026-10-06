package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// defaultServerAPIPort 是节点没有配置 API 端口时使用的端口。
const defaultServerAPIPort = 10085

// serverConfig 是节点侧 xray config.json 的顶层结构。
//
// 用结构体而不是 map:JSON 键顺序固定,同一份输入每次输出字节一致,人也容易照着读。
type serverConfig struct {
	Log       serverLog        `json:"log"`
	API       serverAPI        `json:"api"`
	Stats     struct{}         `json:"stats"`
	Policy    serverPolicy     `json:"policy"`
	Inbounds  []serverInbound  `json:"inbounds"`
	Outbounds []serverOutbound `json:"outbounds"`
	Routing   serverRouting    `json:"routing"`
}

// serverLog 只保留 warning:足够定位问题,又不至于把节点磁盘写满。
type serverLog struct {
	LogLevel string `json:"loglevel"`
}

// serverAPI 打开 xray 的 gRPC 管理接口。
//
// 没有这一段,控制面就无法在运行时增删用户、也读不到 stats。
type serverAPI struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
}

// serverPolicy 让 xray 按用户、按入站累计流量。
type serverPolicy struct {
	Levels map[string]serverLevel `json:"levels"`
	System serverPolicySystem     `json:"system"`
}

// serverLevel 是某个 level 上的统计开关。
//
// 入站客户端不写 level 字段,一律落在默认的 0 层,所以只需配 0。
type serverLevel struct {
	StatsUserUplink   bool `json:"statsUserUplink"`
	StatsUserDownlink bool `json:"statsUserDownlink"`
}

type serverPolicySystem struct {
	StatsInboundUplink   bool `json:"statsInboundUplink"`
	StatsInboundDownlink bool `json:"statsInboundDownlink"`
}

// serverInbound 是一个 xray 入站。
//
// API 入站不写 streamSettings 与 sniffing,因此两者用指针加 omitempty。
type serverInbound struct {
	Tag            string                  `json:"tag"`
	Listen         string                  `json:"listen"`
	Port           int                     `json:"port"`
	Protocol       string                  `json:"protocol"`
	Settings       any                     `json:"settings"`
	StreamSettings *serverStreamSettings   `json:"streamSettings,omitempty"`
	Sniffing       *serverSniffingSettings `json:"sniffing,omitempty"`
}

// serverAPISettings 是 API 入站的 dokodemo-door 配置:把流量转给本机 xray 自身。
type serverAPISettings struct {
	Address string `json:"address"`
}

// serverStreamSettings 是传输层配置。
type serverStreamSettings struct {
	Network         string                 `json:"network"`
	Security        string                 `json:"security"`
	TCPSettings     *serverEmptySettings   `json:"tcpSettings,omitempty"`
	WSSettings      *serverWSSettings      `json:"wsSettings,omitempty"`
	GRPCSettings    *serverGRPCSettings    `json:"grpcSettings,omitempty"`
	TLSSettings     *serverTLSSettings     `json:"tlsSettings,omitempty"`
	RealitySettings *serverRealitySettings `json:"realitySettings,omitempty"`
}

// serverEmptySettings 渲染成 {}:tcp 传输没有可选参数,但键必须在。
type serverEmptySettings struct{}

type serverWSSettings struct {
	Path string `json:"path"`
}

type serverGRPCSettings struct {
	ServiceName string `json:"serviceName"`
}

type serverTLSSettings struct {
	ServerName   string              `json:"serverName"`
	Certificates []serverCertificate `json:"certificates"`
	ALPN         []string            `json:"alpn,omitempty"`
}

type serverCertificate struct {
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

// serverRealitySettings 是 REALITY 服务端参数。
//
// privateKey 只在节点侧出现,绝不能下发到客户端(客户端要的是 publicKey)。
type serverRealitySettings struct {
	Show        bool     `json:"show"`
	Dest        string   `json:"dest"`
	Xver        int      `json:"xver"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIDs    []string `json:"shortIds"`
}

// serverSniffingSettings 让 xray 从流量里嗅探出域名,供路由按域名分流。
type serverSniffingSettings struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
	RouteOnly    bool     `json:"routeOnly"`
}

type serverVLESSClient struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Flow  string `json:"flow,omitempty"`
}

type serverVLESSettings struct {
	Clients    []serverVLESSClient `json:"clients"`
	Decryption string              `json:"decryption"`
}

type serverVMessClient struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	AlterID int    `json:"alterId"`
}

type serverVMessSettings struct {
	Clients []serverVMessClient `json:"clients"`
}

type serverTrojanClient struct {
	Password string `json:"password"`
	Email    string `json:"email"`
}

type serverTrojanSettings struct {
	Clients []serverTrojanClient `json:"clients"`
}

type serverOutbound struct {
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
}

type serverRouting struct {
	Rules []serverRoutingRule `json:"rules"`
}

// serverRoutingRule 只保留两条必需规则:API 流量回到 api 出站,内网地址黑洞丢弃。
type serverRoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	IP          []string `json:"ip,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

// ServerConfig 渲染某台节点上 xray 的 config.json。
//
// 只有 now 时刻状态为 active 且被该入站授权的用户才会被写进配置;过期、超限、
// 停用的用户既不在配置里,也不能通过 xray 认证。
func ServerConfig(node model.Node, inbounds []model.Inbound, users []model.User, now time.Time) ([]byte, error) {
	apiPort := node.APIPort
	if apiPort == 0 {
		apiPort = defaultServerAPIPort
	}

	cfg := serverConfig{
		Log:   serverLog{LogLevel: "warning"},
		API:   serverAPI{Tag: "api", Services: []string{"HandlerService", "StatsService"}},
		Stats: struct{}{},
		Policy: serverPolicy{
			Levels: map[string]serverLevel{"0": {StatsUserUplink: true, StatsUserDownlink: true}},
			System: serverPolicySystem{StatsInboundUplink: true, StatsInboundDownlink: true},
		},
		// 第一个入站固定是 API:dokodemo-door 把请求转给 xray 自身,控制面经 SSH
		// 隧道调用它来增删用户、读流量。它只监听回环,不对外暴露。
		Inbounds: []serverInbound{{
			Tag:      "api",
			Listen:   "127.0.0.1",
			Port:     apiPort,
			Protocol: "dokodemo-door",
			Settings: serverAPISettings{Address: "127.0.0.1"},
		}},
		Outbounds: []serverOutbound{
			{Protocol: "freedom", Tag: "direct"},
			{Protocol: "blackhole", Tag: "block"},
		},
		Routing: serverRouting{Rules: []serverRoutingRule{
			{Type: "field", InboundTag: []string{"api"}, OutboundTag: "api"},
			{Type: "field", IP: []string{"geoip:private"}, OutboundTag: "block"},
		}},
	}

	for _, in := range inbounds {
		rendered, err := serverRenderInbound(in, users, now)
		if err != nil {
			return nil, err
		}
		cfg.Inbounds = append(cfg.Inbounds, rendered)
	}

	return json.MarshalIndent(cfg, "", "  ")
}

// InboundAddUserConfig 渲染「只含一个入站、且该入站只含一个用户」的配置片段。
//
// 这是喂给 xray 自带的 `xray api adu` 的输入:那条命令收一份完整的 xray 配置
// JSON,从里面解出入站再把用户逐个加上去。复用 ServerConfig 的入站渲染是有意的
// ——另写一份 account 的拼装逻辑,两份必然漂移。
func InboundAddUserConfig(in model.Inbound, u model.User, now time.Time) ([]byte, error) {
	rendered, err := serverRenderInbound(in, []model.User{u}, now)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"inbounds": []serverInbound{rendered}})
}

// serverRenderInbound 把一个入站渲染成 xray 的入站对象。
//
// 协议 / 传输 / 安全层超出本项目支持范围时报错,而不是悄悄产出一份 xray 起不来的
// 配置:节点上配置写坏会导致整台服务器断流。
func serverRenderInbound(in model.Inbound, users []model.User, now time.Time) (serverInbound, error) {
	tag := in.Tag
	if tag == "" {
		tag = fmt.Sprintf("%s-%d", in.Protocol, in.Port)
	}
	if !in.Protocol.Valid() {
		return serverInbound{}, fmt.Errorf("入站 %q 的协议 %q 不受支持", tag, in.Protocol)
	}
	// 传输与安全层留空时按 xray 的默认值处理(tcp / none),不因此报错。
	network := in.Network
	if network == "" {
		network = model.NetworkTCP
	}
	if !network.Valid() {
		return serverInbound{}, fmt.Errorf("入站 %q 的传输 %q 不受支持", tag, in.Network)
	}
	security := in.Security
	if security == "" {
		security = model.SecurityNone
	}
	if !security.Valid() {
		return serverInbound{}, fmt.Errorf("入站 %q 的安全层 %q 不受支持", tag, in.Security)
	}

	allowed := serverInboundUsers(users, in.ID, now)

	stream := &serverStreamSettings{Network: string(network), Security: string(security)}
	switch network {
	case model.NetworkTCP:
		stream.TCPSettings = &serverEmptySettings{}
	case model.NetworkWS:
		path := in.WSPath
		if path == "" {
			path = "/"
		}
		stream.WSSettings = &serverWSSettings{Path: path}
	case model.NetworkGRPC:
		stream.GRPCSettings = &serverGRPCSettings{ServiceName: in.GRPCService}
	}

	switch security {
	case model.SecurityTLS:
		stream.TLSSettings = &serverTLSSettings{
			ServerName: in.SNI,
			Certificates: []serverCertificate{{
				CertificateFile: in.CertFile,
				KeyFile:         in.KeyFile,
			}},
			ALPN: in.ALPN,
		}
	case model.SecurityReality:
		// serverNames / shortIds 为空时写成 [],不写 null:xray 只接受数组。
		serverNames := []string{}
		if in.SNI != "" {
			serverNames = append(serverNames, in.SNI)
		}
		shortIDs := in.ShortIDs
		if shortIDs == nil {
			shortIDs = []string{}
		}
		stream.RealitySettings = &serverRealitySettings{
			Show:        false,
			Dest:        in.RealityDest,
			Xver:        0,
			ServerNames: serverNames,
			PrivateKey:  in.RealityPriv,
			ShortIDs:    shortIDs,
		}
	}

	settings, err := serverInboundSettings(in.Protocol, in.Flow, allowed)
	if err != nil {
		return serverInbound{}, fmt.Errorf("入站 %q: %w", tag, err)
	}

	listen := in.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}

	return serverInbound{
		Tag:            tag,
		Listen:         listen,
		Port:           in.Port,
		Protocol:       string(in.Protocol),
		Settings:       settings,
		StreamSettings: stream,
		Sniffing: &serverSniffingSettings{
			Enabled:      true,
			DestOverride: []string{"http", "tls", "quic"},
			RouteOnly:    true,
		},
	}, nil
}

// serverInboundSettings 按协议生成入站 settings。
//
// email 一律用用户名而不是用户 ID:同一个用户在所有入站上的 email 相同,xray 的
// per-user 流量才能按用户聚合,而不是按「用户 × 入站」拆成好几行。
func serverInboundSettings(protocol model.Protocol, flow string, users []*model.User) (any, error) {
	switch protocol {
	case model.ProtocolVLESS:
		clients := make([]serverVLESSClient, 0, len(users))
		for _, u := range users {
			clients = append(clients, serverVLESSClient{
				ID:    model.DeriveUUID(u.Key),
				Email: u.Username,
				Flow:  flow,
			})
		}
		return serverVLESSettings{Clients: clients, Decryption: "none"}, nil

	case model.ProtocolVMess:
		clients := make([]serverVMessClient, 0, len(users))
		for _, u := range users {
			clients = append(clients, serverVMessClient{
				ID:      model.DeriveUUID(u.Key),
				Email:   u.Username,
				AlterID: 0,
			})
		}
		return serverVMessSettings{Clients: clients}, nil

	case model.ProtocolTrojan:
		clients := make([]serverTrojanClient, 0, len(users))
		for _, u := range users {
			clients = append(clients, serverTrojanClient{
				Password: model.DerivePassword(u.Key),
				Email:    u.Username,
			})
		}
		return serverTrojanSettings{Clients: clients}, nil
	}
	return nil, fmt.Errorf("协议 %q 不受支持", protocol)
}

// serverInboundUsers 筛出 now 时刻可用、且被该入站授权的用户,并按用户名排序。
//
// 排序不只是为了好看:同一份输入必须产出同一份配置,否则每次下发都算一次变更,
// 「配置有没有变」也就无从判断。
func serverInboundUsers(users []model.User, inboundID int64, now time.Time) []*model.User {
	out := make([]*model.User, 0, len(users))
	for i := range users {
		u := &users[i]
		if u.Status(now) != model.StatusActive {
			continue
		}
		if !u.AllowsInbound(inboundID) {
			continue
		}
		out = append(out, u)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}
