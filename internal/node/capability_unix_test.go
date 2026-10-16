//go:build unix

package node

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// Uses the SAME config/script helpers as ProbeCapabilities, with only target URL
// injection. The unresolvable .invalid hostname can be reached solely through
// the actual VLESS endpoint: the Xray server redirects it to the test HTTP server.
func TestLiveCapabilityActualProxy(t *testing.T) {
	bin := liveXray(t)
	var count atomic.Int32
	var slow atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if slow.Load() {
			<-r.Context().Done()
			return
		}
		if r.Host != "capability-only.invalid" {
			t.Errorf("wrong request: host=%q path=%q", r.Host, r.URL.Path)
		}
		if r.URL.Path == "/oversizemeta" {
			w.Header().Set("Content-Type", strings.Repeat("a", capabilityMetaLimit+1024))
			_, _ = w.Write([]byte("bounded body"))
			return
		}
		if r.URL.Path == "/oversized" || r.URL.Path == "/largevalid" {
			size := 2 * 1024 * 1024
			if r.URL.Path == "/oversized" {
				size = capabilityBodyLimit + 1024*1024
			}
			// Flushing before the body forces unknown length, the curl 7.81 gap.
			w.Header().Set("Content-Type", "text/html")
			w.(http.Flusher).Flush()
			_, _ = w.Write(bytes.Repeat([]byte("a"), size))
			return
		}
		if r.URL.Path != "/geoip" {
			t.Errorf("wrong path=%q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ip":"8.8.8.8","country_code":"US"}`)
	}))
	defer target.Close()
	dir := t.TempDir()
	port := freePort(t)
	uuid := "8db4cae4-d335-467f-8b68-a9e0626fc91a"
	serverConfig := map[string]any{
		"log":       map[string]any{"loglevel": "none"},
		"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "vless", "settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": uuid}}}}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"redirect": strings.TrimPrefix(target.URL, "http://")}}},
	}
	cfg, _ := json.Marshal(serverConfig)
	configPath := filepath.Join(dir, "server.json")
	if err := os.WriteFile(configPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(bin, "run", "-c", configPath)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Xray server failed to start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ep := render.Endpoint{Address: "127.0.0.1", Port: port, Protocol: model.ProtocolVLESS, UUID: uuid, Network: model.NetworkTCP, Security: model.SecurityNone}
	requests := []capabilityRequest{{"egress", "http://capability-only.invalid/geoip"}}
	client, err := capabilityConfig(ep)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	// NO_PROXY must not bypass SOCKS even for hostile inherited environments.
	t.Setenv("NO_PROXY", "*")
	// Instrument only the private test shell's mktemp/pid assignments; the
	// production request/cleanup code is unchanged and every tracked PID must die.
	tracePath := filepath.Join(dir, "trace")
	traceScript := func() string {
		script := strings.TrimSuffix(strings.TrimPrefix(capabilityScript(requests), "sh -c '"), "'")
		script = strings.ReplaceAll(script, `'\''`, `'`)
		script = strings.Replace(script, `chmod 700 "$tmp"`, `printf 'tmp:%s\n' "$tmp" >> `+shellQuote(tracePath)+"\n"+`chmod 700 "$tmp"`, 1)
		script = strings.Replace(script, `& pid=$!`, `& pid=$!; printf 'pid:%s\n' "$pid" >> `+shellQuote(tracePath), 1)
		script = strings.Replace(script, `& guard=$!`, `& guard=$!; printf 'pid:%s\n' "$guard" >> `+shellQuote(tracePath), 1)
		for _, name := range []string{"sleeper", "curlpid", "bodypid", "metapid"} {
			script = strings.Replace(script, "& "+name+"=$!", "& "+name+"=$!; printf 'pid:%s\\n' \"$"+name+"\" >> "+shellQuote(tracePath), 1)
		}
		script = strings.Replace(script, `  printf 'MPX1`, `  printf 'sizes:%s:%s\n' "$bodybytes" "$metabytes" >> `+shellQuote(tracePath)+"\n"+`  printf 'MPX1`, 1)
		return "sh -c " + shellQuote(script)
	}
	assertCleanup := func() {
		t.Helper()
		trace, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(trace)), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				t.Fatalf("invalid cleanup trace %q", line)
			}
			if key == "sizes" {
				continue
			}
			if key == "tmp" {
				if _, err := os.Stat(value); !os.IsNotExist(err) {
					t.Fatalf("temporary credentials left behind: %q err=%v", value, err)
				}
			} else {
				pid, err := strconv.Atoi(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := syscall.Kill(pid, 0); err == nil {
					t.Fatalf("probe process leaked: %d", pid)
				}
			}
		}
	}
	out, err := (localRunner{xrayBin: bin}).RunWithInput(ctx, model.Node{}, traceScript(), client)
	if err != nil {
		t.Fatal(err)
	}
	responses, err := capabilityResponses(out, requests)
	if err != nil {
		t.Fatalf("%v output=%q", err, out)
	}
	r := responses["egress"]
	if r.curl != 0 || r.code != 200 || count.Load() != 1 || string(r.body) != `{"ip":"8.8.8.8","country_code":"US"}` {
		t.Fatalf("proxy did not reach target: %+v requests=%d", r, count.Load())
	}
	assertCleanup()
	// A different user's credential MUST fail, despite the target being reachable
	// by the server and the previous successful probe using the same actual endpoint.
	ep.UUID = "8db4cae4-d335-467f-8b68-a9e0626fc91b"
	client, err = capabilityConfig(ep)
	if err != nil {
		t.Fatal(err)
	}
	out, err = (localRunner{xrayBin: bin}).RunWithInput(ctx, model.Node{}, traceScript(), client)
	if err != nil {
		t.Fatal(err)
	}
	responses, err = capabilityResponses(out, requests)
	if err != nil || responses["egress"].curl == 0 || count.Load() != 1 {
		t.Fatalf("failed credentials fell back or reached target: %+v count=%d err=%v", responses, count.Load(), err)
	}
	assertCleanup()
	// Simulate curl 7.81 by forwarding to the real curl without --max-filesize.
	// The actual Xray/SOCKS/HTTP chain still runs; FIFO bounds must do the work.
	realCurl, err := exec.LookPath("curl")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "curl")
	// Put the explicit filesize option first, then discard exactly that pair.
	wrapperBody := "#!/bin/sh\n[ \"$1\" = '--max-filesize' ] || exit 91\nshift 2\nexec " + shellQuote(realCurl) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(wrapperBody), 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	ep.UUID = uuid
	client, err = capabilityConfig(ep)
	if err != nil {
		t.Fatal(err)
	}
	originalRequests := requests
	requests = []capabilityRequest{{"licensed", "http://capability-only.invalid/oversized"}, {"youtube", "http://capability-only.invalid/oversizemeta"}, {"egress", "http://capability-only.invalid/geoip"}, {"original", "http://capability-only.invalid/largevalid"}}
	boundedScript := strings.Replace(traceScript(), "curl -q", "curl --max-filesize "+strconv.Itoa(capabilityBodyLimit)+" -q", 1)
	boundedScript = strings.Replace(boundedScript, "--max-time 12 --max-filesize "+strconv.Itoa(capabilityBodyLimit), "--max-time 12", 1)
	// Start a fresh context: wrong-auth curl above may consume the first budget.
	boundedCtx, boundedCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer boundedCancel()
	out, err = (localRunner{xrayBin: bin}).RunWithInput(boundedCtx, model.Node{}, boundedScript, client)
	if err != nil {
		t.Fatal(err)
	}
	responses, err = capabilityResponses(out, requests)
	if err != nil || len(out) > len(requests)*capabilityLineLimit || responses["licensed"].curl != 63 || len(responses["licensed"].body) != 0 || responses["youtube"].curl != 63 || len(responses["youtube"].body) != 0 || responses["egress"].curl != 0 || responses["original"].curl != 0 || len(responses["original"].body) != 2*1024*1024 {
		t.Fatalf("legacy curl bounds failed: err=%v output_bytes=%d oversized_rc=%d egress_rc=%d valid_bytes=%d", err, len(out), responses["licensed"].curl, responses["egress"].curl, len(responses["original"].body))
	}
	assertCleanup()
	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	reachedBodyLimit, reachedMetaLimit := false, false
	for _, line := range strings.Split(string(traceData), "\n") {
		if !strings.HasPrefix(line, "sizes:") {
			continue
		}
		parts := strings.Split(line, ":")
		bodySize, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		metaSize, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
		if bodySize > capabilityBodyLimit+1 || metaSize > capabilityMetaLimit+1 {
			t.Fatalf("disk bounds exceeded: body=%d meta=%d", bodySize, metaSize)
		}
		reachedBodyLimit = reachedBodyLimit || bodySize == capabilityBodyLimit+1
		reachedMetaLimit = reachedMetaLimit || metaSize == capabilityMetaLimit+1
	}
	if !reachedBodyLimit || !reachedMetaLimit {
		t.Fatal("oversize fixtures did not reach both hard bounds")
	}
	t.Log("legacy curl simulation: chunked body and metadata stopped at limit+1; failed bodies omitted; next egress and 2MiB response retained")
	t.Setenv("PATH", oldPath)
	requests = originalRequests
	// Exercise the remote watchdog while curl is still active. Shorten only its
	// test budget; the same trap must kill/reap curl, Xray, guard and its sleeper.
	ep.UUID = uuid
	client, err = capabilityConfig(ep)
	if err != nil {
		t.Fatal(err)
	}
	slow.Store(true)
	started := time.Now()
	_, err = (localRunner{xrayBin: bin}).RunWithInput(ctx, model.Node{}, strings.Replace(traceScript(), "sleep 65", "sleep 3", 1), client)
	if err == nil || time.Since(started) > 6*time.Second {
		t.Fatalf("watchdog not bounded: err=%v elapsed=%v", err, time.Since(started))
	}
	assertCleanup()
}

