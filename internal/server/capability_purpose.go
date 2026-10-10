package server

import (
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"time"
)

// filterPurposeCapabilities runs only after subscriber status, authorization,
// draining, and manual tags have produced their candidate intersection.
func (s *Server) filterPurposeCapabilities(p model.Purpose, eps []render.Endpoint) ([]render.Endpoint, error) {
	if len(p.RequiredCapabilities) == 0 {
		return eps, nil
	}
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, err
	}
	projected, err := s.capabilityResultsForNodes(nodes, time.Now())
	if err != nil {
		return nil, err
	}
	byEndpoint := map[[2]int64]map[string]model.CapabilityResult{}
	for _, results := range projected {
		for _, r := range results {
			key := [2]int64{r.InboundID, r.HostID}
			if byEndpoint[key] == nil {
				byEndpoint[key] = map[string]model.CapabilityResult{}
			}
			byEndpoint[key][r.Target] = r
		}
	}
	out := make([]render.Endpoint, 0, len(eps))
	for _, ep := range eps {
		results := byEndpoint[[2]int64{ep.InboundID, ep.HostID}]
		eligible := true
		for _, target := range p.RequiredCapabilities {
			r, ok := results[target]
			// youtube measures connectivity, not region. Region requests use Netflix's
			// catalog region or the egress geo-IP result, never a synthetic video region.
			region := ""
			if target == "netflix" || target == "egress" {
				region = p.CapabilityRegion
			}
			if !ok || r.Status != model.CapabilityAvailable || (region != "" && r.Region != region) {
				eligible = false
				break
			}
		}
		if eligible && p.CapabilityRegion != "" {
			regionSupported := false
			for _, target := range p.RequiredCapabilities {
				if target == "egress" || target == "netflix" {
					regionSupported = true
				}
			}
			eligible = regionSupported
		}
		if eligible {
			out = append(out, ep)
		}
	}
	return out, nil
}
