package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// CapabilityDetector identifies the fixed request and parsing contract, not the proxy core version.
const CapabilityDetector = "mpx-proxy-capability"
const CapabilityDetectorVersion = "3"

const (
	CapabilityAvailable   = "available"
	CapabilityUnavailable = "unavailable"
	CapabilityUnknown     = "unknown"

	capabilityEgressURL   = "https://api.ip.sb/geoip"
	capabilityYouTubeURL  = "https://www.youtube.com/generate_204"
	capabilityLicensedURL = "https://www.netflix.com/title/81280792"
	capabilityOriginalURL = "https://www.netflix.com/title/80018499"
	capabilityBudget      = 80 * time.Second
	// Real Netflix title pages exceed 1 MiB. Bound disk and transport independently
	// of curl's version; the extra byte detects a truncated response.
	capabilityBodyLimit = 8 * 1024 * 1024
	capabilityMetaLimit = 8 * 1024
	capabilityLineLimit = 4*((capabilityBodyLimit+2)/3) + capabilityMetaLimit + 128
)

// CapabilityObservation is an observation through one actual user endpoint.
// YouTube available means connectivity only; Netflix available means the licensed
// title page has positive catalog evidence, not an authenticated playback test.
// Evidence ownership, expiry, and the choice of authorized user belong to the caller.
type CapabilityObservation struct {
	Target string
	Status string
	Region string
	ExitIP string
	Reason string
}

type capabilityRequest struct{ key, address string }
type capabilityResponse struct {
	code int
	curl int
	url  string
	mime string
	body []byte
	// A damaged known request must not invalidate unrelated target evidence.
	reason string
}

// ProbeCapabilities runs an ephemeral Xray or sing-box client on n using ep as supplied.
// The caller must resolve ep using current, independent, authorized user credentials.
// No resident agent, downloaded script, direct outbound, or direct curl fallback is used.
// Unsupported targets and invalid endpoint configuration are errors; remote execution
// and transport failures yield unknown observations as well as an execution error.
func ProbeCapabilities(ctx context.Context, r Runner, n model.Node, ep render.Endpoint, targets []string) ([]CapabilityObservation, error) {
	requests, err := capabilityRequests(targets)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return []CapabilityObservation{}, nil
	}
	cfg, err := capabilityConfig(ep)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, capabilityBudget)
	defer cancel()
	script := capabilityScriptForCore(requests, ep.Protocol.Core())
	var out string
	// SSH bounds capture before allocation; test/embedded runners retain the
	// fixed shell stream limits and parser validation through the base interface.
	if bounded, ok := r.(interface {
		RunBounded(context.Context, model.Node, string, []byte, int) (string, error)
	}); ok {
		out, err = bounded.RunBounded(ctx, n, script, cfg, len(requests)*capabilityLineLimit)
	} else {
		out, err = r.RunWithInput(ctx, n, script, cfg)
	}
	if err != nil {
		observations := make([]CapabilityObservation, 0, len(targets))
		for _, target := range targets {
			observations = append(observations, CapabilityObservation{Target: target, Status: CapabilityUnknown, Reason: "probe_execution_failed"})
		}
		return observations, fmt.Errorf("capability probe: %w", err)
	}
	responses, parseErr := capabilityResponses(out, requests)
	observations := make([]CapabilityObservation, 0, len(targets))
	for _, target := range targets {
		if parseErr != nil && responses == nil {
			observations = append(observations, CapabilityObservation{Target: target, Status: CapabilityUnknown, Reason: "parser_error: response_envelope"})
			continue
		}
		observations = append(observations, capabilityObserve(target, responses))
	}
	return observations, nil
}

func capabilityRequests(targets []string) ([]capabilityRequest, error) {
	seen := map[string]bool{}
	var requests []capabilityRequest
	for _, target := range targets {
		if seen[target] {
			return nil, fmt.Errorf("capability probe: duplicate target %q", target)
		}
		seen[target] = true
		switch target {
		case "egress":
			requests = append(requests, capabilityRequest{"egress", capabilityEgressURL})
		case "youtube":
			requests = append(requests, capabilityRequest{"youtube", capabilityYouTubeURL})
		case "netflix":
			requests = append(requests, capabilityRequest{"licensed", capabilityLicensedURL}, capabilityRequest{"original", capabilityOriginalURL})
		default:
			return nil, fmt.Errorf("capability probe: unsupported target %q", target)
		}
	}
	return requests, nil
}

