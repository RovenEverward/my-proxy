package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/reach"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// reachRoutes 挂触达检查的接口(只增):
//
//	POST /api/reach/probe    控制面从自己库取目标清单,就地探一轮,返回目标与结果。
//	POST /api/reach/results  存入一次检查的合并结果(操作机发起检查后回报)。
//	GET  /api/reach/last     最近一次检查的完整内容,页面与 mpx node check --last 用。
//	GET  /api/reach          各节点最近一次存档的摘要(不带逐入站明细)。
//	GET  /api/reach/targets  UC-12:探测点/操作机拉目标清单,带周期与新鲜度。
//	PUT  /api/reach/keep     UC-12:按对象身份设置或解除「保留」豁免。
func (s *Server) reachRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/reach/probe", s.handleReachProbe)
	api.HandleFunc("POST /api/reach/results", s.handleReachResults)
	api.HandleFunc("GET /api/reach/last", s.handleReachLast)
	api.HandleFunc("GET /api/reach", s.handleReachList)
	api.HandleFunc("GET /api/reach/targets", s.handleReachTargets)
	api.HandleFunc("PUT /api/reach/keep", s.handleReachKeep)
}

// reachProbeTimeout 给控制面侧整轮探测的上限:单个探针 4s、32 并发,
// 节点多时按批往上抬,封顶两分钟免得把一次 HTTP 调用拖成长跑。
const reachProbeTimeout = 2 * time.Minute

// reachStaleWindow 是判定的新鲜度窗口:一个来源的结果超过 24 小时就不再参与
// 判定,对象两侧都过期时结论回到未知、不做剔除。
const reachStaleWindow = 24 * time.Hour

type reachProbeRequest struct {
	NodeIDs []int64  `json:"node_ids,omitempty"`
	ExtKeys []string `json:"ext_keys,omitempty"` // UC-12:外部端点身份(render.EndpointKey)
}

type reachProbeResponse struct {
	CheckedAt  time.Time                `json:"checked_at"`
	Targets    []reach.Target           `json:"targets"`
	Results    map[int64]*reach.Result  `json:"results"`
	ExtTargets []reach.Target           `json:"ext_targets,omitempty"`
	ExtResults map[string]*reach.Result `json:"ext_results,omitempty"`
}

// reachTargets 从库里摆出要探的目标清单:每台节点 × 它的每个入站,
// 地址用节点对外地址(Host 覆盖的入口不在这次检查范围内)。
// Label 用订阅里的名字,没有就退回 tag。UC-12 起每个目标带上对象与端点身份。
func (s *Server) reachTargets(nodeIDs []int64) ([]reach.Target, error) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range nodeIDs {
		want[id] = true
	}
	names, err := s.subscriptionNames()
	if err != nil {
		names = map[subNameKey]subName{} // 名字算不出不影响探测本身
	}
	var targets []reach.Target
	for _, n := range nodes {
		if len(want) > 0 && !want[n.ID] {
			continue
		}
		inbounds, err := s.store.ListInbounds(n.ID)
		if err != nil {
			return nil, err
		}
		for _, in := range inbounds {
			label := names[subNameKey{in.ID, 0}].name
			if label == "" {
				label = in.Tag
			}
			targets = append(targets, reach.Target{
				InboundID: in.ID, NodeID: n.ID, NodeName: n.Name, Label: label,
				Address: n.Address, Port: in.Port,
				Protocol: string(in.Protocol), Network: string(in.Network),
				Subject: string(model.NodeSubjectKey(n.ID)),
				TID:     "inbound:" + strconv.FormatInt(in.ID, 10),
			})
		}
	}
	return targets, nil
}

// externalEndpoints 是外部订阅的探测对象清单:全部启用来源的最新快照,
// 不管授权给不给谁 —— 一个端点死没死跟谁能看到它无关。
// 返回值里每个端点已填好 SourceID 与署名,Key() 即对象身份。
func (s *Server) externalEndpoints() ([]render.Endpoint, error) {
	sources, err := s.store.ListSources()
	if err != nil {
		return nil, err
	}
	out := []render.Endpoint{}
	for _, src := range sources {
		if !src.Enabled || len(src.Snapshot) == 0 {
			continue
		}
		var eps []render.Endpoint
		if err := json.Unmarshal(src.Snapshot, &eps); err != nil {
			continue
		}
		for i := range eps {
			eps[i].SourceID, eps[i].SourceRegion = src.ID, src.Region
			eps[i].Remark = src.Name + " · " + eps[i].Remark
			out = append(out, eps[i])
		}
	}
	return out, nil
}