// capabilityQUICRunner executes the production script with local trusted cores.
// Instrumentation records cleanup resources without changing proxy requests.
type capabilityQUICRunner struct {
	bin, trust, trace string
	watchdog          bool
}

func (r capabilityQUICRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	return r.RunWithInput(ctx, n, script, nil)
}
func (r capabilityQUICRunner) RunWithInput(ctx context.Context, _ model.Node, script string, input []byte) (string, error) {
	// macOS native Go trust ignores SSL_CERT_FILE. Supply the fixture CA via
	// sing-box's TLS trust setting; hostname and signature checks remain enabled.
	var cfg map[string]any
	if err := json.Unmarshal(input, &cfg); err != nil {
		return "", err
	}
	cfg["outbounds"].([]any)[0].(map[string]any)["tls"].(map[string]any)["certificate_path"] = r.trust
	var err error
	input, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	script = strings.TrimSuffix(strings.TrimPrefix(script, "sh -c '"), "'")
	script = strings.ReplaceAll(script, `'\''`, `'`)
	for _, item := range []struct{ before, after string }{
		{`chmod 700 "$tmp"`, `printf 'tmp:%s\n' "$tmp" >> ` + shellQuote(r.trace) + "\n" + `chmod 700 "$tmp"`},
		{`& pid=$!`, `& pid=$!; printf 'pid:%s\n' "$pid" >> ` + shellQuote(r.trace)},
		{`& guard=$!`, `& guard=$!; printf 'pid:%s\n' "$guard" >> ` + shellQuote(r.trace)},
		{`& sleeper=$!`, `& sleeper=$!; printf 'pid:%s\n' "$sleeper" >> ` + shellQuote(r.trace)},
		{`& curlpid=$!`, `& curlpid=$!; printf 'pid:%s\n' "$curlpid" >> ` + shellQuote(r.trace)},
		{`& bodypid=$!`, `& bodypid=$!; printf 'pid:%s\n' "$bodypid" >> ` + shellQuote(r.trace)},
		{`& metapid=$!`, `& metapid=$!; printf 'pid:%s\n' "$metapid" >> ` + shellQuote(r.trace)},
	} {
		script = strings.Replace(script, item.before, item.after, 1)
	}
	if r.watchdog {
		script = strings.Replace(script, "sleep 65", "sleep 3", 1)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = append(os.Environ(), "MPX_SING_BOX_BIN="+r.bin, "SSL_CERT_FILE="+r.trust, "NO_PROXY=*")
	cmd.Stdin = bytes.NewReader(input)
	// SIGTERM gives the same production trap a chance to clean canceled requests.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("local QUIC probe: %w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}
func capabilityAssertCleanup(t *testing.T, tracePath string) {
	t.Helper()
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(trace)), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("invalid trace %q", line)
		}
		if key == "tmp" {
			if _, err := os.Stat(value); !os.IsNotExist(err) {
				t.Fatalf("credential directory leaked: %q err=%v", value, err)
			}
		} else {
			pid, err := strconv.Atoi(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); err == nil {
				t.Fatalf("process leaked: %d", pid)
			}
		}
	}
}
func capabilityTrustedCertificate(t *testing.T, dir string) (tls.Certificate, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mpx capability local test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"api.ip.sb", "probe.example.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certPath, keyPath := filepath.Join(dir, "trust.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPath, keyPath
}

