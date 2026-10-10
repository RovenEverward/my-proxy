package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/store"
)

var (
	ErrDiagnosticBusy      = errors.New("另一个综合体检正在运行,请稍后重试")
	ErrDiagnosticRateLimit = errors.New("同一节点和测量位置的综合体检至少间隔一分钟")
)

type diagnosticSSHExecutor struct {
	runner node.Runner
	node   model.Node
}

func (e diagnosticSSHExecutor) Exec(ctx context.Context, script string, stdin []byte) (string, error) {
	// Production SSH enforces the allocation limit during transport. A substituted
	// Runner must implement the same bounded contract rather than truncate afterward.
	runner, ok := e.runner.(interface {
		RunBounded(context.Context, model.Node, string, []byte, int) (string, error)
	})
	if !ok {
		return "", fmt.Errorf("SSH 执行器未提供有界输出能力")
	}
	return runner.RunBounded(ctx, e.node, script, stdin, diagnostics.MaxOutputBytes)
}

// DiagnoseNode runs only the network portion for advanced position selection.
func (c *Control) DiagnoseNode(ctx context.Context, n model.Node, cfg diagnostics.Config, now time.Time) (model.DiagnosticRecord, error) {
	record := model.DiagnosticRecord{NodeID: n.ID, Source: cfg.Position, CheckedAt: now}
	current, err := c.store.GetNode(n.ID)
	if err != nil {
		return record, err
	}
	record.MeasuredAddress = current.Address
	if cfg.Position == "" {
		cfg.Position = "node"
	}
	record.Source = cfg.Position
	if err := ctx.Err(); err != nil {
		return record, err
	}
	select {
	case c.diagnosticSlot <- struct{}{}:
		defer func() { <-c.diagnosticSlot }()
	default:
		return record, ErrDiagnosticBusy
	}
	previous, err := c.store.GetDiagnosticReport(n.ID, cfg.Position)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return record, err
	}
	if previous != nil && now.Before(previous.CheckedAt.Add(time.Minute)) {
		return record, ErrDiagnosticRateLimit
	}
	record, runErr := c.runDiagnostic(ctx, *current, cfg, now)
	if !json.Valid(record.Report) {
		return record, runErr
	}
	if err := c.store.SaveDiagnosticReport(record); err != nil {
		return record, err
	}
	return record, runErr
}

// runDiagnostic runs under its caller's slot and does not save intermediate reports.
func (c *Control) runDiagnostic(ctx context.Context, n model.Node, cfg diagnostics.Config, now time.Time) (model.DiagnosticRecord, error) {
	record := model.DiagnosticRecord{NodeID: n.ID, Source: cfg.Position, CheckedAt: now}
	if cfg.Position == "" {
		cfg.Position = "node"
	}
	if cfg.Position != "node" && cfg.Position != "control" {
		return record, fmt.Errorf("服务端测量位置只能是 node 或 control")
	}
	if cfg.SocksURL != "" || cfg.AbuseIPDBKey != "" || cfg.AbuseIPDBKeyEnv != "" || cfg.IPAPIKey != "" || cfg.IPAPIKeyEnv != "" {
		return record, fmt.Errorf("服务端检测不接受代理凭证或自选 API 密钥")
	}
	// Resolve the current identity from storage, not a stale or client-supplied node.
	current, err := c.store.GetNode(n.ID)
	if err != nil {
		return record, err
	}
	n = *current
	record.MeasuredAddress = n.Address
	cfg.NodeID = strconv.FormatInt(n.ID, 10)
	if cfg.Position == "control" && len(cfg.Targets) == 0 {
		cfg.Targets = []diagnostics.Target{{Name: "node-" + strconv.FormatInt(n.ID, 10), Region: "配置节点位置,未经地理验证", Host: n.Address}}
	}
	cfg.AbuseIPDBKeyEnv = "MPX_ABUSEIPDB_KEY"
	cfg.IPAPIKeyEnv = "MPX_IPAPI_IS_KEY"
	cfg, err = cfg.Normalized()
	if err != nil {
		return record, err
	}
	record.Source = cfg.Position
	if err := ctx.Err(); err != nil {
		return record, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TotalTimeoutSeconds)*time.Second)
	defer cancel()
	var executor diagnostics.Executor = diagnostics.LocalExecutor{}
	if cfg.Position == "node" {
		executor = diagnosticSSHExecutor{runner: c.runner, node: n}
	}
	report, runErr := diagnostics.Run(ctx, cfg, executor)
	record.Report, err = json.Marshal(report)
	if err != nil {
		return record, err
	}
	return record, runErr
}
