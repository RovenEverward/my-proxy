package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (e *engine) ip(ctx context.Context) []IPResult {
	var results []IPResult
	address := ""
	for _, family := range []string{"ipv4", "ipv6"} {
		endpoint := e.cfg.IPv4URL
		if family == "ipv6" {
			endpoint = e.cfg.IPv6URL
		}
		r := IPResult{Family: family, Source: endpoint, CheckedAt: time.Now().UTC()}
		if !e.tools["curl"] {
			r.Status = Status{"skipped", "curl not installed"}
			results = append(results, r)
			continue
		}
		h := e.request(ctx, endpoint, "GET", nil, 1024, family, false)
		ip := strings.TrimSpace(h.Body)
		if strings.HasPrefix(ip, "{") {
			var data struct {
				IP string `json:"ip"`
			}
			if json.Unmarshal([]byte(ip), &data) == nil {
				ip = data.IP
			}
		}
		parsed := net.ParseIP(ip)
		if h.Err != nil {
			r.Status = Status{"failed", safeError(h.Err)}
		} else if parsed == nil || (family == "ipv4" && parsed.To4() == nil) || (family == "ipv6" && parsed.To4() != nil) {
			r.Status = Status{"failed", "IP endpoint returned invalid or wrong-family address"}
		} else {
			r.Status = Status{Status: "ok"}
			r.Address = parsed.String()
			if address == "" {
				address = r.Address
			}
		}
		results = append(results, r)
	}
	geoURL := e.cfg.IPInfoURL
	if address != "" && geoURL == "https://ipwho.is/" {
		geoURL += address
	}
	geo := IPResult{Family: "geoip", Source: geoURL, CheckedAt: time.Now().UTC()}
	if !e.tools["curl"] {
		geo.Status = Status{"skipped", "curl not installed"}
	} else {
		h := e.request(ctx, geoURL, "GET", nil, 8192, "", false)
		geo = parseGeoIP(h.Body, geoURL, geo.CheckedAt)
		if address != "" && e.cfg.IPInfoURL == "https://ipwho.is/" && geo.Status.Status == "ok" && !net.ParseIP(geo.Address).Equal(net.ParseIP(address)) {
			geo.Status = Status{"failed", "GeoIP response address differs from requested egress address"}
		}
		if h.Err != nil {
			geo.Status = Status{"failed", safeError(h.Err)}
		}
	}
	results = append(results, geo)
	rep := IPResult{Family: "reputation", Source: "https://docs.abuseipdb.com/#check-endpoint", CheckedAt: time.Now().UTC(), Address: address}
	if e.cfg.AbuseIPDBKey == "" && e.cfg.AbuseIPDBKeyEnv == "" {
		rep.Status = Status{"skipped", "AbuseIPDB API key missing; configure key or executing-machine key environment variable"}
	} else if !e.tools["curl"] {
		rep.Status = Status{"skipped", "curl not installed"}
	} else if address == "" {
		rep.Status = Status{"skipped", "no verified public egress address"}
	} else {
		endpoint := "https://api.abuseipdb.com/api/v2/check?ipAddress=" + url.QueryEscape(address) + "&maxAgeInDays=90"
		h := e.request(ctx, endpoint, "GET", nil, 8192, "", true)
		rep = parseAbuseIPDB(h.Body, rep.Source, address, rep.CheckedAt)
		if h.Err != nil {
			rep.Status = Status{"failed", safeError(h.Err)}
			if errors.Is(h.Err, ErrMissingKey) {
				rep.Status = Status{"skipped", ErrMissingKey.Error()}
			}
		}
	}
	results = append(results, rep)
	flags := IPResult{Family: "provider_flags", Source: "https://ipapi.is/developers.html", Address: address, CheckedAt: time.Now().UTC()}
	if e.cfg.IPAPIKey == "" && e.cfg.IPAPIKeyEnv == "" {
		flags.Status = Status{"skipped", "ipapi.is API key missing; anonymous flags are not assumed"}
	} else if address == "" {
		flags.Status = Status{"skipped", "no verified egress address"}
	} else if !e.tools["curl"] {
		flags.Status = Status{"skipped", "curl not installed"}
	} else {
		h := e.request(ctx, address, "IPAPI", nil, 8192, "", false)
		flags = parseIPAPI(h.Body, address, flags.CheckedAt)
		if h.Err != nil {
			flags.Status = Status{"failed", safeError(h.Err)}
			if errors.Is(h.Err, ErrMissingKey) {
				flags.Status = Status{"skipped", ErrMissingKey.Error()}
			}
		}
	}
	results = append(results, flags)
	return results
}

