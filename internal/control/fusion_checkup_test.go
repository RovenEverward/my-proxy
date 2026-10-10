package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/fusion"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// Stop before LoadExecutable so these integration tests cannot download or run
// the official binary. All other stages use the existing bounded fake transport.
type fusionCheckupRunner struct {
	*checkupRunner
	platformErr  error
	fusionBudget time.Duration
}

func (r *fusionCheckupRunner) RunBounded(ctx context.Context, n model.Node, script string, input []byte, limit int) (string, error) {
	if strings.Contains(script, "uname -m") {
		if limit != 1024 || len(input) != 0 || !strings.Contains(script, "uname -s") {
			return "", fmt.Errorf("unexpected Fusion platform transport")
		}
		if deadline, ok := ctx.Deadline(); ok {
			r.fusionBudget = time.Until(deadline)
		}
		if err := r.stage(ctx, n, "fusion", nil); err != nil {
			return "", err
		}
		return "unsupported-test-architecture\n", r.platformErr
	}
	return r.checkupRunner.RunBounded(ctx, n, script, input, limit)
}

func TestFusionCheckupDefaultKeepsHealthAndActualEntrancesAfterPlatformFailure(t *testing.T) {
	for _, platformErr := range []error{nil, errors.New("deliberate platform failure")} {
		name := "unsupported-architecture"
		if platformErr != nil {
			name = "platform-error"
		}
		t.Run(name, func(t *testing.T) {
			base := &checkupRunner{}
			c, n := newCheckupControl(t, base)
			r := &fusionCheckupRunner{checkupRunner: base, platformErr: platformErr}
			c.runner = r
			in := addInbound(t, c.store, n.ID, "explicit", 443)
			h := model.Host{InboundID: in.ID, Address: "actual-entry.example", Remark: "actual entry"}
			if err := c.store.CreateHost(&h); err != nil {
				t.Fatal(err)
			}
			defaultIn := addInbound(t, c.store, n.ID, "default", 8443)
			user := addUser(t, c.store, activeUser("fusion-checkup"))
			now := time.Now().UTC().Truncate(time.Second)
			old := model.DiagnosticRecord{NodeID: n.ID, Source: "node", CheckedAt: now.Add(-2 * time.Minute), Report: json.RawMessage(`{"previous":true}`)}
			if err := c.store.SaveDiagnosticReport(old); err != nil {
				t.Fatal(err)
			}
			base.beforeEntrance = func() {
				current, err := c.store.GetDiagnosticReport(n.ID, "node")
				if err != nil || string(current.Report) != string(old.Report) {
					t.Fatalf("Fusion wrote an intermediate report: %+v %v", current, err)
				}
			}
			n.Address = "stale.example"
			record, err := c.CheckupNode(context.Background(), n, diagnostics.Config{Speed: true, Hardware: true}, now)
			if err == nil || (platformErr != nil && !errors.Is(err, platformErr)) {
				t.Fatalf("Fusion platform failure lost: %v", err)
			}
			report := decodeCheckup(t, record)
			var evaluation fusion.Report
			if err := json.Unmarshal(report.Fusion, &evaluation); err != nil {
				t.Fatal(err)
			}
			if evaluation.Engine != "fusion" || evaluation.Version != fusion.Version || evaluation.Status != "failed" || evaluation.Error == "" || !evaluation.Speed || !evaluation.Hardware || evaluation.TimeoutSeconds != fusion.DefaultTimeoutSeconds {
				t.Fatalf("default Fusion result=%+v", evaluation)
			}
			if (len(report.Network) != 0 && string(report.Network) != "null") || strings.Join(base.stages, ",") != "health,fusion,entrance,entrance" {
				t.Fatalf("default fell back to light: network=%s stages=%v", report.Network, base.stages)
			}
			if !report.Health.Active || report.Drift != "changed" || record.Source != "node" || record.MeasuredAddress != "stored.example" {
				t.Fatalf("health/current identity lost: record=%+v report=%+v", record, report)
			}
			if len(report.Entrances) != 2 || report.Entrances[0].HostID != h.ID || report.Entrances[1].InboundID != defaultIn.ID || report.Entrances[1].HostID != 0 {
				t.Fatalf("actual entrances lost: %+v", report.Entrances)
			}
			for i, entry := range report.Entrances {
				address := []string{h.Address, "stored.example"}[i]
				if entry.Error != "" || len(entry.Results) != len(model.CapabilityTargets) || !strings.Contains(string(base.inputs[i]), address) || !strings.Contains(string(base.inputs[i]), model.DeriveUUID(user.Key)) {
					t.Fatalf("entrance was not actually probed: %+v", entry)
				}
			}
			for _, address := range base.addresses {
				if address != "stored.example" {
					t.Fatalf("stale SSH identity=%s", address)
				}
			}
			if (diagnostics.Config{}).CheckupTimeout() != 18*time.Minute || (diagnostics.Config{FusionTimeoutSeconds: 1800}).CheckupTimeout() != 33*time.Minute || r.fusionBudget <= 15*time.Minute-time.Second || r.fusionBudget > 15*time.Minute {
				t.Fatalf("default/max full budget drifted or Fusion=%s", r.fusionBudget)
			}
			stored, err := c.store.GetDiagnosticReport(n.ID, "node")
			if err != nil || string(stored.Report) != string(record.Report) || !stored.CheckedAt.Equal(now) {
				t.Fatalf("final partial report missing: %+v %v", stored, err)
			}
			before := len(base.stages)
			for _, cfg := range []diagnostics.Config{{}, {Engine: "light"}} {
				if _, err := c.CheckupNode(context.Background(), n, cfg, now.Add(59*time.Second)); !errors.Is(err, ErrDiagnosticRateLimit) {
					t.Fatalf("partial bypassed shared engine rate: %v", err)
				}
			}
			if _, err := c.DiagnoseNode(context.Background(), n, diagnostics.Config{Engine: "light"}, now.Add(59*time.Second)); !errors.Is(err, ErrDiagnosticRateLimit) {
				t.Fatalf("network-only bypassed partial report rate: %v", err)
			}
			if len(base.stages) != before || len(c.diagnosticSlot) != 0 || len(c.capabilitySlot) != 0 {
				t.Fatal("rate-limited call executed or slot leaked")
			}
		})
	}
}

