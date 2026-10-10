package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/pflag"
)

func runDiagnosticRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// The existing helper resets node children; the new local root command also
	// needs fresh values to represent a separate CLI process for every invocation.
	diagnoseCmd.Flags().VisitAll(func(f *pflag.Flag) {
		if slice, ok := f.Value.(pflag.SliceValue); ok {
			_ = slice.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	})
	diagnoseCmd.SetContext(rootCmd.Context())
	for _, cmd := range nodeCmd.Commands() {
		cmd.SetContext(rootCmd.Context())
	}
	return runCapabilityRoot(t, args...)
}

func diagnosticFixture() diagnostics.Report {
	zero, rtt, failure := 0.0, 7.5, 50.0
	return diagnostics.Report{Position: "node", FinishedAt: time.Now(), DurationMS: 900,
		Budget: diagnostics.Budget{MaxBytes: 32 << 20, TransferredBytes: 1234, ReservedBytes: 65536, TotalTimeoutSeconds: 120},
		Tools:  []diagnostics.Tool{{Name: "mtr", Detail: "not installed"}},
		Targets: []diagnostics.TargetReport{{Target: diagnostics.Target{Name: "fixture", Region: "test-region", Host: "example.com", HTTPURL: "https://example.com/"},
			DNS:   diagnostics.Status{Status: "ok"},
			ICMP:  diagnostics.Latency{Status: diagnostics.Status{Status: "ok"}, Protocol: "icmp", Timing: "ICMP RTT", Attempted: 2, ValidSamples: 2, MeanMS: 7.5, JitterMS: 1.2, JitterSamples: 1, LossPercent: &zero},
			HTTP:  diagnostics.Latency{Status: diagnostics.Status{Status: "partial", Reason: "HTTP 503"}, Protocol: "https", Timing: "curl time_total", Attempted: 2, ValidSamples: 1, MeanMS: 9, HTTPFailureRatePercent: &failure},
			Route: diagnostics.Route{Status: diagnostics.Status{Status: "partial", Reason: "ICMP filtered"}, Tool: "traceroute", Direction: "node -> target", Hops: []diagnostics.Hop{{TTL: 1, Address: "192.0.2.8", ASN: "AS64500", RTTMS: &rtt}}}}},
		Speed: []diagnostics.SpeedResult{{Status: diagnostics.Status{Status: "skipped", Reason: "speed disabled"}, Site: "test-speed", Direction: "download", SourceURL: "https://example.com/docs"}},
		IP:    []diagnostics.IPResult{{Status: diagnostics.Status{Status: "ok"}, Family: "geoip", Address: "203.0.113.8", Source: "https://ipwho.is/", ASN: "AS64501", Organization: "Fixture ISP", CheckedAt: time.Now(), ProviderFlags: map[string]bool{"hosting": true, "proxy": false}}, {Status: diagnostics.Status{Status: "skipped", Reason: "missing key"}, Family: "reputation", Source: "AbuseIPDB"}},
	}
}