// Source schemas intentionally use pointers to distinguish unavailable flags
// from a provider's explicit false. No local quality score is derived.
func parseGeoIP(body, source string, at time.Time) IPResult {
	r := IPResult{Family: "geoip", Source: source, CheckedAt: at}
	var data struct {
		Success    *bool  `json:"success"`
		IP         string `json:"ip"`
		Country    string `json:"country"`
		Region     string `json:"region"`
		City       string `json:"city"`
		Connection struct {
			ASN int64  `json:"asn"`
			Org string `json:"org"`
			ISP string `json:"isp"`
		} `json:"connection"`
	}
	if len(body) > 8192 || json.Unmarshal([]byte(body), &data) != nil || data.Success == nil || !*data.Success || net.ParseIP(data.IP) == nil {
		r.Status = Status{"failed", "invalid ipwho.is response or provider success=false"}
		return r
	}
	if len(data.Country) > 160 || len(data.Region) > 160 || len(data.City) > 160 || len(data.Connection.Org) > 256 || data.Connection.ASN < 0 {
		r.Status = Status{"failed", "GeoIP field exceeds bounds"}
		return r
	}
	r.Address = data.IP
	r.Country = data.Country
	r.Region = data.Region
	r.City = data.City
	r.Organization = data.Connection.Org
	if data.Connection.ASN > 0 {
		r.ASN = "AS" + strconv.FormatInt(data.Connection.ASN, 10)
	}
	r.Status = Status{Status: "ok", Reason: "ipwho.is free GeoIP/ASN; security/proxy/hosting flags unavailable"}
	return r
}

func parseAbuseIPDB(body, source, address string, at time.Time) IPResult {
	r := IPResult{Family: "reputation", Source: source, Address: address, CheckedAt: at}
	var envelope struct {
		Data struct {
			IP        string `json:"ipAddress"`
			Score     *int   `json:"abuseConfidenceScore"`
			Reports   *int   `json:"totalReports"`
			Tor       *bool  `json:"isTor"`
			Public    *bool  `json:"isPublic"`
			Whitelist *bool  `json:"isWhitelisted"`
			ISP       string `json:"isp"`
		} `json:"data"`
	}
	if len(body) > 8192 || json.Unmarshal([]byte(body), &envelope) != nil {
		r.Status = Status{"failed", "invalid AbuseIPDB response"}
		return r
	}
	d := envelope.Data
	if net.ParseIP(d.IP) == nil || net.ParseIP(d.IP).String() != address || d.Score == nil || *d.Score < 0 || *d.Score > 100 || d.Reports == nil || *d.Reports < 0 || len(d.ISP) > 256 {
		r.Status = Status{"failed", "AbuseIPDB response lacks validated requested address, score, or reports"}
		return r
	}
	r.AbuseConfidenceScore = d.Score
	r.TotalReports = d.Reports
	r.Organization = d.ISP
	r.ProviderFlags = map[string]bool{}
	for name, v := range map[string]*bool{"is_tor": d.Tor, "is_public": d.Public, "is_whitelisted": d.Whitelist} {
		if v != nil {
			r.ProviderFlags[name] = *v
		}
	}
	r.Status = Status{Status: "ok", Reason: fmt.Sprintf("AbuseIPDB provider confidence from reports within %d days; not a local quality score", 90)}
	return r
}
