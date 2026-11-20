package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/source"
)

// ---- 清洗池(UC-19) ----

// PoolEndpoint 是清洗池里的一行:一个已经过完命名/质量/活性管线的候选端点。
// 两类来源在池里同构 —— Endpoint 里是连接参数与展示名,Excluded/ExcludeReason
// 是活性判定的落点,Origin 记它从哪条原始管道进来。订阅装配只面对这一张
// 视图,不再在装配函数里拼「自建列表 + 外部列表」两段。
type PoolEndpoint struct {
	render.Endpoint
	// Origin 是来源身份:managed(自建节点)或 external:<source_name>(外部端点)。
	Origin string `json:"origin"`
	// Excluded 为真时这个端点被活性判定剔出订阅,但仍留在池里 —— 剔除是
	// 「订阅视图过滤」不是「池里删行」,预览与后续的池接口要看到它。
	Excluded      bool   `json:"excluded"`
	ExcludeReason string `json:"exclude_reason,omitempty"`
	// Index 是端点在其原始来源里的 1-based 序号(外部=快照条目号,
	// 托管=0):兼容性问题指回原始条目用,和解析期 Issue.Index 同一约定。
	Index int `json:"index,omitempty"`
}

// poolSet 是一次池视图读取的结果:行 + 读原始快照时产生的问题。
type poolSet struct {
	Pool   []PoolEndpoint
	Issues []source.Issue
}

// poolEndpointsFor 是清洗池视图:两类原始端点流经统一管线后的归一化落点。
// 各步骤业务规则不变 —— 命名走 ResolveNamed / resolveExternalRemark,
// 活性剔除走同一份 exclusionState;这张函数只做归一化,不发明新规则。
func (s *Server) poolEndpointsFor(user *model.User, excl exclusionState) (poolSet, error) {
	if status := user.Status(time.Now()); status != model.StatusActive {
		return poolSet{
			Pool:   []PoolEndpoint{{Endpoint: placeholderEndpoint(user, status), Origin: "managed"}},
			Issues: []source.Issue{},
		}, nil
	}
	sources, err := s.store.ListSourcesForUser(user.ID)
	if err != nil {
		return poolSet{}, err
	}
	return s.poolFor(user, excl, sources)
}

// poolFor 是池视图的核心:调用方给「这次视角可见哪些来源」,函数负责
// 两类端点的归一化与剔除标记。操作者视图给全量来源,用户视图给授权来源。
func (s *Server) poolFor(user *model.User, excl exclusionState, sources []model.SubscriptionSource) (poolSet, error) {
	managed, err := s.resolveManagedEndpoints(user)
	if err != nil {
		return poolSet{}, err
	}
	set := poolSet{
		Pool:   make([]PoolEndpoint, 0, len(managed)+16),
		Issues: []source.Issue{},
	}
	for _, rm := range managed {
		pe := PoolEndpoint{Endpoint: rm.Endpoint, Origin: "managed"}
		switch {
		case rm.Draining:
			pe.Excluded, pe.ExcludeReason = true, "节点排水中"
		case managedExcluded(rm, excl):
			pe.Excluded, pe.ExcludeReason = true, managedExcludeReason(rm, excl)
		}
		set.Pool = append(set.Pool, pe)
	}
	extMeta, err := s.store.ListExternalMetadata(0)
	if err != nil {
		return set, err
	}
	for _, src := range sources {
		var eps []render.Endpoint
		if len(src.Snapshot) == 0 {
			continue
		}
		if err := json.Unmarshal(src.Snapshot, &eps); err != nil {
			set.Issues = append(set.Issues, source.Issue{Name: src.Name, Reason: "来源快照无法读取，请刷新该来源"})
			continue
		}
		for i, ep := range eps {
			ep.SourceID, ep.SourceRegion = src.ID, src.Region
			ep.Remark = resolveExternalRemark(src.Name, extMeta, ep)
			ep.AutoRemark = false
			key := string(ep.Key())
			pe := PoolEndpoint{Endpoint: ep, Origin: "external:" + src.Name, Index: i + 1}
			if excl.on && excl.excluded(key) {
				pe.Excluded, pe.ExcludeReason = true, excl.excludeReason(key)
			}
			set.Pool = append(set.Pool, pe)
		}
	}
	return set, nil
}

// subscriptionEndpointsFor is the sole merge boundary. It never fetches upstream.
// 它把清洗池视图收敛成本次订阅要渲染的端点集:剔除标记 Excluded 的行,
// 外部端点按输出格式过兼容(托管端点由 render 兜底,沿用不查的旧约定),
// 其余原样进渲染。
func (s *Server) subscriptionEndpointsFor(user *model.User, format model.ClientFormat) (endpointSet, error) {
	excl, err := s.exclusion()
	if err != nil {
		return endpointSet{}, err
	}
	pool, err := s.poolEndpointsFor(user, excl)
	if err != nil {
		return endpointSet{}, err
	}
	set := endpointSet{
		Endpoints: make([]render.Endpoint, 0, len(pool.Pool)),
		Issues:    pool.Issues,
	}
	for _, pe := range pool.Pool {
		if pe.Excluded {
			continue
		}
		ep := pe.Endpoint
		if ep.SourceID > 0 {
			if err := render.Compatible(ep, format); err != nil {
				set.Issues = append(set.Issues, source.Issue{Index: pe.Index, Name: ep.Remark, Reason: err.Error()})
				continue
			}
		}
		set.Endpoints = append(set.Endpoints, ep)
	}
	set.Endpoints = render.DedupeRemarks(set.Endpoints)

	// UC-20:把实测带宽写进订阅节点名 —— 客户端列表直接能看到速度。
	// 只在有 ok 样本的对象上标;读一次表,渲染层不碰 store。
	if bw, err := s.store.LatestBandwidthViews(); err == nil && len(bw) > 0 {
		for i := range set.Endpoints {
			if v := bandwidthOf(set.Endpoints[i], bw); v != "" {
				set.Endpoints[i].Remark += " · " + v
			}
		}
	}
	return set, nil
}

