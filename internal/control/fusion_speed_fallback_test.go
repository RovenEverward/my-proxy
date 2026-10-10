package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/fusion"
	"github.com/RovenEverward/my-proxy/internal/model"
)

func fallbackSpeedFixture(payload string) json.RawMessage {
	return json.RawMessage(`{"schema_version":"goecs.report/v1","status":"partial","components":[{"name":"basics","payload":{"cpu":"preserved"}},{"name":"speed.registry","schema_version":"goecs.speed/v1","status":"partial","reason":"7/19 selected nodes completed throughput","payload":` + payload + `}]}`)
}

func TestFusionSpeedMeasuredRequiresOfficialThroughput(t *testing.T) {
	for _, tc := range []struct {
		name, data       string
		parsed, measured bool
	}{
		{"missing-data", "", false, false},
		{"malformed", "{", false, false},
		{"null-root", "null", false, false},
		{"non-object", "[]", false, false},
		{"missing-root-schema", `{}`, false, false},
		{"wrong-root-schema", `{"schema_version":"unknown/v1"}`, false, false},
		{"no-component", `{"schema_version":"goecs.report/v1"}`, true, false},
		{"wrong-component", `{"schema_version":"goecs.report/v1","components":[{"name":"speed","schema_version":"goecs.speed/v1","payload":{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"available","download_mbps":12}]}}]}`, true, false},
		{"wrong-envelope", `{"schema_version":"goecs.report/v1","components":[{"name":"speed.registry","schema_version":"unknown/v1","payload":{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"available","download_mbps":12}]}}]}`, true, false},
		{"wrong-payload", string(fallbackSpeedFixture(`{"schema_version":"unknown/v1","benchmarks":[{"status":"available","download_mbps":12}]}`)), true, false},
		{"missing-payload-schema", string(fallbackSpeedFixture(`{"benchmarks":[{"status":"available","download_mbps":12}]}`)), true, false},
		{"null-payload", string(fallbackSpeedFixture(`null`)), true, false},
		{"empty-payload", string(fallbackSpeedFixture(`{}`)), true, false},
		{"missing-results", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1"}`)), true, false},
		{"null-results", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":null,"private_benchmarks":null}`)), true, false},
		{"empty-results", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[],"private_benchmarks":[]}`)), true, false},
		{"error-only", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"timeout","error":"deadline exceeded","latency_ms":459.481715}],"private_benchmarks":[{"status":"unavailable","error":"timeout"}]}`)), true, false},
		{"candidate-latency", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","nodes":[{"availability":"candidate","latency_ms":2000,"download_mbps":12}],"selected":[{"latency_ms":2000}]}`)), true, false},
		{"null-values", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"available","download_mbps":null,"upload_mbps":null}]}`)), true, false},
		{"zero-negative", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"available","download_mbps":0,"upload_mbps":-1}]}`)), true, false},
		{"numeric-string", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"available","download_mbps":"12"}]}`)), true, false},
		{"failed-with-values", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"timeout","download_mbps":12}]}`)), true, false},
		{"no-row-status", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"download_mbps":12}]}`)), true, false},
		{"partial-seven-of-nineteen", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"unavailable","error":"ping failed"},{"name":"Tokyo","status":"available","download_mbps":36.249934956018656,"upload_mbps":15.541677984168787}]}`)), true, true},
		{"private-only", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","private_benchmarks":[{"status":"available","download_mbps":570.1359730508952,"upload_mbps":413.1554059789048}]}`)), true, true},
		{"upload-only", string(fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"ok","upload_mbps":1}]}`)), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, measured := checkupFusionSpeedMeasured(json.RawMessage(tc.data))
			if parsed != tc.parsed || measured != tc.measured {
				t.Fatalf("parsed=%v measured=%v, want %v/%v", parsed, measured, tc.parsed, tc.measured)
			}
		})
	}
}

// Exercise the actual diagnostics script/parser contract through bounded SSH,
// without running a shell or contacting either public speed provider.
type speedFallbackRunner struct {
	*checkupRunner
	t               *testing.T
	n               model.Node
	calls, requests int
	deadline        time.Time
	before          func()
	cancel          context.CancelFunc
}

func (r *speedFallbackRunner) RunBounded(ctx context.Context, n model.Node, script string, input []byte, limit int) (string, error) {
	r.t.Helper()
	r.calls++
	if n != r.n || limit != diagnostics.MaxOutputBytes {
		r.t.Fatalf("node identity/output bound changed: %+v limit=%d", n, limit)
	}
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(r.deadline) || time.Until(deadline) > 10*time.Second {
		r.t.Fatalf("request exceeded shared Fusion/outer deadline: %v max=%v", deadline, r.deadline)
	}
	if r.before != nil {
		r.before()
	}
	if r.cancel != nil {
		r.cancel()
		return "", ctx.Err()
	}
	if strings.Contains(script, "command -v curl") {
		if strings.Contains(script, "command -v ping") || len(input) != 0 {
			r.t.Fatal("fallback performed a non-speed probe")
		}
		return "curl=yes\n", nil
	}
	r.requests++
	if !strings.Contains(script, "--max-time 10") || !strings.Contains(script, "--max-redirs 0") || !strings.Contains(script, "MPX_METRICS") || !strings.Contains(script, "ulimit -f") {
		r.t.Fatal("fallback request missing bounded diagnostics transport")
	}
	if strings.Contains(script, "speed.cloudflare.com") == strings.Contains(script, "la.speedtest.clouvider.net") {
		r.t.Fatal("fallback used an unexpected speed destination")
	}
	if len(input) == 0 {
		if !strings.Contains(script, "--max-filesize 1048576") || (!strings.Contains(script, "bytes=1048576") && !strings.Contains(script, "ckSize=1")) {
			r.t.Fatal("download payload is not capped at one MiB")
		}
		return "MPX_METRICS 200 1 1048576 0\nMPX_EXIT 0\n", nil
	}
	if len(input) != 1<<20 || !strings.Contains(script, "--request POST") || !strings.Contains(script, "--max-filesize 1024") {
		r.t.Fatal("upload payload/response cap changed")
	}
	return "MPX_METRICS 200 1 0 1048576\nMPX_EXIT 0\n", nil
}

func TestFusionSpeedFallbackIndependentReportAndCapturedIdentity(t *testing.T) {
	base := &checkupRunner{}
	c, n := newCheckupControl(t, base)
	started := time.Now().Add(-897 * time.Second)
	result := fusion.Report{Engine: "fusion", Version: fusion.Version, Status: "timeout", Speed: true, TimeoutSeconds: 900, StartedAt: started, Error: "original speed deadline", Output: "baseline text preserved", Data: fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","benchmarks":[{"status":"timeout","error":"deadline exceeded"}]}`)}
	original, _ := json.Marshal(result)
	report := model.NodeCheckupReport{Fusion: original, Drift: "match", Health: model.NodeStatus{NodeID: n.ID, Active: true}}
	r := &speedFallbackRunner{checkupRunner: base, t: t, n: n, deadline: started.Add(900 * time.Second)}
	c.runner = r
	old := model.DiagnosticRecord{NodeID: n.ID, Source: "node", CheckedAt: time.Now().Add(-2 * time.Minute), Report: json.RawMessage(`{"previous":true}`)}
	if err := c.store.SaveDiagnosticReport(old); err != nil {
		t.Fatal(err)
	}
	updated := false
	r.before = func() {
		if !updated {
			changed := n
			changed.Address = "changed-during-fusion.example"
			if err := c.store.UpdateNode(&changed); err != nil {
				t.Fatal(err)
			}
			updated = true
		}
		stored, err := c.store.GetDiagnosticReport(n.ID, "node")
		if err != nil || string(stored.Report) != string(old.Report) {
			t.Fatalf("intermediate report saved: %+v %v", stored, err)
		}
		if _, err := c.store.ListNodes(); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.checkupFusionSpeedFallback(t.Context(), n, result, &report); err != nil {
		t.Fatal(err)
	}
	var network diagnostics.Report
	if err := json.Unmarshal(report.Network, &network); err != nil {
		t.Fatal(err)
	}
	if r.calls != 5 || r.requests != 4 || len(network.Speed) != 4 || network.NodeID != strconv.FormatInt(n.ID, 10) || network.Position != "node" || len(network.Targets) != 0 || len(network.IP) != 0 || network.Budget.MaxBytes != 8<<20 || network.Budget.ReservedBytes > 8<<20 || network.Budget.TransferredBytes != 4<<20 {
		t.Fatalf("fallback contract changed: calls=%d requests=%d report=%+v", r.calls, r.requests, network)
	}
	for _, speed := range network.Speed {
		if speed.Status.Status != "ok" || speed.Bytes != 1<<20 || speed.Mbps != 8.388608 {
			t.Fatalf("speed parser result=%+v", speed)
		}
	}
	if string(report.Fusion) != string(original) || report.Drift != "match" || !report.Health.Active || !strings.Contains(strings.Join(report.Summary, " "), "独立速度补测") || !strings.Contains(strings.Join(report.Summary, " "), "融合怪缺少可用测速结果") {
		t.Fatalf("official/baseline evidence replaced: %+v", report)
	}
}

