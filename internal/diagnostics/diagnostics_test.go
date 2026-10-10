package diagnostics

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testEngine(t *testing.T) *engine {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	cfg, err := (Config{Samples: 1, RequestTimeoutSeconds: 2}).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	return &engine{cfg: cfg, exec: LocalExecutor{}, report: &Report{Budget: Budget{MaxBytes: cfg.MaxBytes}}, tools: map[string]bool{"curl": true}}
}

func TestHTTPIsolationAndBudget(t *testing.T) {
	var okCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/huge":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			for i := 0; i < 1024; i++ {
				if _, err := w.Write(make([]byte, 8192)); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		case "/bad-length":
			c, b, _ := w.(http.Hijacker).Hijack()
			defer c.Close()
			_, _ = b.WriteString("HTTP/1.1 200 OK\r\nContent-Length: bad\r\nConnection: close\r\n\r\nbad")
			_ = b.Flush()
		case "/redirect":
			w.Header().Set("Location", "/ok")
			w.WriteHeader(302)
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(4 * time.Second):
			}
		default:
			okCalls.Add(1)
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	e := testEngine(t)
	for _, path := range []string{"/huge", "/bad-length", "/redirect", "/slow"} {
		r := e.request(context.Background(), srv.URL+path, "GET", nil, 1024, "", false)
		if r.Err == nil {
			t.Errorf("%s accepted", path)
		}
		if len(r.Body) > 1024 {
			t.Fatal("body exceeds bound")
		}
	}
	if okCalls.Load() != 0 {
		t.Fatal("redirect followed")
	}
	r := e.request(context.Background(), srv.URL+"/ok", "GET", nil, 1024, "", false)
	if r.Err != nil || r.Body != "ok" || r.Download != 2 {
		t.Fatalf("independent request failed: %+v", r)
	}
	if e.report.Budget.ReservedBytes != 5*(1024+curlReadAllowance) {
		t.Fatal(e.report.Budget)
	}
	e.cfg.MaxBytes = e.report.Budget.ReservedBytes
	if e.request(context.Background(), srv.URL+"/ok", "GET", nil, 1024, "", false).Err == nil || okCalls.Load() != 1 {
		t.Fatal("budget allowed extra request")
	}
}

func socksFixture(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	count := &atomic.Int64{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				buf := make([]byte, 2)
				if _, err := io.ReadFull(c, buf); err != nil {
					return
				}
				methods := make([]byte, int(buf[1]))
				if _, err := io.ReadFull(c, methods); err != nil {
					return
				}
				_, _ = c.Write([]byte{5, 0})
				h := make([]byte, 4)
				if _, err := io.ReadFull(c, h); err != nil {
					return
				}
				var host string
				switch h[3] {
				case 1:
					b := make([]byte, 4)
					_, _ = io.ReadFull(c, b)
					host = net.IP(b).String()
				case 3:
					b := make([]byte, 1)
					_, _ = io.ReadFull(c, b)
					s := make([]byte, int(b[0]))
					_, _ = io.ReadFull(c, s)
					host = string(s)
				case 4:
					b := make([]byte, 16)
					_, _ = io.ReadFull(c, b)
					host = net.IP(b).String()
				default:
					return
				}
				p := make([]byte, 2)
				_, _ = io.ReadFull(c, p)
				up, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(p))), time.Second)
				if err != nil {
					_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				count.Add(1)
				_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				done := make(chan struct{})
				go func() {
					_, _ = io.Copy(up, c)
					if tcp, ok := up.(*net.TCPConn); ok {
						tcp.CloseWrite()
					}
					close(done)
				}()
				_, _ = io.Copy(c, up)
				<-done
			}()
		}
	}()
	return "socks5h://" + ln.Addr().String(), count
}

