package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// qualityProber 由 control 实现:在节点上跑一次轻量 IP 质量探测,返回分段输出。
// 用可选接口而不是加进 NodeOps,测试里的假实现不必跟着改。
type qualityProber interface {
	ProbeNodeQuality(ctx context.Context, n model.Node, abuseKey, ipapiKey string) (string, error)
}

// qualitySlot 限制同时在探测的节点数:探测是一条 SSH 加几次外网请求,
// 放开并发会把控制面与第三方配额一起打满。
var qualitySlot = make(chan struct{}, 4)

const qualityProbeTimeout = 40 * time.Second

func (s *Server) qualityRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/nodes/{id}/quality", s.handleProbeQuality)
	api.HandleFunc("GET /api/nodes/{id}/quality", s.handleGetQuality)
	api.HandleFunc("GET /api/quality", s.handleListQuality)
	api.HandleFunc("GET /api/quality/last", s.handleQualityLast)
	api.HandleFunc("POST /api/regrade", s.handleRegrade)
	// UC-17 外部端点降级探测:控制面直连第三方 IP 数据服务,没有 SSH。
	api.HandleFunc("POST /api/sources/{id}/endpoints/{key}/quality", s.handleProbeExternalQuality)
	api.HandleFunc("GET /api/sources/{id}/endpoints/{key}/quality", s.handleGetExternalQuality)
	api.HandleFunc("GET /api/sources/{id}/quality", s.handleListExternalQuality)
}

// handleProbeQuality 在节点上探测出口并计算质量分,存档后返回完整记录。
// ipapi.is 与 AbuseIPDB 密钥只在控制面环境变量里找:节点上没有这些变量。
func (s *Server) handleProbeQuality(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	p, ok := s.nodeOps.(qualityProber)
	if !ok {
		writeError(w, http.StatusNotImplemented, "控制面没有接上质量探测器")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), qualityProbeTimeout+10*time.Second)
	defer cancel()
	select {
	case qualitySlot <- struct{}{}:
		defer func() { <-qualitySlot }()
	case <-ctx.Done():
		writeError(w, http.StatusServiceUnavailable, "质量探测队列已满,稍后再试")
		return
	}
	pctx, pcancel := context.WithTimeout(ctx, qualityProbeTimeout)
	raw, err := p.ProbeNodeQuality(pctx, *n, os.Getenv("MPX_ABUSEIPDB_KEY"), os.Getenv("MPX_IPAPI_IS_KEY"))
	pcancel()
	rep := parseQualityReport(raw, err)
	verdict := "untested"
	score := 0
	if rep.EgressIP != "" {
		score, rep.Deductions = model.QualityScore(rep.Flags, rep.AbuseScore)
		rep.Score = score
		verdict = "ok"
		for _, st := range rep.Steps {
			if st.Status == "failed" {
				verdict = "partial"
				break
			}
		}
	}
	body, _ := json.Marshal(rep)
	q := model.NodeQuality{NodeID: n.ID, CheckedAt: time.Now().UTC(), Verdict: verdict, Score: score, Report: body}
	if err := s.store.SaveNodeQuality(q); err != nil {
		s.writeStoreError(w, err, "质量记录")
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) handleGetQuality(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	list, err := s.store.ListNodeQuality(n.ID)
	if err != nil {
		s.writeStoreError(w, err, "质量记录")
		return
	}
	if len(list) == 0 {
		writeError(w, http.StatusNotFound, "这台节点还没有质量探测记录")
		return
	}
	writeJSON(w, http.StatusOK, list[0])
}

