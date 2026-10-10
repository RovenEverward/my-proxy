package diagnostics

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var pingTime = regexp.MustCompile(`time[=<]([0-9]+(?:\.[0-9]+)?)\s*ms`)
var pingSummary = regexp.MustCompile(`([0-9]+) packets transmitted, ([0-9]+)(?: packets)? received`)
var pingLoss = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)% packet loss`)

func summarize(l *Latency) {
	l.ValidSamples = len(l.SamplesMS)
	if l.ValidSamples > 0 {
		l.MinMS = l.SamplesMS[0]
		for i, s := range l.SamplesMS {
			l.MeanMS += s
			l.MinMS = math.Min(l.MinMS, s)
			l.MaxMS = math.Max(l.MaxMS, s)
			if i > 0 {
				l.JitterMS += math.Abs(s - l.SamplesMS[i-1])
			}
		}
		l.MeanMS /= float64(l.ValidSamples)
		l.JitterSamples = l.ValidSamples - 1
		if l.JitterSamples > 0 {
			l.JitterMS /= float64(l.JitterSamples)
		}
	}
	if l.Attempted > 0 {
		fail := 100 * float64(l.Attempted-l.ValidSamples) / float64(l.Attempted)
		if l.Protocol == "ICMP" {
			l.LossPercent = &fail
		} else {
			l.HTTPFailureRatePercent = &fail
		}
	}
	if l.Attempted == 0 {
		l.Status.Status = "skipped"
	} else if l.ValidSamples == l.Attempted {
		l.Status.Status = "ok"
	} else if l.ValidSamples > 0 {
		l.Status.Status = "partial"
	} else {
		l.Status.Status = "failed"
	}
}

func (e *engine) icmp(ctx context.Context, t Target) Latency {
	l := Latency{Protocol: "ICMP", Timing: "native ping round-trip time in milliseconds"}
	if e.cfg.SocksURL != "" {
		l.Status = Status{"skipped", "ICMP cannot traverse SOCKS"}
		return l
	}
	if !e.tools["ping"] {
		l.Status = Status{"skipped", "ping not installed"}
		return l
	}
	// Count bounds work on BSD and Linux; the command wall-clock watchdog bounds
	// DNS/filtered ICMP without relying on incompatible -W/-w units.
	out, err := e.command(ctx, fmt.Sprintf("exec ping -n -c %d %s", e.cfg.Samples, shellQuote(t.Host)), nil)
	for _, m := range pingTime.FindAllStringSubmatch(out, e.cfg.Samples) {
		x, err := strconv.ParseFloat(m[1], 64)
		if err == nil && x >= 0 && !math.IsInf(x, 0) {
			l.SamplesMS = append(l.SamplesMS, x)
		}
	}
	// Startup/permission/DNS failures are not packet loss. Require a real ping
	// transmission summary to compute loss. Planned samples do not prove how
	// many packets were actually sent before cancellation.
	m := pingSummary.FindStringSubmatch(out)
	if len(m) == 3 {
		l.Attempted, _ = strconv.Atoi(m[1])
	}
	summarize(&l)
	if l.Attempted == 0 {
		if l.ValidSamples > 0 {
			l.Status = Status{"partial", "ping transmission count unavailable; loss is unknown"}
		} else {
			l.Status = Status{"failed", "ping produced no transmission summary (DNS, permission, or tool failure)"}
		}
	}
	if err != nil {
		if l.Reason != "" {
			l.Reason += "; " + safeError(err)
		} else {
			l.Reason = safeError(err)
		}
	}
	return l
}

func (e *engine) httpLatency(ctx context.Context, t Target) Latency {
	l := Latency{Protocol: "HTTP", Timing: "curl time_total: DNS + connect + TLS + request + response body completion; fresh process each sample"}
	if t.HTTPURL == "" {
		l.Status = Status{"skipped", "no HTTP endpoint configured"}
		return l
	}
	if !e.tools["curl"] {
		l.Status = Status{"skipped", "curl not installed"}
		return l
	}
	for i := 0; i < e.cfg.Samples && ctx.Err() == nil; i++ {
		r := e.request(ctx, t.HTTPURL, "GET", nil, 16384, "", false)
		if strings.Contains(safeError(r.Err), "budget exhausted") {
			l.Reason = safeError(r.Err)
			break
		}
		l.Attempted++
		if r.Err == nil {
			l.SamplesMS = append(l.SamplesMS, r.Seconds*1000)
		} else {
			l.Reason = safeError(r.Err)
		}
	}
	summarize(&l)
	return l
}

func (e *engine) dns(ctx context.Context, t Target) Status {
	if e.cfg.SocksURL != "" {
		return Status{"skipped", "DNS is delegated to SOCKS via socks5h; HTTP result includes resolution"}
	}
	var cmd string
	if e.tools["getent"] {
		cmd = "exec getent ahosts " + shellQuote(t.Host)
	} else if e.tools["nslookup"] {
		cmd = "exec nslookup " + shellQuote(t.Host)
	} else {
		return Status{"skipped", "getent and nslookup not installed"}
	}
	out, err := e.command(ctx, cmd, nil)
	if err != nil || strings.TrimSpace(out) == "" {
		return Status{"failed", "DNS lookup failed: " + safeError(err)}
	}
	return Status{"ok", "resolved by installed system tool"}
}
