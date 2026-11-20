package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/reach"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// UC-20:节点带宽与境内稳定度。
//
//	GET  /api/metrics              全部对象的稳定度聚合 + 带宽展示值
//	GET  /api/bandwidth/endpoint   一个对象的带宽测量端点配置(管理 token)
//	POST /api/bandwidth/results    回写一次带宽测量存档(管理 token)
//
// 探测点凭据够不着这三个接口(不在 probeAllowed),端点凭据与测量结果只对管理面。
func (s *Server) metricRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/metrics", s.handleMetrics)
	api.HandleFunc("GET /api/bandwidth/endpoint", s.handleBandwidthEndpoint)
	api.HandleFunc("POST /api/bandwidth/results", s.handleBandwidthResults)
}

// ---- 稳定度聚合(GET /api/metrics) ----

// stabilityWindowDays 与 store.sourceSampleMaxAge 同口径:窗口即样本保留期。
const stabilityWindowDays = 7

// metricsResponse 是 GET /api/metrics 的整体形状:对象身份 → 指标视图。
// subjects 用数组而不是 map,顺序稳定,界面不需要再排序键。
type metricsResponse struct {
	WindowDays int                    `json:"window_days"`
	Subjects   []model.SubjectMetrics `json:"subjects"`
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-stabilityWindowDays * 24 * time.Hour)
	samples, err := s.store.ListSourceSamples("", reach.ScopeDomestic, since)
	if err != nil {
		s.writeStoreError(w, err, "稳定度样本")
		return
	}
	bw, err := s.store.ListAllBandwidth()
	if err != nil {
		s.writeStoreError(w, err, "带宽样本")
		return
	}

	// 按对象聚合。
	bySubject := map[string][]model.ReachSourceSample{}
	order := []string{}
	for _, sm := range samples {
		key := string(sm.SubjectKey)
		if _, seen := bySubject[key]; !seen {
			order = append(order, key)
		}
		bySubject[key] = append(bySubject[key], sm)
	}
	sort.Strings(order)

	out := make([]model.SubjectMetrics, 0, len(order))
	for _, key := range order {
		out = append(out, model.SubjectMetrics{
			SubjectKey: model.ProbeSubjectKey(key),
			Stability:  aggregateStability(bySubject[key], stabilityWindowDays),
			Bandwidth:  bandwidthView(bw[key], bw[key].CheckedAt.Unix() > 0),
		})
	}
	// 只有带宽没有稳定度样本的对象(从没被境内探过但手动测过)也要出现。
	for key := range bw {
		if _, ok := bySubject[key]; !ok {
			out = append(out, model.SubjectMetrics{
				SubjectKey: model.ProbeSubjectKey(key),
				Stability:  aggregateStability(nil, stabilityWindowDays),
				Bandwidth:  bandwidthView(bw[key], bw[key].CheckedAt.Unix() > 0),
			})
		}
	}
	writeJSON(w, http.StatusOK, metricsResponse{WindowDays: stabilityWindowDays, Subjects: out})
}

// aggregateStability 把窗口内的境内样本按轮聚成成功率与中位时延。
// 口径:每行(对象 × 来源 × 一次探测)算一轮;成功率 = reachable/probed;
// 时延取可达轮次的中位数。样本不足 StabilityMinRounds 轮标 insufficient。
func aggregateStability(samples []model.ReachSourceSample, windowDays int) model.StabilityMetrics {
	m := model.StabilityMetrics{WindowDays: windowDays}
	if len(samples) == 0 {
		m.Insufficient = true
		return m
	}
	srcs := map[string]bool{}
	var probed, reachable int
	var lats []int64
	for _, sm := range samples {
		srcs[sm.SourceID] = true
		if !sm.Probed {
			continue
		}
		probed++
		if sm.Reachable {
			reachable++
			if sm.LatencyMs > 0 {
				lats = append(lats, sm.LatencyMs)
			}
		}
	}
	m.Sources = len(srcs)
	m.Rounds = probed
	if probed < model.StabilityMinRounds {
		m.Insufficient = true
	}
	if probed > 0 {
		m.SuccessRate = float64(reachable) / float64(probed)
	}
	if len(lats) > 0 {
		sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
		m.LatencyMs = lats[len(lats)/2]
	}
	return m
}