func TestFusionCheckupCancellationSavesPartialAndReleasesSharedSlot(t *testing.T) {
	for _, stage := range []string{"fusion", "entrance"} {
		t.Run(stage, func(t *testing.T) {
			base := &checkupRunner{block: stage, started: make(chan struct{})}
			c, n := newCheckupControl(t, base)
			r := &fusionCheckupRunner{checkupRunner: base}
			c.runner = r
			addInbound(t, c.store, n.ID, "first", 443)
			addInbound(t, c.store, n.ID, "remaining", 8443)
			addUser(t, c.store, activeUser("fusion-cancel"))
			now := time.Now().UTC().Truncate(time.Second)
			old := model.DiagnosticRecord{NodeID: n.ID, Source: "node", CheckedAt: now.Add(-2 * time.Minute), Report: json.RawMessage(`{"previous":true}`)}
			if err := c.store.SaveDiagnosticReport(old); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := c.CheckupNode(ctx, n, diagnostics.Config{}, now)
				done <- err
			}()
			select {
			case <-base.started:
			case <-time.After(time.Second):
				t.Fatal("Fusion checkup did not enter blocked stage")
			}
			for _, cfg := range []diagnostics.Config{{}, {Engine: "light"}} {
				if _, err := c.CheckupNode(context.Background(), n, cfg, now); !errors.Is(err, ErrDiagnosticBusy) {
					t.Fatalf("engine bypassed shared slot: %v", err)
				}
			}
			if _, err := c.DiagnoseNode(context.Background(), n, diagnostics.Config{Engine: "light"}, now); !errors.Is(err, ErrDiagnosticBusy) {
				t.Fatalf("network-only bypassed shared slot: %v", err)
			}
			current, err := c.store.GetDiagnosticReport(n.ID, "node")
			if err != nil || string(current.Report) != string(old.Report) {
				t.Fatalf("intermediate report saved: %+v %v", current, err)
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Fusion cancellation ignored")
			}
			partial, err := c.store.GetDiagnosticReport(n.ID, "node")
			if err != nil {
				t.Fatal(err)
			}
			report := decodeCheckup(t, *partial)
			if len(report.Fusion) == 0 || (len(report.Network) != 0 && string(report.Network) != "null") || !report.Health.Active || len(report.Entrances) != 2 || report.Entrances[1].Error == "" || !strings.Contains(strings.Join(report.Summary, " "), "体检中断") {
				t.Fatalf("canceled partial lost evidence: %+v", report)
			}
			if len(c.diagnosticSlot) != 0 || len(c.capabilitySlot) != 0 {
				t.Fatal("cancel leaked shared slot")
			}
			if _, err := c.CheckupNode(context.Background(), n, diagnostics.Config{}, now.Add(59*time.Second)); !errors.Is(err, ErrDiagnosticRateLimit) {
				t.Fatalf("canceled partial bypassed rate: %v", err)
			}
			base.block = ""
			base.beforeEntrance = nil
			if record, err := c.CheckupNode(context.Background(), n, diagnostics.Config{}, now.Add(time.Minute)); errors.Is(err, ErrDiagnosticBusy) || errors.Is(err, ErrDiagnosticRateLimit) || !json.Valid(record.Report) {
				t.Fatalf("released slot unusable: %v", err)
			}
		})
	}
}