func (s *Server) handleListQuality(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListNodeQuality(0)
	if err != nil {
		s.writeStoreError(w, err, "质量记录")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": list})
}

// handleQualityLast 是「最近一次探测时刻」的摘要,列表页判断要不要显示质量列。
func (s *Server) handleQualityLast(w http.ResponseWriter, r *http.Request) {
	at, ok, err := s.store.LatestNodeQualityAt()
	if err != nil {
		s.writeStoreError(w, err, "质量记录")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "还没有质量探测记录")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checked_at": at})
}

// parseQualityReport 把节点探测的分段输出解析成明细报告。
// SSH/解析失败时 runErr 非空:报告记 untested 原因,不产生分数。
func parseQualityReport(raw string, runErr error) model.QualityReport {
	rep := model.QualityReport{}
	if runErr != nil {
		rep.Error = runErr.Error()
		rep.Steps = []model.QualityStep{{Name: "probe", Status: "failed", Reason: rep.Error}}
		return rep
	}
	sections := map[string]string{}
	cur := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "===MPX_") {
			cur = strings.TrimSuffix(strings.TrimPrefix(line, "===MPX_"), "===")
			continue
		}
		if cur != "" {
			sections[cur] += line + "\n"
		}
	}
	body := func(name string) string { return strings.TrimSpace(sections[name]) }
	step := func(name, status, reason string) {
		rep.Steps = append(rep.Steps, model.QualityStep{Name: name, Status: status, Reason: reason})
	}
	if body("NOCURL") != "" {
		rep.Error = "节点上没有 curl"
		step("curl", "skipped", "not installed")
		return rep
	}
	ipLine := body("IP")
	ip := ""
	if idx := strings.IndexAny(ipLine, "\r\n"); idx >= 0 {
		ip = strings.TrimSpace(ipLine[:idx])
	} else {
		ip = strings.TrimSpace(ipLine)
	}
	if parsed := net.ParseIP(ip); parsed == nil {
		rep.Error = "没测出出口 IP"
		step("egress", "failed", "api.ipify.org 没返回合法地址")
		return rep
	} else {
		rep.EgressIP = parsed.String()
		step("egress", "ok", "")
	}
	var geo struct {
		Success     bool   `json:"success"`
		CountryCode string `json:"country_code"`
		City        string `json:"city"`
	}
	if g := body("GEO"); g == "" || json.Unmarshal([]byte(g), &geo) != nil || !geo.Success {
		step("geo", "failed", "ipwho.is 没给出地区")
	} else {
		rep.Region = strings.ToUpper(strings.TrimSpace(geo.CountryCode))
		rep.CityEn = strings.TrimSpace(geo.City)
		rep.City = model.CityZh(geo.City)
		step("geo", "ok", "")
	}
	var fl struct {
		Datacenter *bool           `json:"is_datacenter"`
		VPN        *bool           `json:"is_vpn"`
		Proxy      *bool           `json:"is_proxy"`
		Tor        *bool           `json:"is_tor"`
		Abuser     *bool           `json:"is_abuser"`
		Bogon      *bool           `json:"is_bogon"`
		ASN        json.RawMessage `json:"asn"`
	}
	if f := body("FLAGS"); f == "" || json.Unmarshal([]byte(f), &fl) != nil {
		step("flags", "failed", "ipapi.is 没返回标记")
	} else {
		rep.Flags = map[string]bool{}
		for name, v := range map[string]*bool{"is_datacenter": fl.Datacenter, "is_vpn": fl.VPN, "is_proxy": fl.Proxy, "is_tor": fl.Tor, "is_abuser": fl.Abuser, "is_bogon": fl.Bogon} {
			if v != nil {
				rep.Flags[name] = *v
			}
		}
		var asn struct {
			ASN int64  `json:"asn"`
			Org string `json:"org"`
		}
		if json.Unmarshal(fl.ASN, &asn) == nil && asn.ASN > 0 {
			rep.ASN = fmt.Sprintf("AS%d", asn.ASN)
			rep.Org = strings.TrimSpace(asn.Org)
		}
		step("flags", "ok", "")
	}
	if a := body("ABUSE"); a != "" {
		var ab struct {
			Data struct {
				Score *int `json:"abuseConfidenceScore"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(a), &ab) == nil && ab.Data.Score != nil {
			rep.AbuseScore = ab.Data.Score
			step("abuse", "ok", "")
		} else {
			step("abuse", "failed", "AbuseIPDB 响应无法解析")
		}
	}
	return rep
}

// RegradeRow 是评级对照表的一行:节点 → 归一化后的地区与城市、质量分、
// 旧字母、建议字母。New 为 "-" 表示这行不写入。Kind 区分托管节点与外部
// 端点;外部行带 SourceID / SourceKey,ManagedID 恒为 0。
type RegradeRow struct {
	Kind      string `json:"kind"`                 // "node" / "external";空等同 node
	ID        int64  `json:"id"`                   // 托管节点 ID;外部行为 0
	SourceID  int64  `json:"source_id,omitempty"`  // 外部端点所属来源
	SourceKey string `json:"source_key,omitempty"` // 外部端点指纹
	Name      string `json:"name"`                 // 节点内部名或外部原始名
	Display   string `json:"display,omitempty"`
	Region    string `json:"region,omitempty"` // 归一化后的地区
	City      string `json:"city,omitempty"`   // 归一化后的城市
	Score     *int   `json:"score,omitempty"`  // nil = 未测
	Verdict   string `json:"verdict"`          // ok / partial / untested
	Old       string `json:"old"`              // 旧字母,空串为没有
	New       string `json:"new"`              // 建议字母;"-" 表示不写入
	Note      string `json:"note,omitempty"`   // 跳过原因或不一致提示
	Changed   bool   `json:"changed"`
}

// RegradePlanItem 是 apply 要写入的一条元数据。apply 在服务端重算,
// 这份结构只给客户端展示与审计用。External 为真时走 external_metadata。
type RegradePlanItem struct {
	ID       int64                  `json:"id"`
	Name     string                 `json:"name"`
	Metadata model.LocationMetadata `json:"metadata"`
	// External 为真时 SourceID + SourceKey 是身份,Metadata 写进 external_metadata;
	// 为假时是托管节点,ID 是 nodes.id。
	External  bool   `json:"external,omitempty"`
	SourceID  int64  `json:"source_id,omitempty"`
	SourceKey string `json:"source_key,omitempty"`
}

// regradeInput 是 POST /api/regrade 的请求体:apply=true 才真正写库。
type regradeInput struct {
	Apply bool `json:"apply"`
}

// regradeNode 是进入重排计算的节点视图。托管节点用 id;外部端点用
// (sourceID, sourceKey),extKey 非空即外部行。rowIdx 指向 rows 里这行,
// 排名循环直接改那行,不用再回查。
type regradeNode struct {
	id        int64
	rowIdx    int
	sourceID  int64
	sourceKey string
	extKey    string // 外部端点:(source_id\x00source_key),托管为空
	name      string
	score     int
	tested    bool                   // 有质量分才参与排名
	m         model.LocationMetadata // 归一化后的目标元数据
	old       model.LocationMetadata // 归一化前的元数据
	note      string
}

// handleRegrade 计算或执行评级:默认只返回对照表,apply=true 持锁重算并写入。
func (s *Server) handleRegrade(w http.ResponseWriter, r *http.Request) {
	var in regradeInput
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &in) {
			return
		}
	}
	metadataMu.Lock()
	defer metadataMu.Unlock()
	rows, plans, err := s.regradePlan()
	if err != nil {
		s.writeStoreError(w, err, "评级计划")
		return
	}
	if !in.Apply {
		writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "plans": plans})
		return
	}
	batch := map[int64]model.LocationMetadata{}
	var extBatch []model.ExternalMetadata
	for _, p := range plans {
		if p.External {
			extBatch = append(extBatch, model.ExternalMetadata{
				SourceID: p.SourceID, SourceKey: p.SourceKey,
				Region: p.Metadata.Region, City: p.Metadata.City, Letter: p.Metadata.Letter,
				Kind: "graded",
			})
			continue
		}
		batch[p.ID] = p.Metadata
	}
	if err := s.store.SaveLocationMetadataBatch(batch); err != nil {
		s.writeStoreError(w, err, "评级写入")
		return
	}
	if err := s.store.SaveExternalMetadataBatch(extBatch); err != nil {
		s.writeStoreError(w, err, "外部评级写入")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "plans": plans, "applied": true})
}

// regradePlan 在 metadataMu 下计算评级对照表。预览与 apply 调同一个函数,
// 客户端看到的和写库的一定是同一份计算。
//
// 规则:
//   - 只有出过分的节点参与排名;未测节点保留原字母。
//   - 已存地区是有效国家码但与探测不一致时不归一化、不参与排名,只提示。
//   - 组内按分数降序、节点 ID 升序;字母池跳过未测节点占用的字母,
//     未测节点字母留在原位,被测节点按名次取池内空位。
func (s *Server) regradePlan() ([]RegradeRow, []RegradePlanItem, error) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, nil, err
	}
	meta, err := s.store.ListLocationMetadata("nodes")
	if err != nil {
		return nil, nil, err
	}
	quals, err := s.store.ListNodeQuality(0)
	if err != nil {
		return nil, nil, err
	}
	byNode := map[int64]model.NodeQuality{}
	reports := map[int64]model.QualityReport{}
	for _, q := range quals {
		byNode[q.NodeID] = q
		var rep model.QualityReport
		if json.Unmarshal(q.Report, &rep) == nil {
			reports[q.NodeID] = rep
		}
	}
	// fixedLetters: 每个 (region,city) 组里被固定占用的字母——未测节点与不参与
	// 重排的节点字母留原位,被测节点取名次位时避开。
	fixedLetters := map[string]map[string]bool{}
	addFixed := func(m model.LocationMetadata) {
		if strings.TrimSpace(m.Region) == "" || m.Letter == "" {
			return
		}
		key := model.LetterGroup(m)
		if fixedLetters[key] == nil {
			fixedLetters[key] = map[string]bool{}
		}
		fixedLetters[key][m.Letter] = true
	}
	groups := map[string][]regradeNode{}
	rows := []RegradeRow{}
	plans := []RegradePlanItem{}
	for _, n := range nodes {
		m := meta[n.ID]
		row := RegradeRow{Kind: "node", ID: n.ID, Name: n.Name, Display: model.NodeDisplay(m), Old: m.Letter, New: "-", Verdict: "untested", Region: m.Region, City: m.City}
		q, ok := byNode[n.ID]
		rep, hasRep := reports[n.ID]
		if !ok || q.Verdict == "untested" || !hasRep {
			row.Note = "未测出分,保留原字母"
			addFixed(m)
			rows = append(rows, row)
			continue
		}
		row.Verdict = q.Verdict
		row.Score = &q.Score
		region, conflict := model.NormalizeRegion(m.Region, rep.Region)
		city := m.City
		if city == "" && rep.City != "" {
			city = rep.City
		}
		row.Region, row.City = region, city
		if conflict {
			row.Note = fmt.Sprintf("已有地区 %s 与探测到的 %s 不一致,不参与重排", m.Region, rep.Region)
			addFixed(m)
			rows = append(rows, row)
			continue
		}
		if model.RegionFlag(region) == "" {
			row.Note = "地区为空且探测没给出国家码,不参与重排"
			addFixed(m)
			rows = append(rows, row)
			continue
		}
		nm := m
		nm.Region, nm.City = region, city
		key := model.LetterGroup(nm)
		groups[key] = append(groups[key], regradeNode{id: n.ID, rowIdx: len(rows), name: n.Name, score: q.Score, tested: true, m: nm, old: m})
		rows = append(rows, row)
	}
	// 外部端点:同一套分组与分配逻辑,键换成 (source_id, source_key)。
	// regradeExternalRows 把行直接追加进 rows 并返回行索引化的分组,
	// 合并进 groups 后走同一个排名循环。
	extGroups, extFixed, err := s.regradeExternalRows(&rows)
	if err != nil {
		return nil, nil, err
	}
	for key, list := range extGroups {
		groups[key] = list
	}
	for key, pool := range extFixed {
		if fixedLetters[key] == nil {
			fixedLetters[key] = map[string]bool{}
		}
		for letter := range pool {
			fixedLetters[key][letter] = true
		}
	}
	// 组内排名并分配字母。
	for key, list := range groups {
		sort.Slice(list, func(i, j int) bool {
			if list[i].score != list[j].score {
				return list[i].score > list[j].score
			}
			return list[i].id < list[j].id
		})
		// 固定占用池 = 未测与不参与节点的字母,去掉本组节点自己的字母。
		taken := map[string]bool{}
		for letter := range fixedLetters[key] {
			taken[letter] = true
		}
		for _, c := range list {
			delete(taken, c.m.Letter)
		}
		for _, c := range list {
			want := model.NextLetter(taken)
			if want == "" {
				break // 字母池耗尽,剩下的保持原样
			}
			taken[want] = true
			row := &rows[c.rowIdx]
			if c.m.Letter == want && c.m.Region == c.old.Region && c.m.City == c.old.City {
				row.New = "-"
				continue
			}
			nm := c.m
			nm.Letter = want
			row.New = model.NodeDisplay(nm)
			row.Changed = true
			if c.extKey != "" {
				plans = append(plans, RegradePlanItem{Name: c.name, Metadata: nm, External: true, SourceID: c.sourceID, SourceKey: c.sourceKey})
				continue
			}
			plans = append(plans, RegradePlanItem{ID: c.id, Name: c.name, Metadata: nm})
		}
	}
	return rows, plans, nil
}

// regradeExternalRows 是 regradePlan 的外部半边:把每个来源快照里的端点
// 按 (源,地区,城市) 分组,行直接追加进 rows(行索引给排名循环用)。返回分组
// 与固定字母池,主函数把它们并进统一排名。
func (s *Server) regradeExternalRows(rows *[]RegradeRow) (map[string][]regradeNode, map[string]map[string]bool, error) {
	sources, err := s.store.ListSources()
	if err != nil {
		return nil, nil, err
	}
	extMeta, err := s.store.ListExternalMetadata(0)
	if err != nil {
		return nil, nil, err
	}
	quals, err := s.store.ListExternalQuality(0)
	if err != nil {
		return nil, nil, err
	}
	byKey := map[string]model.ExternalQuality{}
	reports := map[string]model.QualityReport{}
	for _, q := range quals {
		k := extQualityKey(q.SourceID, q.SourceKey)
		byKey[k] = q
		var rep model.QualityReport
		if json.Unmarshal(q.Report, &rep) == nil {
			reports[k] = rep
		}
	}
	groups := map[string][]regradeNode{}
	fixed := map[string]map[string]bool{}
	addFixed := func(sourceID int64, m model.LocationMetadata) {
		if strings.TrimSpace(m.Region) == "" || m.Letter == "" {
			return
		}
		key := model.ExternalGroup(sourceID, m)
		if fixed[key] == nil {
			fixed[key] = map[string]bool{}
		}
		fixed[key][m.Letter] = true
	}
	for _, src := range sources {
		var eps []render.Endpoint
		if len(src.Snapshot) == 0 || json.Unmarshal(src.Snapshot, &eps) != nil {
			continue
		}
		for _, ep := range eps {
			ep.SourceID = src.ID
			k := extQualityKey(src.ID, ep.SourceKey)
			m, _ := extMeta[externalMetaKey(src.ID, ep.SourceKey)]
			lm := m.AsLocation()
			display := resolveExternalRemark(src.Name, extMeta, ep)
			row := RegradeRow{
				Kind: "external", SourceID: src.ID, SourceKey: ep.SourceKey,
				Name: ep.Remark, Display: display, Old: lm.Letter, New: "-",
				Verdict: "untested", Region: lm.Region, City: lm.City,
			}
			q, ok := byKey[k]
			rep, hasRep := reports[k]
			if !ok || q.Verdict == "untested" || !hasRep {
				row.Note = "未测出分,保留原字母"
				addFixed(src.ID, lm)
				*rows = append(*rows, row)
				continue
			}
			row.Verdict = q.Verdict
			row.Score = &q.Score
			region, conflict := model.NormalizeRegion(lm.Region, rep.Region)
			city := lm.City
			if city == "" && rep.City != "" {
				city = rep.City
			}
			row.Region, row.City = region, city
			if conflict {
				row.Note = fmt.Sprintf("已有地区 %s 与探测到的 %s 不一致,不参与重排", lm.Region, rep.Region)
				addFixed(src.ID, lm)
				*rows = append(*rows, row)
				continue
			}
			if model.RegionFlag(region) == "" {
				row.Note = "地区为空且探测没给出国家码,不参与重排"
				addFixed(src.ID, lm)
				*rows = append(*rows, row)
				continue
			}
			nm := model.LocationMetadata{Region: region, City: city, Letter: lm.Letter}
			key := model.ExternalGroup(src.ID, nm)
			groups[key] = append(groups[key], regradeNode{
				rowIdx: len(*rows), name: ep.Remark, score: q.Score, tested: true,
				m: nm, old: lm, extKey: k, sourceID: src.ID, sourceKey: ep.SourceKey,
			})
			*rows = append(*rows, row)
		}
	}
	return groups, fixed, nil
}

// extQualityKey 是 external_quality 行的键,与 externalMetaKey 同格式。
func extQualityKey(sourceID int64, sourceKey string) string {
	return fmt.Sprintf("%d\x00%s", sourceID, sourceKey)
}

// 名次靠前的先取走最小空位,后面名次自然顺延,不需要按名次跳位。
