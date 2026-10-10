package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
		func(ep *render.Endpoint) { ep.Protocol = "hysteria2" },
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
	script := capabilityScript([]capabilityRequest{{"safe", "http://example.test/'$(touch /tmp/never);echo '"}})
	cmd := exec.Command("sh", "-n", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("POSIX shell syntax: %v %s", err, out)
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
