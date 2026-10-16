package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	setupBodyMax    = 16 << 10 // 请求体上限 16KB
	setupCheckLimit = 60 * time.Second
	setupRunLimit   = 30 * time.Minute
)

// serveSetup 处理 /local/setup*。调用前会话与来源已经校验过。
func (s *Server) serveSetup(w http.ResponseWriter, r *http.Request) {
	if s.opts.Setup == nil {
		writeError(w, http.StatusNotFound, "首次安装没有启用")
		return
	}
	want := http.MethodGet
	if r.URL.Path == "/local/setup/check" || r.URL.Path == "/local/setup/run" {
		want = http.MethodPost
	}
	if r.Method != want {
		writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 "+want)
		return
	}
	switch {
	case r.URL.Path == "/local/setup":
		s.setupStatus(w)
	case r.URL.Path == "/local/setup/check":
		s.setupCheck(w, r)
	case r.URL.Path == "/local/setup/run":
		s.setupRun(w, r)
	case strings.HasPrefix(r.URL.Path, "/local/setup/jobs/"):
		s.setupJobStatus(w, r, strings.TrimPrefix(r.URL.Path, "/local/setup/jobs/"))
	default:
		writeError(w, http.StatusNotFound, "没有这个首次安装接口")
	}
}

// setupStatus:GET /local/setup,给出模式与当前任务(没有则 job 为 null)。
func (s *Server) setupStatus(w http.ResponseWriter) {
	s.mu.Lock()
	job := s.job
	proxy := s.proxy
	s.mu.Unlock()
	mode := "normal"
	if proxy == nil {
		mode = "setup"
	}
	var jobView *setupDelta
	if job != nil {
		d := job.delta(0)
		jobView = &d
	}
	writeJSON(w, map[string]any{"mode": mode, "job": jobView})
}

// setupCheck:POST /local/setup/check,用密码探路,成功返回 probe。
func (s *Server) setupCheck(w http.ResponseWriter, r *http.Request) {
	p, ok := readSetupParams(w, r)
	if !ok {
		return
	}
	if p.Host == "" {
		writeError(w, http.StatusBadRequest, "请填写服务器地址")
		return
	}
	if p.SSHPort < 1 || p.SSHPort > 65535 {
		writeError(w, http.StatusBadRequest, "端口要在 1–65535 之间")
		return
	}
	if p.SSHUser == "" {
		p.SSHUser = "root"
	}
	// password 与 identity 都可以为空:交给 runner 用默认密钥。
	ctx, cancel := context.WithTimeout(r.Context(), setupCheckLimit)
	defer cancel()
	probe, err := s.opts.Setup.Check(ctx, p)
	p.Password = ""
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if probe == nil {
		probe = map[string]string{}
	}
	writeJSON(w, map[string]any{"probe": probe})
}

// setupRun:POST /local/setup/run,建任务并异步执行。
func (s *Server) setupRun(w http.ResponseWriter, r *http.Request) {
	p, ok := readSetupParams(w, r)
	if !ok {
		return
	}
	if p.Password != "" {
		writeError(w, http.StatusBadRequest, "密码只在检查那一步用")
		return
	}
	if p.Name == "" {
		writeError(w, http.StatusBadRequest, "请填写节点名称")
		return
	}
	if p.Username == "" {
		writeError(w, http.StatusBadRequest, "请填写用户名")
		return
	}
	if p.DevicePassword != "" && len(p.DevicePassword) < 12 {
		writeError(w, http.StatusBadRequest, "设备密码至少 12 个字符")
		return
	}
	if p.SSHUser == "" {
		p.SSHUser = "root"
	}
	s.mu.Lock()
	if cur := s.job; cur != nil {
		cur.mu.Lock()
		running := cur.state == "running"
		id := cur.id
		cur.mu.Unlock()
		if running {
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "已经在安装了", "id": id})
			return
		}
	}
	job := newSetupJob()
	s.job = job
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), setupRunLimit)
		defer cancel()
		res, err := s.opts.Setup.Run(ctx, p, job)
		p.Password = ""
		// 先切到正常模式再报 done:页面一看到 done 就会去 /api 取订阅地址。
		if err == nil && res != nil && res.Upstream != nil && res.Upstream.Host != "" {
			s.setUpstream(res.Upstream, res.Token, res.Transport)
			s.mu.Lock()
			s.opts.Profile = res.Profile
			s.mu.Unlock()
		}
		job.finish(res, err)
	}()
	writeJSONCode(w, http.StatusAccepted, map[string]any{"id": job.id})
}

// writeJSONCode 与 writeJSON 相同,但指定状态码。
func writeJSONCode(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// setupJobStatus:GET /local/setup/jobs/{id}?since=N。
func (s *Server) setupJobStatus(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job == nil || job.id != id {
		writeError(w, http.StatusNotFound, "没有这个安装任务")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, job.delta(since))
}

// readSetupParams 读并校验请求体大小;失败时已经写好响应。
func readSetupParams(w http.ResponseWriter, r *http.Request) (SetupParams, bool) {
	var p SetupParams
	r.Body = http.MaxBytesReader(w, r.Body, setupBodyMax)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return p, false
	}
	return p, true
}
