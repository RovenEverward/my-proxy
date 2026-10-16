package node

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

type boundedCapabilityRunner struct {
	capabilityRunner
	limit int
}

func (r *boundedCapabilityRunner) RunBounded(ctx context.Context, n model.Node, script string, stdin []byte, limit int) (string, error) {
	r.limit = limit
	return r.capabilityRunner.RunWithInput(ctx, n, script, stdin)
}

func TestProbeCapabilitiesBoundedTransport(t *testing.T) {
	r := &boundedCapabilityRunner{capabilityRunner: capabilityRunner{output: capabilityEnvelope("youtube", capabilityResponse{code: 204, url: capabilityYouTubeURL})}}
	got, err := ProbeCapabilities(context.Background(), r, model.Node{}, capabilityEndpoint(), []string{"youtube"})
	if err != nil || got[0].Status != CapabilityAvailable || r.limit != capabilityLineLimit || r.calls != 1 {
		t.Fatalf("bounded transport: observations=%+v limit=%d calls=%d err=%v", got, r.limit, r.calls, err)
	}
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
	for _, fragment := range []string{"umask 077", "chmod 700", "chmod 600", "trap cleanup EXIT", "HUP INT TERM", "sleep 65", "wait", "mktemp -d /tmp/mpx-capability", "/dev/urandom", "--socks5-hostname", "--noproxy", "--max-time 12", "--connect-timeout 4", "--max-filesize " + strconv.Itoa(capabilityBodyLimit), "mkfifo", "head -c " + strconv.Itoa(capabilityBodyLimit+1), "head -c " + strconv.Itoa(capabilityMetaLimit+1), "curl -q", "rm -rf --", "${tmp:?}"} {
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

func TestCapabilityIndependentEnvelopes(t *testing.T) {
	requests, _ := capabilityRequests([]string{"egress", "youtube", "netflix"})
	egress := capabilityEnvelope("egress", capabilityResponse{code: 200, url: capabilityEgressURL, mime: "application/json", body: []byte(`{"ip":"8.8.8.8","country_code":"US"}`)})
	youtube := capabilityEnvelope("youtube", capabilityResponse{code: 204, url: capabilityYouTubeURL})
	licensed := capabilityEnvelope("licensed", netflixResponse("81280792", "US"))
	original := capabilityEnvelope("original", netflixResponse("80018499", "US"))
	for _, tc := range []struct {
		name, netflix, reason string
	}{
		{"malformed", "MPX1\tlicensed\t0\t200\n" + original, "parser_error: response_envelope"},
		{"broken base64", "MPX1\tlicensed\t0\t200\tx\tx\t%%\n" + original, "parser_error: response_envelope"},
		{"missing original", licensed, "parser_error: response_envelope"},
		{"duplicate licensed", licensed + original + licensed, "parser_error: response_envelope"},
		{"duplicate original", licensed + original + original, "parser_error: response_envelope"},
		{"oversized", capabilityEnvelope("licensed", capabilityResponse{code: 200, body: bytes.Repeat([]byte("a"), capabilityBodyLimit+1)}) + original, "parser_error: response_too_large"},
		{"oversized metadata", capabilityEnvelope("licensed", capabilityResponse{code: 200, url: strings.Repeat("x", capabilityMetaLimit+1)}) + original, "parser_error: response_too_large"},
		{"curl limit", capabilityEnvelope("licensed", capabilityResponse{curl: 63}) + original, "parser_error: response_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &capabilityRunner{output: egress + youtube + tc.netflix}
			got, err := ProbeCapabilities(context.Background(), r, model.Node{}, capabilityEndpoint(), []string{"egress", "youtube", "netflix"})
			if err != nil || len(got) != 3 || got[0].Status != CapabilityAvailable || got[1].Status != CapabilityAvailable || got[2].Status != CapabilityUnknown || got[2].Reason != tc.reason {
				t.Fatalf("isolated observations=%+v err=%v", got, err)
			}
		})
	}
	for _, bad := range []string{egress + youtube + licensed + original + "unattributed\n", egress + youtube + licensed + original + "\n", strings.Replace(egress+youtube+licensed+original, "MPX1\tlicensed", "MPX2\tlicensed", 1), egress + youtube + capabilityEnvelope("not-requested", capabilityResponse{})} {
		responses, err := capabilityResponses(bad, requests)
		if err == nil || responses != nil {
			t.Fatal("unattributable request envelope retained evidence")
		}
	}
	if _, err := capabilityResponses(youtube, append(requests, requests[0])); err == nil {
		t.Fatal("duplicate request envelope accepted")
	}
	// A page larger than the former 1 MiB bound must retain title/region evidence.
	r := netflixResponse("81280792", "US")
	r.body = append(bytes.Repeat([]byte(" "), 2*1024*1024), r.body...)
	parsed, err := capabilityResponses(egress+youtube+capabilityEnvelope("licensed", r)+original, requests)
	if err != nil || capabilityObserve("netflix", parsed).Status != CapabilityAvailable {
		t.Fatalf("large valid title rejected: %v", err)
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