func TestFusionSpeedFallbackSkipsUsableUnparsedCanceledAndExhausted(t *testing.T) {
	for _, mode := range []string{"valid", "unparsed", "canceled", "expired", "zero-start"} {
		t.Run(mode, func(t *testing.T) {
			base := &checkupRunner{}
			c, n := newCheckupControl(t, base)
			result := fusion.Report{StartedAt: time.Now(), TimeoutSeconds: 900, Data: fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1"}`)}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "valid":
				result.Data = fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1","private_benchmarks":[{"status":"available","upload_mbps":413.1554059789048}]}`)
			case "unparsed":
				result.Data = nil
			case "canceled":
				cancel()
			case "expired":
				result.StartedAt = time.Now().Add(-901 * time.Second)
			case "zero-start":
				result.StartedAt = time.Time{}
			}
			r := &speedFallbackRunner{checkupRunner: base, t: t, n: n}
			c.runner = r
			report := model.NodeCheckupReport{}
			if err := c.checkupFusionSpeedFallback(ctx, n, result, &report); err != nil || r.calls != 0 || len(report.Network) != 0 {
				t.Fatalf("skip failed: calls=%d report=%+v err=%v", r.calls, report, err)
			}
			if (mode == "expired" || mode == "zero-start") && !strings.Contains(strings.Join(report.Summary, " "), "时间预算已耗尽") {
				t.Fatal("exhausted budget not explained")
			}
		})
	}
}