func TestDiagnosticCLIActualParsingAndOutput(t *testing.T) {
	var got diagnostics.Config
	var method, path, auth string
	calls := 0
	report := diagnosticFixture()
	reportRaw, _ := json.Marshal(report)
	record := model.DiagnosticRecord{NodeID: 7, MeasuredAddress: "old-node.example:2222", Source: "node", CheckedAt: time.Now().Add(-time.Minute), Report: reportRaw}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		method, path, auth = r.Method, r.URL.RequestURI(), r.Header.Get("Authorization")
		if r.Method == "POST" {
			got = diagnostics.Config{}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			record.Source = got.Position
		}
		_ = json.NewEncoder(w).Encode(record)
	}))
	defer srv.Close()
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "diagnostic-token")
	out, err := runDiagnosticRoot(t, "node", "diagnose", "7", "--from", "control", "--speed", "--max-bytes", "2097152", "--speed-bytes", "65536", "--samples", "2", "--timeout", "45s", "--request-timeout", "3s", "--target", "example.com", "--target-http", "https://example.com/", "--target-name", "custom", "--target-region", "known", "--ipv4-url", "https://example.com/v4")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || method != "POST" || path != "/api/nodes/7/diagnostics" || auth != "Bearer diagnostic-token" {
		t.Fatalf("request %d %s %s %s", calls, method, path, auth)
	}
	if got.Position != "control" || !got.Speed || got.MaxBytes != 2097152 || got.SpeedBytes != 65536 || got.Samples != 2 || got.TotalTimeoutSeconds != 45 || got.RequestTimeoutSeconds != 3 || got.Targets[0].Region != "known" || got.Targets[0].Name != "custom" || got.IPv4URL != "https://example.com/v4" {
		t.Fatalf("flags lost: %+v", got)
	}
	for _, value := range []string{"测量时节点地址: old-node.example:2222", "control", "距今", "1234", "ICMP RTT", "HTTP 失败率", "50.0%", "HTTP 503", "AS64500", "192.0.2.8", "speed disabled", "hosting=true", "proxy=false", "AbuseIPDB", "missing key"} {
		if !strings.Contains(out, value) {
			t.Errorf("missing %q: %s", value, out)
		}
	}
	out, err = runDiagnosticRoot(t, "node", "diagnostics", "7", "--from", "control", "--output", "json")
	if err != nil || method != "GET" || path != "/api/nodes/7/diagnostics?from=control" {
		t.Fatalf("GET %s err=%v out=%s", path, err, out)
	}
	var decoded model.DiagnosticRecord
	if err := json.Unmarshal([]byte(out), &decoded); err != nil || decoded.NodeID != 7 || decoded.MeasuredAddress != "old-node.example:2222" {
		t.Fatalf("JSON %s err=%v", out, err)
	}
	_, err = runDiagnosticRoot(t, "node", "diagnose", "7", "--from", "control")
	if err != nil || got.Targets != nil || !got.Speed {
		t.Fatalf("defaults overwritten cfg=%+v err=%v", got, err)
	}
}

func TestDiagnosticCLIPartialErrorAndNoRetry(t *testing.T) {
	for _, code := range []int{502, 429} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			reportRaw, _ := json.Marshal(diagnosticFixture())
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(code)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "canceled partial", "record": model.DiagnosticRecord{NodeID: 7, Source: "node", CheckedAt: time.Now(), Report: reportRaw}})
			}))
			defer srv.Close()
			t.Setenv("MPX_SERVER", srv.URL)
			out, err := runDiagnosticRoot(t, "node", "diagnose", "7", "--output", "json")
			if calls != 1 || err == nil || err.Error() != "canceled partial" || !json.Valid([]byte(out)) || !strings.Contains(out, "partial") {
				t.Fatalf("calls=%d err=%v out=%s", calls, err, out)
			}
		})
	}
}

func TestDiagnosticCLIValidationBeforeAnyNetwork(t *testing.T) {
	t.Setenv("MPX_SERVER", "http://127.0.0.1:1")
	for _, tc := range []struct {
		args     []string
		contains string
	}{
		{[]string{"node", "diagnose", "1", "--from", "local"}, "--from"},
		{[]string{"node", "diagnostics", "1", "--from", "unknown"}, "--from"},
		{[]string{"node", "diagnose", "0"}, "正整数"},
		{[]string{"node", "diagnose", "1", "--samples", "6"}, "samples"},
		{[]string{"node", "diagnose", "1", "--max-bytes", "0"}, "正数"},
		{[]string{"node", "diagnose", "1", "--timeout", "1.5s"}, "整秒"},
		{[]string{"node", "diagnose", "1", "--timeout", "3m"}, "total timeout"},
		{[]string{"node", "diagnose", "1", "--target", "example.com", "--target-http", "https://a.example/", "--target-http", "https://b.example/"}, "数量"},
		{[]string{"node", "diagnose", "1", "--ip-reputation-key-env", "MY_KEY"}, "仅用于本地"},
		{[]string{"node", "diagnose", "1", "--ip-quality-key-env", "MY_KEY"}, "仅用于本地"},
		{[]string{"diagnose", "--ip-quality-key-env", "BAD-NAME"}, "environment"},
		{[]string{"diagnose", "--ip-reputation-key-env", "BAD-NAME"}, "environment"},
		{[]string{"diagnose", "--socks5", "socks5h://user:password@localhost:1080"}, "without credentials"},
		{[]string{"diagnose", "--socks5", "socks5://localhost:1080"}, "socks5h"},
		{[]string{"diagnose", "--speed-download", "https://example.com/"}, "同时指定"},
	} {
		_, err := runDiagnosticRoot(t, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.contains) || strings.Contains(err.Error(), "password") {
			t.Errorf("%v => %v want %q", tc.args, err, tc.contains)
		}
	}
}

