package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/fusion"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/server"
	"github.com/RovenEverward/my-proxy/internal/store"
	"github.com/spf13/cobra"
)

// Exercise the production HTTP encoder and CLI reader together. A test server
// that encodes its own response would bypass the indentation bug entirely.
type responseDiagnosticOps struct {
	server.NodeOps
	st    *store.Store
	err   error
	calls atomic.Int64
}

func (o *responseDiagnosticOps) CheckupNode(_ context.Context, n model.Node, cfg diagnostics.Config, _ time.Time) (model.DiagnosticRecord, error) {
	o.calls.Add(1)
	record, err := o.st.GetDiagnosticReport(n.ID, cfg.Position)
	if err != nil {
		return model.DiagnosticRecord{}, err
	}
	return *record, o.err
}

func responseDiagnosticCmd() *cobra.Command {
	cmd := newNodeDiagnoseCmd()
	cmd.SetContext(context.Background())
	return cmd
}

func diagnosticResponseServer(t *testing.T, report json.RawMessage, runErr error) (*httptest.Server, *responseDiagnosticOps, []model.DiagnosticRecord) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "diagnostics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	records := make([]model.DiagnosticRecord, 0, 4)
	for i := 0; i < 4; i++ {
		n := model.Node{Name: fmt.Sprintf("stored-node-%d", i+1), Address: fmt.Sprintf("node-%d.example", i+1)}
		if err := st.CreateNode(&n); err != nil {
			t.Fatal(err)
		}
		record := model.DiagnosticRecord{NodeID: n.ID, MeasuredAddress: n.Address, Source: "node", CheckedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Report: report}
		if err := st.SaveDiagnosticReport(record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	ops := &responseDiagnosticOps{st: st, err: runErr}
	s := server.New(st, "diagnostic-response-token", nil)
	s.SetNodeOps(ops)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "diagnostic-response-token")
	return srv, ops, records
}

