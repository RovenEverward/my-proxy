package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// This runner never invokes a shell, a local executor, SSH or public services.
// Network discovery reports no tools; entrance responses exercise the real parser.
type checkupRunner struct {
	mu             sync.Mutex
	stages         []string
	addresses      []string
	inputs         [][]byte
	budgets        []time.Duration
	block          string
	started        chan struct{}
	once           sync.Once
	healthErr      error
	beforeEntrance func()
}

func (r *checkupRunner) stage(ctx context.Context, n model.Node, name string, input []byte) error {
	r.mu.Lock()
	r.stages = append(r.stages, name)
	r.addresses = append(r.addresses, n.Address)
	if name == "entrance" {
		r.inputs = append(r.inputs, append([]byte(nil), input...))
		deadline, _ := ctx.Deadline()
		r.budgets = append(r.budgets, time.Until(deadline))
	}
	r.mu.Unlock()
	if r.block == name {
		r.once.Do(func() { close(r.started) })
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (r *checkupRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	if !strings.Contains(script, "k=service:") {
		return "", fmt.Errorf("unexpected unbounded script")
	}
	if err := r.stage(ctx, n, "health", nil); err != nil {
		return "", err
	}
	return "k=service:active\nk=version:26.3.27\nk=hash:observed\n", r.healthErr
}

func (r *checkupRunner) RunWithInput(context.Context, model.Node, string, []byte) (string, error) {
	panic("expected bounded transport")
}

func (r *checkupRunner) RunBounded(ctx context.Context, n model.Node, script string, input []byte, limit int) (string, error) {
	if limit == diagnostics.MaxOutputBytes && strings.Contains(script, "command -v curl") {
		if limit != diagnostics.MaxOutputBytes {
			panic("invalid diagnostic transport limit")
		}
		if err := r.stage(ctx, n, "network", nil); err != nil {
			return "", err
		}
		return "curl=no\nping=no\nmtr=no\ntraceroute=no\ngetent=no\nnslookup=no\n", nil
	}
	if r.beforeEntrance != nil {
		r.beforeEntrance()
	}
	if err := r.stage(ctx, n, "entrance", input); err != nil {
		return "", err
	}
	envelope := func(key string, code int, address, mime, body string) string {
		return fmt.Sprintf("MPX1\t%s\t0\t%d\t%s\t%s\t%s\n", key, code, address, mime, base64.StdEncoding.EncodeToString([]byte(body)))
	}
	return envelope("egress", 200, "https://api.ip.sb/geoip", "application/json", `{"ip":"203.0.113.9","country_code":"US"}`) +
		envelope("youtube", 204, "https://www.youtube.com/generate_204", "", "") +
		envelope("licensed", 403, "https://www.netflix.com/title/81280792", "text/html", "Access denied") +
		envelope("original", 403, "https://www.netflix.com/title/80018499", "text/html", "Access denied"), nil
}

func newCheckupControl(t *testing.T, r *checkupRunner) (*Control, model.Node) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "checkup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	n := addNode(t, st, "checkup", "stored.example")
	if err := st.SetNodeDeployed(n.ID, "baseline", time.Now()); err != nil {
		t.Fatal(err)
	}
	return New(st, r, slog.New(slog.DiscardHandler), t.TempDir()), n
}

func decodeCheckup(t *testing.T, record model.DiagnosticRecord) model.NodeCheckupReport {
	t.Helper()
	var report model.NodeCheckupReport
	if err := json.Unmarshal(record.Report, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestCheckupNodeCombinesFreshIdentityHealthSpeedAndAllEntrances(t *testing.T) {
	r := &checkupRunner{}
	c, n := newCheckupControl(t, r)
	in := addInbound(t, c.store, n.ID, "explicit", 443)
	for _, address := range []string{"cdn-one.example", "cdn-two.example"} {
		h := model.Host{InboundID: in.ID, Address: address, Remark: address}
		if err := c.store.CreateHost(&h); err != nil {
			t.Fatal(err)
		}
	}
	defaultIn := addInbound(t, c.store, n.ID, "default", 8443)
	user := addUser(t, c.store, activeUser("checkup"))
	n.Address = "stale.example"
	n.DeployedHash = "observed" // must compare the stored baseline instead.
	record, err := c.CheckupNode(context.Background(), n, diagnostics.Config{Speed: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	report := decodeCheckup(t, record)
	if record.Source != "node" || record.MeasuredAddress != "stored.example" || report.Drift != "changed" || !report.Health.Active {
		t.Fatalf("record=%+v health=%+v drift=%s", record, report.Health, report.Drift)
	}
	if len(report.Entrances) != 3 || report.Entrances[0].HostID == 0 || report.Entrances[1].HostID == 0 || report.Entrances[2].InboundID != defaultIn.ID || report.Entrances[2].HostID != 0 {
		t.Fatalf("entrances=%+v", report.Entrances)
	}
	if got := strings.Join(r.stages, ","); got != "health,network,entrance,entrance,entrance" {
		t.Fatalf("step order=%s", got)
	}
	for i, input := range r.inputs {
		if !strings.Contains(string(input), model.DeriveUUID(user.Key)) {
			t.Fatal("authorized user credentials lost")
		}
		address := []string{"cdn-one.example", "cdn-two.example", "stored.example"}[i]
		if !strings.Contains(string(input), address) {
			t.Fatalf("actual entrance %s lost", address)
		}
		if r.budgets[i] <= 0 || r.budgets[i] > checkupEntranceBudget {
			t.Fatalf("entrance budget=%s", r.budgets[i])
		}
	}
	for _, address := range r.addresses {
		if address != "stored.example" {
			t.Fatalf("stale SSH identity=%s", address)
		}
	}
	for _, entry := range report.Entrances {
		if len(entry.Results) != 3 {
			t.Fatalf("target coverage=%+v", entry)
		}
		for _, result := range entry.Results {
			if result.Status != model.CapabilityUnknown || result.Consecutive != 1 {
				t.Fatalf("single observation invented stable result=%+v", result)
			}
			if result.Target == "youtube" && result.ObservedStatus != model.CapabilityAvailable {
				t.Fatalf("observed success lost=%+v", result)
			}
		}
	}
	var network diagnostics.Report
	if err := json.Unmarshal(report.Network, &network); err != nil {
		t.Fatal(err)
	}
	if len(network.Speed) != 4 || network.Position != "node" {
		t.Fatalf("network=%+v", network)
	}
	for _, speed := range network.Speed {
		if speed.Reason != "curl not installed" {
			t.Fatalf("full speed default disabled: %+v", speed)
		}
	}
	stored, err := c.store.GetDiagnosticReport(n.ID, "node")
	if err != nil || string(stored.Report) != string(record.Report) {
		t.Fatalf("combined latest report missing: %v", err)
	}
	configs, err := c.store.ListCapabilityConfigs(n.ID)
	if err != nil || len(configs) != 0 {
		t.Fatalf("manual checkup altered schedule: %+v %v", configs, err)
	}
}

func TestCheckupNodeContinuesAfterInvalidEntrance(t *testing.T) {
	r := &checkupRunner{}
	c, n := newCheckupControl(t, r)
	in := addInbound(t, c.store, n.ID, "vless", 443)
	for _, address := range []string{"*.example.com", "valid.example.com"} {
		h := model.Host{InboundID: in.ID, Address: address}
		if err := c.store.CreateHost(&h); err != nil {
			t.Fatal(err)
		}
	}
	addUser(t, c.store, activeUser("probe"))
	record, err := c.CheckupNode(context.Background(), n, diagnostics.Config{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	report := decodeCheckup(t, record)
	if len(report.Entrances) != 2 || report.Entrances[0].Error == "" || len(report.Entrances[1].Results) != 3 || len(r.inputs) != 1 {
		t.Fatalf("failed entrance prevented later probe: %+v", report.Entrances)
	}
}

func TestCheckupNodeCancellationOwnsFullSlotAndSavesPartial(t *testing.T) {
	for _, stage := range []string{"health", "network", "entrance"} {
		t.Run(stage, func(t *testing.T) {
			r := &checkupRunner{block: stage, started: make(chan struct{})}
			c, n := newCheckupControl(t, r)
			addInbound(t, c.store, n.ID, "first", 443)
			addInbound(t, c.store, n.ID, "remaining", 8443)
			addUser(t, c.store, activeUser("active"))
			old := model.DiagnosticRecord{NodeID: n.ID, Source: "node", CheckedAt: time.Now().Add(-2 * time.Minute), Report: json.RawMessage(`{"previous":true}`)}
			if err := c.store.SaveDiagnosticReport(old); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			now := time.Now().UTC().Truncate(time.Second)
			done := make(chan error, 1)
			go func() { _, err := c.CheckupNode(ctx, n, diagnostics.Config{Speed: true}, now); done <- err }()
			select {
			case <-r.started:
			case <-time.After(time.Second):
				t.Fatal("stage not started")
			}
			if _, err := c.CheckupNode(context.Background(), n, diagnostics.Config{}, now); !errors.Is(err, ErrDiagnosticBusy) {
				t.Fatalf("full checkup busy=%v", err)
			}
			if _, err := c.DiagnoseNode(context.Background(), n, diagnostics.Config{}, now); !errors.Is(err, ErrDiagnosticBusy) {
				t.Fatalf("network bypassed checkup slot=%v", err)
			}
			current, err := c.store.GetDiagnosticReport(n.ID, "node")
			if err != nil || string(current.Report) != string(old.Report) {
				t.Fatalf("intermediate network report saved before completion: %+v %v", current, err)
			}
			if _, err := c.store.ListNodes(); err != nil {
				t.Fatal(err)
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancel ignored")
			}
			if len(c.diagnosticSlot) != 0 || len(c.capabilitySlot) != 0 {
				t.Fatal("slot leaked")
			}
			partial, err := c.store.GetDiagnosticReport(n.ID, "node")
			if err != nil {
				t.Fatal(err)
			}
			report := decodeCheckup(t, *partial)
			if len(report.Entrances) != 2 || report.Entrances[1].Error == "" || partial.MeasuredAddress != n.Address {
				t.Fatalf("partial=%+v", report)
			}
			if stage != "health" && len(report.Network) == 0 {
				t.Fatal("partial network evidence missing")
			}
			before := len(r.stages)
			if _, err := c.CheckupNode(context.Background(), n, diagnostics.Config{}, now.Add(59*time.Second)); !errors.Is(err, ErrDiagnosticRateLimit) {
				t.Fatalf("partial report bypassed rate=%v", err)
			}
			if len(r.stages) != before {
				t.Fatal("rate limited checkup executed")
			}
		})
	}
}

func TestCheckupNodeBusyCapabilityAndUnreachableAreExplicit(t *testing.T) {
	for _, unreachable := range []bool{false, true} {
		r := &checkupRunner{}
		if unreachable {
			r.healthErr = errors.New("ssh unavailable")
		}
		c, n := newCheckupControl(t, r)
		addInbound(t, c.store, n.ID, "first", 443)
		addInbound(t, c.store, n.ID, "second", 8443)
		c.capabilitySlot <- struct{}{}
		record, err := c.CheckupNode(context.Background(), n, diagnostics.Config{}, time.Now())
		<-c.capabilitySlot
		if err != nil {
			t.Fatal(err)
		}
		report := decodeCheckup(t, record)
		if len(report.Entrances) != 2 || report.Entrances[0].Error == "" || report.Entrances[1].Error == "" {
			t.Fatalf("errors not captured: %+v", report)
		}
		if unreachable && report.Drift != "unknown" {
			t.Fatalf("unreachable invented drift=%s", report.Drift)
		}
		if len(r.inputs) != 0 {
			t.Fatal("busy/unreachable entrance probed")
		}
	}
}

func TestCheckupNodeRejectsConfigAndHonorsSpeedFalse(t *testing.T) {
	r := &checkupRunner{}
	c, n := newCheckupControl(t, r)
	for _, cfg := range []diagnostics.Config{{Position: "local"}, {SocksURL: "socks5h://127.0.0.1:1"}, {IPAPIKeyEnv: "HOME"}, {TotalTimeoutSeconds: 171}} {
		if _, err := c.CheckupNode(context.Background(), n, cfg, time.Now()); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if len(r.stages) != 0 {
		t.Fatal("invalid config started health check")
	}
	record, err := c.CheckupNode(context.Background(), n, diagnostics.Config{Speed: false}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var network diagnostics.Report
	if err := json.Unmarshal(decodeCheckup(t, record).Network, &network); err != nil {
		t.Fatal(err)
	}
	for _, speed := range network.Speed {
		if speed.Reason != "speed tests require explicit opt-in" {
			t.Fatalf("explicit speed=false ignored: %+v", speed)
		}
	}
}

func TestCheckupCoreDriftHonorsQUICAndMixedBaselines(t *testing.T) {
	for _, tc := range []struct {
		name       string
		n          model.Node
		deployment model.CoreDeployment
		inbounds   []model.Inbound
		health     model.NodeStatus
		want       string
	}{
		{"quic-match", model.Node{}, model.CoreDeployment{DeployedHash: "sb"}, []model.Inbound{{Protocol: model.ProtocolHysteria2}}, model.NodeStatus{Reachable: true, ObservedHash: "sb", Cores: []model.CoreStatus{{Core: model.CoreSingBox, ObservedHash: "sb"}}}, "match"},
		{"mixed-changed", model.Node{DeployedHash: "xray"}, model.CoreDeployment{DeployedHash: "sb"}, []model.Inbound{{Protocol: model.ProtocolVLESS}, {Protocol: model.ProtocolHysteria2}}, model.NodeStatus{Reachable: true, ObservedHash: "xray", Cores: []model.CoreStatus{{Core: model.CoreSingBox, ObservedHash: "changed"}}}, "changed"},
		{"mixed-missing", model.Node{DeployedHash: "xray"}, model.CoreDeployment{}, []model.Inbound{{Protocol: model.ProtocolVLESS}, {Protocol: model.ProtocolHysteria2}}, model.NodeStatus{Reachable: true, ObservedHash: "xray", Cores: []model.CoreStatus{{Core: model.CoreSingBox, ObservedHash: "sb"}}}, "missing-baseline"},
		{"unreachable", model.Node{DeployedHash: "xray"}, model.CoreDeployment{DeployedHash: "sb"}, nil, model.NodeStatus{ObservedHash: "changed"}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var summary []string
			if got := checkupCoreDrift(tc.n, tc.deployment, tc.inbounds, tc.health, &summary); got != tc.want {
				t.Fatalf("drift=%s want=%s summary=%v", got, tc.want, summary)
			}
		})
	}
}

func TestCheckupReportBoundsLargePartialAndKeepsValidJSON(t *testing.T) {
	report := model.NodeCheckupReport{Drift: "unknown", Summary: []string{strings.Repeat("错误", 100000)}}
	network := diagnostics.Report{Position: "node", Warnings: []string{"original warning"}}
	report.Network, _ = json.Marshal(network)
	for i := 0; i < 1000; i++ {
		report.Entrances = append(report.Entrances, model.EntranceDiagnostic{InboundID: int64(i + 1), Name: strings.Repeat("入口", 1000), Error: strings.Repeat("失败", 1000)})
	}
	raw, err := marshalCheckupReport(report)
	if err != nil || len(raw) > checkupReportLimit || !json.Valid(raw) {
		t.Fatalf("bounded partial size=%d err=%v", len(raw), err)
	}
	var got model.NodeCheckupReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(got.Summary, " "), "省略") || !strings.Contains(string(got.Network), "original warning") {
		t.Fatalf("silent omission/network lost=%+v", got.Summary)
	}
}