func capabilityConfig(ep render.Endpoint) ([]byte, error) {
	if strings.TrimSpace(ep.Address) == "" || ep.Port < 1 || ep.Port > 65535 || ep.AllowInsecure {
		return nil, fmt.Errorf("capability probe: invalid address, port, or insecure TLS endpoint")
	}
	if ep.Protocol.QUIC() {
		// ProbeClient requires a nonzero port. Normalize only its sole loopback
		// inbound to a script placeholder; outbound endpoint ports stay untouched.
		body, err := render.ProbeClient(ep, 1)
		if err != nil {
			return nil, fmt.Errorf("capability probe: %w", err)
		}
		var cfg struct {
			Inbounds  []map[string]any  `json:"inbounds"`
			Outbounds []json.RawMessage `json:"outbounds"`
			Route     json.RawMessage   `json:"route"`
			Log       json.RawMessage   `json:"log"`
		}
		if err := json.Unmarshal(body, &cfg); err != nil || len(cfg.Inbounds) != 1 || len(cfg.Outbounds) != 1 {
			return nil, fmt.Errorf("capability probe: invalid rendered QUIC config")
		}
		cfg.Inbounds[0]["listen_port"] = 0
		cfg.Inbounds[0]["type"] = "socks"
		return json.MarshalIndent(cfg, "", "  ")
	}
	switch ep.Protocol {
	case model.ProtocolVLESS, model.ProtocolVMess:
		if ep.UUID == "" {
			return nil, fmt.Errorf("capability probe: missing user UUID")
		}
	case model.ProtocolTrojan, model.ProtocolShadowsocks:
		if ep.Password == "" {
			return nil, fmt.Errorf("capability probe: missing user password")
		}
	case model.ProtocolWireguard:
	default:
		return nil, fmt.Errorf("capability probe: unsupported Xray protocol %q", ep.Protocol)
	}
	// Port zero is replaced ONLY in the inbound by the remote script. Endpoint ports
	// are validated nonzero. Passing an explicit empty outbound array avoids default
	// template DNS/routing/direct rules; Xray always selects the actual proxy first.
	template := []byte(`{"log":{"loglevel":"none"},"inbounds":[{"tag":"capability-socks","listen":"127.0.0.1","port":0,"protocol":"socks","settings":{"auth":"noauth","udp":false}}],"outbounds":[]}`)
	configs, err := render.Xray([]render.Endpoint{ep}, template)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(configs, &items); err != nil || len(items) != 1 {
		return nil, fmt.Errorf("capability probe: invalid rendered config")
	}
	return items[0], nil
}

func capabilityScript(requests []capabilityRequest) string {
	return capabilityScriptForCore(requests, model.CoreXray)
}