// reachExtTargets 把外部订阅端点摆成探测目标:一个端点一个目标,身份是
// EndpointKey。keys 非空时按对象身份过滤。
func (s *Server) reachExtTargets(keys []string) ([]reach.Target, error) {
	eps, err := s.externalEndpoints()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var targets []reach.Target
	for _, ep := range eps {
		tid := string(ep.Key())
		if len(want) > 0 && !want[tid] {
			continue
		}
		targets = append(targets, reach.Target{
			NodeName: ep.Remark, Label: ep.Remark,
			Address: ep.Address, Port: ep.Port,
			Protocol: string(ep.Protocol), Network: string(ep.Network),
			Subject: tid, TID: tid,
		})
	}
	return targets, nil
}

func (s *Server) handleReachProbe(w http.ResponseWriter, r *http.Request) {
	var req reachProbeRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
			return
		}
	}
	targets, err := s.reachTargets(req.NodeIDs)
	if err != nil {
		s.writeStoreError(w, err, "节点或入站")
		return
	}
	ext, err := s.reachExtTargets(req.ExtKeys)
	if err != nil {
		s.writeStoreError(w, err, "外部端点")
		return
	}
	if len(targets) == 0 && len(ext) == 0 {
		writeError(w, http.StatusBadRequest, "范围内没有带入站的节点或外部端点")
		return
	}
	ctx := r.Context()
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > reachProbeTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, reachProbeTimeout)
		defer cancel()
	}
	resp := reachProbeResponse{
		CheckedAt: time.Now().UTC(),
		Targets:   targets,
		Results:   reach.Probe(ctx, targets, reach.Options{}),
	}
	if len(ext) > 0 {
		resp.ExtTargets = ext
		resp.ExtResults = reach.ProbeKeyed(ctx, ext, reach.Options{})
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- 目标清单(GET /api/reach/targets) ----

// reachTargetsResponse 是探测点与操作机拉到的调度输入:目标、周期、各对象
// 各侧的新鲜度。freshness 按对象身份键;到期规则是「该侧距今超过 interval」。
type reachTargetsResponse struct {
	FetchedAt time.Time `json:"fetched_at"`
	Interval  int       `json:"interval_minutes"`
	StaleSec  int64     `json:"stale_seconds"`
	// ExcludeEnabled 告诉探测端:剔除开着,境内侧欠账才值得清(UC-12)。
	ExcludeEnabled bool                       `json:"exclude_enabled"`
	Targets        []reach.Target             `json:"targets"`
	Freshness      map[string]reach.Freshness `json:"freshness"`
}

func (s *Server) handleReachTargets(w http.ResponseWriter, r *http.Request) {
	managed, err := s.reachTargets(nil)
	if err != nil {
		s.writeStoreError(w, err, "节点或入站")
		return
	}
	ext, err := s.reachExtTargets(nil)
	if err != nil {
		s.writeStoreError(w, err, "外部端点")
		return
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	fresh, err := s.subjectFreshness()
	if err != nil {
		s.writeStoreError(w, err, "触达结果")
		return
	}
	writeJSON(w, http.StatusOK, reachTargetsResponse{
		FetchedAt:      time.Now().UTC(),
		Interval:       settings.ReachInterval(),
		StaleSec:       int64(reachStaleWindow / time.Second),
		ExcludeEnabled: settings.ReachExclude,
		Targets:        append(managed, ext...),
		Freshness:      fresh,
	})
}

// subjectFreshness 汇总各对象各侧(境内/控制面)最近一次探测时刻。
// 来自来源结果表;还没有来源行的对象退回对象存档的 checked_at,
// 两侧共用同一时刻(老存档分不清哪侧什么时候探的)。
func (s *Server) subjectFreshness() (map[string]reach.Freshness, error) {
	out := map[string]reach.Freshness{}
	rows, err := s.store.ListSourceChecks("")
	if err != nil {
		return nil, err
	}
	for _, c := range rows {
		f := out[string(c.SubjectKey)]
		at := c.CheckedAt
		switch c.Scope {
		case reach.ScopeDomestic:
			if f.Domestic == nil || at.After(*f.Domestic) {
				f.Domestic = &at
			}
		default: // cp 与未标 scope 的历史行都算控制面侧
			if f.CP == nil || at.After(*f.CP) {
				f.CP = &at
			}
		}
		out[string(c.SubjectKey)] = f
	}
	checks, err := s.store.ListSubjectChecks()
	if err != nil {
		return nil, err
	}
	for _, c := range checks {
		k := string(c.SubjectKey)
		if _, ok := out[k]; ok {
			continue
		}
		at := c.CheckedAt
		out[k] = reach.Freshness{Domestic: &at, CP: &at}
	}
	return out, nil
}

// ---- 结果回写(POST /api/reach/results) ----

// reachResultsRequest 兼容两种形状:
//   - 旧:results 是 []NodeCheck(操作机合并后的节点级结果),按 node_id 存档。
//   - 新(UC-12):subjects 是探测端报回的按对象 × 来源的原始结果,由控制面
//     折叠出判定与明细再存档。
type reachResultsRequest struct {
	CheckedAt time.Time           `json:"checked_at"`
	Results   []reach.NodeCheck   `json:"results,omitempty"`
	Scope     string              `json:"scope,omitempty"`     // domestic / cp(新形状)
	SourceID  string              `json:"source_id,omitempty"` // 探测点身份,probe:<id>
	Subjects  []reach.SourceCheck `json:"subjects,omitempty"`
}

func (s *Server) handleReachResults(w http.ResponseWriter, r *http.Request) {
	var req reachResultsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if req.CheckedAt.IsZero() {
		req.CheckedAt = time.Now().UTC()
	}
	if len(req.Results) == 0 && len(req.Subjects) == 0 {
		writeError(w, http.StatusBadRequest, "没有可保存的检查结果")
		return
	}

	saved := 0
	// 新形状:按(对象,来源)存档,再重算对象判定。
	for _, sc := range req.Subjects {
		if sc.SubjectKey == "" {
			writeError(w, http.StatusBadRequest, "检查结果缺少对象身份")
			return
		}
		scope := sc.Scope
		if scope == "" {
			scope = req.Scope
		}
		if scope != reach.ScopeDomestic && scope != reach.ScopeCP {
			writeError(w, http.StatusBadRequest, "检查结果缺少来源方位(scope 须为 domestic 或 cp)")
			return
		}
		srcID := sc.SourceID
		if srcID == "" {
			srcID = req.SourceID
		}
		if srcID == "" {
			// 探测点凭据报的结果按探测点身份记账(ISS-14):不能与其他
			// 来源混记为方位名,否则操作机的假数据会覆盖真探测点的行。
			if pid, ok := r.Context().Value(probeIDKey).(int64); ok && pid > 0 {
				srcID = fmt.Sprintf("probe:%d", pid)
			} else {
				srcID = scope // 兼容:没报身份的来源按方位记账
			}
		}
		key := model.ProbeSubjectKey(sc.SubjectKey)
		sum := sc.Summary
		if len(sc.Results) > 0 {
			// 报来明细时以明细为准重算摘要:探测端不用算,控制面口径统一。
			targets, err := s.targetsForSubject(key)
			if err != nil {
				s.writeStoreError(w, err, "触达检查结果")
				return
			}
			sum = reach.SummarizeKeyed(targets, sc.Results)
		}
		sumRaw, _ := json.Marshal(sum)
		res, _ := json.Marshal(sc.Results)
		if err := s.store.SaveSourceCheck(key, srcID, scope, req.CheckedAt, sumRaw, res); err != nil {
			s.writeStoreError(w, err, "触达检查结果")
			return
		}
		if err := s.recomputeSubject(key); err != nil {
			s.writeStoreError(w, err, "触达检查结果")
			return
		}
		saved++
	}

	// 旧形状:存档按对象写(身份 node:<id>),来源行补两份摘要,
	// 让新读径与新鲜度口径也认得这次检查。
	for _, nc := range req.Results {
		if nc.NodeID <= 0 || !model.ReachVerdicts[nc.Verdict] {
			writeError(w, http.StatusBadRequest, "检查结果缺少节点或判定")
			return
		}
		report, err := json.Marshal(nc)
		if err != nil {
			writeError(w, http.StatusBadRequest, "检查结果无法序列化")
			return
		}
		key := model.NodeSubjectKey(nc.NodeID)
		if err := s.store.SaveSubjectCheck(model.ReachSubjectCheck{
			SubjectKey: key, CheckedAt: req.CheckedAt, Verdict: nc.Verdict, Report: report,
		}); err != nil {
			s.writeStoreError(w, err, "触达检查结果")
			return
		}
		for _, side := range []struct {
			src   string
			scope string
			sum   reach.SourceSummary
		}{
			{"local", reach.ScopeDomestic, nc.Local},
			{"cp", reach.ScopeCP, nc.CP},
		} {
			raw, _ := json.Marshal(side.sum)
			if err := s.store.SaveSourceCheck(key, "src-"+side.src, side.scope, req.CheckedAt, raw, nil); err != nil {
				s.writeStoreError(w, err, "触达检查结果")
				return
			}
		}
		saved++
	}

	// 剔除口径变了就换一代缓存:判定落库可能让一批端点进/出订阅,
	// 不管这次有没有真的变化,失效成本都比逐订阅读库低。
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, map[string]int{"saved": saved})
}

// recomputeSubject 从一个对象的来源结果重算判定并重写对象存档。
// 来源行缺明细时(老形状只报了摘要)行里对应侧留空,节点级判定照旧出。
func (s *Server) recomputeSubject(key model.ProbeSubjectKey) error {
	rows, err := s.store.ListSourceChecks(key)
	if err != nil {
		return err
	}
	targets, err := s.targetsForSubject(key)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		// 对象已经不在名册里(节点删了或外部端点换了指纹):存档与来源一起清。
		return s.store.DeleteSubjectResults(key)
	}
	now := time.Now()
	var dom, cp []reach.SourceSummary
	domRes := map[string]*reach.Result{}
	cpRes := map[string]*reach.Result{}
	checked := time.Time{}
	for _, c := range rows {
		if now.Sub(c.CheckedAt) > reachStaleWindow {
			continue // 过期来源不进判定
		}
		var sum reach.SourceSummary
		if err := json.Unmarshal(c.Summary, &sum); err != nil {
			continue
		}
		var res map[string]*reach.Result
		if len(c.Results) > 0 {
			_ = json.Unmarshal(c.Results, &res)
		}
		if c.CheckedAt.After(checked) {
			checked = c.CheckedAt
		}
		if c.Scope == reach.ScopeDomestic {
			dom = append(dom, sum)
			for k, v := range res {
				domRes[k] = v
			}
		} else {
			cp = append(cp, sum)
			for k, v := range res {
				cpRes[k] = v
			}
		}
	}
	local := reach.FoldSources(dom)
	remote := reach.FoldSources(cp)
	verdict := reach.Verdict(local, remote)
	merged := reach.MergeKeyed(targets, domRes, cpRes)
	if checked.IsZero() {
		// 全部来源行都过了新鲜窗:对象还在名册,落一条以现在为时间的
		// unknown 存档,而不是因为缺时间写不进(ISS-14)。
		checked = now
	}

	var report []byte
	if nodeID, ok := model.ParseNodeSubjectKey(key); ok {
		nc := reach.NodeCheck{
			NodeID: nodeID, NodeName: firstNodeName(targets), CheckedAt: checked,
			Verdict: verdict, Local: local, CP: remote, Rows: merged,
		}
		report, _ = json.Marshal(nc)
	} else {
		t := targets[0]
		ec := reach.ExtCheck{
			SubjectKey: string(key), Name: t.Label, CheckedAt: checked,
			Verdict: verdict, Local: local, CP: remote,
			Address: t.Address, Port: t.Port, Protocol: t.Protocol,
			Rows: merged,
		}
		// 对象身份是 "s:<source_id>:<指纹>",顺手补上来源名,reach 列表
		// 与订阅预览的署名才拿得出来(原来两栏恒空)。
		if id, ok := parseExternalSubjectID(string(key)); ok {
			ec.SourceID = id
			if src, err := s.store.GetSource(id); err == nil {
				ec.SourceName = src.Name
			}
		}
		report, _ = json.Marshal(ec)
	}
	return s.store.SaveSubjectCheck(model.ReachSubjectCheck{
		SubjectKey: key, CheckedAt: checked, Verdict: verdict, Report: report,
	})
}

