package server

import (
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// 订阅里的入口名,给界面看(只读,只增字段)。
//
// 界面以前在入口没起名时显示「(无名)」,可客户端里看到的其实是节点显示名
// (重名时加协议后缀与序号)。这里用和订阅完全相同的流水线 —— ResolveNamed →
// AddProtocolSuffix → DedupeRemarks —— 算一遍「能用全部入站的用户」会看到的
// 名字,界面直接显示它。只读、不进订阅缓存,订阅本身一个字节都不变。
//
// 与真实订阅的差别只在边角:挂了套餐只能用部分入站的用户,同一节点上可见的
// 入口变少,协议后缀可能省掉;排水中的节点与满员的入站这里照算(界面上它们还在)。

// subNameKey 认一个入口:入站 id + 对外入口 id(0 是入站自己的默认入口)。
type subNameKey struct{ inbound, host int64 }

type subName struct {
	name string
	auto bool // 没起名,跟随节点显示名
}

// subscriptionNames 算出每个入口在订阅里的名字。
func (s *Server) subscriptionNames() (map[subNameKey]subName, error) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, err
	}
	inbounds, err := s.store.ListInbounds(0)
	if err != nil {
		return nil, err
	}
	hosts, err := s.store.ListHosts(0)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.ListLocationMetadata("nodes")
	if err != nil {
		return nil, err
	}
	byNode := make(map[int64][]model.Inbound, len(nodes))
	for _, in := range inbounds {
		byNode[in.NodeID] = append(byNode[in.NodeID], in)
	}
	byInbound := make(map[int64][]model.Host, len(hosts))
	for _, h := range hosts {
		byInbound[h.InboundID] = append(byInbound[h.InboundID], h)
	}
	// 没挂套餐、没限定入站的用户能用全部入站;Key 只影响 UUID/密码,与名字无关。
	everyone := &model.User{Key: "display-names"}
	var eps []render.Endpoint
	for _, n := range nodes {
		eps = append(eps, render.ResolveNamed(n, model.NodeDisplay(meta[n.ID]), byNode[n.ID], byInbound, everyone)...)
	}
	auto := make([]bool, len(eps))
	for i, ep := range eps {
		auto[i] = ep.AutoRemark
	}
	eps = render.DedupeRemarks(render.AddProtocolSuffix(eps))
	out := make(map[subNameKey]subName, len(eps))
	for i, ep := range eps {
		out[subNameKey{ep.InboundID, ep.HostID}] = subName{name: ep.Remark, auto: auto[i]}
	}
	return out, nil
}
