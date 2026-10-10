package diagnostics

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// DefaultTargets use provider-documented regional mirrors, not anycast geography.
func DefaultTargets() []Target {
	return []Target{
		{Name: "Clouvider Los Angeles", Region: "Los Angeles, USA (LibreSpeed official server registry)", Host: "la.speedtest.clouvider.net", HTTPURL: "https://la.speedtest.clouvider.net/backend/empty.php"},
		{Name: "Clouvider Frankfurt", Region: "Frankfurt, Germany (LibreSpeed official server registry)", Host: "fra.speedtest.clouvider.net", HTTPURL: "https://fra.speedtest.clouvider.net/backend/empty.php"},
		{Name: "Cloudflare anycast", Region: "Anycast; serving location is not assumed", Host: "www.cloudflare.com", HTTPURL: "https://www.cloudflare.com/cdn-cgi/trace"},
	}
}

func (c Config) Validate() error { _, err := c.Normalized(); return err }

// Normalized returns a validated copy with safe defaults. Callers should not persist secrets.
func (c Config) Normalized() (Config, error) {
	if c.Position == "" {
		c.Position = "local"
	}
	if c.Position != "local" && c.Position != "node" && c.Position != "control" {
		return c, fmt.Errorf("position must be node, control, or local")
	}
	if len(c.NodeID) > 128 {
		return c, fmt.Errorf("node_id too long")
	}
	if c.Samples == 0 {
		c.Samples = 3
	}
	if c.Samples < 1 || c.Samples > 5 {
		return c, fmt.Errorf("samples must be 1..5")
	}
	if c.RequestTimeoutSeconds == 0 {
		c.RequestTimeoutSeconds = 5
	}
	if c.RequestTimeoutSeconds < 1 || c.RequestTimeoutSeconds > 15 {
		return c, fmt.Errorf("request timeout must be 1..15 seconds")
	}
	if c.TotalTimeoutSeconds == 0 {
		c.TotalTimeoutSeconds = 120
	}
	if c.TotalTimeoutSeconds < 1 || c.TotalTimeoutSeconds > 170 {
		return c, fmt.Errorf("total timeout must be 1..170 seconds")
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = DefaultMaxBytes
	}
	if c.MaxBytes < 1024 || c.MaxBytes > DefaultMaxBytes {
		return c, fmt.Errorf("max_bytes must be 1024..33554432")
	}
	if c.SpeedBytes == 0 {
		c.SpeedBytes = 1 << 20
	}
	if c.SpeedBytes < 1024 || c.SpeedBytes > 8<<20 {
		return c, fmt.Errorf("speed_bytes must be 1024..8388608")
	}
	if len(c.Targets) == 0 {
		c.Targets = DefaultTargets()
	}
	if len(c.Targets) > 6 {
		return c, fmt.Errorf("at most six targets")
	}
	for _, t := range c.Targets {
		if !safeLabel(t.Name, 80) || !safeLabel(t.Region, 160) || !validHost(t.Host) {
			return c, fmt.Errorf("invalid target name, region, or host")
		}
		if t.HTTPURL != "" {
			if err := validURL(t.HTTPURL, false); err != nil {
				return c, fmt.Errorf("invalid target HTTP URL: %w", err)
			}
		}
	}
	if len(c.SpeedSites) == 0 {
		c.SpeedSites = DefaultSpeedSites()
	}
	if len(c.SpeedSites) > 3 {
		return c, fmt.Errorf("at most three speed sites")
	}
	for _, s := range c.SpeedSites {
		if !safeLabel(s.Name, 80) {
			return c, fmt.Errorf("invalid speed site name")
		}
		if s.DownloadUnitBytes < 0 || s.DownloadUnitBytes > 1<<20 || (strings.Contains(s.DownloadURL, "{chunks}") && s.DownloadUnitBytes == 0) {
			return c, fmt.Errorf("download unit must be 1..1048576 when chunks are used")
		}
		if s.UploadMethod != "" && s.UploadMethod != "POST" && s.UploadMethod != "PUT" {
			return c, fmt.Errorf("upload method must be POST or PUT")
		}
		for _, u := range []string{strings.ReplaceAll(strings.ReplaceAll(s.DownloadURL, "{bytes}", "1024"), "{chunks}", "1"), s.UploadURL, s.SourceURL} {
			if err := validURL(u, false); err != nil {
				return c, fmt.Errorf("invalid speed site URL: %w", err)
			}
		}
	}
	if c.SocksURL != "" {
		if c.Position != "local" {
			return c, fmt.Errorf("SOCKS is supported only at local position")
		}
		if len(c.SocksURL) > 512 {
			return c, fmt.Errorf("SOCKS URL too long")
		}
		u, err := url.Parse(c.SocksURL)
		if err != nil || u.Scheme != "socks5h" || u.Hostname() == "" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !validHost(u.Hostname()) {
			return c, fmt.Errorf("SOCKS URL must be socks5h://host:port without credentials")
		}
		if _, err := net.LookupPort("tcp", u.Port()); err != nil {
			return c, fmt.Errorf("invalid SOCKS port")
		}
	}
	if c.IPInfoURL == "" {
		c.IPInfoURL = "https://ipwho.is/"
	}
	if c.IPv4URL == "" {
		c.IPv4URL = "https://api.ipify.org"
	}
	if c.IPv6URL == "" {
		c.IPv6URL = "https://api6.ipify.org"
	}
	for _, u := range []string{c.IPInfoURL, c.IPv4URL, c.IPv6URL} {
		if err := validURL(u, false); err != nil {
			return c, fmt.Errorf("invalid IP endpoint: %w", err)
		}
	}
	if len(c.IPAPIKey) > 256 || (c.IPAPIKey != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(c.IPAPIKey)) {
		return c, fmt.Errorf("invalid ipapi.is key")
	}
	if c.IPAPIKeyEnv != "" && !envPattern.MatchString(c.IPAPIKeyEnv) {
		return c, fmt.Errorf("invalid ipapi.is key environment variable")
	}
	if len(c.AbuseIPDBKey) > 256 || strings.ContainsAny(c.AbuseIPDBKey, "\r\n\x00") {
		return c, fmt.Errorf("invalid AbuseIPDB key")
	}
	if c.AbuseIPDBKeyEnv != "" && !envPattern.MatchString(c.AbuseIPDBKeyEnv) {
		return c, fmt.Errorf("invalid API key environment variable")
	}
	return c, nil
}

func safeLabel(s string, max int) bool { return len(s) <= max && !strings.ContainsAny(s, "\r\n\x00") }
func validHost(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	if !hostPattern.MatchString(s) || strings.Contains(s, "..") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}
func validURL(raw string, proxy bool) error {
	if len(raw) > 1024 || strings.ContainsAny(raw, "\r\n\x00'\"`\\<>{} ") || strings.Contains(raw, "$(") {
		return fmt.Errorf("URL contains forbidden characters or exceeds limit")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || !validHost(u.Hostname()) || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("expected HTTP(S) URL without credentials or fragment")
	}
	if u.Port() != "" {
		if _, err := net.LookupPort("tcp", u.Port()); err != nil {
			return fmt.Errorf("invalid port")
		}
	}
	return nil
}

// shellQuote is applied even to validated data; no untrusted value becomes shell syntax.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
