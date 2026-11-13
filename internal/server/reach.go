package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/reach"
)

// reachRoutes 挂触达检查的四条接口(只增):
//
//	POST /api/reach/probe   控制面从自己库取目标清单,就地探一轮,返回目标与结果。
//	POST /api/reach/results 存入一次检查的合并结果(操作机发起检查后回报)。
//	GET  /api/reach/last    最近一次检查的完整内容,页面与 mpx node check --last 用。
//	GET  /api/reach         各节点最近一次存档的摘要(不带逐入站明细)。
func (s *Server) reachRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/reach/probe", s.handleReachProbe)
	api.HandleFunc("POST /api/reach/results", s.handleReachResults)
	api.HandleFunc("GET /api/reach/last", s.handleReachLast)
	api.HandleFunc("GET /api/reach", s.handleReachList)
}

// reachProbeTimeout 给控制面侧整轮探测的上限:单个探针 4s、32 并发,
// 节点多时按批往上抬,封顶两分钟免得把一次 HTTP 调用拖成长跑。
const reachProbeTimeout = 2 * time.Minute

type reachProbeRequest struct {
	NodeIDs []int64 `json:"node_ids,omitempty"`
}

type reachProbeResponse struct {
	CheckedAt time.Time               `json:"checked_at"`
	Targets   []reach.Target          `json:"targets"`
	Results   map[int64]*reach.Result `json:"results"`
}

// reachTargets 从库里摆出要探的目标清单:每台节点 × 它的每个入站,
// 地址用节点对外地址(Host 覆盖的入口不在这次检查范围内)。
// Label 用订阅里的名字,没有就退回 tag。
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
			})
		}
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
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, "范围内没有带入站的节点")
		return
	}
	ctx := r.Context()
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > reachProbeTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, reachProbeTimeout)
		defer cancel()
	}
	writeJSON(w, http.StatusOK, reachProbeResponse{
		CheckedAt: time.Now().UTC(),
		Targets:   targets,
		Results:   reach.Probe(ctx, targets, reach.Options{}),
	})
}

type reachResultsRequest struct {
	CheckedAt time.Time         `json:"checked_at"`
	Results   []reach.NodeCheck `json:"results"`
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
	if len(req.Results) == 0 {
		writeError(w, http.StatusBadRequest, "没有可保存的检查结果")
		return
	}
	checks := make([]model.ReachCheck, 0, len(req.Results))
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
		checks = append(checks, model.ReachCheck{NodeID: nc.NodeID, CheckedAt: req.CheckedAt, Verdict: nc.Verdict, Report: report})
	}
	if err := s.store.SaveReachChecks(checks); err != nil {
		s.writeStoreError(w, err, "触达检查结果")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"saved": len(checks)})
}

// reachLastResponse 是最近一次检查的完整内容:checked_at 取各存档里的最大时刻,
// nodes 附带明细(从存着的报告原样回)。
type reachLastResponse struct {
	CheckedAt time.Time         `json:"checked_at"`
	Counts    map[string]int    `json:"counts"`
	Nodes     []json.RawMessage `json:"nodes"`
}

func (s *Server) handleReachLast(w http.ResponseWriter, r *http.Request) {
	checks, err := s.store.ListReachChecks(0)
	if err != nil {
		s.writeStoreError(w, err, "触达检查结果")
		return
	}
	if len(checks) == 0 {
		writeError(w, http.StatusNotFound, "还没有触达检查记录")
		return
	}
	out := reachLastResponse{Counts: map[string]int{}, Nodes: []json.RawMessage{}}
	for _, c := range checks {
		out.Counts[c.Verdict]++
		if c.CheckedAt.After(out.CheckedAt) {
			out.CheckedAt = c.CheckedAt
		}
		out.Nodes = append(out.Nodes, c.Report)
	}
	writeJSON(w, http.StatusOK, out)
}

// reachListResponse 是列表页用的摘要:判定与时延,不带逐入站明细。
type reachNodeSummary struct {
	NodeID    int64               `json:"node_id"`
	CheckedAt time.Time           `json:"checked_at"`
	Verdict   string              `json:"verdict"`
	Local     reach.SourceSummary `json:"local"`
	CP        reach.SourceSummary `json:"cp"`
}

func (s *Server) handleReachList(w http.ResponseWriter, r *http.Request) {
	checks, err := s.store.ListReachChecks(0)
	if err != nil {
		s.writeStoreError(w, err, "触达检查结果")
		return
	}
	out := struct {
		CheckedAt time.Time          `json:"checked_at,omitempty"`
		Nodes     []reachNodeSummary `json:"nodes"`
	}{Nodes: []reachNodeSummary{}}
	for _, c := range checks {
		sum := reachNodeSummary{NodeID: c.NodeID, CheckedAt: c.CheckedAt, Verdict: c.Verdict}
		var nc reach.NodeCheck
		if json.Unmarshal(c.Report, &nc) == nil {
			sum.Local = nc.Local
			sum.CP = nc.CP
		}
		out.Nodes = append(out.Nodes, sum)
		if c.CheckedAt.After(out.CheckedAt) {
			out.CheckedAt = c.CheckedAt
		}
	}
	writeJSON(w, http.StatusOK, out)
}