func largeDiagnosticResponseFixture(t *testing.T) json.RawMessage {
	t.Helper()
	media := make([]any, 360)
	for i := range media {
		media[i] = map[string]any{"name": fmt.Sprintf("Media service %03d", i), "status": "Yes", "ip_version": "ipv6", "region": "US", "error": "", "latency_ms": 12.345, "http_status": 200, "details": map[string]any{"exit_ip": "2001:db8::1", "available": true}}
	}
	mail := make([]any, 205)
	for i := range mail {
		mail[i] = map[string]any{"host": fmt.Sprintf("mx-%03d.example.com", i), "port": 25, "platform": "Fixture mail", "protocol": "smtp", "tls": false, "status": "open", "error": "", "latency_ms": 7.25}
	}
	tcp := make([]any, 93)
	for i := range tcp {
		tcp[i] = map[string]any{"target": map[string]any{"province_code": fmt.Sprint(i / 3), "province_name": fmt.Sprintf("ISP target %03d", i), "carrier": []string{"ct", "cu", "cm"}[i%3], "host": "203.0.113.8", "ip_version": "ipv4"}, "port": 443, "attempts": 2, "successful": 2, "failed": 0, "loss_percent": 0, "success_rate": 100, "mean": 13000000, "min": 12500000, "max": 13500000, "p50": 13000000, "p95": 13500000}
	}
	data, err := json.Marshal(map[string]any{"schema_version": "goecs.report/v1", "ecs_version": fusion.Version, "counter": json.Number("9007199254740993"), "components": []any{
		map[string]any{"name": "unlocktests.media", "schema_version": "goecs.unlocktests/media-v1", "status": "ok", "payload": map[string]any{"results": media}},
		map[string]any{"name": "portchecker.email", "schema_version": "goecs.portchecker/mail-v1", "status": "ok", "payload": map[string]any{"fixed": mail[:132], "local": mail[132:138], "mx": mail[138:183], "outbound_smtp25": mail[183:]}},
		map[string]any{"name": "nt3.province_latency", "schema_version": "goecs.nt3/province-latency-v1", "status": "ok", "payload": tcp},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f := fusion.NewReport(fusion.Config{Speed: true, TimeoutSeconds: 900})
	f.Status, f.Data, f.Output = "partial", data, "Synthetic upstream evidence: preserved through HTTP and stdout\n"
	f.Error = "fixture module failed"
	f.StartedAt, f.FinishedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC)
	fusionRaw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	report, err := json.Marshal(model.NodeCheckupReport{Summary: []string{"stored large checkup"}, Health: model.NodeStatus{Reachable: true, Active: true}, Drift: "match", Fusion: fusionRaw})
	if err != nil {
		t.Fatal(err)
	}
	if len(report) > 128<<10 {
		t.Fatalf("fixture exceeds 128 KiB: %d", len(report))
	}
	pretty, err := json.MarshalIndent(map[string]any{"error": "fixture module failed", "record": model.DiagnosticRecord{NodeID: 1, Source: "node", Report: report}}, "", "  ")
	if err != nil || len(pretty) <= (256<<10)+4096 {
		t.Fatalf("fixture does not reproduce oversized pretty response: report=%d pretty=%d err=%v", len(report), len(pretty), err)
	}
	t.Logf("synthetic report=%d pretty error+record=%d media=360 mail=205 TCP=93", len(report), len(pretty))
	return report
}

func assertDiagnosticResponseRecord(t *testing.T, got, want model.DiagnosticRecord) {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, got.Report); err != nil {
		t.Fatal(err)
	}
	if got.NodeID != want.NodeID || got.MeasuredAddress != want.MeasuredAddress || got.Source != want.Source || !got.CheckedAt.Equal(want.CheckedAt) || !bytes.Equal(compact.Bytes(), want.Report) {
		t.Fatalf("diagnostic record changed: node=%d report bytes=%d, want node=%d bytes=%d", got.NodeID, compact.Len(), want.NodeID, len(want.Report))
	}
}

func TestDiagnosticResponseLargeHandlerAndCLI(t *testing.T) {
	report := largeDiagnosticResponseFixture(t)
	for _, runErr := range []error{nil, errors.New("fixture module failed")} {
		name := "success"
		if runErr != nil {
			name = "partial502"
		}
		t.Run(name, func(t *testing.T) {
			srv, ops, records := diagnosticResponseServer(t, report, runErr)
			c := &Client{server: srv.URL, token: "diagnostic-response-token", http: srv.Client()}
			// GET recovers all four stored reports without executing diagnostics.
			for i, record := range records {
				var raw []byte
				var err error
				if i == len(records)-1 {
					raw, err = c.do(http.MethodGet, fmt.Sprintf("/api/nodes/%d/diagnostics", record.NodeID), nil)
					if len(raw) > (256<<10)+4096 {
						t.Fatalf("latest wire response exceeds cap: %d", len(raw))
					}
					t.Logf("latest wire bytes=%d", len(raw))
				} else {
					var out string
					out, err = runDiagnosticRoot(t, "node", "diagnostics", fmt.Sprint(record.NodeID), "--output", "json")
					raw = []byte(out)
				}
				var got model.DiagnosticRecord
				if err != nil || json.Unmarshal(raw, &got) != nil {
					t.Fatalf("latest record %d: err=%v stdout bytes=%d", record.NodeID, err, len(raw))
				}
				assertDiagnosticResponseRecord(t, got, record)
			}
			if ops.calls.Load() != 0 {
				t.Fatal("reading stored reports executed diagnostics")
			}
			raw, err := diagnosticRequest(responseDiagnosticCmd(), c, "/api/nodes/1/diagnostics", diagnosticPayload{Config: diagnostics.Config{Engine: "fusion", Position: "node"}})
			if len(raw) == 0 || len(raw) > (256<<10)+4096 {
				t.Fatalf("bounded response bytes=%d err=%v", len(raw), err)
			}
			t.Logf("POST wire bytes=%d partial=%t", len(raw), runErr != nil)
			var got model.DiagnosticRecord
			if runErr == nil {
				if err != nil || json.Unmarshal(raw, &got) != nil {
					t.Fatalf("POST record: %v", err)
				}
			} else {
				var apiErr *APIError
				var partial struct {
					Error  string                 `json:"error"`
					Record model.DiagnosticRecord `json:"record"`
				}
				if !errors.As(err, &apiErr) || apiErr.StatusCode != 502 || json.Unmarshal(raw, &partial) != nil || partial.Error != runErr.Error() {
					t.Fatalf("partial POST: bytes=%d err=%v", len(raw), err)
				}
				got = partial.Record
			}
			assertDiagnosticResponseRecord(t, got, records[0])
			out, err := runDiagnosticRoot(t, "node", "diagnose", "--all", "--output", "json")
			var results []diagnosticAllResult
			if json.Unmarshal([]byte(out), &results) != nil || len(results) != 4 {
				t.Fatalf("all reports: err=%v stdout bytes=%d", err, len(out))
			}
			if (runErr == nil && err != nil) || (runErr != nil && (err == nil || err.Error() != "4/4 个节点体检失败")) {
				t.Fatalf("all exit status: %v", err)
			}
			for i, item := range results {
				if item.Record == nil || (runErr != nil && item.Error != runErr.Error()) || (runErr == nil && item.Error != "") {
					t.Fatalf("all node %d lost partial evidence or error", item.NodeID)
				}
				assertDiagnosticResponseRecord(t, *item.Record, records[i])
			}
			if ops.calls.Load() != 5 {
				t.Fatalf("diagnostic executions=%d, want 5 without retry", ops.calls.Load())
			}
		})
	}
}

func TestDiagnosticResponseHTMLAtStorageLimit(t *testing.T) {
	// The store accepts raw JSON, including unescaped keys, '<', '>' and '&'.
	// Default HTML escaping expands this legal UTF-8 report several times over.
	text := strings.Repeat("<> &汉", 37000)
	text += strings.Repeat("x", (256<<10)-len(text)-len(`{"<property>&":""}`))
	report := json.RawMessage(`{"<property>&":"` + text + `"}`)
	if len(report) > 256<<10 || !json.Valid(report) {
		t.Fatalf("invalid storage-limit fixture: %d", len(report))
	}
	escaped, err := json.Marshal(model.DiagnosticRecord{NodeID: 1, Source: "node", Report: report})
	if err != nil || len(escaped) <= (256<<10)+4096 {
		t.Fatalf("fixture did not exceed wire cap with HTML escaping: %d %v", len(escaped), err)
	}
	for _, runErr := range []error{nil, errors.New("<partial>& failure")} {
		srv, _, records := diagnosticResponseServer(t, report, runErr)
		c := &Client{server: srv.URL, token: "diagnostic-response-token", http: srv.Client()}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			var raw []byte
			if method == http.MethodGet {
				raw, err = c.do(method, "/api/nodes/1/diagnostics", nil)
			} else {
				raw, err = diagnosticRequest(responseDiagnosticCmd(), c, "/api/nodes/1/diagnostics", diagnosticPayload{Config: diagnostics.Config{Engine: "fusion", Position: "node"}})
			}
			if len(raw) == 0 || len(raw) > (256<<10)+4096 || !bytes.Contains(raw, []byte(`"<property>&"`)) {
				t.Fatalf("%s raw report expanded: bytes=%d err=%v", method, len(raw), err)
			}
			var got model.DiagnosticRecord
			if method == http.MethodPost && runErr != nil {
				var partial struct {
					Record model.DiagnosticRecord `json:"record"`
				}
				if err == nil || err.Error() != runErr.Error() || json.Unmarshal(raw, &partial) != nil {
					t.Fatalf("partial HTML response: %v", err)
				}
				got = partial.Record
			} else if err != nil || json.Unmarshal(raw, &got) != nil {
				t.Fatalf("HTML response: %v", err)
			}
			assertDiagnosticResponseRecord(t, got, records[0])
		}
		t.Logf("HTML raw report=%d default escaped=%d", len(report), len(escaped))
	}
}

