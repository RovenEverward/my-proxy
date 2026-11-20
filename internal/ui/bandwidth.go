package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/bwtest"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// bandwidth.go 是 /local/bandwidth:操作机上手动触发的带宽测量(UC-20)。
// 与 reachJob 同一形状:页面点「测速」→ 后台 job 跑 → 页面轮询快照,
// 结果由 job 直接回写控制面,不经过 /api 转发层(转发层只放 GET /api)。

type bwJob struct {
	mu      sync.Mutex
	id      string
	subject string
	started time.Time
	stage   string // measure / save / done / error
	result  *bwtest.Result
	err     string
}

func (j *bwJob) set(stage string) { j.mu.Lock(); j.stage = stage; j.mu.Unlock() }

func (j *bwJob) finish(res bwtest.Result, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err != nil {
		j.stage = "error"
		j.err = err.Error()
		return
	}
	j.result = &res
	if res.Status == "ok" {
		j.stage = "done"
	} else {
		j.stage = "error"
		j.err = res.Error
	}
}

func (j *bwJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]any{
		"id": j.id, "subject": j.subject, "stage": j.stage,
		"started_at": j.started.Format(time.RFC3339),
	}
	if j.err != "" {
		out["error"] = j.err
	}
	if j.result != nil {
		out["result"] = j.result
	}
	return out
}

// serveBandwidth 处理 /local/bandwidth/*。调用前会话与来源已在外层校验过。
func (s *Server) serveBandwidth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	up, token, tr := s.opts.Upstream, s.opts.Token, s.opts.Transport
	s.mu.Unlock()
	if up == nil {
		writeError(w, http.StatusServiceUnavailable, "还没有控制面:先完成首次安装")
		return
	}
	switch {
	case r.URL.Path == "/local/bandwidth/measure" && r.Method == http.MethodPost:
		var req struct {
			Subject string `json:"subject"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
		if req.Subject == "" {
			writeError(w, http.StatusBadRequest, "缺少 subject")
			return
		}
		s.mu.Lock()
		if s.bw != nil {
			s.bw.mu.Lock()
			busy := s.bw.stage != "done" && s.bw.stage != "error"
			subj := s.bw.subject
			s.bw.mu.Unlock()
			if busy && subj == req.Subject {
				snap := s.bw.snapshot()
				s.mu.Unlock()
				writeJSON(w, snap)
				return
			}
		}
		job := &bwJob{id: randomToken()[:10], subject: req.Subject, started: time.Now(), stage: "measure"}
		s.bw = job
		s.mu.Unlock()
		go s.runBandwidth(job, up.String(), token, tr, req.Subject)
		writeJSON(w, job.snapshot())
	case r.URL.Path == "/local/bandwidth/measure" && r.Method == http.MethodGet:
		s.mu.Lock()
		job := s.bw
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

// runBandwidth 拉端点 → 本机测速 → 回写控制面。
// 测量整体上限 3 分钟(两轮 20s 下载 + 起 sing-box + 接口往返)。
func (s *Server) runBandwidth(job *bwJob, upstream, token string, tr http.RoundTripper, subject string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := func() (bwtest.Result, error) {
		var ep struct {
			Subject   string                  `json:"subject"`
			Endpoints []render.EndpointConfig `json:"endpoints"`
			TestURL   string                  `json:"test_url"`
		}
		raw, err := reachCall[json.RawMessage](tr, upstream, token, "GET",
			"/api/bandwidth/endpoint?subject="+url.QueryEscape(subject), nil)
		if err != nil {
			return bwtest.Result{}, fmt.Errorf("取测量端点: %w", err)
		}
		if err := json.Unmarshal(raw, &ep); err != nil {
			return bwtest.Result{}, fmt.Errorf("解析端点: %w", err)
		}
		job.set("measure")
		res := bwtest.Measure(ctx, ep.Endpoints, bwtest.Options{TestURL: ep.TestURL})

		job.set("save")
		if _, err := reachCall[json.RawMessage](tr, upstream, token, "POST",
			"/api/bandwidth/results", map[string]any{
				"subject_key":   subject,
				"status":        res.Status,
				"bytes_per_sec": res.BytesPerSec,
				"duration_ms":   res.DurationMs,
				"bytes":         res.Bytes,
				"test_url":      bwtest.DefaultTestURL,
				"error":         res.Error,
			}); err != nil {
			return res, fmt.Errorf("回存结果: %w", err)
		}
		return res, nil
	}()
	job.finish(res, err)
}
