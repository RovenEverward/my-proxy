package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// 后台任务接口:体检、安装这类要跑很久的事,交给控制面在后台做完。
//
// 与同步接口的关系:校验一字不差地先走一遍(parseDiagnostic / prepareInstall),
// 通过之后把执行放进 goroutine,接口立刻回 202 和一件 running 的任务。执行时用
// context.Background() —— 这是这个接口存在的理由:关掉页面、断掉连接都不影响它。
// 结论按同步接口的样子落库(状态码 + 正文),GET /api/jobs/{id} 拿到的就是同步
// 接口当时会回的同一份东西。

const (
	// jobListDefault / jobListMax 是 GET /api/nodes/{id}/jobs 的条数范围。
	jobListDefault = 5
	jobListMax     = 20
)

func (s *Server) jobRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	api.HandleFunc("GET /api/nodes/{id}/jobs", s.handleListNodeJobs)
	api.HandleFunc("POST /api/nodes/{id}/diagnostics/jobs", s.handleDiagnoseNodeJob)
	api.HandleFunc("POST /api/nodes/{id}/install/jobs", s.handleInstallNodeJob)
}

// handleGetJob:GET /api/jobs/{id},读一件后台任务的结局。
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	job, err := s.store.GetJob(id)
	if err != nil {
		s.writeStoreError(w, err, "后台任务")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleListNodeJobs:GET /api/nodes/{id}/jobs?kind=&active=1&limit=N,列出这台
// 节点的后台任务,最新的在前。kind 只能是 diagnose / install 或留空(全部)。
func (s *Server) handleListNodeJobs(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "" && kind != model.JobKindDiagnose && kind != model.JobKindInstall {
		writeError(w, http.StatusBadRequest, "kind 只能是 diagnose 或 install")
		return
	}
	var active bool
	switch q.Get("active") {
	case "", "0":
	case "1":
		active = true
	default:
		writeError(w, http.StatusBadRequest, "active 只能是 0 或 1")
		return
	}
	limit := jobListDefault
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit 必须是 1 到 20 之间的整数")
			return
		}
		if n > jobListMax {
			n = jobListMax
		}
		limit = n
	}
	// 节点不存在时给 404,而不是回一份空列表让人以为这台节点从没跑过任务。
	if _, err := s.store.GetNode(id); err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	jobs, err := s.store.ListNodeJobs(id, kind, active, limit)
	if err != nil {
		s.writeStoreError(w, err, "后台任务")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// handleDiagnoseNodeJob:POST /api/nodes/{id}/diagnostics/jobs,请求体与
// POST /api/nodes/{id}/diagnostics 相同。已有同类任务在跑的,回 200 与那件任务。
func (s *Server) handleDiagnoseNodeJob(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	cfg, run, budget, ok := s.parseDiagnostic(w, r, n)
	if !ok {
		return
	}
	job, reused, ok := s.startJob(w, model.JobKindDiagnose, n.ID)
	if !ok {
		return
	}
	if !reused {
		go s.runDiagnosticJob(job.ID, n, cfg, run, budget)
	}
	writeJobStart(w, job, reused)
}

// handleInstallNodeJob:POST /api/nodes/{id}/install/jobs?core=&version=,参数与
// POST /api/nodes/{id}/install 相同。已有安装任务在跑的,回 200 与那件任务。
func (s *Server) handleInstallNodeJob(w http.ResponseWriter, r *http.Request) {
	n, core, version, ok := s.prepareInstall(w, r)
	if !ok {
		return
	}
	job, reused, ok := s.startJob(w, model.JobKindInstall, n.ID)
	if !ok {
		return
	}
	if !reused {
		go s.runInstallJob(job.ID, n, core, version)
	}
	writeJobStart(w, job, reused)
}

// startJob 落一件后台任务,失败时写好 500。
func (s *Server) startJob(w http.ResponseWriter, kind string, nodeID int64) (model.Job, bool, bool) {
	job, reused, err := s.store.StartJob(kind, nodeID, time.Now())
	if err != nil {
		s.writeStoreError(w, err, "后台任务")
		return model.Job{}, false, false
	}
	return job, reused, true
}

// writeJobStart 回答任务已受理:复用的回 200,新起的回 202。
func writeJobStart(w http.ResponseWriter, job model.Job, reused bool) {
	if reused {
		writeJSON(w, http.StatusOK, map[string]any{"job": job, "reused": true})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

// runDiagnosticJob 在后台跑一次体检,跑完把结论落库。体检自带时间预算,到期
// 由执行方按超时收场,不会一直挂着。
func (s *Server) runDiagnosticJob(id int64, n *model.Node, cfg diagnostics.Config, run diagnosticRunner, budget time.Duration) {
	s.finishJob(id, n, "体检", func() (int, any) {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		return s.runDiagnostic(ctx, n, cfg, run)
	})
}

// runInstallJob 在后台装一次核心,装完把结论落库。
func (s *Server) runInstallJob(id int64, n *model.Node, core model.Core, version string) {
	s.finishJob(id, n, "安装", func() (int, any) {
		return s.runInstall(n, core, version)
	})
}

// finishJob 跑执行体并把结论写成任务的结局:200 是 done,其余是 failed(状态码、
// 正文、错误说明与界面提示都照同步接口的样子存)。执行体 panic 也得有个结局,
// 否则这件任务会一直显示在跑,还挡住下一次。
func (s *Server) finishJob(id int64, n *model.Node, what string, exec func() (int, any)) {
	var code int
	var body any
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("后台任务意外中断", "node", n.Name, "job", id, "panic", rec)
				code = http.StatusInternalServerError
				body = map[string]string{"error": fmt.Sprintf("控制面在%s时出了意外: %v", what, rec)}
			}
		}()
		code, body = exec()
	}()

	status := model.JobStatusFailed
	if code == http.StatusOK {
		status = model.JobStatusDone
	}
	raw, err := json.Marshal(body)
	if err != nil {
		s.logger.Error("序列化后台任务结论失败", "node", n.Name, "job", id, "error", err)
		status, code = model.JobStatusFailed, http.StatusInternalServerError
		raw = []byte(`{"error":"结论没能写下来"}`)
	}
	errMsg, hint := jobErrorFields(body)
	if err := s.store.FinishJob(id, status, code, raw, errMsg, hint, time.Now()); err != nil {
		s.logger.Error("记录后台任务结局失败", "node", n.Name, "job", id, "error", err)
	}
}

// jobErrorFields 从同步接口的错误正文里取出 error 与 ui_hint。
//
// 正文是错误对象时才有这两样:体检碰到的错误回答里还带着一份部分报告,那也是
// 错误,照样取 error;成功时的回答(体检报告、安装结果)没有 error 字段,两个
// 都留空。
func jobErrorFields(body any) (errMsg, hint string) {
	switch m := body.(type) {
	case map[string]string:
		return m["error"], m["ui_hint"]
	case map[string]any:
		msg, _ := m["error"].(string)
		uiHint, _ := m["ui_hint"].(string)
		return msg, uiHint
	}
	return "", ""
}
