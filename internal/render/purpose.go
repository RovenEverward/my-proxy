package render

import (
	"encoding/json"
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
	"strconv"
	"strings"
)

// PurposeSubscription reuses the ordinary renderers and adds a group for the
// selected candidates. Non-daily traffic retains the original template routing.
func PurposeSubscription(all, candidates []Endpoint, p model.Purpose, format model.ClientFormat, template []byte) ([]byte, error) {
	switch format {
	case model.FormatClashMeta:
		return clashPurpose(all, candidates, p, template)
	case model.FormatSingBox:
		return singboxPurpose(all, candidates, p, template)
	default:
		return nil, fmt.Errorf("用途路由只支持 clash-meta / sing-box；原订阅仍支持该格式")
	}
}

// AddPurposeRouting layers a second group onto already-rendered config without reinjecting nodes.
func AddPurposeRouting(body []byte, all, candidates []Endpoint, p model.Purpose, f model.ClientFormat) ([]byte, error) {
	switch f {
	case model.FormatClashMeta:
		return addClashPurpose(body, all, candidates, p)
	case model.FormatSingBox:
		return addSingboxPurpose(body, all, candidates, p)
	default:
		return nil, fmt.Errorf("用途路由只支持 clash-meta / sing-box")
	}
}
func clashPurpose(all, candidates []Endpoint, p model.Purpose, template []byte) ([]byte, error) {
	body, err := Clash(all, true, template)
	if err != nil {
		return nil, err
	}
	return addClashPurpose(body, all, candidates, p)
}
func addClashPurpose(body []byte, all, candidates []Endpoint, p model.Purpose) ([]byte, error) {
	var cfg map[string]any
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return nil, err
	}
	groups, _ := cfg["proxy-groups"].([]any)
	used := map[string]struct{}{"DIRECT": {}, "REJECT": {}}
	for _, ep := range all {
		used[ep.Remark] = struct{}{}
	}
	for _, g := range groups {
		if m, ok := g.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				used[n] = struct{}{}
			}
		}
	}
	tag := uniqueTag("mpx-"+p.Slug, "purpose", used)
	names := make([]string, 0, len(candidates))
	for _, ep := range candidates {
		names = append(names, ep.Remark)
	}
	group := map[string]any{"name": tag, "type": p.Strategy, "proxies": names}
	if p.Strategy == "url-test" {
		group["url"] = p.TestURL
		group["interval"] = p.Interval
	}
	cfg["proxy-groups"] = append(groups, group)
	rules, _ := cfg["rules"].([]any)
	if p.Kind == "daily" {
		// Keep template's direct/reject decisions and send its proxy decisions to the daily group.
		for i, r := range rules {
			if v, ok := r.(string); ok {
				rules[i] = clashPurposeRule(v, tag)
			}
		}
	} else {
		prefix := make([]any, 0, len(p.Domains))
		for _, d := range p.Domains {
			prefix = append(prefix, "DOMAIN-SUFFIX,"+d+","+tag)
		}
		rules = append(prefix, rules...)
	}
	cfg["rules"] = rules
	return yaml.Marshal(cfg)
}
func clashPurposeRule(rule, tag string) string {
	// The destination is the last field, except the optional no-resolve suffix.
	parts := strings.Split(rule, ",")
	i := len(parts) - 1
	if i > 0 && parts[i] == "no-resolve" {
		i--
	}
	if i > 0 && parts[i] != "DIRECT" && parts[i] != "REJECT" && parts[i] != "REJECT-DROP" {
		parts[i] = tag
	}
	return strings.Join(parts, ",")
}
func singboxPurpose(all, candidates []Endpoint, p model.Purpose, template []byte) ([]byte, error) {
	body, err := SingBox(all, template)
	if err != nil {
		return nil, err
	}
	return addSingboxPurpose(body, all, candidates, p)
}
func addSingboxPurpose(body []byte, all, candidates []Endpoint, p model.Purpose) ([]byte, error) {
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, err
	}
	outs := toAnySlice(cfg["outbounds"])
	used := map[string]struct{}{}
	for _, o := range outs {
		if m, ok := o.(map[string]any); ok {
			if t, ok := m["tag"].(string); ok {
				used[t] = struct{}{}
			}
		}
	}
	// Reconstruct the existing renderer's tags, including reserved names and WireGuard endpoints.
	nodeUsed := map[string]struct{}{"proxy": {}, "auto": {}, "direct": {}, "block": {}}
	candidateIDs := map[[3]int64]bool{}
	for _, ep := range candidates {
		candidateIDs[[3]int64{ep.NodeID, ep.InboundID, ep.HostID}] = true
	}
	tags := []string{}
	wg := 0
	for _, ep := range all {
		t := ""
		if ep.Protocol == model.ProtocolWireguard {
			t = "wg-" + strconv.Itoa(wg)
			wg++
			used[t] = struct{}{}
		} else {
			t = uniqueTag(ep.Remark, ep.Address, nodeUsed)
		}
		if candidateIDs[[3]int64{ep.NodeID, ep.InboundID, ep.HostID}] {
			tags = append(tags, t)
		}
	}
	groupTag := uniqueTag("mpx-"+p.Slug, "purpose", used)
	group := map[string]any{"tag": groupTag, "type": "selector", "outbounds": tags}
	if p.Strategy == "url-test" {
		// WireGuard endpoints cannot be measured by sing-box urltest. Keep them selectable.
		measured := []string{}
		for _, t := range tags {
			if !isWGTag(t, all) {
				measured = append(measured, t)
			}
		}
		if len(measured) > 0 {
			autoTag := uniqueTag(groupTag+"-auto", "purpose-auto", used)
			outs = append(outs, map[string]any{"tag": autoTag, "type": "urltest", "outbounds": measured, "url": p.TestURL, "interval": strconv.Itoa(p.Interval) + "s"})
			group["outbounds"] = append([]string{autoTag}, tags...)
			group["default"] = autoTag
		}
	}
	cfg["outbounds"] = append(outs, group)
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		cfg["route"] = route
	}
	rules := toAnySlice(route["rules"])
	if p.Kind == "daily" {
		route["final"] = groupTag
		replacePurposeOutbound(rules, groupTag)
	} else {
		rules = append([]any{map[string]any{"domain_suffix": p.Domains, "action": "route", "outbound": groupTag}}, rules...)
	}
	route["rules"] = rules
	return json.MarshalIndent(cfg, "", "  ")
}
func replacePurposeOutbound(v any, tag string) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			replacePurposeOutbound(item, tag)
		}
	case map[string]any:
		if o, ok := x["outbound"].(string); ok && o != "direct" && o != "block" {
			x["outbound"] = tag
		}
		if r, ok := x["rules"]; ok {
			replacePurposeOutbound(r, tag)
		}
	}
}
func isWGTag(tag string, all []Endpoint) bool {
	i := 0
	for _, ep := range all {
		if ep.Protocol == model.ProtocolWireguard {
			if tag == "wg-"+strconv.Itoa(i) {
				return true
			}
			i++
		}
	}
	return false
}