// parseExternalSubjectID 从外部对象身份 "s:<source_id>:<指纹>" 拆出来源 ID。
// 不是这个形状或拆不出返回 ok=false。
func parseExternalSubjectID(key string) (int64, bool) {
	if !strings.HasPrefix(key, "s:") {
		return 0, false
	}
	rest := key[2:]
	i := strings.Index(rest, ":")
	if i <= 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(rest[:i], 10, 64)
	return id, err == nil
}

func firstNodeName(targets []reach.Target) string {
	if len(targets) > 0 {
		return targets[0].NodeName
	}
	return ""
}

// targetsForSubject 摆出一个对象名下的全部探测目标;节点是各入站,
// 外部端点就是它自己。对象不在名册时返回空。
func (s *Server) targetsForSubject(key model.ProbeSubjectKey) ([]reach.Target, error) {
	if nodeID, ok := model.ParseNodeSubjectKey(key); ok {
		return s.reachTargets([]int64{nodeID})
	}
	return s.reachExtTargets([]string{string(key)})
}

// ---- 剔除(UC-12) ----

// exclusionState 是订阅组装时的剔除视图:开关、豁免、当前应剔除的对象集合。
// verdicts 记下每个应剔除对象的判定词(blocked/down),界面用它说明原因。
type exclusionState struct {
	on       bool
	keeps    map[string]bool
	nodes    map[int64]bool
	exts     map[string]bool
	verdicts map[string]string
}

