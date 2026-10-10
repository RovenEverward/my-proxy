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
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
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

type capabilityRunner struct {
	script string
	stdin  []byte
	output string
	err    error
	budget time.Duration
	calls  int
}

func (r *capabilityRunner) Run(context.Context, model.Node, string) (string, error) {
	panic("capability must use RunWithInput")
}
func (r *capabilityRunner) RunWithInput(ctx context.Context, n model.Node, script string, stdin []byte) (string, error) {
	r.calls++
	r.script, r.stdin = script, append([]byte(nil), stdin...)
	deadline, ok := ctx.Deadline()
	if ok {
		r.budget = time.Until(deadline)
	}
	return r.output, r.err
}

func capabilityEndpoint() render.Endpoint {
	return render.Endpoint{Address: "actual.example.com", Port: 443, Protocol: model.ProtocolVLESS, UUID: "8db4cae4-d335-467f-8b68-a9e0626fc91a", Network: model.NetworkWS, Path: "/actual", Host: "edge.example.com", Security: model.SecurityTLS, SNI: "edge.example.com"}
}

func capabilityEnvelope(key string, r capabilityResponse) string {
	return fmt.Sprintf("MPX1\t%s\t%d\t%d\t%s\t%s\t%s\n", key, r.curl, r.code, r.url, r.mime, base64.StdEncoding.EncodeToString(r.body))
}

func netflixResponse(id, region string) capabilityResponse {
	address := "https://www.netflix.com/" + strings.ToLower(region) + "/title/" + id
	body := fmt.Sprintf(`<html><script type="application/ld+json">{"@type":"TVSeries","name":"A title","url":%q}</script><script>{"countryCode":%q}</script></html>`, address, region)
	return capabilityResponse{code: 200, url: address, mime: "text/html", body: []byte(body)}
}

