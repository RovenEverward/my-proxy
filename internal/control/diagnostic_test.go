package control

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

type diagnosticRunner struct {
	calls    atomic.Int32
	started  chan struct{}
	block    bool
	seenNode string
}

func (r *diagnosticRunner) Run(context.Context, model.Node, string) (string, error) {
	panic("unbounded diagnostic SSH")
}
func (r *diagnosticRunner) RunWithInput(context.Context, model.Node, string, []byte) (string, error) {
	panic("unbounded diagnostic SSH stdin")
}
func (r *diagnosticRunner) RunBounded(ctx context.Context, n model.Node, script string, stdin []byte, limit int) (string, error) {
	r.calls.Add(1)
	r.seenNode = n.Address
	if limit != diagnostics.MaxOutputBytes {
		panic("incorrect diagnostic limit")
	}
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	if r.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if strings.Contains(script, "command -v curl") {
		return "curl=no\nping=no\nmtr=no\ntraceroute=no\ngetent=no\nnslookup=no\n", nil
	}
	return "", nil
}
func newDiagnosticControl(t *testing.T, r *diagnosticRunner) (*Control, model.Node) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	n := model.Node{Name: "diagnostic", Address: "current.example"}
	if err := st.CreateNode(&n); err != nil {
		t.Fatal(err)
	}
	return New(st, r, slog.Default(), t.TempDir()), n
}
func TestDiagnoseNodeBoundsLatestRateAndStoredIdentity(t *testing.T) {
	runner := &diagnosticRunner{}
	c, n := newDiagnosticControl(t, runner)
	n.Address = "stale.example"
	cfg := diagnostics.Config{Position: "node", Targets: []diagnostics.Target{{Name: "test", Host: "example.com"}}}
	now := time.Now().UTC().Truncate(time.Second)
	record, err := c.DiagnoseNode(context.Background(), n, cfg, now)
	if err != nil || record.Source != "node" || runner.seenNode != "current.example" {
		t.Fatalf("record=%+v err=%v actual=%s", record, err, runner.seenNode)
	}
	var report diagnostics.Report
	if err := json.Unmarshal(record.Report, &report); err != nil {
		t.Fatal(err)
	}
	if report.Position != "node" || report.NodeID == "" || report.ThroughSOCKS || len(report.Targets) != 1 || report.Targets[0].ICMP.Status.Status != "skipped" {
		t.Fatalf("report=%+v", report)
	}
	stored, err := c.store.GetNode(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Address = "updated.example"
	if err := c.store.UpdateNode(stored); err != nil {
		t.Fatal(err)
	}
	historical, err := c.store.GetDiagnosticReport(n.ID, "node")
	if err != nil || historical.MeasuredAddress != "current.example" {
		t.Fatalf("historical identity=%+v err=%v", historical, err)
	}
	before := runner.calls.Load()
	if _, err := c.DiagnoseNode(context.Background(), n, cfg, now.Add(59*time.Second)); !errors.Is(err, ErrDiagnosticRateLimit) {
		t.Fatalf("rate=%v", err)
	}
	if runner.calls.Load() != before {
		t.Fatal("rate-limited run executed")
	}
	got, err := c.store.GetDiagnosticReport(n.ID, "node")
	if err != nil || string(got.Report) != string(record.Report) || got.MeasuredAddress != "current.example" {
		t.Fatalf("stored report %v", err)
	}
	for _, bad := range []diagnostics.Config{{Position: "local"}, {Position: "node", SocksURL: "socks5h://127.0.0.1:1"}, {Position: "node", AbuseIPDBKey: "secret"}, {Position: "node", AbuseIPDBKeyEnv: "HOME"}} {
		if _, err := c.DiagnoseNode(context.Background(), n, bad, now.Add(time.Minute)); err == nil {
			t.Fatal("unsafe server config accepted")
		}
	}
	if runner.calls.Load() != before {
		t.Fatal("invalid config executed")
	}
}
func TestDiagnoseNodeCancellationReleasesSlotAndDB(t *testing.T) {
	runner := &diagnosticRunner{started: make(chan struct{}, 1), block: true}
	c, n := newDiagnosticControl(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.DiagnoseNode(ctx, n, diagnostics.Config{Position: "node"}, time.Now())
		done <- err
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner not started")
	}
	if _, err := c.DiagnoseNode(context.Background(), n, diagnostics.Config{Position: "node"}, time.Now()); !errors.Is(err, ErrDiagnosticBusy) {
		t.Fatalf("busy=%v", err)
	}
	dbDone := make(chan error, 1)
	go func() { _, err := c.store.ListNodes(); dbDone <- err }()
	select {
	case err := <-dbDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("network held SQLite lock")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation ignored")
	}
	if len(c.diagnosticSlot) != 0 {
		t.Fatal("diagnostic slot leaked")
	}
	record, err := c.store.GetDiagnosticReport(n.ID, "node")
	if err != nil || !json.Valid(record.Report) {
		t.Fatalf("partial report missing: %v", err)
	}
}
