package store

import (
	"github.com/RovenEverward/my-proxy/internal/model"
	"testing"
)

func TestCapabilityEditDuringProbeDoesNotReviveEvidence(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "node")
	in := newTestInbound(t, s, n.ID, "in")
	cfg := model.CapabilityConfig{InboundID: in.ID, Enabled: true}
	if err := s.SaveCapabilityConfig(cfg); err != nil {
		t.Fatal(err)
	}
	generation, err := s.CapabilityGeneration(in.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	result := []model.CapabilityResult{{NodeID: n.ID, InboundID: in.ID, Target: "egress", Status: model.CapabilityAvailable}}
	saved, err := s.SaveCapabilityResultsIfCurrent(result, generation)
	if err != nil || !saved {
		t.Fatalf("fresh rejected %v %v", saved, err)
	}
	cfg.Enabled = false
	if err := s.SaveCapabilityConfig(cfg); err != nil {
		t.Fatal(err)
	}
	saved, err = s.SaveCapabilityResultsIfCurrent(result, generation)
	if err != nil || saved {
		t.Fatalf("stale accepted %v %v", saved, err)
	}
	results, _ := s.ListCapabilityResults(n.ID)
	if len(results) != 0 {
		t.Fatal("disabled detector evidence revived")
	}
}
