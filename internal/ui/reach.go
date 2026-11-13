package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/reach"
)

// reach.go 是 /local/reach:操作机上的触达检查编排。
//
// 一次检查的顺序与 mpx node check 相同:先让控制面自己探一轮(POST
// /api/reach/probe),本机再对同一批目标探一轮,合并后存回控制面(POST
// /api/reach/results)。页面不逐条打控制面接口 —— 编排只能在这台机器上做,
// 控制面替它做不了本机这一侧。
//
// 检查起在后台任务里:POST 立刻回任务号,GET 拿进展。关掉页面任务照样跑完
// (与安装、体检同一处理),结果最终落在控制面,不在本进程。

// reachJob 是一次触达检查的任务状态。
type reachJob struct {
	mu      sync.Mutex
	id      string
	started time.Time
	stage   string // cp / local / save / done / error
	nodes   int
	counts  map[string]int
	err     string
}

func (j *reachJob) set(stage string) {
	j.mu.Lock()
	j.stage = stage
	j.mu.Unlock()
}

func (j *reachJob) finish(counts map[string]int, err error) {
	j.mu.Lock()
	if err != nil {
		j.stage, j.err = "error", err.Error()
	} else {
		j.stage, j.counts = "done", counts
	}
	j.mu.Unlock()
}

func (j *reachJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]any{
		"id": j.id, "stage": j.stage, "nodes": j.nodes,
		"started_at": j.started.Format(time.RFC3339),
	}
	if j.err != "" {
		out["error"] = j.err
	}
	if j.counts != nil {
		out["counts"] = j.counts
	}
	return out
}

// serveReach 处理 /local/reach/*。调用前会话与来源已在外层校验过。
func (s *Server) serveReach(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	up, token, tr := s.opts.Upstream, s.opts.Token, s.opts.Transport
	s.mu.Unlock()
	if up == nil {
		writeError(w, http.StatusServiceUnavailable, "还没有控制面:先完成首次安装")
		return
	}
	switch {
	case r.URL.Path == "/local/reach/check" && r.Method == http.MethodPost:
		s.mu.Lock()
		if s.reach != nil {
			s.reach.mu.Lock()
			stage := s.reach.stage
			s.reach.mu.Unlock()
			if stage != "done" && stage != "error" {
				s.mu.Unlock()
				writeJSON(w, s.reach.snapshot())
				return
			}
		}
		job := &reachJob{id: randomToken()[:10], started: time.Now(), stage: "cp"}
		s.reach = job
		s.mu.Unlock()
		var nodeIDs []int64
		var req struct {
			NodeIDs []int64 `json:"node_ids,omitempty"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err == nil {
			nodeIDs = req.NodeIDs
		}
		go s.runReachCheck(job, up.String(), token, tr, nodeIDs)
		writeJSON(w, job.snapshot())
	case r.URL.Path == "/local/reach/check" && r.Method == http.MethodGet:
		s.mu.Lock()
		job := s.reach
		s.mu.Unlock()
		if job == nil {
			writeJSON(w, map[string]any{"stage": "none"})
			return
		}
		writeJSON(w, job.snapshot())
	default:
		writeError(w, http.StatusNotFound, "没有这个接口")
	}
}

// reachHTTPTimeout 给编排里每个 HTTP 调用的上限。控制面探测本身的服务端
// 上限是 2 分钟,这里留宽一点等它答完。
const reachHTTPTimeout = 140 * time.Second

// runReachCheck 跑一轮检查:控制面探 → 本机探 → 合并 → 存回。
func (s *Server) runReachCheck(job *reachJob, upstream, token string, tr http.RoundTripper, nodeIDs []int64) {
	counts, err := func() (map[string]int, error) {
		probe, err := reachCall[struct {
			CheckedAt time.Time               `json:"checked_at"`
			Targets   []reach.Target          `json:"targets"`
			Results   map[int64]*reach.Result `json:"results"`
		}](tr, upstream, token, "POST", "/api/reach/probe", map[string]any{"node_ids": nodeIDs})
		if err != nil {
			return nil, fmt.Errorf("控制面探测: %w", err)
		}
		if len(probe.Targets) == 0 {
			return nil, fmt.Errorf("范围内没有带入站的节点")
		}
		nodeSet := map[int64]bool{}
		for _, t := range probe.Targets {
			nodeSet[t.NodeID] = true
		}
		job.mu.Lock()
		job.nodes = len(nodeSet)
		job.mu.Unlock()
		job.set("local")
		ctx, cancel := context.WithTimeout(context.Background(), reachHTTPTimeout)
		defer cancel()
		local := reach.Probe(ctx, probe.Targets, reach.Options{})

		job.set("save")
		byNode := map[int64][]reach.Target{}
		names := map[int64]string{}
		for _, t := range probe.Targets {
			byNode[t.NodeID] = append(byNode[t.NodeID], t)
			names[t.NodeID] = t.NodeName
		}
		checks := make([]reach.NodeCheck, 0, len(byNode))
		for nodeID, targets := range byNode {
			rows, lv, cv := reach.Merge(targets, local, probe.Results)
			checks = append(checks, reach.NodeCheck{
				NodeID: nodeID, NodeName: names[nodeID], CheckedAt: probe.CheckedAt,
				Verdict: reach.Verdict(lv, cv), Local: lv, CP: cv, Rows: rows,
			})
		}
		if _, err := reachCall[map[string]int](tr, upstream, token, "POST", "/api/reach/results",
			map[string]any{"checked_at": probe.CheckedAt, "results": checks}); err != nil {
			return nil, fmt.Errorf("回存结果: %w", err)
		}
		counts := map[string]int{}
		for _, nc := range checks {
			counts[nc.Verdict]++
		}
		return counts, nil
	}()
	job.finish(counts, err)
}

// reachCall 是 ui 进程对控制面的小客户端:和转发层同一个上游与 token,
// 不带 cookie、不经浏览器。只在 /local/reach 里用。
func reachCall[T any](tr http.RoundTripper, upstream, token, method, path string, body any) (T, error) {
	var out T
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return out, err
		}
		payload = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), reachHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(upstream, "/")+path, payload)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return out, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var msg struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &msg) == nil && msg.Error != "" {
			return out, fmt.Errorf("%s", msg.Error)
		}
		return out, fmt.Errorf("控制面返回 %d", resp.StatusCode)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}
