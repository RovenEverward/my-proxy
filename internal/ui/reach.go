package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/probeagent"
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
		var req struct {
			NodeIDs []int64  `json:"node_ids,omitempty"`
			ExtKeys []string `json:"ext_keys,omitempty"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		go s.runReachCheck(job, up.String(), token, tr, req.NodeIDs, req.ExtKeys)
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
// UC-12 起外部订阅端点也在范围内:它们按对象 × 来源的新形状(subjects)
// 存回,节点级结果仍走旧形状,与 mpx node check 同口径。
func (s *Server) runReachCheck(job *reachJob, upstream, token string, tr http.RoundTripper, nodeIDs []int64, extKeys []string) {
	counts, err := func() (map[string]int, error) {
		probe, err := reachCall[struct {
			CheckedAt  time.Time                `json:"checked_at"`
			Targets    []reach.Target           `json:"targets"`
			Results    map[int64]*reach.Result  `json:"results"`
			ExtTargets []reach.Target           `json:"ext_targets,omitempty"`
			ExtResults map[string]*reach.Result `json:"ext_results,omitempty"`
		}](tr, upstream, token, "POST", "/api/reach/probe", map[string]any{"node_ids": nodeIDs, "ext_keys": extKeys})
		if err != nil {
			return nil, fmt.Errorf("控制面探测: %w", err)
		}
		if len(probe.Targets) == 0 && len(probe.ExtTargets) == 0 {
			return nil, fmt.Errorf("范围内没有带入站的节点或外部端点")
		}
		nodeSet := map[int64]bool{}
		for _, t := range probe.Targets {
			nodeSet[t.NodeID] = true
		}
		job.mu.Lock()
		job.nodes = len(nodeSet) + len(probe.ExtTargets)
		job.mu.Unlock()
		job.set("local")
		ctx, cancel := context.WithTimeout(context.Background(), reachHTTPTimeout)
		defer cancel()
		local := reach.Probe(ctx, probe.Targets, reach.Options{})
		localExt := reach.ProbeKeyed(ctx, probe.ExtTargets, reach.Options{})

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
		// 外部端点按(对象,来源)新形状存:明细报回,控制面重算判定,
		// 与探测点报数据的径是同一个。
		subjects := []reach.SourceCheck{}
		if len(probe.ExtTargets) > 0 {
			for _, sc := range []reach.SourceCheck{
				{Scope: reach.ScopeDomestic, SourceID: "src-local", Results: localExt},
				{Scope: reach.ScopeCP, SourceID: "src-cp", Results: probe.ExtResults},
			} {
				if len(sc.Results) == 0 {
					continue
				}
				sc.Summary = reach.SummarizeKeyed(probe.ExtTargets, sc.Results)
				sc.CheckedAt = probe.CheckedAt
				subjects = append(subjects, sc)
			}
			// subjects 的 SubjectKey 由请求体按对象逐条带:同一批结果在
			// 同一对象下成对出现。
			subjects = groupSubjects(subjects, probe.ExtTargets)
		}
		payload := map[string]any{"checked_at": probe.CheckedAt, "results": checks}
		if len(subjects) > 0 {
			payload["subjects"] = subjects
		}
		if _, err := reachCall[map[string]int](tr, upstream, token, "POST", "/api/reach/results", payload); err != nil {
			return nil, fmt.Errorf("回存结果: %w", err)
		}
		counts := map[string]int{}
		for _, nc := range checks {
			counts[nc.Verdict]++
		}
		for _, t := range probe.ExtTargets {
			lv := reach.SummarizeKeyed([]reach.Target{t}, localExt)
			cv := reach.SummarizeKeyed([]reach.Target{t}, probe.ExtResults)
			counts[reach.Verdict(lv, cv)]++
		}
		return counts, nil
	}()
	job.finish(counts, err)
}

// groupSubjects 把按来源汇总的两条结果拆成「对象 × 来源」的逐条清单:
// 每个外部端点一条 domestic + 一条 cp,与 POST /api/reach/results 的
// subjects 契约对上。
func groupSubjects(scoped []reach.SourceCheck, targets []reach.Target) []reach.SourceCheck {
	out := make([]reach.SourceCheck, 0, len(targets)*len(scoped))
	for _, t := range targets {
		for _, sc := range scoped {
			res := map[string]*reach.Result{}
			if r := sc.Results[t.Key()]; r != nil {
				res[t.Key()] = r
			}
			row := sc
			row.SubjectKey = t.Subject
			row.Results = res
			row.Summary = reach.SummarizeKeyed([]reach.Target{t}, res)
			out = append(out, row)
		}
	}
	return out
}

// ReachRotation 在 mpx ui 进程里常驻轮转探测(UC-12):本机就是一台境内
// 探测端,页面开着就顺手把欠账清了 —— 与 mpx probe run 同一份调度,
// 凭据用的是页面已有的管理 token,不另发探测点 token。
// 每次调用才现读 upstream:setup 模式下还没有控制面,装完 setUpstream
// 之后下一轮自然接上,不用重启轮转。
func (s *Server) ReachRotation(ctx context.Context, logf func(string, ...any)) {
	call := func(method, path string, body any) ([]byte, error) {
		s.mu.Lock()
		up, token, tr := s.opts.Upstream, s.opts.Token, s.opts.Transport
		s.mu.Unlock()
		if up == nil {
			return nil, errors.New("还没有控制面")
		}
		return reachCall[json.RawMessage](tr, up.String(), token, method, path, body)
	}
	probeagent.Loop(ctx, call, func(err error) {
		if logf != nil {
			logf("轮转探测这一轮失败: %v", err)
		}
	})
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
