package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Run returns partial evidence for individual failures. Errors are reserved for
// invalid input/executor or canceled/expired overall context (with partial report).
func Run(ctx context.Context, cfg Config, exec Executor) (Report, error) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return Report{}, err
	}
	if ctx == nil || exec == nil {
		return Report{}, fmt.Errorf("context and executor are required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TotalTimeoutSeconds)*time.Second)
	defer cancel()
	report := Report{Position: cfg.Position, NodeID: cfg.NodeID, ThroughSOCKS: cfg.SocksURL != "", StartedAt: time.Now().UTC(), Budget: Budget{MaxBytes: cfg.MaxBytes, TotalTimeoutSeconds: cfg.TotalTimeoutSeconds}}
	e := &engine{cfg: cfg, exec: exec, report: &report, tools: map[string]bool{}}
	names := []string{"curl", "ping", "mtr", "traceroute", "getent", "nslookup"}
	var cmd strings.Builder
	for _, name := range names {
		fmt.Fprintf(&cmd, "if command -v %s >/dev/null 2>&1; then printf '%s=yes\\n'; else printf '%s=no\\n'; fi\n", name, name, name)
	}
	out, toolErr := e.command(ctx, cmd.String(), nil)
	for _, name := range names {
		yes := strings.Contains(out, name+"=yes")
		e.tools[name] = yes
		t := Tool{Name: name, Available: yes}
		if !yes {
			t.Detail = "not installed or tool discovery failed"
		}
		report.Tools = append(report.Tools, t)
	}
	if toolErr != nil {
		report.Warnings = append(report.Warnings, "tool discovery: "+safeError(toolErr))
	}
	for _, t := range cfg.Targets {
		if ctx.Err() != nil {
			break
		}
		report.Targets = append(report.Targets, TargetReport{Target: t, DNS: e.dns(ctx, t), ICMP: e.icmp(ctx, t), HTTP: e.httpLatency(ctx, t), Route: e.route(ctx, t)})
	}
	report.Speed = e.speed(ctx)
	report.IP = e.ip(ctx)
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = float64(report.FinishedAt.Sub(report.StartedAt)) / float64(time.Millisecond)
	report.Warnings = append(report.Warnings, "Bytes count logical HTTP bodies, excluding headers, TLS and TCP. Reservations include failure attempts and conservative read allowance; redirects are disabled.", "ICMP filtering or unanswered intermediate hops do not prove end-to-end failure; no carrier quality is inferred.", "IPv6 describes observed egress/service support; transparent host networking or a SOCKS server may provide it.")
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return report, ctx.Err()
	}
	return report, nil
}
