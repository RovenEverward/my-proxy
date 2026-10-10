// Package diagnostics runs bounded, manual network diagnostics using installed tools.
package diagnostics

import (
	"context"
	"time"
)

// Executor must honor cancellation, kill child processes, and bound stdout and
// stderr individually to MaxOutputBytes before allocating them. Return stdout;
// stderr must not be returned or logged. stdin can contain secrets:
// never log it.
type Executor interface {
	Exec(context.Context, string, []byte) (string, error)
}

const (
	MaxOutputBytes               = 64 << 10
	MaxReportEvidenceBytes       = 4096
	DefaultMaxBytes        int64 = 32 << 20
)

// Config uses explicit seconds/bytes units. Zero limits use safe defaults.
// Position labels where commands execute; callers adapt their node.Runner.
// Secrets are excluded from JSON and must never be persisted or logged.
type Config struct {
	Position              string      `json:"position"` // node, control, local
	NodeID                string      `json:"node_id,omitempty"`
	SocksURL              string      `json:"socks_url,omitempty"` // local only, socks5h; never direct fallback
	Speed                 bool        `json:"speed"`
	Targets               []Target    `json:"targets,omitempty"`
	SpeedSites            []SpeedSite `json:"speed_sites,omitempty"`
	Samples               int         `json:"samples,omitempty"`
	RequestTimeoutSeconds int         `json:"request_timeout_seconds,omitempty"`
	TotalTimeoutSeconds   int         `json:"total_timeout_seconds,omitempty"`
	MaxBytes              int64       `json:"max_bytes,omitempty"`   // all HTTP response bodies + upload bodies; reservations include errors
	SpeedBytes            int64       `json:"speed_bytes,omitempty"` // per direction/site
	IPInfoURL             string      `json:"ip_info_url,omitempty"` // defaults to https://ipwho.is/
	IPv4URL               string      `json:"ipv4_url,omitempty"`    // defaults to https://api.ipify.org
	IPv6URL               string      `json:"ipv6_url,omitempty"`    // defaults to https://api6.ipify.org
	AbuseIPDBKey          string      `json:"-"`
	AbuseIPDBKeyEnv       string      `json:"-"` // resolved on executing machine, validated identifier
	IPAPIKey              string      `json:"-"`
	IPAPIKeyEnv           string      `json:"-"`
}

type Target struct {
	Name    string `json:"name"`
	Region  string `json:"region"` // documented location, not inferred from hostname/IP
	Host    string `json:"host"`
	HTTPURL string `json:"http_url"`
}

type SpeedSite struct {
	Name              string `json:"name"`
	DownloadURL       string `json:"download_url"`                  // {bytes} substituted with bounded integer
	UploadURL         string `json:"upload_url"`                    // POST binary body
	SourceURL         string `json:"source_url"`                    // provider endpoint documentation
	DownloadUnitBytes int64  `json:"download_unit_bytes,omitempty"` // {chunks} units; requests round up and charge actual size
	UploadMethod      string `json:"upload_method,omitempty"`       // POST (default) or PUT
}

type Report struct {
	Position     string         `json:"position"`
	NodeID       string         `json:"node_id,omitempty"`
	ThroughSOCKS bool           `json:"through_socks"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at"`
	DurationMS   float64        `json:"duration_ms"`
	Tools        []Tool         `json:"tools"`
	Targets      []TargetReport `json:"targets"`
	Speed        []SpeedResult  `json:"speed"`
	IP           []IPResult     `json:"ip"`
	Budget       Budget         `json:"budget"`
	Warnings     []string       `json:"warnings,omitempty"`
}

type Tool struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}
type Budget struct {
	MaxBytes            int64 `json:"max_bytes"`
	ReservedBytes       int64 `json:"reserved_bytes"`    // conservative charge before each request, even on failure
	TransferredBytes    int64 `json:"transferred_bytes"` // curl measured body bytes, excluding headers/TLS
	TotalTimeoutSeconds int   `json:"total_timeout_seconds"`
}
type Status struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
} // ok, partial, failed, skipped

type TargetReport struct {
	Target Target  `json:"target"`
	DNS    Status  `json:"dns"`
	ICMP   Latency `json:"icmp"`
	HTTP   Latency `json:"http"`
	Route  Route   `json:"route"`
}

type Latency struct {
	Status
	Protocol               string    `json:"protocol"`
	Timing                 string    `json:"timing"` // ICMP RTT, or curl time_total through response body completion
	Attempted              int       `json:"attempted"`
	ValidSamples           int       `json:"valid_samples"`
	SamplesMS              []float64 `json:"samples_ms,omitempty"`
	MinMS                  float64   `json:"min_ms,omitempty"`
	MeanMS                 float64   `json:"mean_ms,omitempty"`
	MaxMS                  float64   `json:"max_ms,omitempty"`
	JitterMS               float64   `json:"jitter_ms,omitempty"`    // mean absolute difference of adjacent successful samples
	JitterSamples          int       `json:"jitter_samples"`         // max(valid_samples-1, 0)
	LossPercent            *float64  `json:"loss_percent,omitempty"` // ICMP only
	HTTPFailureRatePercent *float64  `json:"http_failure_rate_percent,omitempty"`
}

type Route struct {
	Status
	Tool      string `json:"tool,omitempty"`
	Direction string `json:"direction"`
	Hops      []Hop  `json:"hops,omitempty"`
	Evidence  string `json:"evidence,omitempty"` // truncated raw tool evidence, no inferred carrier quality
}
type Hop struct {
	TTL         int      `json:"ttl"`
	Address     string   `json:"address,omitempty"`
	LossPercent *float64 `json:"loss_percent,omitempty"`
	RTTMS       *float64 `json:"rtt_ms,omitempty"`
	ASN         string   `json:"asn,omitempty"`
	Unanswered  bool     `json:"unanswered"`
}

type SpeedResult struct {
	Status
	Site       string  `json:"site"`
	SourceURL  string  `json:"source_url"`
	Direction  string  `json:"direction"`
	Bytes      int64   `json:"bytes"`
	DurationMS float64 `json:"duration_ms"`
	Mbps       float64 `json:"mbps"` // successful bytes*8/time_total/1e6
}

type IPResult struct {
	Status
	Family               string          `json:"family"` // ipv4, ipv6, geoip, reputation
	Source               string          `json:"source"`
	CheckedAt            time.Time       `json:"checked_at"`
	Address              string          `json:"address,omitempty"`
	Country              string          `json:"country,omitempty"`
	Region               string          `json:"region,omitempty"`
	City                 string          `json:"city,omitempty"`
	ASN                  string          `json:"asn,omitempty"`
	Organization         string          `json:"organization,omitempty"`
	ProviderFlags        map[string]bool `json:"provider_flags,omitempty"`
	AbuseConfidenceScore *int            `json:"abuse_confidence_score,omitempty"` // provider's score, not a local quality score
	TotalReports         *int            `json:"total_reports,omitempty"`
}
