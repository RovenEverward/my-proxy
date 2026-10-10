package store

import (
	"github.com/RovenEverward/my-proxy/internal/model"
	"testing"
	"time"
)

func TestCapabilityBoundedPersistenceAndCascade(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "node")
	in := newTestInbound(t, s, n.ID, "in")
	h := newTestHost(t, s, in.ID, "host")
	cfg := model.CapabilityConfig{InboundID: in.ID, HostID: h.ID, Enabled: true}
	if err := s.SaveCapabilityConfig(cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	result := model.CapabilityResult{NodeID: n.ID, InboundID: in.ID, HostID: h.ID, Target: "egress", Status: model.CapabilityAvailable, CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
	for i := 0; i < 5; i++ {
		if err := s.SaveCapabilityResults([]model.CapabilityResult{result}); err != nil {
			t.Fatal(err)
		}
	}
	results, err := s.ListCapabilityResults(n.ID)
	if err != nil || len(results) != 1 {
		t.Fatalf("latest-only: %+v %v", results, err)
	}
	other := newTestInbound(t, s, n.ID, "other")
	if err := s.SaveCapabilityConfig(model.CapabilityConfig{InboundID: other.ID, HostID: h.ID}); err == nil {
		t.Fatal("foreign host accepted")
	}
	if err := s.SaveCapabilityConfig(cfg); err != nil {
		t.Fatal(err)
	}
	results, _ = s.ListCapabilityResults(n.ID)
	if len(results) != 0 {
		t.Fatal("config change retained evidence")
	}
	if err := s.SaveCapabilityResults([]model.CapabilityResult{result}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHost(h.ID); err != nil {
		t.Fatal(err)
	}
	configs, _ := s.ListCapabilityConfigs(n.ID)
	results, _ = s.ListCapabilityResults(n.ID)
	if len(configs) != 0 || len(results) != 0 {
		t.Fatal("host deletion left evidence/config")
	}
	cfg.HostID = 0
	if err := s.SaveCapabilityConfig(cfg); err != nil {
		t.Fatal(err)
	}
	result.HostID = 0
	if err := s.SaveCapabilityResults([]model.CapabilityResult{result}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	configs, _ = s.ListCapabilityConfigs(0)
	results, _ = s.ListCapabilityResults(0)
	if len(configs) != 0 || len(results) != 0 {
		t.Fatal("node deletion left evidence/config")
	}
}