func TestProbeCapabilitiesContract(t *testing.T) {
	r := &capabilityRunner{output: capabilityEnvelope("youtube", capabilityResponse{code: 204, url: capabilityYouTubeURL})}
	ep := capabilityEndpoint()
	ep.Password = "secret'$(touch /tmp/should-not-exist)"
	got, err := ProbeCapabilities(context.Background(), r, model.Node{Address: "ssh.example.com"}, ep, []string{"youtube"})
	if err != nil || len(got) != 1 || got[0].Status != CapabilityAvailable || got[0].Reason != "youtube_connectivity_only" {
		t.Fatalf("observations=%+v err=%v", got, err)
	}
	if r.budget <= 0 || r.budget > capabilityBudget || r.calls != 1 {
		t.Fatalf("unbounded/multiple execution: %v calls=%d", r.budget, r.calls)
	}
	if strings.Contains(r.script, ep.UUID) || strings.Contains(r.script, ep.Password) || strings.Contains(r.script, ep.Address) {
		t.Fatal("endpoint or credentials embedded in shell")
	}
	var cfg struct {
		Inbounds []struct {
			Listen   string `json:"listen"`
			Protocol string `json:"protocol"`
			Port     int    `json:"port"`
		} `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(r.stdin, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inbounds) != 1 || cfg.Inbounds[0].Listen != "127.0.0.1" || cfg.Inbounds[0].Protocol != "socks" || cfg.Inbounds[0].Port != 0 || len(cfg.Outbounds) != 1 || cfg.Outbounds[0]["protocol"] != "vless" {
		t.Fatalf("unexpected minimal config: %s", r.stdin)
	}
	for _, value := range []string{ep.UUID, ep.Address, ep.Path, ep.Host, ep.SNI} {
		if !strings.Contains(string(r.stdin), value) {
			t.Fatalf("actual endpoint lost: %q", value)
		}
	}
	for _, fragment := range []string{"umask 077", "chmod 700", "chmod 600", "trap cleanup EXIT", "HUP INT TERM", "sleep 65", "wait", "mktemp -d /tmp/mpx-capability", "/dev/urandom", "--socks5-hostname", "--noproxy", "--max-time 12", "--connect-timeout 4", "--max-filesize 1048576", "curl -q", "rm -rf --", "${tmp:?}"} {
		if !strings.Contains(r.script, fragment) {
			t.Fatalf("missing shell safety %q", fragment)
		}
	}
}

func TestProbeCapabilitiesValidationAndFailures(t *testing.T) {
	for _, targets := range [][]string{{"chatgpt"}, {"netflix; echo secret"}, {"youtube", "youtube"}, {"egress", "invalid"}} {
		r := &capabilityRunner{}
		if _, err := ProbeCapabilities(context.Background(), r, model.Node{}, capabilityEndpoint(), targets); err == nil || r.calls != 0 {
			t.Fatalf("invalid targets executed: %v err=%v", targets, err)
		}
	}
	r := &capabilityRunner{}
	if got, err := ProbeCapabilities(context.Background(), r, model.Node{}, render.Endpoint{}, nil); err != nil || len(got) != 0 || r.calls != 0 {
		t.Fatalf("empty targets: %v %v", got, err)
	}
	for _, change := range []func(*render.Endpoint){
		func(ep *render.Endpoint) { ep.Port = 0 },
		func(ep *render.Endpoint) { ep.Address = "" },
		func(ep *render.Endpoint) { ep.UUID = "" },
		func(ep *render.Endpoint) { ep.AllowInsecure = true },
		func(ep *render.Endpoint) { ep.Protocol = "unsupported" },
	} {
		ep := capabilityEndpoint()
		change(&ep)
		if _, err := capabilityConfig(ep); err == nil {
			t.Fatalf("invalid endpoint accepted: %+v", ep)
		}
	}
	r = &capabilityRunner{err: errors.New("ssh unavailable")}
	got, err := ProbeCapabilities(context.Background(), r, model.Node{}, capabilityEndpoint(), []string{"egress", "netflix"})
	if err == nil || len(got) != 2 || got[0].Status != CapabilityUnknown || got[1].Status != CapabilityUnknown {
		t.Fatalf("runner failure marked definitive: %+v %v", got, err)
	}
	r = &capabilityRunner{output: "unexpected shell text"}
	got, err = ProbeCapabilities(context.Background(), r, model.Node{}, capabilityEndpoint(), []string{"youtube"})
	if err != nil || got[0].Status != CapabilityUnknown || got[0].Reason != "parser_error: response_envelope" {
		t.Fatalf("parser error: %+v %v", got, err)
	}
}

func TestProbeCapabilitiesQUICContract(t *testing.T) {
	for _, protocol := range []model.Protocol{model.ProtocolHysteria2, model.ProtocolTUIC} {
		ep := capabilityEndpoint()
		ep.Protocol, ep.Network, ep.Security = protocol, model.NetworkUDP, model.SecurityTLS
		ep.Password, ep.CongestionControl = "secret'$(echo nope)\n\"listen_port\": 0", "cubic"
		r := &capabilityRunner{output: capabilityEnvelope("youtube", capabilityResponse{code: 204, url: capabilityYouTubeURL})}
		if _, err := ProbeCapabilities(context.Background(), r, model.Node{}, ep, []string{"youtube"}); err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(r.stdin, &cfg); err != nil {
			t.Fatal(err)
		}
		inbounds := cfg["inbounds"].([]any)
		outbounds := cfg["outbounds"].([]any)
		in, out := inbounds[0].(map[string]any), outbounds[0].(map[string]any)
		if len(inbounds) != 1 || len(outbounds) != 1 || in["type"] != "socks" || in["listen"] != "127.0.0.1" || in["listen_port"] != float64(0) || out["type"] != string(protocol) || out["password"] != ep.Password || out["server_port"] != float64(ep.Port) || out["server"] != ep.Address {
			t.Fatalf("QUIC endpoint altered: %s", r.stdin)
		}
		if strings.Contains(r.script, ep.Password) || strings.Contains(r.script, ep.Address) || !strings.Contains(r.script, singBoxBin) || !strings.Contains(r.script, CapabilityDetector+"/"+CapabilityDetectorVersion) {
			t.Fatal("QUIC script core selection or secret isolation failed")
		}
		for _, change := range []func(*render.Endpoint){
			func(ep *render.Endpoint) { ep.Password = "" },
			func(ep *render.Endpoint) { ep.Port = 0 },
			func(ep *render.Endpoint) { ep.Address = "" },
			func(ep *render.Endpoint) { ep.Network = model.NetworkTCP },
			func(ep *render.Endpoint) { ep.Security = model.SecurityNone },
			func(ep *render.Endpoint) { ep.AllowInsecure = true },
		} {
			bad := ep
			change(&bad)
			runner := &capabilityRunner{}
			if _, err := ProbeCapabilities(context.Background(), runner, model.Node{}, bad, []string{"egress"}); err == nil || runner.calls != 0 {
				t.Fatal("invalid QUIC endpoint reached SSH")
			}
		}
		if protocol == model.ProtocolTUIC {
			for _, change := range []func(*render.Endpoint){func(ep *render.Endpoint) { ep.UUID = "" }, func(ep *render.Endpoint) { ep.CongestionControl = "invalid" }} {
				bad := ep
				change(&bad)
				runner := &capabilityRunner{}
				if _, err := ProbeCapabilities(context.Background(), runner, model.Node{}, bad, []string{"egress"}); err == nil || runner.calls != 0 {
					t.Fatal("invalid TUIC auth/cc reached SSH")
				}
			}
		}
	}
}

func TestCapabilityEgressParser(t *testing.T) {
	for _, tc := range []struct {
		name, body, mime, address, want string
	}{
		{"valid", `{"ip":"8.8.8.8","country_code":"US"}`, "application/json; charset=utf-8", capabilityEgressURL, CapabilityAvailable},
		{"bad json", `{`, "application/json", capabilityEgressURL, CapabilityUnknown},
		{"missing region", `{"ip":"8.8.8.8"}`, "application/json", capabilityEgressURL, CapabilityUnknown},
		{"bad region", `{"ip":"8.8.8.8","country_code":"United States"}`, "application/json", capabilityEgressURL, CapabilityUnknown},
		{"sentinel region", `{"ip":"8.8.8.8","country_code":"ZZ"}`, "application/json", capabilityEgressURL, CapabilityUnknown},
		{"bad ip", `{"ip":"not-an-ip","country_code":"US"}`, "application/json", capabilityEgressURL, CapabilityUnknown},
		{"private ip", `{"ip":"192.168.1.1","country_code":"US"}`, "application/json", capabilityEgressURL, CapabilityUnknown},
		{"loopback", `{"ip":"127.0.0.1","country_code":"US"}`, "application/json", capabilityEgressURL, CapabilityUnknown},
		{"wrong origin", `{"ip":"8.8.8.8","country_code":"US"}`, "application/json", "https://api.ip.sb.evil/geoip", CapabilityUnknown},
		{"html masquerade", `{"ip":"8.8.8.8","country_code":"US"}`, "text/html", capabilityEgressURL, CapabilityUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := capabilityObserve("egress", map[string]capabilityResponse{"egress": {code: 200, mime: tc.mime, url: tc.address, body: []byte(tc.body)}})
			if got.Status != tc.want || got.Status == CapabilityUnknown && !strings.HasPrefix(got.Reason, "parser_error:") {
				t.Fatalf("got %+v", got)
			}
			if got.Status == CapabilityAvailable && (got.Region != "US" || got.ExitIP != "8.8.8.8") {
				t.Fatalf("missing trusted geo: %+v", got)
			}
		})
	}
}

func TestCapabilityNetflixParser(t *testing.T) {
	for _, tc := range []struct {
		name, status, reason string
		modify               func(map[string]capabilityResponse)
	}{
		{"licensed catalog", CapabilityAvailable, "netflix_licensed_catalog", func(rs map[string]capabilityResponse) {}},
		{"generic 200", CapabilityUnknown, "parser_error: netflix_title_evidence", func(rs map[string]capabilityResponse) {
			r := rs["licensed"]
			r.body = []byte("Welcome to Netflix")
			rs["licensed"] = r
		}},
		{"403 originals only", CapabilityUnavailable, "netflix_originals_only", func(rs map[string]capabilityResponse) { r := rs["licensed"]; r.code = 403; rs["licensed"] = r }},
		{"403 denied", CapabilityUnavailable, "http_denied_403", func(rs map[string]capabilityResponse) {
			r := rs["licensed"]
			r.code = 403
			rs["licensed"] = r
			r = rs["original"]
			r.code = 403
			rs["original"] = r
		}},
		{"originals only", CapabilityUnavailable, "netflix_originals_only", func(rs map[string]capabilityResponse) { r := rs["licensed"]; r.code = 404; rs["licensed"] = r }},
		{"Oh no denied", CapabilityUnavailable, "netflix_title_unavailable", func(rs map[string]capabilityResponse) {
			r := rs["licensed"]
			r.body = []byte("Oh no!")
			rs["licensed"] = r
		}},
		{"timeout", CapabilityUnknown, "request_timeout", func(rs map[string]capabilityResponse) { r := rs["licensed"]; r.curl = 28; rs["licensed"] = r }},
		{"network", CapabilityUnknown, "request_network_error_7", func(rs map[string]capabilityResponse) { r := rs["licensed"]; r.curl = 7; rs["licensed"] = r }},
		{"original 403", CapabilityUnavailable, "http_denied_403", func(rs map[string]capabilityResponse) { r := rs["original"]; r.code = 403; rs["original"] = r }},
		{"original 404", CapabilityUnavailable, "http_denied_404", func(rs map[string]capabilityResponse) { r := rs["original"]; r.code = 404; rs["original"] = r }},
		{"original timeout", CapabilityUnknown, "request_timeout", func(rs map[string]capabilityResponse) { r := rs["original"]; r.curl = 28; rs["original"] = r }},
		{"wrong title", CapabilityUnknown, "parser_error: netflix_title_evidence", func(rs map[string]capabilityResponse) { rs["licensed"] = netflixResponse("80018499", "US") }},
		{"redirect to login", CapabilityUnknown, "parser_error: netflix_title_evidence", func(rs map[string]capabilityResponse) {
			r := rs["licensed"]
			r.url = "https://www.netflix.com/login"
			rs["licensed"] = r
		}},
		{"conflicting region", CapabilityUnknown, "parser_error: netflix_region", func(rs map[string]capabilityResponse) {
			r := rs["licensed"]
			r.body = append(r.body, []byte(`{"countryCode":"JP"}`)...)
			rs["licensed"] = r
		}},
		{"broken JSONLD", CapabilityUnknown, "parser_error: netflix_title_evidence", func(rs map[string]capabilityResponse) {
			r := rs["licensed"]
			r.body = []byte(`<script type="application/ld+json">{broken</script>`)
			rs["licensed"] = r
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := map[string]capabilityResponse{"licensed": netflixResponse("81280792", "US"), "original": netflixResponse("80018499", "US")}
			tc.modify(rs)
			got := capabilityObserve("netflix", rs)
			if got.Status != tc.status || got.Reason != tc.reason {
				t.Fatalf("got %+v; want %s %s", got, tc.status, tc.reason)
			}
		})
	}
	// The mature reference's id/countryName signal is recognized on a title URL
	// without a region path. Unknown/conflicting country signals remain unknown.
	r := netflixResponse("81280792", "US")
	r.url = capabilityLicensedURL
	r.body = []byte(`{"id":"SG","countryName":"Singapore"}`)
	if got := netflixRegion(r); got != "SG" {
		t.Fatalf("reference country signal: %q", got)
	}
}

func TestCapabilityYouTubeAndEnvelope(t *testing.T) {
	for _, r := range []capabilityResponse{{code: 200, url: capabilityYouTubeURL}, {code: 204, url: "https://evil.test/generate_204"}, {code: 204, curl: 28, url: capabilityYouTubeURL}} {
		if got := capabilityObserve("youtube", map[string]capabilityResponse{"youtube": r}); got.Status != CapabilityUnknown {
			t.Fatalf("false connectivity: %+v", got)
		}
	}
	requests, err := capabilityRequests([]string{"egress", "youtube", "netflix"})
	if err != nil || len(requests) != 4 || requests[2].address != capabilityLicensedURL || requests[3].address != capabilityOriginalURL {
		t.Fatalf("fixed request contract: %+v %v", requests, err)
	}
	one := []capabilityRequest{{"youtube", capabilityYouTubeURL}}
	valid := capabilityEnvelope("youtube", capabilityResponse{code: 204, url: capabilityYouTubeURL})
	for _, bad := range []string{"", valid + valid, strings.Replace(valid, "MPX1", "MPX2", 1), strings.Replace(valid, "youtube", "unexpected", 1), "MPX1\tyoutube\t0\t204\tx\tx\t%%\n"} {
		if _, err := capabilityResponses(bad, one); err == nil {
			t.Fatalf("malformed envelope accepted: %q", bad)
		}
	}
}

func TestCapabilityShellSyntaxAndQuoting(t *testing.T) {
	for _, core := range []model.Core{model.CoreXray, model.CoreSingBox} {
		script := capabilityScriptForCore([]capabilityRequest{{"safe", "http://example.test/'$(touch /tmp/never);echo '"}}, core)
		cmd := exec.Command("sh", "-n", "-c", script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s POSIX shell syntax: %v %s", core, err, out)
		}
	}
}

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
		if r.Host != "capability-only.invalid" || r.URL.Path != "/geoip" {
			t.Errorf("wrong request: host=%q path=%q", r.Host, r.URL.Path)
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

// Public availability varies with the machine's route. This opt-in observation
// exercises the production URLs and parser without using any user's infrastructure.
func TestLiveCapabilityPublicTargets(t *testing.T) {
	if os.Getenv("MPX_CAPABILITY_PUBLIC_TEST") != "1" {
		t.Skip("set MPX_CAPABILITY_PUBLIC_TEST=1 to observe fixed public targets")
	}
	bin := liveXray(t)
	dir := t.TempDir()
	port := freePort(t)
	uuid := "8db4cae4-d335-467f-8b68-a9e0626fc91a"
	config := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"decryption":"none","clients":[{"id":%q}]}}],"outbounds":[{"protocol":"freedom"}]}`, port, uuid)
	configPath := filepath.Join(dir, "public-server.json")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
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
	observations, err := ProbeCapabilities(context.Background(), localRunner{xrayBin: bin}, model.Node{}, ep, []string{"egress", "youtube", "netflix"})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 3 {
		t.Fatal("missing target observations")
	}
	for _, observation := range observations {
		t.Logf("target=%s status=%s region=%s reason=%s ip_present=%t", observation.Target, observation.Status, observation.Region, observation.Reason, observation.ExitIP != "")
		if observation.Status != CapabilityAvailable && observation.Status != CapabilityUnavailable && observation.Status != CapabilityUnknown {
			t.Fatal("invalid observation status")
		}
	}
}