func capabilityScriptForCore(requests []capabilityRequest, core model.Core) string {
	binSetup, binVariable, portKey := xrayBin, "XRAY_BIN", "port"
	if core == model.CoreSingBox {
		binSetup, binVariable, portKey = singBoxBin, "SING_BOX_BIN", "listen_port"
	}
	// The watchdog outlives an SSH disconnect and bounds remote processes even if
	// the caller's context is canceled. Its own sleep is killed and reaped on exit.
	script := `set -eu
umask 077
` + binSetup + `
PROBE_BIN="$` + binVariable + `"
command -v curl >/dev/null
[ -x "$PROBE_BIN" ]
tmp=$(mktemp -d /tmp/mpx-capability.XXXXXX)
chmod 700 "$tmp"
pid= guard= curlpid= bodypid= metapid=
cleanup() {
  trap - EXIT HUP INT TERM
  for child in "$curlpid" "$bodypid" "$metapid" "$pid" "$guard"; do
    if [ -n "$child" ]; then kill "$child" 2>/dev/null || :; wait "$child" 2>/dev/null || :; fi
  done
  rm -rf -- "${tmp:?}"
}
trap cleanup EXIT
trap 'exit 124' HUP INT TERM
owner=$$
(
  sleeper=
  trap 'if [ -n "$sleeper" ]; then kill "$sleeper" 2>/dev/null || :; wait "$sleeper" 2>/dev/null || :; fi; exit' HUP INT TERM
  sleep 65 & sleeper=$!
  wait "$sleeper"
  kill -TERM "$owner" 2>/dev/null || :
) & guard=$!
cat > "$tmp/input.json"
chmod 600 "$tmp/input.json"
ready=0
for attempt in 1 2 3; do
  random=$(od -An -N2 -tu2 /dev/urandom)
  port=$((49152 + random % 16384))
  sed "s/^\\([[:space:]]*\"` + portKey + `\": \\)0\\([,]*[[:space:]]*\\)$/\\1$port\\2/" "$tmp/input.json" > "$tmp/config.json"
  chmod 600 "$tmp/config.json"
  "$PROBE_BIN" run -c "$tmp/config.json" >/dev/null 2>&1 & pid=$!
  sleep 1
  if kill -0 "$pid" 2>/dev/null; then ready=1; break; fi
  wait "$pid" 2>/dev/null || :
  pid=
done
[ "$ready" = 1 ] || exit 70
request() {
  key=$1
  address=$2
  rc=0
  # Bound both streams before they reach disk, including chunked responses on
  # curl 7.81. head closes at limit+1: curl gets EPIPE, not an unbounded file.
  mkfifo "$tmp/body.pipe" "$tmp/meta.pipe"
  head -c ` + strconv.Itoa(capabilityBodyLimit+1) + ` < "$tmp/body.pipe" > "$tmp/body" & bodypid=$!
  head -c ` + strconv.Itoa(capabilityMetaLimit+1) + ` < "$tmp/meta.pipe" > "$tmp/meta" & metapid=$!
  # -q ignores .curlrc; empty noproxy defeats NO_PROXY. Target DNS and redirects
  # stay on SOCKS. %{stderr} (curl >=7.63) separates bounded metadata from body.
  # Silent errors prevent bodies, URLs or credentials appearing in diagnostics.
  curl -q --silent --location --max-redirs 3 \
    --proto '=http,https' --proto-redir '=http,https' \
    --socks5-hostname "127.0.0.1:$port" --noproxy '' \
    --connect-timeout 4 --max-time 12 --max-filesize ` + strconv.Itoa(capabilityBodyLimit) + ` \
    --user-agent '` + CapabilityDetector + `/` + CapabilityDetectorVersion + `' --header 'Accept-Language: en-US,en;q=0.9' \
    --output - --write-out '%{stderr}%{http_code}\t%{url_effective}\t%{content_type}' \
    --url "$address" > "$tmp/body.pipe" 2> "$tmp/meta.pipe" & curlpid=$!
  wait "$curlpid" || rc=$?
  curlpid=
  wait "$bodypid" || :
  bodypid=
  wait "$metapid" || :
  metapid=
  bodybytes=$(wc -c < "$tmp/body")
  metabytes=$(wc -c < "$tmp/meta")
  if [ "$bodybytes" -gt ` + strconv.Itoa(capabilityBodyLimit) + ` ] || [ "$metabytes" -gt ` + strconv.Itoa(capabilityMetaLimit) + ` ]; then rc=63; fi
  printf 'MPX1\t%s\t%s\t' "$key" "$rc"
  if [ "$rc" -ne 0 ]; then
    # Partial bodies and metadata are never evidence. Keep only the exit class.
    printf '000\t\t\t\n'
  else
    cat "$tmp/meta"
    printf '\t'
    base64 < "$tmp/body" | tr -d '\r\n'
    printf '\n'
  fi
  rm -f -- "$tmp/body" "$tmp/meta" "$tmp/body.pipe" "$tmp/meta.pipe"
}
`
	for _, request := range requests {
		script += "request " + shellQuote(request.key) + " " + shellQuote(request.address) + "\n"
	}
	// SSH invokes the user's shell; use a private POSIX shell so $$ and traps refer
	// to the same process on Linux and in localRunner tests.
	return "sh -c " + shellQuote(script)
}