func TestDiagnosticResponseCLIBinaryStdout(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mpx")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/mpx")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	report := largeDiagnosticResponseFixture(t)
	for _, runErr := range []error{nil, errors.New("fixture module failed")} {
		srv, _, records := diagnosticResponseServer(t, report, runErr)
		for _, format := range []string{"json", "table"} {
			command := exec.Command(binary, "--server", srv.URL, "--token", "diagnostic-response-token", "node", "diagnose", "--all", "--output", format)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if (runErr == nil && (err != nil || stderr.Len() != 0)) || (runErr != nil && (err == nil || !strings.Contains(stderr.String(), "4/4 个节点体检失败"))) {
				t.Fatalf("binary format=%s status=%v stderr=%s stdout bytes=%d", format, err, stderr.String(), stdout.Len())
			}
			if format == "json" {
				var results []diagnosticAllResult
				if json.Unmarshal(stdout.Bytes(), &results) != nil || len(results) != 4 {
					t.Fatalf("invalid binary JSON stdout: %d bytes", stdout.Len())
				}
				for i, result := range results {
					if result.Record == nil {
						t.Fatal("missing binary record")
					}
					assertDiagnosticResponseRecord(t, *result.Record, records[i])
				}
			} else {
				for _, text := range []string{"stored-node-4", "Synthetic upstream evidence", "Media service 359", "mx-204.example.com", "ISP target 092"} {
					if !strings.Contains(stdout.String(), text) {
						t.Fatalf("table stdout missing %q", text)
					}
				}
			}
		}
	}
}

func TestDiagnosticResponseCLIRetainsTransportCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", (256<<10)+4097))
	}))
	defer srv.Close()
	c := &Client{server: srv.URL, http: srv.Client()}
	raw, err := diagnosticRequest(responseDiagnosticCmd(), c, "/api/nodes/1/diagnostics", diagnosticPayload{})
	if len(raw) != 0 || err == nil || err.Error() != "诊断响应超过 260 KiB" {
		t.Fatalf("oversized response accepted: bytes=%d err=%v", len(raw), err)
	}
}