func TestDiagnosticCLILocalWithoutLoginAndConfigSecrets(t *testing.T) {
	t.Setenv("MPX_SERVER", "")
	t.Setenv("MPX_TOKEN", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })
	out, err := runDiagnosticRoot(t, "diagnose", "--output", "json")
	if err != context.Canceled || !json.Valid([]byte(out)) || !strings.Contains(out, `"position": "local"`) {
		t.Fatalf("local err=%v out=%s", err, out)
	}
	defaults, err := diagnosticConfig(newLocalDiagnoseCmd(), true)
	if err != nil || defaults.AbuseIPDBKeyEnv != "MPX_ABUSEIPDB_KEY" || defaults.IPAPIKeyEnv != "MPX_IPAPI_IS_KEY" {
		t.Fatalf("local env defaults %+v err=%v", defaults, err)
	}
	cmd := newLocalDiagnoseCmd()
	if err := cmd.ParseFlags([]string{"--ip-reputation-key-env", "LOCAL_SECRET", "--ip-quality-key-env", "LOCAL_IPAPI", "--socks5", "socks5h://127.0.0.1:1080", "--target-http", "https://example.com/", "--speed-site-name", "custom", "--speed-download", "https://example.com/down?chunks={chunks}", "--speed-upload", "https://example.com/up", "--speed-source", "https://example.com/docs", "--speed-download-unit-bytes", "1048576", "--speed-upload-method", "PUT"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := diagnosticConfig(cmd, true)
	if err != nil || cfg.AbuseIPDBKeyEnv != "LOCAL_SECRET" || cfg.IPAPIKeyEnv != "LOCAL_IPAPI" || cfg.Targets[0].Host != "example.com" || cfg.SpeedSites[0].DownloadUnitBytes != 1048576 || cfg.SpeedSites[0].UploadMethod != "PUT" {
		t.Fatalf("local cfg=%+v err=%v", cfg, err)
	}
	cfg.AbuseIPDBKey = "secret-value"
	cfg.IPAPIKey = "ipapi-secret-value"
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "LOCAL_SECRET") || strings.Contains(string(raw), "secret-value") {
		t.Fatal("secret in JSON")
	}
}

