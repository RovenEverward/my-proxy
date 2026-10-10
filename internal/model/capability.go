package model

import (
	"fmt"
	"time"
)

// CapabilityTargets names observations, not promises about speed or playback.
var CapabilityTargets = []string{"egress", "youtube", "netflix"}

func ValidCapabilityTarget(target string) bool {
	for _, t := range CapabilityTargets {
		if t == target {
			return true
		}
	}
	return false
}

type CapabilityStatus string

const (
	CapabilityAvailable   CapabilityStatus = "available"
	CapabilityUnavailable CapabilityStatus = "unavailable"
	CapabilityUnknown     CapabilityStatus = "unknown"
)

// CapabilityConfig scopes probes to one actual proxy entrance. HostID zero selects
// the inbound's default exposure; it never means all of its advertised hosts.
type CapabilityConfig struct {
	InboundID       int64    `json:"inbound_id"`
	HostID          int64    `json:"host_id"`
	Enabled         bool     `json:"enabled"`
	Targets         []string `json:"targets"`
	IntervalSeconds int      `json:"interval_seconds"`
	TTLSeconds      int      `json:"ttl_seconds"`
}

func (c *CapabilityConfig) Validate() error {
	if c.InboundID <= 0 || c.HostID < 0 {
		return fmt.Errorf("入站 ID 必须为正数,入口 ID 不能为负数")
	}
	if len(c.Targets) == 0 {
		c.Targets = append([]string(nil), CapabilityTargets...)
	}
	seen := map[string]bool{}
	for _, target := range c.Targets {
		if !ValidCapabilityTarget(target) || seen[target] {
			return fmt.Errorf("检测目标未知或重复: %q (支持 egress/youtube/netflix)", target)
		}
		seen[target] = true
	}
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 3600
	}
	if c.TTLSeconds == 0 {
		c.TTLSeconds = 10800
	}
	if c.IntervalSeconds < 300 || c.IntervalSeconds > 86400 {
		return fmt.Errorf("检测间隔必须在 300–86400 秒之间")
	}
	if c.TTLSeconds < c.IntervalSeconds || c.TTLSeconds > 604800 {
		return fmt.Errorf("检测有效期必须不小于间隔且不超过 604800 秒")
	}
	return nil
}

// CapabilityResult retains only the latest observation and stable decision.
// Two matching observations establish or change a definite decision; unknown
// observations immediately remove eligibility instead of preserving old success.
type CapabilityResult struct {
	NodeID         int64            `json:"node_id"`
	InboundID      int64            `json:"inbound_id"`
	HostID         int64            `json:"host_id"`
	Target         string           `json:"target"`
	Status         CapabilityStatus `json:"status"`
	ObservedStatus CapabilityStatus `json:"observed_status"`
	Consecutive    int              `json:"consecutive"`
	Region         string           `json:"region,omitempty"`
	ExitIP         string           `json:"exit_ip,omitempty"`
	Location       string           `json:"location"`
	Detector       string           `json:"detector"`
	Fingerprint    string           `json:"fingerprint"`
	CheckedAt      time.Time        `json:"checked_at"`
	ExpiresAt      time.Time        `json:"expires_at"`
	Reason         string           `json:"reason,omitempty"`
}

func (r CapabilityResult) Available(now time.Time, fingerprint, region string) bool {
	return r.Status == CapabilityAvailable && r.Fingerprint == fingerprint && now.Before(r.ExpiresAt) && (region == "" || r.Region == region)
}

func StabilizeCapability(previous *CapabilityResult, next CapabilityResult) CapabilityResult {
	next.Consecutive = 1
	next.Status = CapabilityUnknown
	if previous != nil && previous.Fingerprint == next.Fingerprint && previous.ObservedStatus == next.ObservedStatus && previous.Region == next.Region && next.CheckedAt.Before(previous.ExpiresAt) {
		next.Consecutive = previous.Consecutive + 1
		if next.Consecutive > 2 {
			next.Consecutive = 2
		}
	}
	if next.ObservedStatus == CapabilityUnknown {
		next.Status = CapabilityUnknown
	} else if next.Consecutive >= 2 {
		next.Status = next.ObservedStatus
	}
	// A changed observed region cannot borrow an old stable availability decision.
	return next
}