// bandwidthView 把一条样本记录整理成展示形状:最近一次 ok 的值 + 最近状态。
func bandwidthView(b model.BandwidthSample, ok bool) model.BandwidthView {
	if !ok {
		return model.BandwidthView{Status: "untested"}
	}
	v := model.BandwidthView{Status: b.Status, MeasuredAt: &b.CheckedAt, SourceID: b.SourceID, Error: b.Error}
	if b.Status == "ok" {
		v.BytesPerSec = b.BytesPerSec
	}
	return v
}

// ---- 带宽测量端点下发(GET /api/bandwidth/endpoint) ----

// bandwidthEndpoints 用 render.EndpointConfig 做传输形状,
// 理由见 render/endpoint_wire.go。
// bandwidthEndpointResponse 一次测量可用的全部端点:自建节点是多入站的候选
// 集合,外部端点就是一条。measure 侧逐个尝试直到拿到可用值。
type bandwidthEndpointResponse struct {
	Subject   string              `json:"subject"`
	Endpoints []render.EndpointConfig `json:"endpoints"`
}

func (s *Server) handleBandwidthEndpoint(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject")
	if subject == "" {
		writeError(w, http.StatusBadRequest, "缺少 subject 参数")
		return
	}
	key := model.ProbeSubjectKey(subject)
	eps, err := s.bandwidthEndpoints(r.Context(), key)
	if err != nil {
		s.writeStoreError(w, err, "测量端点")
		return
	}
	if len(eps) == 0 {
		writeError(w, http.StatusNotFound, "对象没有可用端点")
		return
	}
	out := make([]render.EndpointConfig, 0, len(eps))
	for _, ep := range eps {
		out = append(out, render.WireEndpoint(ep))
	}
	writeJSON(w, http.StatusOK, bandwidthEndpointResponse{Subject: subject, Endpoints: out})
}

// bandwidthEndpoints 摆出一个对象的可测端点:node/host 走测量用户解析,
// 外部端点直接读订阅快照(凭据在快照里)。
func (s *Server) bandwidthEndpoints(ctx context.Context, key model.ProbeSubjectKey) ([]render.Endpoint, error) {
	if nodeID, ok := model.ParseNodeSubjectKey(key); ok {
		return s.managedBandwidthEndpoints(nodeID, 0)
	}
	if hostID, ok := model.ParseHostSubjectKey(key); ok {
		h, err := s.store.GetHost(hostID)
		if err != nil {
			return nil, nil
		}
		in, err := s.store.GetInbound(h.InboundID)
		if err != nil {
			return nil, err
		}
		return s.managedBandwidthEndpoints(in.NodeID, hostID)
	}
	if srcID, ok := parseExternalSubjectID(string(key)); ok {
		src, err := s.store.GetSource(srcID)
		if err != nil {
			return nil, err
		}
		if len(src.Snapshot) == 0 {
			return nil, nil
		}
		var eps []render.Endpoint
		if err := json.Unmarshal(src.Snapshot, &eps); err != nil {
			return nil, err
		}
		for _, ep := range eps {
			ep.SourceID = srcID
			if string(ep.Key()) == string(key) {
				return []render.Endpoint{ep}, nil
			}
		}
		return nil, nil
	}
	return nil, nil
}

