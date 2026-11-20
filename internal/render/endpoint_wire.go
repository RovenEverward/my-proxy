package render

import (
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// EndpointConfig 是 render.Endpoint 的传输型(UC-20):给带宽测量这类
// 「端点配置下发到本机执行端」的通道用的 snake_case JSON 形状。
//
// 为什么不直接给 Endpoint 加 json tag:Endpoint 默认编码写进
// source_snapshots 存量数据,一旦加 tag 新二进制就读不回老快照。传输型
// 独立出来,两边互不影响。
//
// 字段按 Endpoint 一对一镜像;新增 Endpoint 字段时这里同步加。
type EndpointConfig struct {
	NodeID    int64  `json:"node_id,omitempty"`
	InboundID int64  `json:"inbound_id,omitempty"`
	HostID    int64  `json:"host_id,omitempty"`
	SourceID  int64  `json:"source_id,omitempty"`
	SourceKey string `json:"source_key,omitempty"`

	Remark            string `json:"remark,omitempty"`
	Address           string `json:"address"`
	Port              int    `json:"port"`
	Protocol          string `json:"protocol"`
	UUID              string `json:"uuid,omitempty"`
	Password          string `json:"password,omitempty"`
	CongestionControl string `json:"congestion_control,omitempty"`
	Flow              string `json:"flow,omitempty"`
	SSMethod          string `json:"ss_method,omitempty"`
	VMessSecurity     string `json:"vmess_security,omitempty"`
	VMessAlterID      int    `json:"vmess_alter_id,omitempty"`

	WGPrivateKey string `json:"wg_private_key,omitempty"`
	WGPublicKey  string `json:"wg_public_key,omitempty"`
	WGAddress    string `json:"wg_address,omitempty"`

	Network string `json:"network,omitempty"`
	Path    string `json:"path,omitempty"`
	Host    string `json:"host,omitempty"`
	Mode    string `json:"mode,omitempty"`

	Security      string   `json:"security,omitempty"`
	SNI           string   `json:"sni,omitempty"`
	ALPN          []string `json:"alpn,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	RealityPub    string   `json:"reality_pub,omitempty"`
	RealitySID    string   `json:"reality_sid,omitempty"`
	AllowInsecure bool     `json:"allow_insecure,omitempty"`
}

// EndpointConfig 把一个端点转成传输形状。
func WireEndpoint(ep Endpoint) EndpointConfig {
	return EndpointConfig{
		NodeID: ep.NodeID, InboundID: ep.InboundID, HostID: ep.HostID,
		SourceID: ep.SourceID, SourceKey: ep.SourceKey,
		Remark: ep.Remark, Address: ep.Address, Port: ep.Port,
		Protocol: string(ep.Protocol), UUID: ep.UUID, Password: ep.Password,
		CongestionControl: ep.CongestionControl, Flow: ep.Flow,
		SSMethod: ep.SSMethod, VMessSecurity: ep.VMessSecurity, VMessAlterID: ep.VMessAlterID,
		WGPrivateKey: ep.WGPrivateKey, WGPublicKey: ep.WGPublicKey, WGAddress: ep.WGAddress,
		Network: string(ep.Network), Path: ep.Path, Host: ep.Host, Mode: ep.Mode,
		Security: string(ep.Security), SNI: ep.SNI, ALPN: ep.ALPN,
		Fingerprint: ep.Fingerprint, RealityPub: ep.RealityPub, RealitySID: ep.RealitySID,
		AllowInsecure: ep.AllowInsecure,
	}
}

// Endpoint 把传输形状还原成 render.Endpoint。协议/传输/安全层枚举
// 按字符串复原,渲染侧自己会做合法性校验。
func (c EndpointConfig) Endpoint() Endpoint {
	return Endpoint{
		NodeID: c.NodeID, InboundID: c.InboundID, HostID: c.HostID,
		SourceID: c.SourceID, SourceKey: c.SourceKey,
		Remark: c.Remark, Address: c.Address, Port: c.Port,
		Protocol: model.Protocol(c.Protocol), UUID: c.UUID, Password: c.Password,
		CongestionControl: c.CongestionControl, Flow: c.Flow,
		SSMethod: c.SSMethod, VMessSecurity: c.VMessSecurity, VMessAlterID: c.VMessAlterID,
		WGPrivateKey: c.WGPrivateKey, WGPublicKey: c.WGPublicKey, WGAddress: c.WGAddress,
		Network: model.Network(c.Network), Path: c.Path, Host: c.Host, Mode: c.Mode,
		Security: model.Security(c.Security), SNI: c.SNI, ALPN: c.ALPN,
		Fingerprint: c.Fingerprint, RealityPub: c.RealityPub, RealitySID: c.RealitySID,
		AllowInsecure: c.AllowInsecure,
	}
}

// ALPNCSV 是 ALPN 的逗号拼接形式,界面与 CLI 显示用。
func ALPNCSV(alpn []string) string { return strings.Join(alpn, ",") }