func TestFusionCheckupEscapedOutputTrimKeepsHealthEntrancesAndData(t *testing.T) {
	// HTML escaping expands each byte to six bytes while the original text still
	// fits Fusion's own 64 KiB budget. Only that raw text needs to be shortened.
	output := strings.Repeat("<>&", fusion.MaxOutputBytes/3-10) + "latest evidence"
	evaluation := fusion.Report{Engine: "fusion", Version: fusion.Version, Status: "done", Output: output, Data: json.RawMessage(`{"schema_version":"goecs.report/v1","status":"ok","details":{"retained":true}}`)}
	encodedFusion, err := json.Marshal(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	report := model.NodeCheckupReport{
		Summary:   []string{"health and entries complete"},
		Drift:     "changed",
		Health:    model.NodeStatus{NodeID: 17, Reachable: true, Active: true, Error: strings.Repeat("health detail ", 60)},
		Fusion:    encodedFusion,
		Entrances: []model.EntranceDiagnostic{{InboundID: 19, HostID: 23, Name: strings.Repeat("entry detail ", 60), Results: []model.CapabilityResult{{Target: "youtube", ObservedStatus: model.CapabilityAvailable, Reason: strings.Repeat("probe detail ", 60)}}}},
	}
	before, err := json.Marshal(report)
	if err != nil || len(before) <= checkupReportLimit || len(output) > fusion.MaxOutputBytes {
		t.Fatalf("fixture did not exceed combined budget: raw=%d output=%d err=%v", len(before), len(output), err)
	}
	raw, err := marshalCheckupReport(report)
	if err != nil || len(raw) > checkupReportLimit || !json.Valid(raw) {
		t.Fatalf("bounded combined report: size=%d err=%v", len(raw), err)
	}
	got := decodeCheckup(t, model.DiagnosticRecord{Report: raw})
	var trimmed fusion.Report
	if err := json.Unmarshal(got.Fusion, &trimmed); err != nil {
		t.Fatal(err)
	}
	if !trimmed.Truncated || trimmed.Status != "partial" || len(trimmed.Warnings) == 0 || !strings.Contains(strings.Join(trimmed.Warnings, " "), "256 KiB") || len(trimmed.Output) >= len(output) || !strings.HasSuffix(trimmed.Output, "latest evidence") {
		t.Fatalf("Fusion truncation missing explicit evidence: %+v", trimmed)
	}
	if string(trimmed.Data) != string(evaluation.Data) {
		t.Fatalf("structured Fusion data lost: %s", trimmed.Data)
	}
	if got.Health.Error != report.Health.Error || got.Health.NodeID != report.Health.NodeID || got.Health.Active != report.Health.Active || got.Drift != report.Drift || len(got.Entrances) != 1 || got.Entrances[0].Name != report.Entrances[0].Name || got.Entrances[0].Results[0].Reason != report.Entrances[0].Results[0].Reason {
		t.Fatal("trimming Fusion text needlessly changed health or actual entrance evidence")
	}
	if len(got.Network) != 0 && string(got.Network) != "null" {
		t.Fatal("Fusion report invented light network evidence")
	}
}

func TestFusionCheckupLargeHealthTrimKeepsSmallStructuredData(t *testing.T) {
	evaluation := fusion.Report{Engine: "fusion", Version: fusion.Version, Status: "done", Data: json.RawMessage(`{"status":"ok","retained":true}`)}
	encodedFusion, err := json.Marshal(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	report := model.NodeCheckupReport{
		Summary:   []string{"health failed but upstream measurements completed"},
		Drift:     "unknown",
		Health:    model.NodeStatus{NodeID: 17, Error: strings.Repeat("E", 400<<10)},
		Fusion:    encodedFusion,
		Entrances: []model.EntranceDiagnostic{{InboundID: 19, HostID: 23, Error: "health check could not connect"}},
	}
	raw, err := marshalCheckupReport(report)
	if err != nil || len(raw) > checkupReportLimit || !json.Valid(raw) {
		t.Fatalf("large health report not bounded: size=%d err=%v", len(raw), err)
	}
	got := decodeCheckup(t, model.DiagnosticRecord{Report: raw})
	var trimmed fusion.Report
	if err := json.Unmarshal(got.Fusion, &trimmed); err != nil {
		t.Fatal(err)
	}
	if string(trimmed.Data) != string(evaluation.Data) {
		t.Fatalf("small structured Fusion measurements lost to large health error: %s", trimmed.Data)
	}
	if !trimmed.Truncated || trimmed.Status != "partial" || len(trimmed.Warnings) == 0 {
		t.Fatalf("combined truncation was not saved for empty output: %+v", trimmed)
	}
	if got.Health.NodeID != report.Health.NodeID || len(got.Health.Error) >= len(report.Health.Error) || !strings.Contains(got.Health.Error, "已截断") || len(got.Entrances) != 1 || got.Entrances[0].HostID != 23 || got.Entrances[0].Error != report.Entrances[0].Error {
		t.Fatal("bounded health verdict or entrance identity lost")
	}
}
