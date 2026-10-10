package model

import "time"

// Core 是入站使用的协议核心。旧入站仍由 Xray 承载。
type Core string

const (
	CoreXray          Core     = "xray"
	CoreSingBox       Core     = "sing-box"
	ProtocolHysteria2 Protocol = "hysteria2"
	ProtocolTUIC      Protocol = "tuic"
	NetworkUDP        Network  = "udp"
)

func (c Core) Valid() bool { return c == CoreXray || c == CoreSingBox }

func (p Protocol) Core() Core {
	if p == ProtocolHysteria2 || p == ProtocolTUIC {
		return CoreSingBox
	}
	return CoreXray
}

func (p Protocol) QUIC() bool { return p == ProtocolHysteria2 || p == ProtocolTUIC }

// CoreDeployment 独立保存第二核心的监听、配置与下发记录。
// Xray 使用 Node 原有字段,两核心操作不会共用路径或计量基准。
type CoreDeployment struct {
	NodeID       int64      `json:"node_id"`
	Core         Core       `json:"core"`
	APIPort      int        `json:"api_port"`
	ConfigPath   string     `json:"config_path"`
	ServiceName  string     `json:"service_name"`
	DeployedHash string     `json:"deployed_hash,omitempty"`
	DeployedAt   *time.Time `json:"deployed_at,omitempty"`
}

func (d CoreDeployment) APIPortOrDefault() int {
	if d.APIPort != 0 {
		return d.APIPort
	}
	return 10086
}
func (d CoreDeployment) ConfigPathOrDefault() string {
	if d.ConfigPath != "" {
		return d.ConfigPath
	}
	return "/etc/mpx/sing-box/config.json"
}
func (d CoreDeployment) ServiceNameOrDefault() string {
	if d.ServiceName != "" {
		return d.ServiceName
	}
	return "mpx-sing-box"
}

// InboundsForCore 按核心筛选,保留原有排序。
func InboundsForCore(inbounds []Inbound, core Core) []Inbound {
	out := make([]Inbound, 0, len(inbounds))
	for _, in := range inbounds {
		if in.Protocol.Core() == core {
			out = append(out, in)
		}
	}
	return out
}