// bandwidthOf 给一个端点挑展示用的带宽标签:入口(host)实测过用入口的,
// 节点实测过用节点的,外部端点按 s: 身份查。都只认最近一次 ok 的存档值。
func bandwidthOf(ep render.Endpoint, bw map[string]model.BandwidthView) string {
	keys := []string{}
	if ep.SourceID > 0 {
		keys = append(keys, string(ep.Key()))
	} else {
		if ep.HostID > 0 {
			keys = append(keys, string(model.HostSubjectKey(ep.HostID)))
		}
		keys = append(keys, string(model.NodeSubjectKey(ep.NodeID)))
	}
	for _, k := range keys {
		v := bw[k]
		if v.Status == "ok" && v.BytesPerSec > 0 {
			return model.FormatBps(v.BytesPerSec)
		}
	}
	return ""
}

// ---- 池可视化 ----

// poolRow 是 /api/pool 的一行:清洗池里一个端点的可读视图。凭证字段
// (UUID/密码/UUID 衍生物)不落进 API,池是可见性视图不是配置导出。
type poolRow struct {
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	Origin        string `json:"origin"`
	SourceID      int64  `json:"source_id"`
	Region        string `json:"region,omitempty"`
	Excluded      bool   `json:"excluded"`
	ExcludeReason string `json:"exclude_reason,omitempty"`
}

// handlePool 回答「现在清洗池里有什么」:操作者视角的全量视图 —
// 自建全量入站 + 全部来源的外部端点,带剔除标记与原因。
func (s *Server) handlePool(w http.ResponseWriter, r *http.Request) {
	excl, err := s.exclusion()
	if err != nil {
		s.writeStoreError(w, err, "剔除判定")
		return
	}
	sources, err := s.store.ListSources()
	if err != nil {
		s.writeStoreError(w, err, "来源")
		return
	}
	// 操作者视角用一个"全员可见"的合成用户:Enabled、不限入站。
	viewer := &model.User{Username: "operator", Key: "operator", Enabled: true}
	pool, err := s.poolFor(viewer, excl, sources)
	if err != nil {
		s.writeStoreError(w, err, "节点池")
		return
	}
	rows := make([]poolRow, 0, len(pool.Pool))
	included := 0
	for _, pe := range pool.Pool {
		if !pe.Excluded {
			included++
		}
		rows = append(rows, poolRow{
			Name: pe.Remark, Protocol: render.ProtocolShort(pe.Endpoint),
			Address: pe.Address, Port: pe.Port, Origin: pe.Origin,
			SourceID: pe.SourceID, Region: pe.SourceRegion,
			Excluded: pe.Excluded, ExcludeReason: pe.ExcludeReason,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":     rows,
		"total":    len(rows),
		"included": included,
		"excluded": len(rows) - included,
		"issues":   pool.Issues,
	})
}

// managedExcluded 是托管端点剔除的端点级判定(ISS-15):
//   - 有独立对外地址的入口(Host.Address≠节点 IP,CDN 类)不随节点
//     「blocked」连坐 —— GFW 封的是节点 IP,CDN 边缘还能回源;它只随
//     自己的 host:<id> 判定和节点「down」(回源真死)剔除。
//   - 其余入口(默认入口、Host 与节点同址)仍随节点判定,与旧版一致。
func managedExcluded(rm resolvedManaged, excl exclusionState) bool {
	if !excl.on {
		return false
	}
	if rm.HostID > 0 && rm.Address != "" && rm.Address != rm.NodeAddress {
		if excl.exts[string(model.HostSubjectKey(rm.HostID))] {
			return true
		}
		return excl.verdicts[string(model.NodeSubjectKey(rm.NodeID))] == "down"
	}
	return excl.nodes[rm.NodeID]
}

// managedExcludeReason 给出端点级剔除的说明文字,指明是谁判的。
func managedExcludeReason(rm resolvedManaged, excl exclusionState) string {
	if rm.HostID > 0 && rm.Address != "" && rm.Address != rm.NodeAddress {
		hk := string(model.HostSubjectKey(rm.HostID))
		if excl.exts[hk] {
			return excl.excludeReason(hk)
		}
		if excl.verdicts[string(model.NodeSubjectKey(rm.NodeID))] == "down" {
			return "自动剔除:节点不可用"
		}
	}
	return excl.excludeReason(string(model.NodeSubjectKey(rm.NodeID)))
}
