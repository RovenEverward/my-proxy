package model

import "github.com/RovenEverward/my-proxy/internal/netutil"

// SubMirror 是一个对外提供订阅的端点:托管在某台节点上、监听一个公开端口,
// 把到达的请求转发给控制面的 /sub。它本身不持真相,也不校验 token —— 订阅
// 内容以控制面为唯一来源。
//
// 节点端只是一个 TCP 转发(xray 的 dokodemo-door / sing-box 的 direct),
// 没有 MPX 组件;「能 SSH 的节点」与「订阅入口」分开,一台节点可以同时是
// 代理承载者与订阅镜像。
type SubMirror struct {
	ID     int64 `json:"id"`
	NodeID int64 `json:"node_id"`
	// Port 是节点对外监听的订阅端口。与入站端口同一套约束:1-65535,
	// 与同节点的入站和 API 端口区分开。
	Port int `json:"port"`
	// Upstream 是被转发的目标,host:port。一般是控制面地址;控制面迁移后
	// 改这里就能让镜像跟随新控制面,而不需要重装节点。
	Upstream string `json:"upstream"`
	// Remark 是可选的展示名(比如「东京镜像」),不填就按节点名拼。
	Remark string `json:"remark"`
}

// EndpointURL 给出这个镜像对外的订阅地址前缀(不带 /sub/...)。
func (m SubMirror) EndpointURL(nodeAddress string) string {
	if nodeAddress == "" {
		return ""
	}
	return "http://" + netutil.JoinHostPort(nodeAddress, m.Port)
}