// exclusion 读设置与触达存档,算出当前剔除集。开关关掉时所有集合为空,
// 订阅输出与旧版逐字节一致。
func (s *Server) exclusion() (exclusionState, error) {
	st := exclusionState{
		keeps: map[string]bool{}, nodes: map[int64]bool{},
		exts: map[string]bool{}, verdicts: map[string]string{},
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		return st, err
	}
	st.on = settings.ReachExclude
	for k, v := range settings.ReachKeeps {
		st.keeps[k] = v
	}
	if !st.on {
		return st, nil
	}
	fresh, err := s.subjectFreshness()
	if err != nil {
		return st, err
	}
	checks, err := s.store.ListSubjectChecks()
	if err != nil {
		return st, err
	}
	now := time.Now()
	for _, c := range checks {
		if c.Verdict != "blocked" && c.Verdict != "down" {
			continue
		}
		k := string(c.SubjectKey)
		if st.keeps[k] {
			continue
		}
		f, ok := fresh[k]
		if !ok || f.Domestic == nil {
			continue // 没有境内侧新鲜数据的对象不参与剔除
		}
		if now.Sub(*f.Domestic) > reachStaleWindow {
			continue // 判定过期,回到未知
		}
		st.verdicts[k] = c.Verdict
		if nodeID, isNode := model.ParseNodeSubjectKey(c.SubjectKey); isNode {
			st.nodes[nodeID] = true
		} else {
			st.exts[k] = true
		}
	}
	return st, nil
}

