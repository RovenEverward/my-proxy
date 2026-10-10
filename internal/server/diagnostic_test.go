package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
)

type fakeDiagnosticOps struct {
	fakeNodeOps
	calls  int
	cfg    diagnostics.Config
	budget time.Duration
	err    error
	raw    json.RawMessage
}

func (f *fakeDiagnosticOps) DiagnoseNode(ctx context.Context, n model.Node, cfg diagnostics.Config, now time.Time) (model.DiagnosticRecord, error) {
	f.calls++
	f.cfg = cfg
	if deadline, ok := ctx.Deadline(); ok {
		f.budget = time.Until(deadline)
	}
	raw := f.raw
	if raw == nil {
		raw = json.RawMessage(`{"position":"node","warnings":["partial evidence"]}`)
	}
	return model.DiagnosticRecord{NodeID: n.ID, Source: cfg.Position, CheckedAt: now, Report: raw}, f.err
}

func TestDiagnosticAPIIdentityValidationAndDefaults(t *testing.T) {
	ops := &fakeDiagnosticOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("test", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/diagnostics", n.ID)
	for _, method := range []string{"POST", "GET"} {
		if code, _ := e.request(method, path, map[string]any{}, nil); code != 401 {
			t.Fatalf("%s auth=%d", method, code)
		}
		if code, _ := e.api(method, "/api/nodes/99999/diagnostics", map[string]any{}); code != 404 {
			t.Fatalf("%s missing=%d", method, code)
		}
		if code, _ := e.api(method, "/api/nodes/no/diagnostics", map[string]any{}); code != 400 {
			t.Fatalf("%s malformed=%d", method, code)
		}
	}
	for _, body := range []string{
		`{"position":"local"}`, `{"position":"other"}`, `{"socks_url":"socks5h://user:secret@127.0.0.1:1080"}`,
		`{"samples":6}`, `{"max_bytes":33554433}`, `{"speed_bytes":8388609}`, `{"total_timeout_seconds":181}`, `{"request_timeout_seconds":16}`,
		`{"abuse_ipdb_key":"secret"}`, `{"abuse_ipdb_key_env":"PATH"}`, `{"targets":[{"host":"bad;command"}]}`, `{} {}`, `null`,
	} {
		code, raw := e.requestRaw("POST", path, []byte(body))
		if code != http.StatusBadRequest {
			t.Errorf("%s => %d %s", body, code, raw)
		}
		if strings.Contains(string(raw), "secret") {
			t.Fatal("request secret disclosed")
		}
	}
	if ops.calls != 0 {
		t.Fatalf("invalid inputs called engine %d times", ops.calls)
	}
	code, raw := e.api("POST", path, map[string]any{"node_id": "forged", "network_only": true})
	if code != 200 {
		t.Fatalf("default %d %s", code, raw)
	}
	record := decodeJSON[model.DiagnosticRecord](t, raw)
	if record.NodeID != n.ID || record.Source != "node" || ops.cfg.NodeID != fmt.Sprint(n.ID) || ops.cfg.Position != "node" || ops.cfg.Targets != nil || ops.cfg.Speed {
		t.Fatalf("record=%+v cfg=%+v", record, ops.cfg)
	}
	if ops.budget <= 179*time.Second || ops.budget > 3*time.Minute {
		t.Fatalf("HTTP budget %s", ops.budget)
	}
	if code, raw := e.api("POST", path, map[string]any{"position": "control", "network_only": true}); code != 200 || ops.cfg.Targets != nil {
		t.Fatalf("control %d %s cfg=%+v", code, raw, ops.cfg)
	}
}

func TestDiagnosticAPILatestBySource(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("test", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/diagnostics", n.ID)
	if code, _ := e.api("GET", path, nil); code != 404 {
		t.Fatalf("missing report=%d", code)
	}
	if code, _ := e.api("GET", path+"?from=local", nil); code != 400 {
		t.Fatalf("invalid source=%d", code)
	}
	for _, source := range []string{"node", "control"} {
		record := model.DiagnosticRecord{NodeID: n.ID, Source: source, CheckedAt: time.Now(), Report: json.RawMessage(fmt.Sprintf(`{"position":%q}`, source))}
		if err := e.st.SaveDiagnosticReport(record); err != nil {
			t.Fatal(err)
		}
		code, raw := e.api("GET", path+"?from="+source, nil)
		if code != 200 {
			t.Fatalf("read %d %s", code, raw)
		}
		got := decodeJSON[model.DiagnosticRecord](t, raw)
		if got.Source != source || got.NodeID != n.ID || !json.Valid(got.Report) {
			t.Fatalf("record=%+v", got)
		}
	}
	if code, _ := e.api("POST", path, map[string]any{"network_only": true}); code != 501 {
		t.Fatalf("disconnected=%d", code)
	}
}

func TestDiagnosticAPIErrorsPreserveOnlyValidPartialReports(t *testing.T) {
	ops := &fakeDiagnosticOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("test", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/diagnostics", n.ID)
	for _, tc := range []struct {
		err  error
		code int
	}{
		{fmt.Errorf("wrapped: %w", control.ErrDiagnosticBusy), 409},
		{fmt.Errorf("wrapped: %w", control.ErrDiagnosticRateLimit), 429},
		{context.Canceled, 502}, {errors.New("execution failed"), 502},
	} {
		ops.err = tc.err
		code, raw := e.api("POST", path, map[string]any{"network_only": true})
		view := decodeJSON[struct {
			Error  string                  `json:"error"`
			Record *model.DiagnosticRecord `json:"record"`
		}](t, raw)
		if code != tc.code || view.Error == "" || view.Record == nil || !json.Valid(view.Record.Report) {
			t.Fatalf("error %d %s", code, raw)
		}
	}
	ops.raw = json.RawMessage(`invalid`)
	code, raw := e.api("POST", path, map[string]any{"network_only": true})
	if code != 502 || strings.Contains(string(raw), `"record"`) {
		t.Fatalf("invalid partial %d %s", code, raw)
	}
	ops.err = nil
	code, raw = e.api("POST", path, map[string]any{"network_only": true})
	if code != 502 {
		t.Fatalf("invalid successful report %d %s", code, raw)
	}
}

type fakeCheckupOps struct {
	fakeDiagnosticOps
	checkups int
}

func (f *fakeCheckupOps) CheckupNode(ctx context.Context, n model.Node, cfg diagnostics.Config, now time.Time) (model.DiagnosticRecord, error) {
	f.checkups++
	return f.DiagnoseNode(ctx, n, cfg, now)
}
func TestDiagnosticAPIDefaultCompleteCheckup(t *testing.T) {
	ops := &fakeCheckupOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("checkup", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/diagnostics", n.ID)
	code, raw := e.api("POST", path, map[string]any{})
	if code != 200 || ops.checkups != 1 || !ops.cfg.Speed || ops.cfg.Engine != "fusion" || ops.cfg.FusionTimeoutSeconds != 0 || ops.budget < 1109*time.Second || ops.budget > 1110*time.Second {
		t.Fatalf("full %d %s ops=%+v", code, raw, ops)
	}
	code, raw = e.api("POST", path, map[string]any{"speed": false})
	if code != 200 || ops.cfg.Speed || ops.checkups != 2 {
		t.Fatalf("speed false %d %s cfg=%+v", code, raw, ops.cfg)
	}
	code, raw = e.api("POST", path, map[string]any{"network_only": true})
	if code != 200 || ops.cfg.Engine != "light" || ops.cfg.Speed || ops.checkups != 2 || ops.budget > 180*time.Second {
		t.Fatalf("network %d %s ops=%+v", code, raw, ops)
	}
	for _, tc := range []struct {
		body   map[string]any
		engine string
		budget time.Duration
	}{
		{map[string]any{"position": "control"}, "light", 390 * time.Second},
		{map[string]any{"engine": "light"}, "light", 390 * time.Second},
		{map[string]any{"engine": "fusion", "hardware": true, "fusion_timeout_seconds": 1800}, "fusion", 2010 * time.Second},
		{map[string]any{"engine": "fusion", "fusion_timeout_seconds": 60}, "fusion", 270 * time.Second},
	} {
		code, raw = e.api("POST", path, tc.body)
		if code != 200 || ops.cfg.Engine != tc.engine || ops.budget <= tc.budget-time.Second || ops.budget > tc.budget {
			t.Fatalf("body=%v code=%d raw=%s cfg=%+v budget=%s", tc.body, code, raw, ops.cfg, ops.budget)
		}
		if hardware, ok := tc.body["hardware"].(bool); ok && ops.cfg.Hardware != hardware {
			t.Fatalf("hardware lost: %+v", ops.cfg)
		}
	}
	legacy := newEnvWith(t, &fakeDiagnosticOps{})
	legacyNode := legacy.createNode("legacy", "node.example.com")
	if code, _ := legacy.api("POST", fmt.Sprintf("/api/nodes/%d/diagnostics", legacyNode.ID), map[string]any{}); code != 501 {
		t.Fatalf("legacy default %d", code)
	}
}

func TestDiagnosticAPIFusionOptionsRejectedBeforeExecution(t *testing.T) {
	ops := &fakeCheckupOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("checkup", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/diagnostics", n.ID)
	for _, body := range []string{
		`{"engine":"other"}`, `{"engine":"fusion","position":"control"}`,
		`{"engine":"fusion","fusion_timeout_seconds":59}`, `{"fusion_timeout_seconds":1801}`, `{"fusion_timeout_seconds":0}`,
		`{"engine":"light","hardware":true}`, `{"engine":"light","hardware":false}`, `{"position":"control","hardware":false}`,
		`{"engine":"light","fusion_timeout_seconds":900}`, `{"engine":"light","fusion_timeout_seconds":0}`, `{"position":"control","fusion_timeout_seconds":0}`,
		`{"network_only":true,"engine":"fusion"}`, `{"network_only":true,"hardware":false}`, `{"network_only":true,"fusion_timeout_seconds":0}`,
		`{"engine":"fusion","max_bytes":1024}`, `{"engine":"fusion","samples":1}`, `{"engine":"fusion","total_timeout_seconds":60}`,
		`{"engine":"fusion","targets":[{"name":"fixture","host":"example.com"}]}`,
	} {
		if code, raw := e.requestRaw("POST", path, []byte(body)); code != http.StatusBadRequest {
			t.Errorf("%s => %d %s", body, code, raw)
		}
	}
	if ops.calls != 0 || ops.checkups != 0 {
		t.Fatalf("invalid requests executed: %+v", ops)
	}
}