// The official URL remains unchanged. Only the actual server redirects that
// destination to the local trusted HTTPS origin: SOCKS -> QUIC ep -> HTTPS.
func TestLiveCapabilityQUICActualProxy(t *testing.T) {
	bin := os.Getenv("MPX_SING_BOX_BIN")
	if bin == "" {
		t.Skip("set MPX_SING_BOX_BIN to an official sing-box with_quic binary")
	}
	for _, protocol := range []model.Protocol{model.ProtocolHysteria2, model.ProtocolTUIC} {
		t.Run(string(protocol), func(t *testing.T) {
			dir := t.TempDir()
			cert, certPath, keyPath := capabilityTrustedCertificate(t, dir)
			var count atomic.Int32
			var slow atomic.Bool
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if slow.Load() {
					<-r.Context().Done()
					return
				}
				if r.Host != "api.ip.sb" || r.URL.Path != "/geoip" || r.UserAgent() != CapabilityDetector+"/"+CapabilityDetectorVersion {
					t.Errorf("unexpected official request host=%q path=%q agent=%q", r.Host, r.URL.Path, r.UserAgent())
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"ip":"8.8.8.8","country_code":"US"}`)
			}))
			target.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
			target.StartTLS()
			defer target.Close()
			l, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := l.LocalAddr().(*net.UDPAddr).Port
			_ = l.Close()
			password, uuid := "authorized-independent-test-password", "8db4cae4-d335-467f-8b68-a9e0626fc91a"
			user := map[string]any{"password": password}
			in := map[string]any{"type": string(protocol), "listen": "127.0.0.1", "listen_port": port, "users": []any{user}, "tls": map[string]any{"enabled": true, "certificate_path": certPath, "key_path": keyPath}}
			if protocol == model.ProtocolTUIC {
				user["uuid"] = uuid
				in["congestion_control"] = "cubic"
			}
			targetPort := target.Listener.Addr().(*net.TCPAddr).Port
			config := map[string]any{"log": map[string]any{"level": "error"}, "inbounds": []any{in}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"rules": []any{map[string]any{"action": "route-options", "override_address": "127.0.0.1", "override_port": targetPort}}, "final": "direct"}}
			body, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, "quic-server.json")
			if err := os.WriteFile(configPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			server := exec.Command(bin, "run", "-c", configPath)
			if err := server.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
			t.Setenv("CURL_CA_BUNDLE", certPath)
			ep := render.Endpoint{Address: "127.0.0.1", Port: port, Protocol: protocol, Password: password, UUID: uuid, Network: model.NetworkUDP, Security: model.SecurityTLS, SNI: "probe.example.test", CongestionControl: "cubic"}
			runner := capabilityQUICRunner{bin: bin, trust: certPath, trace: filepath.Join(dir, "trace")}
			observations, err := ProbeCapabilities(context.Background(), runner, model.Node{}, ep, []string{"egress"})
			if err != nil || len(observations) != 1 || observations[0].Status != CapabilityAvailable || observations[0].Region != "US" || count.Load() != 1 {
				t.Fatalf("actual QUIC proxy failed: obs=%+v err=%v hits=%d", observations, err, count.Load())
			}
			capabilityAssertCleanup(t, runner.trace)
			ep.Password = "wrong-authentication"
			observations, err = ProbeCapabilities(context.Background(), runner, model.Node{}, ep, []string{"egress"})
			if err != nil || len(observations) != 1 || observations[0].Status != CapabilityUnknown || !strings.HasPrefix(observations[0].Reason, "request_") || count.Load() != 1 {
				t.Fatalf("wrong auth fallback: obs=%+v err=%v hits=%d", observations, err, count.Load())
			}
			capabilityAssertCleanup(t, runner.trace)
			ep.Password = password
			slow.Store(true)
			runner.watchdog = true
			started := time.Now()
			observations, err = ProbeCapabilities(context.Background(), runner, model.Node{}, ep, []string{"egress"})
			if err == nil || observations[0].Status != CapabilityUnknown || time.Since(started) > 6*time.Second {
				t.Fatalf("watchdog unbounded: %+v %v", observations, err)
			}
			capabilityAssertCleanup(t, runner.trace)
			runner.watchdog = false
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			observations, err = ProbeCapabilities(ctx, runner, model.Node{}, ep, []string{"egress"})
			if err == nil || observations[0].Status != CapabilityUnknown {
				t.Fatalf("cancel definitive: %+v %v", observations, err)
			}
			capabilityAssertCleanup(t, runner.trace)
			t.Logf("%s production entry: trusted TLS, authorized hit, wrong auth no hit, watchdog/cancel cleanup verified", protocol)
		})
	}
}
