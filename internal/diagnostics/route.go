package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
)

func (e *engine) route(ctx context.Context, t Target) Route {
	r := Route{Direction: e.cfg.Position + " -> " + t.Host}
	if e.cfg.SocksURL != "" {
		r.Status = Status{"skipped", "ICMP/UDP route tracing cannot traverse SOCKS"}
		return r
	}
	if e.tools["mtr"] {
		r.Tool = "mtr"
		out, err := e.command(ctx, fmt.Sprintf("exec mtr --json --report --no-dns --report-cycles 1 --max-ttl 12 %s", shellQuote(t.Host)), nil)
		r.Evidence = limitEvidence(out)
		r.Hops = parseMTR(out)
		if len(r.Hops) > 0 {
			r.Status = Status{Status: "ok"}
			if err != nil {
				r.Status = Status{"partial", safeError(err)}
			}
			return r
		}
		r.Reason = "mtr JSON unavailable or invalid; trying traceroute"
	}
	if e.tools["traceroute"] {
		r.Tool = "traceroute"
		out, err := e.command(ctx, "exec traceroute -n -m 12 -q 1 -w 1 "+shellQuote(t.Host), nil)
		r.Evidence = limitEvidence(out)
		r.Hops = parseTraceroute(out)
		if len(r.Hops) > 0 {
			r.Status = Status{Status: "ok"}
			if err != nil {
				r.Status = Status{"partial", safeError(err)}
			}
		} else {
			r.Status = Status{"failed", "traceroute returned no parseable hops: " + safeError(err)}
		}
		return r
	}
	if e.tools["mtr"] {
		r.Status = Status{"failed", "mtr JSON invalid and traceroute not installed"}
	} else {
		r.Status = Status{"skipped", "mtr and traceroute not installed"}
	}
	return r
}

func limitEvidence(s string) string {
	if len(s) > MaxReportEvidenceBytes {
		return s[:MaxReportEvidenceBytes] + "\n[truncated]"
	}
	return s
}
func parseMTR(raw string) []Hop {
	var envelope struct {
		Report struct {
			Hubs []struct {
				Count int             `json:"count"`
				Host  string          `json:"host"`
				Loss  *float64        `json:"Loss%"`
				Avg   *float64        `json:"Avg"`
				ASN   json.RawMessage `json:"ASN"`
			} `json:"hubs"`
		} `json:"report"`
	}
	if len(raw) > MaxOutputBytes || json.Unmarshal([]byte(raw), &envelope) != nil {
		return nil
	}
	var hops []Hop
	for _, h := range envelope.Report.Hubs {
		if len(hops) >= 12 {
			break
		}
		if h.Count < 1 || h.Count > 12 || h.Loss == nil || h.Avg == nil || *h.Loss < 0 || *h.Loss > 100 || *h.Avg < 0 || math.IsInf(*h.Avg, 0) {
			continue
		}
		hop := Hop{TTL: h.Count, Unanswered: h.Host == "???" || h.Host == ""}
		if net.ParseIP(h.Host) != nil {
			hop.Address = h.Host
		} else if !hop.Unanswered {
			continue
		}
		loss, avg := *h.Loss, *h.Avg
		hop.LossPercent = &loss
		if !hop.Unanswered {
			hop.RTTMS = &avg
		}
		if len(h.ASN) > 0 {
			var s string
			if json.Unmarshal(h.ASN, &s) == nil && len(s) < 32 {
				hop.ASN = s
			} else {
				var n int64
				if json.Unmarshal(h.ASN, &n) == nil && n > 0 {
					hop.ASN = "AS" + strconv.FormatInt(n, 10)
				}
			}
		}
		hops = append(hops, hop)
	}
	return hops
}

var traceRTT = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*ms`)

func parseTraceroute(raw string) []Hop {
	var hops []Hop
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		ttl, err := strconv.Atoi(f[0])
		if err != nil || ttl < 1 || ttl > 12 {
			continue
		}
		h := Hop{TTL: ttl, Unanswered: true}
		for _, v := range f[1:] {
			if net.ParseIP(strings.Trim(v, "()")) != nil {
				h.Address = strings.Trim(v, "()")
				h.Unanswered = false
				break
			}
		}
		if m := traceRTT.FindStringSubmatch(line); len(m) == 2 {
			x, err := strconv.ParseFloat(m[1], 64)
			if err == nil && x >= 0 && !math.IsInf(x, 0) {
				h.RTTMS = &x
			}
		}
		if h.Address == "" && !strings.Contains(line, "*") {
			continue
		}
		hops = append(hops, h)
		if len(hops) >= 12 {
			break
		}
	}
	return hops
}