func TestDiagnosticCLICompleteAllPreservesPartialAndContinues(t *testing.T) {
	network, _ := json.Marshal(diagnosticFixture())
	checkup := model.NodeCheckupReport{Summary: []string{"服务配置变化"}, Drift: "changed", Health: model.NodeStatus{Reachable: true, Active: true, Version: "26.3", CheckedAt: time.Now()}, Network: network, Entrances: []model.EntranceDiagnostic{{Name: "entrance-A", InboundID: 9, HostID: 8, Results: []model.CapabilityResult{{Target: "Netflix", ObservedStatus: model.CapabilityAvailable, Status: model.CapabilityUnknown, Region: "US", ExitIP: "203.0.113.9", Reason: "observed once"}}}}}
	report, _ := json.Marshal(checkup)
	calls := []int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode([]model.NodeView{{Node: model.Node{ID: 1, Name: "first"}}, {Node: model.Node{ID: 2, Name: "second"}}})
			return
		}
		var cfg diagnosticPayload
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			t.Error(err)
		}
		if cfg.Speed == nil || !*cfg.Speed || cfg.NetworkOnly {
			t.Errorf("default not complete %+v", cfg)
		}
		id := int64(1)
		if strings.Contains(r.URL.Path, "/2/") {
			id = 2
		}
		calls = append(calls, id)
		rec := model.DiagnosticRecord{NodeID: id, Source: "node", CheckedAt: time.Now(), MeasuredAddress: "snapshot", Report: report}
		if id == 1 {
			w.WriteHeader(502)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "partial", "record": rec})
		} else {
			_ = json.NewEncoder(w).Encode(rec)
		}
	}))
	defer srv.Close()
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "x")
	out, err := runDiagnosticRoot(t, "node", "diagnose", "--all", "--output", "json")
	var results []diagnosticAllResult
	if err == nil || json.Unmarshal([]byte(out), &results) != nil || len(results) != 2 || results[0].Error == "" || results[0].Record == nil || results[1].Record == nil || len(calls) != 2 {
		t.Fatalf("all err=%v out=%s calls=%v", err, out, calls)
	}
	out, err = runDiagnosticRoot(t, "node", "diagnose", "2")
	for _, value := range []string{"服务配置变化", "健康: 可达 是", "配置漂移 changed", "entrance-A", "Netflix", "available", "unknown", "US", "observed once", "ICMP RTT"} {
		if !strings.Contains(out, value) {
			t.Errorf("missing %q %s", value, out)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runDiagnosticRoot(t, "node", "diagnose", "1", "--all"); err == nil {
		t.Fatal("accepted id with all")
	}
}

type diagnosticRoundTripFunc func(*http.Request) (*http.Response, error)

func (f diagnosticRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDiagnosticCLIAllCancelRetainsCompletedReports(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := newNodeDiagnoseCmd()
	cmd.SetContext(ctx)
	var out bytes.Buffer
	cmd.SetOut(&out)
	previous := opts.output
	opts.output = outputJSON
	t.Cleanup(func() { opts.output = previous })
	report, _ := json.Marshal(diagnosticFixture())
	calls := 0
	c := &Client{server: "http://fixture", http: &http.Client{Transport: diagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var raw []byte
		if r.Method == "GET" {
			raw, _ = json.Marshal([]model.NodeView{{Node: model.Node{ID: 1}}, {Node: model.Node{ID: 2}}})
		} else {
			calls++
			raw, _ = json.Marshal(model.DiagnosticRecord{NodeID: 1, Source: "node", Report: report})
			cancel()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
	})}}
	err := diagnoseAllNodes(cmd, c, diagnostics.Config{}, false)
	var results []diagnosticAllResult
	if err != context.Canceled || calls != 1 || json.Unmarshal(out.Bytes(), &results) != nil || len(results) != 1 || results[0].Record == nil {
		t.Fatalf("cancel %v calls=%d out=%s", err, calls, out.String())
	}
}

func TestDiagnosticCLISpeedFalseAndNetworkModePayload(t *testing.T) {
	report, _ := json.Marshal(diagnosticFixture())
	var got diagnosticPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(model.DiagnosticRecord{NodeID: 1, Source: "node", Report: report})
	}))
	defer srv.Close()
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "x")
	if _, err := runDiagnosticRoot(t, "node", "diagnose", "1", "--speed=false"); err != nil {
		t.Fatal(err)
	}
	if got.Speed == nil || *got.Speed || got.NetworkOnly {
		t.Fatalf("speed false lost %+v", got)
	}
	if _, err := runDiagnosticRoot(t, "node", "diagnose", "1", "--network-only"); err != nil {
		t.Fatal(err)
	}
	if got.Speed == nil || *got.Speed || !got.NetworkOnly {
		t.Fatalf("network mode lost %+v", got)
	}
}

// Run the actual binary because SetOut in in-process command tests hides Cobra's
// default stream choice. No external diagnostic tools or public URLs are used.
func TestDiagnosticCLIBinaryAllUsesStdout(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mpx")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/mpx")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	network, _ := json.Marshal(diagnosticFixture())
	checkup, _ := json.Marshal(model.NodeCheckupReport{Summary: []string{"stdout-fixture"}, Health: model.NodeStatus{Reachable: true, Active: true}, Drift: "match", Network: network})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]model.NodeView{{Node: model.Node{ID: 1, Name: "mock-node"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(model.DiagnosticRecord{NodeID: 1, Source: "node", MeasuredAddress: "mock.example", CheckedAt: time.Now(), Report: checkup})
	}))
	defer srv.Close()
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			command := exec.Command(binary, "--server", srv.URL, "--token", "fixture-token", "node", "diagnose", "--all", "--output", format)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("CLI %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("successful report leaked to stderr: %s", stderr.String())
			}
			if format == "json" {
				var records []diagnosticAllResult
				if err := json.Unmarshal(stdout.Bytes(), &records); err != nil || len(records) != 1 || records[0].Record == nil {
					t.Fatalf("JSON stdout=%s err=%v", stdout.String(), err)
				}
			} else if !strings.Contains(stdout.String(), "stdout-fixture") || !strings.Contains(stdout.String(), "mock-node") || !strings.Contains(stdout.String(), "实测流量") {
				t.Fatalf("table stdout=%s", stdout.String())
			}
		})
	}
}
