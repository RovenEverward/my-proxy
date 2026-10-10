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
	NetworkOnly bool  `json:"network_only,omitempty"`
	Speed       *bool `json:"speed,omitempty"`
}

func (s *Server) diagnosticRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/nodes/{id}/diagnostics", s.handleDiagnoseNode)
	api.HandleFunc("GET /api/nodes/{id}/diagnostics", s.handleGetDiagnostics)
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
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleDiagnoseNode(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	var input *diagnosticInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, diagnostics.MaxOutputBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input == nil {
		writeError(w, http.StatusBadRequest, "诊断请求不是合法配置 JSON")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "诊断请求只能包含一个 JSON 对象")
		return
	}
	cfg := input.Config
	cfg.Speed = !input.NetworkOnly
	if input.Speed != nil {
		cfg.Speed = *input.Speed
	}
	cfg.Position, ok = diagnosticSource(cfg.Position)
	if !ok || cfg.SocksURL != "" {
		writeError(w, http.StatusBadRequest, "API 只支持 node/control 位置,不能指定 SOCKS 代理")
		return
	}
	cfg.NodeID = strconv.FormatInt(n.ID, 10)
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var run func(context.Context, model.Node, diagnostics.Config, time.Time) (model.DiagnosticRecord, error)
	budget := 6*time.Minute + 30*time.Second
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
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
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
		writeJSON(w, code, response)
		return
	}
	if !json.Valid(record.Report) {
		writeError(w, http.StatusBadGateway, "诊断没有返回合法报告")
		return
	}
	writeJSON(w, http.StatusOK, record)
}