func capabilityResponses(out string, requests []capabilityRequest) (map[string]capabilityResponse, error) {
	expected := map[string]bool{}
	for _, request := range requests {
		if request.key == "" || expected[request.key] {
			return nil, fmt.Errorf("invalid request envelope")
		}
		expected[request.key] = true
	}
	responses := map[string]capabilityResponse{}
	var parseErr error
	// Cut keeps allocation bounded even for an oversized known record. Validate
	// the request identity before decoding; no damaged record can be overwritten.
	for remaining := strings.TrimSuffix(out, "\n"); remaining != ""; {
		line, rest, more := strings.Cut(remaining, "\n")
		if more && rest == "" {
			return nil, fmt.Errorf("invalid response envelope")
		}
		remaining = rest
		header := strings.SplitN(line, "\t", 3)
		// Unattributable output cannot establish a trustworthy request set.
		if len(header) < 2 || header[0] != "MPX1" || !expected[header[1]] {
			return nil, fmt.Errorf("invalid response envelope")
		}
		key := header[1]
		r := capabilityResponse{reason: "parser_error: response_envelope"}
		if _, duplicate := responses[key]; duplicate {
			responses[key] = r
			parseErr = fmt.Errorf("duplicate response")
			continue
		}
		if len(line) > capabilityLineLimit {
			r.reason = "parser_error: response_too_large"
		} else if fields := strings.SplitN(line, "\t", 8); len(fields) == 7 {
			r = capabilityResponseFields(fields)
		}
		if r.reason != "" {
			parseErr = fmt.Errorf("invalid response fields")
		}
		responses[key] = r
	}
	for key := range expected {
		if _, ok := responses[key]; !ok {
			responses[key] = capabilityResponse{reason: "parser_error: response_envelope"}
			parseErr = fmt.Errorf("missing response")
		}
	}
	return responses, parseErr
}

func capabilityResponseFields(fields []string) capabilityResponse {
	r := capabilityResponse{reason: "parser_error: response_envelope"}
	rc, rcErr := strconv.Atoi(fields[2])
	code, codeErr := strconv.Atoi(fields[3])
	if rcErr != nil || codeErr != nil || rc < 0 || rc > 255 || code < 0 || code > 599 {
		return r
	}
	if len(fields[4])+len(fields[5]) > capabilityMetaLimit || len(fields[6]) > base64.StdEncoding.EncodedLen(capabilityBodyLimit) {
		r.reason = "parser_error: response_too_large"
		return r
	}
	body, bodyErr := base64.StdEncoding.DecodeString(fields[6])
	if bodyErr != nil {
		return r
	}
	if len(body) > capabilityBodyLimit {
		r.reason = "parser_error: response_too_large"
		return r
	}
	return capabilityResponse{code: code, curl: rc, url: fields[4], mime: fields[5], body: body}
}

func capabilityObserve(target string, responses map[string]capabilityResponse) CapabilityObservation {
	o := CapabilityObservation{Target: target, Status: CapabilityUnknown}
	key := target
	if target == "netflix" {
		key = "licensed"
	}
	r, ok := responses[key]
	if !ok {
		o.Reason = "parser_error: response_envelope"
		return o
	}
	if r.reason != "" {
		o.Reason = r.reason
		return o
	}
	if target == "netflix" {
		original, ok := responses["original"]
		if !ok || original.reason != "" {
			o.Reason = "parser_error: response_envelope"
			if original.reason != "" {
				o.Reason = original.reason
			}
			return o
		}
	}
	if r.curl != 0 {
		o.Reason = capabilityCurlReason(r.curl)
		return o
	}
	if r.code == 403 || r.code == 404 || r.code == 451 {
		o.Status, o.Reason = CapabilityUnavailable, "http_denied_"+strconv.Itoa(r.code)
		if target == "netflix" && (r.code == 403 || r.code == 404) && netflixTitleEvidence(responses["original"], "80018499") {
			o.Reason = "netflix_originals_only"
		}
		return o
	}
	switch target {
	case "egress":
		return capabilityEgress(r)
	case "youtube":
		if r.code == 204 && capabilityOfficialURL(r.url, "www.youtube.com", "/generate_204") {
			o.Status, o.Reason = CapabilityAvailable, "youtube_connectivity_only"
		} else {
			o.Reason = "parser_error: youtube_connectivity_response"
		}
	case "netflix":
		if strings.Contains(strings.ToLower(string(r.body)), "not available in your country") || strings.Contains(string(r.body), "Oh no!") {
			o.Status, o.Reason = CapabilityUnavailable, "netflix_title_unavailable"
		} else if !netflixTitleEvidence(r, "81280792") {
			o.Reason = "parser_error: netflix_title_evidence"
		} else if region := netflixRegion(r); region == "" {
			o.Reason = "parser_error: netflix_region"
		} else if original := responses["original"]; original.curl != 0 {
			o.Reason = capabilityCurlReason(original.curl)
		} else if original.code == 403 || original.code == 404 || original.code == 451 {
			o.Status, o.Reason = CapabilityUnavailable, "http_denied_"+strconv.Itoa(original.code)
		} else if !netflixTitleEvidence(original, "80018499") {
			o.Reason = "parser_error: netflix_original_evidence"
		} else {
			o.Status, o.Region, o.Reason = CapabilityAvailable, region, "netflix_licensed_catalog"
		}
	}
	return o
}

