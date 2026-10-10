package model

import (
	"testing"
	"time"
)

func TestCapabilityEvidenceStabilityAndExpiry(t *testing.T) {
	now := time.Now()
	next := CapabilityResult{Fingerprint: "route-one", ObservedStatus: CapabilityAvailable, Region: "JP", CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
	first := StabilizeCapability(nil, next)
	if first.Available(now, "route-one", "JP") {
		t.Fatal("single observation became verified")
	}
	next.CheckedAt = now.Add(time.Minute)
	verified := StabilizeCapability(&first, next)
	if !verified.Available(next.CheckedAt, "route-one", "JP") {
		t.Fatal("two equal observations didn't verify")
	}
	if verified.Available(verified.ExpiresAt, "route-one", "") || verified.Available(now, "route-two", "") || verified.Available(now, "route-one", "US") {
		t.Fatal("stale/different route/region evidence accepted")
	}
	next.ObservedStatus = CapabilityUnavailable
	failure := StabilizeCapability(&verified, next)
	if failure.Available(now, "route-one", "") || failure.Status != CapabilityUnknown {
		t.Fatal("first failure retained availability")
	}
	denied := StabilizeCapability(&failure, next)
	if denied.Status != CapabilityUnavailable {
		t.Fatal("two refusals didn't stabilize")
	}
	next.ObservedStatus = CapabilityAvailable
	recovering := StabilizeCapability(&denied, next)
	if recovering.Available(now, "route-one", "") {
		t.Fatal("first recovery immediately included")
	}
	recovered := StabilizeCapability(&recovering, next)
	if !recovered.Available(now, "route-one", "JP") {
		t.Fatal("stable recovery excluded")
	}
	next.Region = "US"
	changed := StabilizeCapability(&recovered, next)
	if changed.Available(now, "route-one", "US") {
		t.Fatal("changed service region reused old success")
	}
	next.Region = "JP"
	next.ObservedStatus = CapabilityUnknown
	unknown := StabilizeCapability(&recovered, next)
	if unknown.Status != CapabilityUnknown {
		t.Fatal("unknown preserved success")
	}
}

func TestCapabilityConfigBoundaries(t *testing.T) {
	good := CapabilityConfig{InboundID: 1}
	if err := good.Validate(); err != nil || good.IntervalSeconds != 3600 || good.TTLSeconds != 10800 || len(good.Targets) != 3 {
		t.Fatalf("defaults: %+v %v", good, err)
	}
	for _, cfg := range []CapabilityConfig{
		{InboundID: 0}, {InboundID: 1, HostID: -1}, {InboundID: 1, Targets: []string{"speed"}},
		{InboundID: 1, Targets: []string{"egress", "egress"}}, {InboundID: 1, IntervalSeconds: 299},
		{InboundID: 1, IntervalSeconds: 300, TTLSeconds: 300}, {InboundID: 1, IntervalSeconds: 300, TTLSeconds: 599},
		{InboundID: 1, IntervalSeconds: 86401}, {InboundID: 1, TTLSeconds: 300}, {InboundID: 1, TTLSeconds: 604801},
	} {
		if cfg.Validate() == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}
