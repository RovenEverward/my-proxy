package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/render"
)

const capabilityBudget = 2 * time.Minute
const capabilityDetector = "mpx-proxy-v1"

// CapabilityFingerprint excludes subscriber-specific credentials; results describe
// one concrete exposure and deployed route, never the machine's default egress.
func CapabilityFingerprint(n model.Node, in model.Inbound, h model.Host) string {
	body, _ := json.Marshal(struct {
		Node    model.Node
		Inbound model.Inbound
		Host    model.Host
	}{n, in, h})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (c *Control) ValidateCapabilityEndpoint(n model.Node, cfg model.CapabilityConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	in, err := c.store.GetInbound(cfg.InboundID)
	if err != nil {
		return err
	}
	if in.NodeID != n.ID {
		return fmt.Errorf("入站不属于所选节点")
	}
	exposureHost := model.Host{}
	if cfg.HostID > 0 {
		h, err := c.store.GetHost(cfg.HostID)
		if err != nil {
			return err
		}
		if h.InboundID != in.ID {
			return fmt.Errorf("入口不属于所选入站")
		}
		exposureHost = *h
	} else {
		hosts, err := c.store.ListHosts(in.ID)
		if err != nil {
			return err
		}
		if len(hosts) > 0 {
			return fmt.Errorf("入站已有显式入口,请指定 --host")
		}
	}
	if !render.CapabilityExposureStable(n, *in, exposureHost) {
		return fmt.Errorf("用户专属动态域名无法共享检测结果,请使用固定入口")
	}
	return nil
}

func (c *Control) ProbeCapabilities(ctx context.Context, n model.Node, cfg model.CapabilityConfig, now time.Time) ([]model.CapabilityResult, error) {
	generation := cfg.Generation
	var err error
	if generation == 0 {
		generation, err = c.store.CapabilityGeneration(cfg.InboundID, cfg.HostID)
		if err != nil {
			return nil, err
		}
	} else {
		current, e := c.store.CapabilityGeneration(cfg.InboundID, cfg.HostID)
		if e != nil {
			return nil, e
		}
		if current != generation {
			return nil, fmt.Errorf("检测配置已变化,已跳过旧调度任务")
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	select {
	case c.capabilitySlot <- struct{}{}:
		defer func() { <-c.capabilitySlot }()
	default:
		return nil, fmt.Errorf("已有能力检测进行中,请稍后重试")
	}
	ctx, cancel := context.WithTimeout(ctx, capabilityBudget)
	defer cancel()
	in, err := c.store.GetInbound(cfg.InboundID)
	if err != nil {
		return nil, err
	}
	if in.NodeID != n.ID {
		return nil, fmt.Errorf("入站不属于所选节点")
	}
	h := model.Host{}
	if cfg.HostID > 0 {
		host, err := c.store.GetHost(cfg.HostID)
		if err != nil {
			return nil, err
		}
		if host.InboundID != in.ID {
			return nil, fmt.Errorf("入口不属于所选入站")
		}
		h = *host
	} else {
		hosts, err := c.store.ListHosts(in.ID)
		if err != nil {
			return nil, err
		}
		if len(hosts) > 0 {
			return nil, fmt.Errorf("入站已有显式入口,请指定 --host (不能冒充默认入口)")
		}
	}
	if !render.CapabilityExposureStable(n, *in, h) {
		return nil, fmt.Errorf("用户专属动态域名无法共享检测结果,请使用固定地址、Host 与 SNI")
	}
	fingerprint := CapabilityFingerprint(n, *in, h)
	previous, err := c.store.ListCapabilityResults(n.ID)
	if err != nil {
		return nil, err
	}
	for _, old := range previous {
		if old.InboundID == in.ID && old.HostID == h.ID && now.Sub(old.CheckedAt) < 30*time.Second {
			return nil, fmt.Errorf("同一入口检测最少间隔 30 秒")
		}
	}
	users, err := c.store.ListUsers()
	if err != nil {
		return nil, err
	}
	var ep render.Endpoint
	found := false
	if !n.Draining {
		for i := range users {
			if users[i].Status(now) != model.StatusActive || !users[i].AllowsInbound(in.ID) {
				continue
			}
			candidates := render.Resolve(n, []model.Inbound{*in}, map[int64][]model.Host{in.ID: {h}}, &users[i])
			if len(candidates) > 0 {
				ep = candidates[0]
				found = true
				break
			}
		}
	}
	var observations []node.CapabilityObservation
	if found {
		observations, err = node.ProbeCapabilities(ctx, c.runner, n, ep, cfg.Targets)
	}
	failure := "没有当前有效且获授权的检测用户,未发起请求"
	if n.Draining {
		failure = "节点已 drain,未发起请求"
	}
	if err != nil {
		failure = "代理探测失败: " + err.Error()
	}
	if !found || err != nil {
		observations = nil
		for _, target := range cfg.Targets {
			observations = append(observations, node.CapabilityObservation{Target: target, Status: string(model.CapabilityUnknown), Reason: failure})
		}
	}
	checked := time.Now().UTC()
	results := make([]model.CapabilityResult, 0, len(observations))
	for _, o := range observations {
		next := model.CapabilityResult{NodeID: n.ID, InboundID: in.ID, HostID: h.ID, Target: o.Target, ObservedStatus: model.CapabilityStatus(o.Status), Region: o.Region, ExitIP: o.ExitIP, Location: fmt.Sprintf("节点 %s 经入站 %d/入口 %d 的代理出口", n.Name, in.ID, h.ID), Detector: node.CapabilityDetector + "/" + node.CapabilityDetectorVersion, Fingerprint: fingerprint, CheckedAt: checked, ExpiresAt: checked.Add(time.Duration(cfg.TTLSeconds) * time.Second), Reason: o.Reason}
		var old *model.CapabilityResult
		for i := range previous {
			if previous[i].InboundID == in.ID && previous[i].HostID == h.ID && previous[i].Target == o.Target {
				old = &previous[i]
				break
			}
		}
		next = model.StabilizeCapability(old, next)
		if next.Status == model.CapabilityUnknown && next.ObservedStatus != model.CapabilityUnknown {
			next.Reason += "; 等待连续两次一致结果"
		}
		results = append(results, next)
	}
	saved, err := c.store.SaveCapabilityResultsIfCurrent(results, generation)
	if err != nil {
		return nil, err
	}
	if !saved {
		return nil, fmt.Errorf("检测期间配置已变化,结果已丢弃;请重新检测")
	}
	for _, next := range results {
		for _, old := range previous {
			if old.InboundID == next.InboundID && old.HostID == next.HostID && old.Target == next.Target && old.Status != next.Status {
				c.logger.Info("节点能力状态变更", "node", n.Name, "target", next.Target, "status", next.Status)
				settings, e := c.store.GetSettings()
				if e == nil && settings.WebhookURL != "" {
					e = PostWebhook(ctx, settings.WebhookURL, WebhookEvent{Event: "capability_changed", Kind: next.Target, NodeID: n.ID, NodeName: n.Name, Message: fmt.Sprintf("入站 %d/入口 %d: %s %s → %s (%s)", in.ID, h.ID, next.Target, old.Status, next.Status, next.Reason), At: checked}, c.logger)
					if e != nil {
						c.logger.Warn("能力变更通知失败", "error", e)
					}
				}
			}
		}
	}
	return results, nil
}

// RunCapabilityChecks owns its ticker and resource budget independently from
// traffic sampling and user reconciliation. One global slot also bounds manual
// requests. Disabled configs and unconfigured endpoints do no background work.
func (c *Control) RunCapabilityChecks(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		c.checkDueCapabilities(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Control) checkDueCapabilities(ctx context.Context, now time.Time) {
	configs, err := c.store.ListCapabilityConfigs(0)
	if err != nil {
		c.logger.Warn("读取能力检测配置失败", "error", err)
		return
	}
	results, err := c.store.ListCapabilityResults(0)
	if err != nil {
		c.logger.Warn("读取能力结果失败", "error", err)
		return
	}
	for _, cfg := range configs {
		if !cfg.Enabled || ctx.Err() != nil {
			continue
		}
		latest := time.Time{}
		for _, r := range results {
			if r.InboundID == cfg.InboundID && r.HostID == cfg.HostID && r.CheckedAt.After(latest) {
				latest = r.CheckedAt
			}
		}
		if now.Sub(latest) < time.Duration(cfg.IntervalSeconds)*time.Second {
			continue
		}
		in, err := c.store.GetInbound(cfg.InboundID)
		if err != nil {
			continue
		}
		n, err := c.store.GetNode(in.NodeID)
		if err != nil {
			continue
		}
		if _, err = c.ProbeCapabilities(ctx, *n, cfg, now); err != nil {
			c.logger.Warn("自动能力检测失败", "node", n.Name, "error", err)
		}
	}
}
