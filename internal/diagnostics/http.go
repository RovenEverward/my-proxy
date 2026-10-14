package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// curl may deliver one final write after a file limit. Reserve 64 KiB per
// request in addition to its response cap (curl's standard body write is 16 KiB).
// This is an application-body budget, not a TCP/TLS/header wire-byte budget.
const curlReadAllowance int64 = 64 << 10

var ErrMissingKey = errors.New("API key environment variable is missing")

type httpResult struct {
	Code             int
	Seconds          float64
	Download, Upload int64
	Body             string
	Err              error
}
type engine struct {
	cfg    Config
	exec   Executor
	report *Report
	tools  map[string]bool
}

func (e *engine) command(ctx context.Context, command string, stdin []byte) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	c, cancel := context.WithTimeout(ctx, time.Duration(e.cfg.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	out, err := e.exec.Exec(c, boundedScript(command, e.cfg.RequestTimeoutSeconds), stdin)
	if len(out) > MaxOutputBytes {
		return "", ErrOutputLimit
	}
	return out, err
}

func (e *engine) request(ctx context.Context, url, method string, upload []byte, capBytes int64, family string, key bool) httpResult {
	r := httpResult{}
	ipAPI := method == "IPAPI"
	if ipAPI {
		upload = make([]byte, 512)
	}
	reserve := capBytes + curlReadAllowance + int64(len(upload))
	if e.report.Budget.ReservedBytes+reserve > e.cfg.MaxBytes {
		r.Err = fmt.Errorf("total application-byte budget exhausted")
		return r
	}
	e.report.Budget.ReservedBytes += reserve
	if !e.tools["curl"] {
		r.Err = fmt.Errorf("curl not installed")
		return r
	}
	args := []string{"curl", "-q", "--silent", "--show-error", "--globoff", "--proto", "'=http,https'", "--proto-redir", "'=http,https'", "--max-redirs", "0", "--connect-timeout", strconv.Itoa(e.cfg.RequestTimeoutSeconds), "--max-time", strconv.Itoa(e.cfg.RequestTimeoutSeconds), "--max-filesize", strconv.FormatInt(capBytes, 10), "--output", `"$d/body"`, "--write-out", shellQuote("\nMPX_METRICS %{http_code} %{time_total} %{size_download} %{size_upload}\n"), "--noproxy", "''"}
	if e.cfg.SocksURL != "" {
		args = append(args, "--proxy", shellQuote(e.cfg.SocksURL))
	} else {
		args = append(args, "--proxy", "''")
		if family == "ipv4" {
			args = append(args, "-4")
		}
		if family == "ipv6" {
			args = append(args, "-6")
		}
	}
	if method == "POST" || method == "PUT" {
		// --data-binary reads the bounded stdin payload before sending, so curl
		// sets the true Content-Length and size_upload excludes chunk framing.
		args = append(args, "--request", method, "--header", shellQuote("Content-Type: application/octet-stream"), "--data-binary", "@-")
	}
	if strings.Contains(url, "speedtest.tele2.net/") && method == "GET" {
		args = append(args, "--range", shellQuote("0-"+strconv.FormatInt(capBytes-1, 10)))
	}
	if key {
		args = append(args, "--config", `"$d/key"`)
	}
	args = append(args, "--url", shellQuote(url))
	// curl --max-filesize rejects advertised large bodies; RLIMIT_FSIZE bounds
	// old curl's unknown/chunked bodies as well, with no response body in stdout.
	prep := `d=$(mktemp -d) || exit 1
c=''
cleanup_http() { [ -z "$c" ] || kill "$c" 2>/dev/null || :; rm -rf -- "$d"; }
trap cleanup_http EXIT
trap 'exit 124' HUP INT TERM
`
	if ipAPI {
		args = append(args, "--request", "POST", "--header", shellQuote("Content-Type: application/json"), "--data-binary", `@"$d/payload"`)
		if e.cfg.IPAPIKey != "" {
			body, _ := json.Marshal(map[string]string{"q": url, "key": e.cfg.IPAPIKey})
			upload = body
			prep += `cat > "$d/payload"
`
		} else {
			prep += fmt.Sprintf(`eval 'k=${%s-}'
[ -n "$k" ] || { printf 'MPX_MISSING_KEY\n'; exit 3; }
case "$k" in *[!A-Za-z0-9_-]*) exit 3;; esac
[ "${#k}" -le 256 ] || exit 3
printf '{"q":"%%s","key":"%%s"}' %s "$k" > "$d/payload"
`, e.cfg.IPAPIKeyEnv, shellQuote(url))
			upload = nil
		}
		// Replace the query IP with the fixed provider URL, never put the key in URL.
		for i := range args {
			if i > 0 && args[i-1] == "--url" {
				args[i] = shellQuote("https://api.ipapi.is/")
			}
		}
	}
	if key {
		if e.cfg.AbuseIPDBKey != "" {
			raw, _ := json.Marshal("Key: " + e.cfg.AbuseIPDBKey)
			prep += `IFS= read -r line
printf '%s\n' "$line" > "$d/key"
`
			upload = []byte("header = " + string(raw) + "\n")
		} else {
			prep += fmt.Sprintf(`eval 'k=${%s-}'
[ -n "$k" ] || { printf 'MPX_MISSING_KEY\n'; exit 3; }
case "$k" in *[!A-Za-z0-9_-]*) exit 3;; esac
printf 'header = "Key: %%s"\n' "$k" > "$d/key"
`, e.cfg.AbuseIPDBKeyEnv)
		}
	}
	// The script always runs under sh -c. POSIX sh counts ulimit -f in 512-byte
	// blocks, and dash and bash in posix mode (bash run as sh) both do; only bash
	// outside posix mode uses 1024. Dividing by 1024 on Linux halved the limit
	// wherever /bin/sh is dash (Debian/Ubuntu), so curl was killed by SIGXFSZ
	// (exit 153) halfway through a legitimate body.
	command := prep + fmt.Sprintf("ulimit -f %d\n", max(int64(1), capBytes/512)) + strings.Join(args, " ") + ` <&3 2> "$d/error" &
c=$!
wait "$c"
s=$?
c=''
printf '\nMPX_EXIT %s\n' "$s"
head -c 256 "$d/error"
`
	if capBytes <= 16384 {
		command += `printf '\nMPX_BODY\n'
head -c ` + strconv.FormatInt(capBytes, 10) + ` "$d/body"
`
	}
	command += "exit \"$s\"\n"
	out, err := e.command(ctx, command, upload)
	r.Err = err
	header := out
	if idx := strings.Index(out, "\nMPX_BODY\n"); idx >= 0 {
		header = out[:idx]
	}
	lines := strings.Split(header, "\n")
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "MPX_METRICS ") {
			f := strings.Fields(line)
			if len(f) == 5 {
				code, a := strconv.Atoi(f[1])
				sec, b := strconv.ParseFloat(f[2], 64)
				down, c := strconv.ParseFloat(f[3], 64)
				up, d := strconv.ParseFloat(f[4], 64)
				if a == nil && b == nil && c == nil && d == nil && !math.IsNaN(sec) && !math.IsInf(sec, 0) && sec >= 0 && down >= 0 && up >= 0 && down <= float64(capBytes+curlReadAllowance) && up <= float64(max(len(upload), 512*boolInt(ipAPI))) {
					r.Code = code
					r.Seconds = sec
					r.Download = int64(down)
					r.Upload = int64(up)
					found = true
				}
			}
		}
	}
	e.report.Budget.TransferredBytes += r.Download + r.Upload
	if idx := strings.Index(out, "\nMPX_BODY\n"); idx >= 0 {
		r.Body = out[idx+len("\nMPX_BODY\n"):]
		if int64(len(r.Body)) > capBytes {
			r.Body = r.Body[:capBytes]
		}
	}
	if strings.Contains(out, "MPX_MISSING_KEY") {
		r.Err = ErrMissingKey
	}
	if !found && r.Err == nil {
		r.Err = fmt.Errorf("invalid curl metrics")
	}
	if (r.Code < 200 || r.Code >= 300) && r.Err == nil {
		r.Err = fmt.Errorf("HTTP status %d (redirects disabled)", r.Code)
	}
	if r.Err != nil && !errors.Is(r.Err, ErrMissingKey) {
		r.Err = fmt.Errorf("request failed: %s", safeError(r.Err))
	}
	return r
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	// Do not return command/stdin/stderr, which can contain proxy URLs or keys.
	if err == context.Canceled {
		return "canceled"
	}
	if err == context.DeadlineExceeded {
		return "deadline exceeded"
	}
	if strings.Contains(err.Error(), "budget exhausted") {
		return "total application-byte budget exhausted"
	}
	if strings.Contains(err.Error(), "API key environment variable is missing") {
		return "API key environment variable is missing"
	}
	if strings.Contains(err.Error(), "HTTP status ") {
		return "HTTP non-success status (redirects disabled)"
	}
	if strings.Contains(err.Error(), "invalid curl metrics") {
		return "invalid curl metrics"
	}
	if err == ErrOutputLimit {
		return "command output limit exceeded"
	}
	return "tool execution failed or response exceeded limit"
}
