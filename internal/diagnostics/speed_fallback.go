package diagnostics

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunSpeedFallback collects bounded, independent two-site speed samples when
// Fusion has no usable speed result. Callers must not invoke it after cancellation.
// Individual request failures remain in the report; context errors return it too.
func RunSpeedFallback(ctx context.Context, nodeID string, exec Executor) (Report, error) {
	if ctx == nil || exec == nil {
		return Report{}, fmt.Errorf("context and executor are required")
	}
	cfg, err := (Config{
		Engine:                "light",
		Position:              "node",
		NodeID:                nodeID,
		Speed:                 true,
		RequestTimeoutSeconds: 10,
		TotalTimeoutSeconds:   50,
		MaxBytes:              8 << 20,
		SpeedBytes:            1 << 20,
		SpeedSites:            DefaultSpeedSites(),
	}).Normalized()
	if err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TotalTimeoutSeconds)*time.Second)
	defer cancel()
	report := Report{
		Position:  cfg.Position,
		NodeID:    cfg.NodeID,
		StartedAt: time.Now().UTC(),
		Budget:    Budget{MaxBytes: cfg.MaxBytes, TotalTimeoutSeconds: cfg.TotalTimeoutSeconds},
		Warnings: []string{
			"融合怪测速未提供有效结果，使用独立两站有界小样本测速；不是融合怪三网测速结果",
			"Bytes count logical HTTP bodies, excluding headers, TLS and TCP. Reservations include failure attempts and conservative read allowance; redirects are disabled.",
		},
	}
	e := &engine{cfg: cfg, exec: exec, report: &report, tools: map[string]bool{}}
	if ctx.Err() == nil {
		out, toolErr := e.command(ctx, "if command -v curl >/dev/null 2>&1; then printf 'curl=yes\\n'; else printf 'curl=no\\n'; fi\n", nil)
		available := strings.Contains(out, "curl=yes")
		e.tools["curl"] = available
		tool := Tool{Name: "curl", Available: available}
		if !available {
			tool.Detail = "not installed or tool discovery failed"
		}
		report.Tools = append(report.Tools, tool)
		if toolErr != nil {
			report.Warnings = append(report.Warnings, "tool discovery: "+safeError(toolErr))
		}
	}
	if ctx.Err() == nil {
		report.Speed = e.speed(ctx)
	}
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = float64(report.FinishedAt.Sub(report.StartedAt)) / float64(time.Millisecond)
	return report, ctx.Err()
}