// ExcludedSubject 回答一个对象当前是否被自动剔除(页面与明细用同一口径)。
func (st exclusionState) excluded(key string) bool {
	if !st.on || st.keeps[key] {
		return false
	}
	if nodeID, ok := model.ParseNodeSubjectKey(model.ProbeSubjectKey(key)); ok {
		return st.nodes[nodeID]
	}
	return st.exts[key]
}

// excludeReason 是被剔除对象在预览/清单里的原因说明。
func (st exclusionState) excludeReason(key string) string {
	switch st.verdicts[key] {
	case "blocked":
		return "自动剔除:境内不通"
	case "down":
		return "自动剔除:不可用"
	}
	return "自动剔除"
}

// handleReachKeep 设置或解除对象的保留豁免:keep=true 时该对象永远不进剔除集,
// 取消后下一次订阅请求按现行判定重新计算。
func (s *Server) handleReachKeep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subject string `json:"subject"`
		Keep    bool   `json:"keep"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "缺少对象身份")
		return
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	if settings.ReachKeeps == nil {
		settings.ReachKeeps = map[string]bool{}
	}
	if req.Keep {
		settings.ReachKeeps[req.Subject] = true
	} else {
		delete(settings.ReachKeeps, req.Subject)
	}
	if err := s.store.SaveSettings(settings); err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, map[string]bool{"keep": req.Keep})
}

// ---- 存档读取(GET /api/reach/last、GET /api/reach) ----

// reachLastResponse 是最近一次检查的完整内容:checked_at 取各存档里的最大时刻,
// nodes 附带明细(从存着的报告原样回)。UC-12 起 ext 带外部端点存档。
type reachLastResponse struct {
	CheckedAt time.Time         `json:"checked_at"`
	Counts    map[string]int    `json:"counts"`
	Nodes     []json.RawMessage `json:"nodes"`
	Ext       []json.RawMessage `json:"ext,omitempty"`
	Exclude   bool              `json:"exclude"`
}

func (s *Server) handleReachLast(w http.ResponseWriter, r *http.Request) {
	checks, err := s.store.ListSubjectChecks()
	if err != nil {
		s.writeStoreError(w, err, "触达检查结果")
		return
	}
	if len(checks) == 0 {
		writeError(w, http.StatusNotFound, "还没有触达检查记录")
		return
	}
	st, err := s.exclusion()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	out := reachLastResponse{Counts: map[string]int{}, Nodes: []json.RawMessage{}, Exclude: st.on}
	for _, c := range checks {
		out.Counts[c.Verdict]++
		if c.CheckedAt.After(out.CheckedAt) {
			out.CheckedAt = c.CheckedAt
		}
		key := string(c.SubjectKey)
		// 存档报告上加剔除状态:报告是落盘时的样子,剔除是此刻的判定,
		// 两个事实分开说,界面才能讲清「不通但被保留」与「不通已剔除」。
		var rep map[string]any
		if err := json.Unmarshal(c.Report, &rep); err != nil {
			continue
		}
		rep["excluded"] = st.excluded(key)
		rep["kept"] = st.keeps[key]
		raw, _ := json.Marshal(rep)
		if _, isNode := model.ParseNodeSubjectKey(c.SubjectKey); isNode {
			out.Nodes = append(out.Nodes, raw)
		} else {
			out.Ext = append(out.Ext, raw)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// reachNodeSummary 是列表页用的摘要:判定与时延,不带逐入站明细。
type reachNodeSummary struct {
	NodeID    int64               `json:"node_id"`
	CheckedAt time.Time           `json:"checked_at"`
	Verdict   string              `json:"verdict"`
	Local     reach.SourceSummary `json:"local"`
	CP        reach.SourceSummary `json:"cp"`
	Excluded  bool                `json:"excluded,omitempty"`
	Kept      bool                `json:"kept,omitempty"`
}

func (s *Server) handleReachList(w http.ResponseWriter, r *http.Request) {
	checks, err := s.store.ListSubjectChecks()
	if err != nil {
		s.writeStoreError(w, err, "触达检查结果")
		return
	}
	st, err := s.exclusion()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	out := struct {
		CheckedAt time.Time          `json:"checked_at,omitempty"`
		Nodes     []reachNodeSummary `json:"nodes"`
		Ext       []reach.ExtCheck   `json:"ext,omitempty"`
		Exclude   bool               `json:"exclude"`
	}{Nodes: []reachNodeSummary{}, Exclude: st.on}
	for _, c := range checks {
		if c.CheckedAt.After(out.CheckedAt) {
			out.CheckedAt = c.CheckedAt
		}
		key := string(c.SubjectKey)
		if nodeID, isNode := model.ParseNodeSubjectKey(c.SubjectKey); isNode {
			sum := reachNodeSummary{NodeID: nodeID, CheckedAt: c.CheckedAt, Verdict: c.Verdict,
				Excluded: st.excluded(key), Kept: st.keeps[key]}
			var nc reach.NodeCheck
			if json.Unmarshal(c.Report, &nc) == nil {
				sum.Local = nc.Local
				sum.CP = nc.CP
			}
			out.Nodes = append(out.Nodes, sum)
		} else {
			var ec reach.ExtCheck
			if json.Unmarshal(c.Report, &ec) == nil {
				ec.Excluded = st.excluded(key)
				ec.Kept = st.keeps[key]
				out.Ext = append(out.Ext, ec)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}
