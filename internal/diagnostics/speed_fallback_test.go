package diagnostics

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the exported entry point and real shell/curl against local fixtures.
// Only the URL changes; request bounds, stdin, parsing and cleanup stay intact.
type speedFallbackFixtureExecutor struct {
	t        *testing.T
	baseURL  string
	probes   int
	requests int
	deadline time.Time
}

func (e *speedFallbackFixtureExecutor) Exec(ctx context.Context, script string, stdin []byte) (string, error) {
	e.t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 10*time.Second {
		e.t.Fatalf("command lacks ten-second context bound: %v %v", deadline, ok)
	}
	if !e.deadline.IsZero() && deadline.After(e.deadline) {
		e.t.Fatalf("command escaped caller deadline: %v > %v", deadline, e.deadline)
	}
	for _, forbidden := range []string{"command -v ping", "command -v mtr", "command -v traceroute", "command -v getent", "command -v nslookup", "ipwho.is", "api.ipify.org", "api6.ipify.org", "api.ipapi.is"} {
		if strings.Contains(script, forbidden) {
			e.t.Fatalf("unrelated diagnostic command: %s", forbidden)
		}
	}
	if strings.Contains(script, "command -v curl") {
		e.probes++
		if len(stdin) != 0 || strings.Contains(script, "--url") {
			e.t.Fatal("tool discovery performed HTTP or used stdin")
		}
		return (LocalExecutor{}).Exec(ctx, script, stdin)
	}
	if !strings.Contains(script, "curl -q ") || !strings.Contains(script, "--connect-timeout 10 --max-time 10") {
		e.t.Fatal("unexpected command or missing curl bounds")
	}
	if !strings.Contains(script, "sleep 10 &") || !strings.Contains(script, "--max-redirs 0") {
		e.t.Fatal("missing watchdog or redirect bound")
	}
	sites := DefaultSpeedSites()
	mapped := false
	for i, site := range sites {
		down := strings.ReplaceAll(site.DownloadURL, "{bytes}", strconv.Itoa(1<<20))
		down = strings.ReplaceAll(down, "{chunks}", "1")
		for _, direction := range []string{"download", "upload"} {
			original := down
			if direction == "upload" {
				original = site.UploadURL
			}
			if strings.Contains(script, shellQuote(original)) {
				if direction == "download" && len(stdin) != 0 || direction == "upload" && len(stdin) != 1<<20 {
					e.t.Fatalf("incorrect %s payload: %d bytes", direction, len(stdin))
				}
				script = strings.ReplaceAll(script, shellQuote(original), shellQuote(fmt.Sprintf("%s/%d/%s", e.baseURL, i, direction)))
				mapped = true
			}
		}
	}
	if !mapped {
		e.t.Fatal("unexpected HTTP destination")
	}
	e.requests++
	return (LocalExecutor{}).Exec(ctx, script, stdin)
}

func speedFallbackFixture(t *testing.T, handler http.HandlerFunc) *speedFallbackFixtureExecutor {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &speedFallbackFixtureExecutor{t: t, baseURL: srv.URL}
}

func checkSpeedFallbackEnvelope(t *testing.T, report Report) {
	t.Helper()
	if report.Position != "node" || report.NodeID != "fixture-node" || report.ThroughSOCKS {
		t.Fatalf("incorrect execution position: %+v", report)
	}
	if len(report.Tools) != 1 || report.Tools[0].Name != "curl" || !report.Tools[0].Available {
		t.Fatalf("unexpected tool discovery: %+v", report.Tools)
	}
	if len(report.Targets) != 0 || len(report.IP) != 0 {
		t.Fatal("speed-only fallback ran unrelated diagnostics")
	}
	if report.Budget.MaxBytes != 8<<20 || report.Budget.TotalTimeoutSeconds != 50 || report.Budget.ReservedBytes > report.Budget.MaxBytes {
		t.Fatalf("incorrect shared budget: %+v", report.Budget)
	}
	if report.StartedAt.IsZero() || report.FinishedAt.Before(report.StartedAt) || report.DurationMS <= 0 {
		t.Fatalf("invalid run timing: %+v", report)
	}
	if len(report.Warnings) == 0 || report.Warnings[0] != "融合怪测速未提供有效结果，使用独立两站有界小样本测速；不是融合怪三网测速结果" {
		t.Fatalf("fallback provenance missing: %v", report.Warnings)
	}
}