func TestSOCKSTrafficAndNoFallback(t *testing.T) {
	var direct atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		if r.URL.Path == "/upload" {
			b, _ := io.Copy(io.Discard, r.Body)
			_, _ = fmt.Fprint(w, b)
		} else {
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	proxy, count := socksFixture(t)
	e := testEngine(t)
	e.cfg.SocksURL = proxy
	for _, method := range []string{"GET", "POST"} {
		var body []byte
		path := "/"
		if method == "POST" {
			body = make([]byte, 1024)
			path = "/upload"
		}
		r := e.request(context.Background(), srv.URL+path, method, body, 1024, "", false)
		if r.Err != nil {
			t.Fatalf("proxy request: %+v", r)
		}
	}
	if count.Load() != 2 || direct.Load() != 2 {
		t.Fatalf("counts proxy=%d destination=%d", count.Load(), direct.Load())
	}
	if e.icmp(context.Background(), Target{Host: "127.0.0.1"}).Status.Status != "skipped" || e.route(context.Background(), Target{Host: "127.0.0.1"}).Status.Status != "skipped" {
		t.Fatal("native probes in SOCKS mode")
	}
	e.cfg.SocksURL = "socks5h://127.0.0.1:1"
	if e.request(context.Background(), srv.URL, "GET", nil, 1024, "", false).Err == nil || direct.Load() != 2 {
		t.Fatal("SOCKS failure fell back to direct")
	}
}

func TestPUTExactBodyDirectAndSOCKS(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if r.Method != "PUT" || err != nil || len(r.TransferEncoding) != 0 || r.ContentLength != n || (n != 1024 && n != 1<<20) {
			t.Errorf("PUT method=%s length=%d received=%d transfer=%v err=%v", r.Method, r.ContentLength, n, r.TransferEncoding, err)
			w.WriteHeader(400)
			return
		}
		requests.Add(1)
		_, _ = fmt.Fprint(w, n)
	}))
	defer srv.Close()
	proxy, count := socksFixture(t)
	e := testEngine(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, mode := range []string{"", proxy} {
		e.cfg.SocksURL = mode
		for _, n := range []int{1024, 1 << 20} {
			r := e.request(context.Background(), srv.URL, "PUT", make([]byte, n), 1024, "", false)
			if r.Err != nil || r.Upload != int64(n) || r.Body != fmt.Sprint(n) {
				t.Fatalf("PUT proxy=%q length=%d result=%+v", mode, n, r)
			}
		}
	}
	if requests.Load() != 4 || count.Load() != 2 {
		t.Fatalf("PUT total=%d proxy=%d", requests.Load(), count.Load())
	}
	files, err := os.ReadDir(tmp)
	if err != nil || len(files) != 0 {
		t.Fatalf("PUT temporary files %v %v", files, err)
	}
}

func TestExecutorBoundsCancellationAndCleanup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	started := time.Now()
	out, err := (LocalExecutor{}).Exec(context.Background(), "yes x & sleep 20", nil)
	if err == nil || len(out) > MaxOutputBytes || time.Since(started) > time.Second {
		t.Fatalf("limit out=%d err=%v", len(out), err)
	}
	e := testEngine(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := e.request(ctx, srv.URL, "GET", nil, 1024, "", false)
	if r.Err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancel %+v", r)
	}
	files, err := os.ReadDir(tmp)
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files remain: %v %v", files, err)
	}
	// Remote-style watchdog must bound the tool even without context cancellation.
	start = time.Now()
	_, err = (LocalExecutor{}).Exec(context.Background(), boundedScript("exec sleep 20", 1), nil)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("watchdog failed: %v", err)
	}
}