func TestFusionSpeedFallbackParentDeadlineAndCancelRetainsPartial(t *testing.T) {
	base := &checkupRunner{}
	c, n := newCheckupControl(t, base)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	result := fusion.Report{StartedAt: time.Now(), Data: fallbackSpeedFixture(`{"schema_version":"goecs.speed/v1"}`)} // zero timeout uses the existing 900-second default.
	original, _ := json.Marshal(result)
	report := model.NodeCheckupReport{Fusion: original}
	r := &speedFallbackRunner{checkupRunner: base, t: t, n: n, deadline: deadline, cancel: cancel}
	c.runner = r
	err := c.checkupFusionSpeedFallback(ctx, n, result, &report)
	if !errors.Is(err, context.Canceled) || r.calls != 1 || !json.Valid(report.Network) || string(report.Fusion) != string(original) {
		t.Fatalf("cancellation partial lost: calls=%d report=%+v err=%v", r.calls, report, err)
	}
}

func TestFusionCheckupSpeedFalseNeverStartsFallback(t *testing.T) {
	base := &checkupRunner{}
	c, n := newCheckupControl(t, base)
	c.runner = &fusionCheckupRunner{checkupRunner: base, platformErr: fmt.Errorf("controlled failure")}
	record, err := c.CheckupNode(t.Context(), n, diagnostics.Config{Speed: false}, time.Now())
	if err == nil {
		t.Fatal("original Fusion failure disappeared")
	}
	report := decodeCheckup(t, record)
	if len(report.Network) != 0 && string(report.Network) != "null" {
		t.Fatalf("speed=false ran fallback: %s", report.Network)
	}
	if strings.Join(base.stages, ",") != "health,fusion" {
		t.Fatalf("unexpected work: %v", base.stages)
	}
}