// managedBandwidthEndpoints 用测量用户解析一台节点的可测端点;hostID>0 时
// 只取那个入口(UC-12 的 host 对象按入口测)。
func (s *Server) managedBandwidthEndpoints(nodeID int64, hostID int64) ([]render.Endpoint, error) {
	n, err := s.store.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	user, err := s.ensureMeasureUser()
	if err != nil {
		return nil, err
	}
	inbounds, err := s.store.ListInbounds(nodeID)
	if err != nil {
		return nil, err
	}
	var inIDs []int64
	byInbound := map[int64][]model.Host{}
	if hostID > 0 {
		h, err := s.store.GetHost(hostID)
		if err != nil {
			return nil, err
		}
		inIDs = []int64{h.InboundID}
		byInbound[h.InboundID] = []model.Host{*h}
	} else {
		for _, in := range inbounds {
			inIDs = append(inIDs, in.ID)
		}
		hosts, err := s.store.ListHosts(0)
		if err != nil {
			return nil, err
		}
		for _, h := range hosts {
			byInbound[h.InboundID] = append(byInbound[h.InboundID], h)
		}
	}
	var ins []model.Inbound
	for _, in := range inbounds {
		for _, id := range inIDs {
			if in.ID == id {
				ins = append(ins, in)
			}
		}
	}
	return render.ResolveNamed(*n, n.Name, ins, byInbound, user), nil
}

// ensureMeasureUser 取(或建)专用测量用户 mpx-probe:enabled、无套餐、
// InboundIDs 空(=全部入站)、无 SubToken(不出现在订阅端点)。流量计量归它,
// 操作者在用户页能看到测量消耗,不做暗账。
const measureUsername = "mpx-probe"

func (s *Server) ensureMeasureUser() (*model.User, error) {
	users, err := s.store.ListUsers()
	if err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Username == measureUsername {
			u := users[i]
			return &u, nil
		}
	}
	u := &model.User{
		Username:      measureUsername,
		Key:           mustNewKey(),
		Enabled:       true,
		ResetStrategy: model.ResetMonthly,
	}
	if err := s.store.CreateUser(u); err != nil {
		return nil, fmt.Errorf("创建测量用户: %w", err)
	}
	return u, nil
}

// mustNewKey 生成测量用户的密钥种子;生成失败属平台级故障,直接 panic
// 与 NewKey 的其它调用点同一处理口径。
func mustNewKey() string {
	k, err := model.NewKey()
	if err != nil {
		panic(err)
	}
	return k
}

// ---- 带宽结果回写(POST /api/bandwidth/results) ----

func (s *Server) handleBandwidthResults(w http.ResponseWriter, r *http.Request) {
	var b model.BandwidthSample
	if !decodeBody(w, r, &b) {
		return
	}
	if b.SourceID == "" {
		// 手动测量的来源固定记 operator,与探测点 source_id 区分开。
		b.SourceID = "operator"
	}
	b.CheckedAt = time.Now().UTC()
	if err := s.store.SaveBandwidthSample(b); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"saved": true})
}

// ---- 孤儿指标清理 ----

// pruneOrphanMetrics 删掉名册里已经不存在对象的样本与带宽存档。名册 =
// reachTargets(自建 + host)+ reachExtTargets(外部端点)的全部 Subject。
// 由 handleReachTargets 顺带驱动(探测端每轮都会拉,不需要新周期)。
func (s *Server) pruneOrphanMetrics() {
	const minInterval = time.Hour
	s.orphanMu.Lock()
	if time.Since(s.orphanLast) < minInterval {
		s.orphanMu.Unlock()
		return
	}
	s.orphanLast = time.Now()
	s.orphanMu.Unlock()

	managed, err := s.reachTargets(nil)
	if err != nil {
		return
	}
	ext, err := s.reachExtTargets(nil)
	if err != nil {
		return
	}
	roster := map[string]bool{}
	for _, t := range append(managed, ext...) {
		roster[t.Subject] = true
	}
	if err := s.store.PruneMetricsNotIn(roster); err != nil {
		// 清不动只留到下一轮,不升级成错误响应。
		_ = err
	}
}