func TestMissingKeyAndPartialFlags(t *testing.T) {
	e := testEngine(t)
	t.Setenv("MPX_TEST_MISSING_KEY", "")
	e.cfg.AbuseIPDBKeyEnv = "MPX_TEST_MISSING_KEY"
	if r := e.request(context.Background(), "https://api.abuseipdb.com/api/v2/check", "GET", nil, 8192, "", true); r.Err != ErrMissingKey {
		t.Fatalf("missing key %v", r.Err)
	}
	e.cfg.IPAPIKeyEnv = "MPX_TEST_MISSING_KEY"
	if r := e.request(context.Background(), "1.1.1.1", "IPAPI", nil, 8192, "", false); r.Err != ErrMissingKey {
		t.Fatalf("missing key %v", r.Err)
	}
	r := parseIPAPI(`{"ip":"1.1.1.1","is_bogon":false}`, "1.1.1.1", time.Now())
	if r.Status.Status != "partial" || len(r.ProviderFlags) != 1 {
		t.Fatal(r)
	}
	if h := parseMTR(`{"report":{"hubs":[{"count":1,"host":"192.0.2.1"}]}}`); len(h) != 0 {
		t.Fatal("missing RTT/loss became zero", h)
	}
}

func TestOldCurlUnknownBodyBound(t *testing.T) {
	actual, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl not installed")
	}
	tmp := t.TempDir()
	script := `#!/bin/sh
first="$1"; shift
# This invocation uses --max-filesize immediately before --output.
while [ "$#" -gt 0 ]; do
 if [ "$1" = '--max-filesize' ]; then shift 2; break; fi
 first="$first $1"; shift
done
# All first arguments are fixed curl flags with no whitespace-sensitive values.
exec ` + shellQuote(actual) + ` $first "$@"
`
	if err := os.WriteFile(tmp+"/curl", []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp+":"+os.Getenv("PATH"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for i := 0; i < 1024; i++ {
			if _, err := w.Write(make([]byte, 8192)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	e := testEngine(t)
	r := e.request(context.Background(), srv.URL, "GET", nil, 1024, "", false)
	if r.Err == nil || len(r.Body) > 1024 || r.Download > 1024+curlReadAllowance {
		t.Fatalf("old curl bound %+v", r)
	}
}

func TestSourceContractsAndValidation(t *testing.T) {
	at := time.Now()
	geo := parseGeoIP(`{"success":true,"ip":"1.1.1.1","country":"Australia","connection":{"asn":13335,"org":"APNIC"}}`, "source", at)
	if geo.Status.Status != "ok" || geo.ASN != "AS13335" || geo.ProviderFlags != nil {
		t.Fatalf("%+v", geo)
	}
	if parseGeoIP(`{"success":false,"ip":"1.1.1.1"}`, "source", at).Status.Status != "failed" {
		t.Fatal("provider false accepted")
	}
	rep := parseAbuseIPDB(`{"data":{"ipAddress":"1.1.1.1","abuseConfidenceScore":0,"totalReports":0,"isTor":false,"isWhitelisted":null}}`, "source", "1.1.1.1", at)
	if rep.Status.Status != "ok" || rep.ProviderFlags["is_tor"] != false {
		t.Fatal(rep)
	}
	if _, ok := rep.ProviderFlags["is_whitelisted"]; ok {
		t.Fatal("null interpreted as false")
	}
	flags := parseIPAPI(`{"ip":"1.1.1.1","is_vpn":false,"is_datacenter":true,"asn":{"asn":13335,"org":"Cloudflare"}}`, "1.1.1.1", at)
	if flags.Status.Status != "ok" || !flags.ProviderFlags["is_datacenter"] || len(flags.ProviderFlags) != 2 {
		t.Fatal(flags)
	}
	for _, host := range []string{"127.0.0.1;touch /tmp/diag-injection", "$(id)", "-x", "a\nsh"} {
		if (Config{Targets: []Target{{Host: host}}}).Validate() == nil {
			t.Errorf("host accepted %q", host)
		}
	}
	for _, u := range []string{"https://a/$(id)", "https://a/'x'", "file:///etc/passwd", "https://a/\nsh"} {
		if (Config{IPInfoURL: u}).Validate() == nil {
			t.Errorf("URL accepted %q", u)
		}
	}
	cfg := Config{AbuseIPDBKey: "secretmarker", IPAPIKey: "secretmarker"}
	b, _ := json.Marshal(cfg)
	if strings.Contains(string(b), "secretmarker") {
		t.Fatal("key leaked")
	}
}

func TestInterruptedPingDoesNotInventLoss(t *testing.T) {
	e := testEngine(t)
	e.cfg.Samples = 5
	e.cfg.RequestTimeoutSeconds = 1
	e.tools["ping"] = true
	if _, err := exec.LookPath("ping"); err == nil {
		l := e.icmp(context.Background(), Target{Host: "127.0.0.1"})
		if l.ValidSamples == 0 {
			t.Fatal("actual loopback ping returned no samples", l)
		}
		if l.Attempted == 0 && (l.LossPercent != nil || l.Status.Status != "partial") {
			t.Fatal("actual interrupted loss", l)
		}
	}
	tmp := t.TempDir()
	script := `#!/bin/sh
printf '64 bytes from 127.0.0.1: icmp_seq=0 ttl=64 time=0.100 ms\n64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time=0.200 ms\n'
exec sleep 20
`
	if err := os.WriteFile(tmp+"/ping", []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp+":"+os.Getenv("PATH"))
	l := e.icmp(context.Background(), Target{Host: "127.0.0.1"})
	if l.Attempted != 0 || l.ValidSamples != 2 || l.LossPercent != nil || l.Status.Status != "partial" || l.JitterSamples != 1 {
		t.Fatalf("interrupted ping invented loss: %+v", l)
	}
}

func TestActualRouteAndPingParsers(t *testing.T) {
	mtr := `{"report":{"hubs":[{"count":1,"host":"192.0.2.1","Loss%":0.0,"Avg":1.4},{"count":2,"host":"???","Loss%":100.0,"Avg":0}]}}`
	if h := parseMTR(mtr); len(h) != 2 || h[0].RTTMS == nil || !h[1].Unanswered {
		t.Fatal(h)
	}
	if h := parseTraceroute(" 1  192.0.2.1  1.2 ms\n 2 *\n"); len(h) != 2 || h[0].Address != "192.0.2.1" || !h[1].Unanswered {
		t.Fatal(h)
	}
	if p, err := exec.LookPath("traceroute"); err == nil {
		out, _ := exec.Command(p, "-n", "-m", "1", "-q", "1", "-w", "1", "127.0.0.1").CombinedOutput()
		if h := parseTraceroute(string(out)); len(h) != 1 {
			t.Fatalf("actual %s", out)
		}
	}
	e := testEngine(t)
	if _, err := exec.LookPath("ping"); err == nil {
		e.tools["ping"] = true
		l := e.icmp(context.Background(), Target{Host: "127.0.0.1"})
		if l.ValidSamples != 1 || l.Attempted != 1 || l.LossPercent == nil || *l.LossPercent != 0 {
			t.Fatal(l)
		}
	}

}

func TestPublicEndpointsManual(t *testing.T) {
	if os.Getenv("MPX_DIAGNOSTICS_PUBLIC_TEST") != "1" {
		t.Skip("opt-in external endpoint verification")
	}
	cfg := Config{Samples: 1, Speed: true, SpeedBytes: 1024, MaxBytes: 8 << 20, RequestTimeoutSeconds: 5, TotalTimeoutSeconds: 100}
	report, err := Run(context.Background(), cfg, LocalExecutor{})
	b, _ := json.MarshalIndent(report, "", "  ")
	if path := os.Getenv("MPX_DIAGNOSTICS_REPORT"); path != "" {
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("public report=%s", b)
	if err != nil {
		t.Fatal(err)
	}
	if report.Budget.ReservedBytes > cfg.MaxBytes || report.Budget.TransferredBytes > report.Budget.ReservedBytes {
		t.Fatal(report.Budget)
	}
}