func TestRunSpeedFallbackFourLocalRequests(t *testing.T) {
	var calls atomic.Int64
	payload := bytes.Repeat([]byte{'x'}, 1<<20)
	e := speedFallbackFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/upload") {
			n, err := io.Copy(io.Discard, r.Body)
			if r.Method != "POST" || err != nil || n != 1<<20 || r.ContentLength != n {
				t.Errorf("upload method=%s bytes=%d length=%d error=%v", r.Method, n, r.ContentLength, err)
			}
			_, _ = w.Write([]byte("ok"))
			return
		}
		if r.Method != "GET" {
			t.Errorf("download method=%s", r.Method)
		}
		_, _ = w.Write(payload)
	})
	report, err := RunSpeedFallback(context.Background(), "fixture-node", e)
	if err != nil {
		t.Fatal(err)
	}
	checkSpeedFallbackEnvelope(t, report)
	if calls.Load() != 4 || e.requests != 4 || e.probes != 1 || len(report.Speed) != 4 {
		t.Fatalf("requests=%d commands=%d probes=%d results=%d", calls.Load(), e.requests, e.probes, len(report.Speed))
	}
	for i, result := range report.Speed {
		direction := "download"
		if i%2 == 1 {
			direction = "upload"
		}
		if result.Status.Status != "ok" || result.Direction != direction || result.Site != DefaultSpeedSites()[i/2].Name || result.Bytes != 1<<20 || result.DurationMS <= 0 {
			t.Fatalf("invalid measured result: %+v", result)
		}
		wantMbps := float64(result.Bytes) * 8 / (result.DurationMS / 1000) / 1e6
		if result.Mbps != wantMbps {
			// Conversion between milliseconds and seconds can round the last bit.
			if diff := result.Mbps - wantMbps; diff < -1e-8 || diff > 1e-8 {
				t.Fatalf("speed does not use actual duration: %+v want=%v", result, wantMbps)
			}
		}
	}
	wantReserved := int64(4*(1<<20) + 4*curlReadAllowance + 2*1024)
	if report.Budget.ReservedBytes != wantReserved || report.Budget.TransferredBytes != 4*(1<<20)+4 {
		t.Fatalf("incorrect measured/reserved bytes: %+v", report.Budget)
	}
}

func TestRunSpeedFallbackFailedPartialRequestContinues(t *testing.T) {
	var calls atomic.Int64
	e := speedFallbackFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/0/download" {
			w.Header().Set("Content-Length", "1024")
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, 128))
			return // Deliberately truncated body: curl must report a partial failure.
		}
		if strings.HasSuffix(r.URL.Path, "/upload") {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		_, _ = w.Write([]byte("ok"))
	})
	report, err := RunSpeedFallback(context.Background(), "fixture-node", e)
	if err != nil {
		t.Fatal(err)
	}
	checkSpeedFallbackEnvelope(t, report)
	if calls.Load() != 4 || e.requests != 4 || len(report.Speed) != 4 {
		t.Fatalf("failure stopped independent requests: %+v", report)
	}
	failed := report.Speed[0]
	if failed.Status.Status != "failed" || failed.Reason == "" || failed.Bytes != 128 || failed.DurationMS <= 0 || failed.Mbps != 0 {
		t.Fatalf("partial failure evidence lost or fabricated: %+v", failed)
	}
	for _, result := range report.Speed[1:] {
		if result.Status.Status != "ok" {
			t.Fatalf("independent sample failed: %+v", result)
		}
	}
	if report.Budget.ReservedBytes != int64(4*(1<<20)+4*curlReadAllowance+2*1024) || report.Budget.TransferredBytes != 128+2*(1<<20)+6 {
		t.Fatalf("failed request not charged: %+v", report.Budget)
	}
}

func TestRunSpeedFallbackCancellationAndCallerDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 Unix ping/curl 与临时目录清理语义")
	}
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ctx, cancel := context.WithCancel(context.Background())
			wantError := context.Canceled
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
				wantError = context.DeadlineExceeded
			}
			defer cancel()
			stopped := make(chan struct{})
			var calls atomic.Int64
			e := speedFallbackFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode == "cancel" {
					cancel()
				}
				<-r.Context().Done()
				close(stopped)
			})
			e.deadline, _ = ctx.Deadline()
			started := time.Now()
			report, err := RunSpeedFallback(ctx, "fixture-node", e)
			if !errors.Is(err, wantError) || time.Since(started) > time.Second {
				t.Fatalf("context not honored promptly: err=%v duration=%v", err, time.Since(started))
			}
			checkSpeedFallbackEnvelope(t, report)
			if calls.Load() != 1 || e.requests != 1 || e.probes != 1 {
				t.Fatalf("commands continued after context error: calls=%d requests=%d probes=%d", calls.Load(), e.requests, e.probes)
			}
			for _, result := range report.Speed {
				if result.Status.Status == "ok" || result.Mbps != 0 {
					t.Fatalf("canceled sample reported success: %+v", result)
				}
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("curl remained connected after cancellation")
			}
			files, readErr := os.ReadDir(tmp)
			if readErr != nil || len(files) != 0 {
				t.Fatalf("temporary request files remain: %v %v", files, readErr)
			}
		})
	}
}

func TestRunSpeedFallbackRejectsInvalidAndCanceledInput(t *testing.T) {
	if _, err := RunSpeedFallback(nil, "fixture-node", LocalExecutor{}); err == nil {
		t.Fatal("accepted nil context")
	}
	if _, err := RunSpeedFallback(context.Background(), "fixture-node", nil); err == nil {
		t.Fatal("accepted nil executor")
	}
	if _, err := RunSpeedFallback(context.Background(), strings.Repeat("n", 129), LocalExecutor{}); err == nil {
		t.Fatal("accepted invalid node ID")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := &speedFallbackFixtureExecutor{t: t}
	report, err := RunSpeedFallback(ctx, "fixture-node", e)
	if !errors.Is(err, context.Canceled) || e.probes != 0 || e.requests != 0 || report.Budget.ReservedBytes != 0 || len(report.Speed) != 0 {
		t.Fatalf("already canceled context started fallback: %+v err=%v", report, err)
	}
}
