package server

import (
	"github.com/RovenEverward/my-proxy/internal/model"
	"time"
)

type subCapability struct {
	Entrance string
	Target   string
	Status   string
	Region   string
	ExitIP   string
	Checked  string
	Expires  string
	Location string
	Reason   string
}

func (s *Server) subscriberCapabilities(user *model.User, now time.Time) ([]subCapability, error) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, err
	}
	var out []subCapability
	if user.Status(now) != model.StatusActive {
		return out, nil
	}
	results, err := s.capabilityResultsForNodes(nodes, now)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Draining {
			continue
		}
		for _, result := range results[n.ID] {
			if !user.AllowsInbound(result.InboundID) {
				continue
			}
			in, err := s.store.GetInbound(result.InboundID)
			if err != nil {
				return nil, err
			}
			name := n.Name + " / " + in.TagOrDefault()
			if result.HostID > 0 {
				h, err := s.store.GetHost(result.HostID)
				if err != nil {
					return nil, err
				}
				name += " / " + h.Address
			}
			status := "未知"
			switch result.Status {
			case model.CapabilityAvailable:
				status = "可用"
			case model.CapabilityUnavailable:
				status = "不可用"
			}
			out = append(out, subCapability{Entrance: name, Target: result.Target, Status: status, Region: result.Region, ExitIP: result.ExitIP, Checked: result.CheckedAt.UTC().Format(time.RFC3339), Expires: result.ExpiresAt.UTC().Format(time.RFC3339), Location: result.Location, Reason: result.Reason})
		}
	}
	return out, nil
}
