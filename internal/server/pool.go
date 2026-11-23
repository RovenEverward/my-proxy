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
	// Excluded 为真时这个端点被结构性剔出订阅(目前只有排水),但仍留在
	// 池里 —— 剔除(UC-30 后只剩排水)是「订阅视图过滤」不是「池里删行」,预览与后续的池接
	// 口要看到它。
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
// 各步骤业务规则不变 —— 命名走 ResolveNamed / resolveExternalRemark;
// 这张函数只做归一化,不发明新规则。
func (s *Server) poolEndpointsFor(user *model.User) (poolSet, error) {
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
	return s.poolFor(user, sources)
}

// poolFor 是池视图的核心:调用方给「这次视角可见哪些来源」,函数负责
// 两类端点的归一化与排水标记。操作者视图给全量来源,用户视图给授权来源。
func (s *Server) poolFor(user *model.User, sources []model.SubscriptionSource) (poolSet, error) {
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
			pe := PoolEndpoint{Endpoint: ep, Origin: "external:" + src.Name, Index: i + 1}
			set.Pool = append(set.Pool, pe)
		}
	}
	return set, nil
}

// subscriptionEndpointsFor is the sole merge boundary. It never fetches upstream.
// 它把清洗池视图收敛成本次订阅要渲染的端点集:排水标记 Excluded 的行,
// 外部端点按输出格式过兼容(托管端点由 render 兜底,沿用不查的旧约定),
// 其余原样进渲染。
func (s *Server) subscriptionEndpointsFor(user *model.User, format model.ClientFormat) (endpointSet, error) {
	pool, err := s.poolEndpointsFor(user)
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
	return set, nil
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
// 自建全量入站 + 全部来源的外部端点,带排水标记与原因。
func (s *Server) handlePool(w http.ResponseWriter, r *http.Request) {
	sources, err := s.store.ListSources()
	if err != nil {
		s.writeStoreError(w, err, "来源")
		return
	}
	// 操作者视角用一个"全员可见"的合成用户:Enabled、不限入站。
	viewer := &model.User{Username: "operator", Key: "operator", Enabled: true}
	pool, err := s.poolFor(viewer, sources)
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
