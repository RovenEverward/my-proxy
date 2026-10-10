package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// CapabilityOps is optional: existing NodeOps implementations need not probe.
type CapabilityOps interface {
	ProbeCapabilities(ctx context.Context, n model.Node, cfg model.CapabilityConfig, now time.Time) ([]model.CapabilityResult, error)
}

type capabilityView struct {
	Configs []model.CapabilityConfig `json:"configs"`
	Results []model.CapabilityResult `json:"results"`
}

func (s *Server) capabilityNode(w http.ResponseWriter, r *http.Request) (*model.Node, bool) {
	id, ok := s.pathID(w, r)
	if !ok {
		return nil, false
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return nil, false
	}
	return n, true
}

// capabilityEndpoint validates identity, independently of whether a probe is enabled.
func (s *Server) capabilityEndpoint(n model.Node, cfg model.CapabilityConfig) (*model.Inbound, model.Host, error) {
	in, err := s.store.GetInbound(cfg.InboundID)
	if err != nil {
		return nil, model.Host{}, err
	}
	if in.NodeID != n.ID {
		return nil, model.Host{}, fmt.Errorf("入站不属于所选节点")
	}
	h := model.Host{}
	if cfg.HostID != 0 {
		host, err := s.store.GetHost(cfg.HostID)
		if err != nil {
			return nil, h, err
		}
		if host.InboundID != in.ID {
			return nil, h, fmt.Errorf("入口不属于所选入站")
		}
		h = *host
	} else {
		hosts, err := s.store.ListHosts(in.ID)
		if err != nil {
			return nil, h, err
		}
		if len(hosts) > 0 {
			return nil, h, fmt.Errorf("入站已有显式入口,请指定 --host (不能冒充默认入口)")
		}
	}
	return in, h, nil
}

func (s *Server) capabilityRequest(w http.ResponseWriter, r *http.Request, n model.Node) (model.CapabilityConfig, bool) {
	var cfg model.CapabilityConfig
	if !decodeBody(w, r, &cfg) {
		return cfg, false
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return cfg, false
	}
	if _, _, err := s.capabilityEndpoint(n, cfg); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeStoreError(w, err, "入站或入口")
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return cfg, false
	}
	return cfg, true
}

func (s *Server) handleGetCapabilities(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	configs, err := s.store.ListCapabilityConfigs(n.ID)
	if err != nil {
		s.writeStoreError(w, err, "能力配置")
		return
	}
	results, err := s.capabilityResults(*n, time.Now())
	if err != nil {
		s.writeStoreError(w, err, "能力结果")
		return
	}
	writeJSON(w, http.StatusOK, capabilityView{Configs: configs, Results: results})
}

func (s *Server) handlePutCapabilities(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	cfg, ok := s.capabilityRequest(w, r, *n)
	if !ok {
		return
	}
	if err := s.store.SaveCapabilityConfig(cfg); err != nil {
		s.writeStoreError(w, err, "能力配置")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleProbeCapabilities(w http.ResponseWriter, r *http.Request) {
	n, ok := s.capabilityNode(w, r)
	if !ok {
		return
	}
	cfg, ok := s.capabilityRequest(w, r, *n)
	if !ok {
		return
	}
	ops, ok := s.nodeOps.(CapabilityOps)
	if !ok {
		writeError(w, http.StatusNotImplemented, "这个服务端没有接上能力检测")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	results, err := ops.ProbeCapabilities(ctx, *n, cfg, time.Now())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, results)
}

// capabilityResults returns a read projection without changing stored observations.
func (s *Server) capabilityResults(n model.Node, now time.Time) ([]model.CapabilityResult, error) {
	results, err := s.capabilityResultsForNodes([]model.Node{n}, now)
	if err != nil {
		return nil, err
	}
	return results[n.ID], nil
}

// capabilityResultsForNodes loads all evidence and endpoint associations once for
// node list responses. Every status is checked against current authorization.
func (s *Server) capabilityResultsForNodes(nodes []model.Node, now time.Time) (map[int64][]model.CapabilityResult, error) {
	out := make(map[int64][]model.CapabilityResult, len(nodes))
	selected := make(map[int64]model.Node, len(nodes))
	for _, n := range nodes {
		selected[n.ID] = n
		out[n.ID] = []model.CapabilityResult{}
	}
	if len(nodes) == 0 {
		return out, nil
	}
	results, err := s.store.ListCapabilityResults(0)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return out, nil
	}
	inbounds, err := s.store.ListInbounds(0)
	if err != nil {
		return nil, err
	}
	hosts, err := s.store.ListHosts(0)
	if err != nil {
		return nil, err
	}
	users, err := s.store.ListUsers()
	if err != nil {
		return nil, err
	}
	ins := map[int64]model.Inbound{}
	hs := map[int64]model.Host{}
	explicit := map[int64]bool{}
	for _, in := range inbounds {
		ins[in.ID] = in
	}
	for _, h := range hosts {
		hs[h.ID] = h
		explicit[h.InboundID] = true
	}
	type endpointState struct{ fingerprint, reason string }
	states := map[[2]int64]endpointState{}
	for _, result := range results {
		in, inExists := ins[result.InboundID]
		n, chosen := selected[in.NodeID]
		if !chosen {
			continue
		}
		key := [2]int64{result.InboundID, result.HostID}
		state, exists := states[key]
		if !exists {
			h, hostExists := hs[result.HostID]
			switch {
			case !inExists || (result.HostID != 0 && !hostExists):
				state.reason = "入站或入口已不存在"
			case result.NodeID != n.ID:
				state.reason = "入站不属于所选节点"
			case result.HostID != 0 && h.InboundID != in.ID:
				state.reason = "入口不属于所选入站"
			case result.HostID == 0 && explicit[in.ID]:
				state.reason = "入站已有显式入口,请指定 --host"
			default:
				state.fingerprint = control.CapabilityFingerprint(n, in, h)
				if n.Draining {
					state.reason = "节点已 drain"
				} else {
					eligible := false
					for j := range users {
						if users[j].Status(now) != model.StatusActive || !users[j].AllowsInbound(in.ID) {
							continue
						}
						if len(render.Resolve(n, []model.Inbound{in}, map[int64][]model.Host{in.ID: {h}}, &users[j])) > 0 {
							eligible = true
							break
						}
					}
					if !eligible {
						state.reason = "没有当前有效且获授权的检测用户"
					}
				}
			}
			states[key] = state
		}
		reason := state.reason
		if reason == "" && result.Fingerprint != state.fingerprint {
			reason = "节点、入站或入口配置已变化,请重新检测"
		}
		if reason == "" && !now.Before(result.ExpiresAt) {
			reason = "检测结果已过期,请重新检测"
		}
		if reason != "" {
			result.Status = model.CapabilityUnknown
			if result.Reason != "" {
				reason += "; 原始观测: " + result.Reason
			}
			result.Reason = reason
		}
		out[n.ID] = append(out[n.ID], result)
	}
	return out, nil
}