func capabilityCurlReason(code int) string {
	if code == 28 {
		return "request_timeout"
	}
	if code == 63 {
		return "parser_error: response_too_large"
	}
	return "request_network_error_" + strconv.Itoa(code)
}

func capabilityOfficialURL(raw, host, path string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == host && u.User == nil && u.Path == path
}

func capabilityEgress(r capabilityResponse) CapabilityObservation {
	o := CapabilityObservation{Target: "egress", Status: CapabilityUnknown, Reason: "parser_error: egress_json"}
	if r.code != 200 || !capabilityOfficialURL(r.url, "api.ip.sb", "/geoip") || !strings.HasPrefix(strings.ToLower(r.mime), "application/json") {
		return o
	}
	var geo struct {
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
	}
	if err := json.Unmarshal(r.body, &geo); err != nil {
		return o
	}
	ip, err := netip.ParseAddr(geo.IP)
	if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || !capabilityRegion(geo.CountryCode) {
		return o
	}
	o.Status, o.ExitIP, o.Region, o.Reason = CapabilityAvailable, ip.String(), geo.CountryCode, "egress_geoip"
	return o
}

var (
	capabilityCountry = regexp.MustCompile(`^[A-Z]{2}$`)
	netflixURLPath    = regexp.MustCompile(`^/(?:([a-z]{2})(?:-[a-z]{2})?/)?title/([0-9]+)$`)
	netflixJSONLD     = regexp.MustCompile(`(?is)<script\b[^>]*type\s*=\s*["']application/ld\+json["'][^>]*>(.*?)</script>`)
	netflixCountry    = regexp.MustCompile(`"countryCode"\s*:\s*"([A-Z]{2})"`)
	netflixCountryID  = regexp.MustCompile(`"id"\s*:\s*"([A-Z]{2})"\s*,\s*"countryName"\s*:\s*"[^"]+"`)
)

func netflixTitleURL(raw, id string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "www.netflix.com" || u.User != nil {
		return false
	}
	match := netflixURLPath.FindStringSubmatch(u.Path)
	return len(match) == 3 && match[2] == id
}

func netflixTitleEvidence(r capabilityResponse, id string) bool {
	if r.curl != 0 || r.code != 200 || !netflixTitleURL(r.url, id) {
		return false
	}
	for _, match := range netflixJSONLD.FindAllSubmatch(r.body, -1) {
		var title struct {
			Type string `json:"@type"`
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if json.Unmarshal(match[1], &title) == nil && (title.Type == "Movie" || title.Type == "TVSeries") && strings.TrimSpace(title.Name) != "" && netflixTitleURL(title.URL, id) {
			return true
		}
	}
	return false
}

// ISO 3166-1 alpha-2, plus XK used by geo-IP providers. Shape alone would
// incorrectly accept provider sentinel codes such as XX/ZZ as a real region.
func capabilityRegion(region string) bool {
	const codes = " AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS XK YE YT ZA ZM ZW "
	return capabilityCountry.MatchString(region) && strings.Contains(codes, " "+region+" ")
}

func netflixRegion(r capabilityResponse) string {
	regions := map[string]bool{}
	if u, err := url.Parse(r.url); err == nil {
		if match := netflixURLPath.FindStringSubmatch(u.Path); len(match) == 3 && match[1] != "" {
			regions[strings.ToUpper(match[1])] = true
		}
	}
	for _, pattern := range []*regexp.Regexp{netflixCountry, netflixCountryID} {
		for _, match := range pattern.FindAllSubmatch(r.body, -1) {
			regions[string(match[1])] = true
		}
	}
	if len(regions) == 1 {
		for region := range regions {
			if capabilityRegion(region) {
				return region
			}
		}
	}
	return ""
}
