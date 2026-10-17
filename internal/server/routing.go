package server

import (
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// routingPlan 收齐渲染某个用户订阅需要的分流信息:用户层规则在前、全局在后,
// 去掉停用的;兜底先看用户覆盖,再看全局;策略组成员按这个用户实际拿到的端点算。
func (s *Server) routingPlan(user *model.User, eps []render.Endpoint) (render.RoutingPlan, error) {
	var p render.RoutingPlan
	groups, err := s.store.ListProxyGroups()
	if err != nil {
		return p, err
	}
	for _, scope := range []int64{user.ID, 0} {
		rules, err := s.store.ListRules(scope)
		if err != nil {
			return p, err
		}
		for _, r := range rules {
			if r.Enabled {
				p.Rules = append(p.Rules, r)
			}
		}
		if p.Final == "" {
			sc, err := s.store.GetRouteScope(scope)
			if err != nil {
				return p, err
			}
			p.Final = sc.Final
		}
		if scope == 0 {
			break
		}
	}
	if p.Final == "" {
		p.Final = model.TargetDirect
		if len(groups) > 0 {
			p.Final = model.GroupTarget(groups[0].ID)
		}
	}
	cands, err := s.groupCandidates(eps, groups)
	if err != nil {
		return p, err
	}
	for _, g := range groups {
		rg := render.RoutingGroup{ProxyGroup: g}
		for i, ep := range eps {
			if g.Admits(cands[i]) {
				rg.Members = append(rg.Members, ep)
			}
		}
		p.Groups = append(p.Groups, rg)
	}
	return p, nil
}

// groupCandidates 给每个端点算出策略组条件要看的属性:地区来自节点/入口元数据
// (入口上非空的字段覆盖节点),协议用订阅名后缀同一套短名,能力取已稳定的结果。
// v0.7.0 起 metadata 不再有标签来源,所以候选的 Tags 恒为空。
func (s *Server) groupCandidates(eps []render.Endpoint, groups []model.ProxyGroup) ([]model.GroupCandidate, error) {
	nodeMeta, err := s.store.ListLocationMetadata("nodes")
	if err != nil {
		return nil, err
	}
	hostMeta, err := s.store.ListLocationMetadata("hosts")
	if err != nil {
		return nil, err
	}
	caps := map[[2]int64]map[string]bool{}
	needCaps := false
	for _, g := range groups {
		if len(g.Capabilities) > 0 {
			needCaps = true
		}
	}
	if needCaps {
		nodes, err := s.store.ListNodes()
		if err != nil {
			return nil, err
		}
		projected, err := s.capabilityResultsForNodes(nodes, time.Now())
		if err != nil {
			return nil, err
		}
		for _, results := range projected {
			for _, r := range results {
				if r.Status != model.CapabilityAvailable {
					continue
				}
				k := [2]int64{r.InboundID, r.HostID}
				if caps[k] == nil {
					caps[k] = map[string]bool{}
				}
				caps[k][r.Target] = true
			}
		}
	}
	out := make([]model.GroupCandidate, len(eps))
	for i, ep := range eps {
		m := model.EffectiveLocationMetadata(nodeMeta[ep.NodeID], hostMeta[ep.HostID])
		out[i] = model.GroupCandidate{Region: m.Region, Protocol: render.ProtocolShort(ep), Capabilities: caps[[2]int64{ep.InboundID, ep.HostID}]}
	}
	return out, nil
}
