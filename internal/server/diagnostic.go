package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// DiagnosticOps is optional; implementations of NodeOps need not run diagnostics.
type DiagnosticOps interface {
	DiagnoseNode(context.Context, model.Node, diagnostics.Config, time.Time) (model.DiagnosticRecord, error)
}

// CheckupOps runs the complete node checkup, including health and entrances.
type CheckupOps interface {
	CheckupNode(context.Context, model.Node, diagnostics.Config, time.Time) (model.DiagnosticRecord, error)
}

type diagnosticInput struct {
	diagnostics.Config
	NetworkOnly          bool  `json:"network_only,omitempty"`
	Speed                *bool `json:"speed,omitempty"`
	Hardware             *bool `json:"hardware,omitempty"`
	FusionTimeoutSeconds *int  `json:"fusion_timeout_seconds,omitempty"`
}

func (s *Server) diagnosticRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/nodes/{id}/diagnostics", s.handleDiagnoseNode)
	api.HandleFunc("GET /api/nodes/{id}/diagnostics", s.handleGetDiagnostics)
}

// Diagnostic reports contain deeply nested raw JSON within a 256 KiB storage
// budget. Keep the wire representation compact so indentation and HTML escaping
// cannot push a valid report past the CLI's 260 KiB response limit. These are
// application/json API responses, not JSON embedded in an HTML document.
func writeDiagnosticJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func diagnosticSource(source string) (string, bool) {
	if source == "" {
		source = "node"
	}
	return source, source == "node" || source == "control"
}

func (s *Server) handleGetDiagnostics(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	source, ok := diagnosticSource(r.URL.Query().Get("from"))
	if !ok {
		writeError(w, http.StatusBadRequest, "from 只能是 node 或 control")
		return
	}
	record, err := s.store.GetDiagnosticReport(n.ID, source)
	if err != nil {
		s.writeStoreError(w, err, "诊断报告")
		return
	}
	writeDiagnosticJSON(w, http.StatusOK, record)
}

// diagnosticRunner 跑一次体检。两种引擎共用这个签名,由 parseDiagnostic 挑。
type diagnosticRunner func(context.Context, model.Node, diagnostics.Config, time.Time) (model.DiagnosticRecord, error)

func (s *Server) handleDiagnoseNode(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	cfg, run, budget, ok := s.parseDiagnostic(w, r, n)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	code, body := s.runDiagnostic(ctx, n, cfg, run)
	writeDiagnosticJSON(w, code, body)
}

// parseDiagnostic 解析并校验体检请求体,挑出执行的那一路,算出它的时间预算。
//
// 不合法的请求由它自己写好 400 / 501,调用方看 ok 就返回。同步接口和后台任务接口
// 共用这一份:两边的合法与不合法必须一模一样。
func (s *Server) parseDiagnostic(w http.ResponseWriter, r *http.Request, n *model.Node) (cfg diagnostics.Config, run diagnosticRunner, budget time.Duration, ok bool) {
	var input *diagnosticInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, diagnostics.MaxOutputBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input == nil {
		writeError(w, http.StatusBadRequest, "诊断请求不是合法配置 JSON")
		return cfg, nil, 0, false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "诊断请求只能包含一个 JSON 对象")
		return cfg, nil, 0, false
	}
	cfg = input.Config
	cfg.Speed = !input.NetworkOnly
	if input.Speed != nil {
		cfg.Speed = *input.Speed
	}
	cfg.Position, ok = diagnosticSource(cfg.Position)
	if !ok || cfg.SocksURL != "" {
		writeError(w, http.StatusBadRequest, "API 只支持 node/control 位置,不能指定 SOCKS 代理")
		return cfg, nil, 0, false
	}
	if input.NetworkOnly && (cfg.Engine == "fusion" || input.Hardware != nil || input.FusionTimeoutSeconds != nil) {
		writeError(w, http.StatusBadRequest, "network_only 仅支持 light,不能指定 hardware 或 fusion_timeout_seconds")
		return cfg, nil, 0, false
	}
	if cfg.Engine == "" {
		cfg.Engine = "fusion"
		if input.NetworkOnly || cfg.Position == "control" {
			cfg.Engine = "light"
		}
	}
	if cfg.Engine == "light" && (input.Hardware != nil || input.FusionTimeoutSeconds != nil) {
		writeError(w, http.StatusBadRequest, "hardware 和 fusion_timeout_seconds 仅支持 fusion 引擎")
		return cfg, nil, 0, false
	}
	if input.Hardware != nil {
		cfg.Hardware = *input.Hardware
	}
	if input.FusionTimeoutSeconds != nil {
		cfg.FusionTimeoutSeconds = *input.FusionTimeoutSeconds
		if cfg.FusionTimeoutSeconds == 0 {
			writeError(w, http.StatusBadRequest, "fusion_timeout_seconds 必须在 60..1800 秒之间")
			return cfg, nil, 0, false
		}
	}
	if cfg.Engine == "fusion" && cfg.Position != "node" {
		writeError(w, http.StatusBadRequest, "Fusion 体检只能在 node 位置执行;control 位置请使用 light")
		return cfg, nil, 0, false
	}
	cfg.NodeID = strconv.FormatInt(n.ID, 10)
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return cfg, nil, 0, false
	}
	budget = cfg.CheckupTimeout() + 30*time.Second
	if input.NetworkOnly {
		ops, connected := s.nodeOps.(DiagnosticOps)
		if connected {
			run = ops.DiagnoseNode
		}
		budget = 3 * time.Minute
	} else {
		ops, connected := s.nodeOps.(CheckupOps)
		if connected {
			run = ops.CheckupNode
		}
	}
	if run == nil {
		writeError(w, http.StatusNotImplemented, "这个服务端没有接上节点体检")
		return cfg, nil, 0, false
	}
	return cfg, run, budget, true
}

// runDiagnostic 跑一次体检并给出同步接口会回的状态码与正文。同步接口把它直接
// 写出来;后台任务接口把它连同结局一起落库 —— 两边看到的是同一份回答。
func (s *Server) runDiagnostic(ctx context.Context, n *model.Node, cfg diagnostics.Config, run diagnosticRunner) (int, any) {
	record, err := run(ctx, *n, cfg, time.Now())
	if err != nil {
		code := http.StatusBadGateway
		switch {
		case errors.Is(err, control.ErrDiagnosticBusy):
			code = http.StatusConflict
		case errors.Is(err, control.ErrDiagnosticRateLimit):
			code = http.StatusTooManyRequests
		}
		response := map[string]any{"error": err.Error()}
		if json.Valid(record.Report) {
			response["record"] = record
		}
		return code, response
	}
	if !json.Valid(record.Report) {
		return http.StatusBadGateway, map[string]string{"error": "诊断没有返回合法报告"}
	}
	return http.StatusOK, record
}
