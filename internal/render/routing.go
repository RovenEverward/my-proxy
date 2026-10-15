package render

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

// 由分流规则生成订阅的分流部分(见 docs/internal/routing-rules-rfc.md)。
//
// 模板只提供与分流无关的段(DNS、嗅探等);Clash 的 rule-providers / proxy-groups /
// rules 与 sing-box 的 route.rule_set / route.rules / route.final 由这里生成并覆盖。

// RoutingGroup 是一个策略组及其在当前用户订阅里的成员。
type RoutingGroup struct {
	model.ProxyGroup
	Members []Endpoint
}

// RoutingPlan 是渲染一个用户订阅需要的全部分流信息:有效规则(用户层在前、已去掉
// 停用的)、兜底去向和全部策略组。
type RoutingPlan struct {
	Rules  []model.RouteRule
	Final  string
	Groups []RoutingGroup
}

// FinalGroupName 是「其他全部」在客户端里的组名。
const FinalGroupName = "其他全部"

// routingNames 给每个去向分配客户端里的组名,避开节点名与模板里留下的名字。
type routingNames struct {
	group map[int64]string // 策略组 id → 组名
	sets  []string         // 第 i 条规则若是规则组,对应的 select 组名
	final string
}

func planNames(p RoutingPlan, used map[string]struct{}) routingNames {
	n := routingNames{group: map[int64]string{}, sets: make([]string, len(p.Rules))}
	for _, g := range p.Groups {
		n.group[g.ID] = uniqueTag(g.Name, "group", used)
	}
	for i, r := range p.Rules {
		if r.Type == model.RuleSetRef {
			rs, _ := model.RuleSetByKey(r.Value)
			n.sets[i] = uniqueTag(rs.Name, r.Value, used)
		}
	}
	n.final = uniqueTag(FinalGroupName, "final", used)
	return n
}

// choices 是「可在客户端里切换」的 select 组的选项:配置的去向排第一(RFC 定案 1),
// 其后是其余策略组与直连;首选是拦截时把拦截放第一、直连第二。
func (n routingNames) choices(p RoutingPlan, first string, direct, reject string) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(n.target(first, direct, reject))
	if first == model.TargetReject {
		add(direct)
	}
	for _, g := range p.Groups {
		add(n.group[g.ID])
	}
	add(direct)
	return out
}

func (n routingNames) target(t, direct, reject string) string {
	switch t {
	case model.TargetDirect:
		return direct
	case model.TargetReject:
		return reject
	}
	if id, ok := model.TargetGroupID(t); ok {
		if name, ok := n.group[id]; ok {
			return name
		}
	}
	return direct // 指向已不存在的组:落到直连,而不是生成坏配置
}

// ---- Clash Meta ----

// ClashRouting 生成 mihomo 订阅:模板注入节点后,用规则表替换分流相关的三段。
func ClashRouting(eps []Endpoint, p RoutingPlan, template []byte) ([]byte, error) {
	body, err := Clash(eps, true, template)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	doc := root.Content[0]
	used := map[string]struct{}{"DIRECT": {}, "REJECT": {}, "REJECT-DROP": {}, "PASS": {}, "GLOBAL": {}}
	for _, ep := range eps {
		used[ep.Remark] = struct{}{}
	}
	names := planNames(p, used)

	groups := []map[string]any{}
	for _, g := range p.Groups {
		members := []string{}
		for _, ep := range g.Members {
			members = append(members, ep.Remark)
		}
		m := map[string]any{"name": names.group[g.ID]}
		switch {
		case len(members) == 0: // RFC 定案 3:没有成员也给一个能用的组
			m["type"], m["proxies"] = "select", []string{"DIRECT"}
		case g.Mode == model.GroupAuto:
			m["type"], m["proxies"], m["url"], m["interval"], m["tolerance"] = "url-test", members, g.TestURL, g.Interval, 50
		default:
			m["type"], m["proxies"] = "select", members
		}
		groups = append(groups, m)
	}
	providers := map[string]any{}
	rules := []string{}
	for i, r := range p.Rules {
		to := names.target(r.Target, "DIRECT", "REJECT")
		switch r.Type {
		case model.RuleSuffix:
			rules = append(rules, "DOMAIN-SUFFIX,"+r.Value+","+to)
		case model.RuleKeyword:
			rules = append(rules, "DOMAIN-KEYWORD,"+r.Value+","+to)
		case model.RuleGeoIP:
			rules = append(rules, "GEOIP,"+r.Value+","+to)
		case model.RuleCIDR:
			kind := "IP-CIDR"
			if pfx, err := netip.ParsePrefix(r.Value); err == nil && pfx.Addr().Is6() {
				kind = "IP-CIDR6"
			}
			rules = append(rules, kind+","+r.Value+","+to+",no-resolve")
		case model.RuleSetRef:
			rs, ok := model.RuleSetByKey(r.Value)
			if !ok {
				continue
			}
			groups = append(groups, map[string]any{"name": names.sets[i], "type": "select", "proxies": names.choices(p, r.Target, "DIRECT", "REJECT")})
			for _, c := range rs.Clash {
				providers[c.Name] = map[string]any{"type": "http", "behavior": c.Behavior, "format": "yaml", "interval": 86400, "url": c.URL, "path": "./ruleset/" + c.Name + ".yaml"}
				line := "RULE-SET," + c.Name + "," + names.sets[i]
				if c.NoResolve {
					line += ",no-resolve"
				}
				rules = append(rules, line)
			}
		}
	}
	groups = append(groups, map[string]any{"name": names.final, "type": "select", "proxies": names.choices(p, p.Final, "DIRECT", "REJECT")})
	rules = append(rules, "MATCH,"+names.final)
	for k, v := range map[string]any{"rule-providers": providers, "proxy-groups": groups, "rules": rules} {
		if err := setYAMLKey(doc, k, v); err != nil {
			return nil, err
		}
	}
	return yaml.Marshal(&root)
}

func setYAMLKey(doc *yaml.Node, key string, v any) error {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return err
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == key {
			doc.Content[i+1] = &n
			return nil
		}
	}
	doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
	return nil
}

// ---- sing-box ----

// SingBoxRouting 生成 sing-box 订阅:模板注入节点后,替换 route 的规则集、规则与兜底,
// 去掉模板里的分类选择器(它们只被旧规则引用),保留 proxy/auto/direct/block 与节点。
func SingBoxRouting(eps []Endpoint, p RoutingPlan, template []byte) ([]byte, error) {
	body, err := SingBox(eps, template)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, err
	}
	// 和 SingBox 一样重建节点 tag(含保留名与 WireGuard 端点)。
	nodeUsed := map[string]struct{}{"proxy": {}, "auto": {}, "direct": {}, "block": {}}
	tagOf := map[[3]int64]string{}
	wgTag := map[string]bool{}
	wg := 0
	for _, ep := range eps {
		var t string
		if ep.Protocol == model.ProtocolWireguard {
			t = "wg-" + strconv.Itoa(wg)
			wg++
			wgTag[t] = true
		} else {
			t = uniqueTag(ep.Remark, ep.Address, nodeUsed)
		}
		tagOf[[3]int64{ep.NodeID, ep.InboundID, ep.HostID}] = t
	}
	keep := []any{}
	used := map[string]struct{}{}
	for _, o := range toAnySlice(cfg["outbounds"]) {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		typ, _ := m["type"].(string)
		if (typ == "selector" || typ == "urltest") && tag != singboxProxyTag && tag != "auto" {
			continue
		}
		keep = append(keep, m)
		used[tag] = struct{}{}
	}
	for t := range wgTag {
		used[t] = struct{}{}
	}
	names := planNames(p, used)
	for _, g := range p.Groups {
		tags, measured := []string{}, []string{}
		for _, ep := range g.Members {
			t := tagOf[[3]int64{ep.NodeID, ep.InboundID, ep.HostID}]
			tags = append(tags, t)
			if !wgTag[t] {
				measured = append(measured, t)
			}
		}
		name := names.group[g.ID]
		switch {
		case len(tags) == 0:
			keep = append(keep, map[string]any{"tag": name, "type": "selector", "outbounds": []string{"direct"}})
		case g.Mode == model.GroupAuto && len(measured) == len(tags):
			keep = append(keep, map[string]any{"tag": name, "type": "urltest", "outbounds": tags, "url": g.TestURL, "interval": strconv.Itoa(g.Interval) + "s", "tolerance": 50})
		case g.Mode == model.GroupAuto && len(measured) > 0:
			// urltest 测不了 WireGuard 端点:测得的放进内层 urltest,WG 留在外层可手选。
			inner := uniqueTag(name+"-auto", "auto", used)
			keep = append(keep, map[string]any{"tag": inner, "type": "urltest", "outbounds": measured, "url": g.TestURL, "interval": strconv.Itoa(g.Interval) + "s", "tolerance": 50})
			keep = append(keep, map[string]any{"tag": name, "type": "selector", "outbounds": append([]string{inner}, tags...), "default": inner})
		default:
			keep = append(keep, map[string]any{"tag": name, "type": "selector", "outbounds": tags})
		}
	}
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		cfg["route"] = route
	}
	// 模板开头连续的 sniff / hijack-dns / resolve 等纯动作规则要留在最前(见 purpose-validation.md)。
	rules := []any{}
	for _, r := range toAnySlice(route["rules"]) {
		m, ok := r.(map[string]any)
		if !ok {
			break
		}
		action, _ := m["action"].(string)
		if _, routed := m["outbound"]; routed || (action != "sniff" && action != "hijack-dns" && action != "resolve") {
			break
		}
		rules = append(rules, m)
	}
	sets := []any{}
	setSeen := map[string]bool{}
	addSet := func(tag, url string) {
		if !setSeen[tag] {
			setSeen[tag] = true
			sets = append(sets, map[string]any{"type": "remote", "tag": tag, "format": "binary", "url": url, "download_detour": "direct"})
		}
	}
	routeTo := func(rule map[string]any, t, outbound string) map[string]any {
		if t == model.TargetReject {
			rule["action"] = "reject"
		} else {
			rule["outbound"] = outbound
		}
		return rule
	}
	for i, r := range p.Rules {
		to := names.target(r.Target, "direct", "block")
		switch r.Type {
		case model.RuleSuffix:
			rules = append(rules, routeTo(map[string]any{"domain_suffix": []string{r.Value}}, r.Target, to))
		case model.RuleKeyword:
			rules = append(rules, routeTo(map[string]any{"domain_keyword": []string{r.Value}}, r.Target, to))
		case model.RuleCIDR:
			rules = append(rules, routeTo(map[string]any{"ip_cidr": []string{r.Value}}, r.Target, to))
		case model.RuleGeoIP:
			tag := "geoip-" + strings.ToLower(r.Value)
			addSet(tag, "https://cdn.jsdelivr.net/gh/SagerNet/sing-geoip@rule-set/"+tag+".srs")
			rules = append(rules, routeTo(map[string]any{"rule_set": []string{tag}}, r.Target, to))
		case model.RuleSetRef:
			rs, ok := model.RuleSetByKey(r.Value)
			if !ok {
				continue
			}
			keep = append(keep, map[string]any{"tag": names.sets[i], "type": "selector", "outbounds": names.choices(p, r.Target, "direct", "block")})
			if rs.PrivateIP {
				rules = append(rules, map[string]any{"ip_is_private": true, "outbound": names.sets[i]})
			}
			tags := []string{}
			for _, s := range rs.SingBox {
				addSet(s.Tag, s.URL)
				tags = append(tags, s.Tag)
			}
			rules = append(rules, map[string]any{"rule_set": tags, "outbound": names.sets[i]})
		}
	}
	keep = append(keep, map[string]any{"tag": names.final, "type": "selector", "outbounds": names.choices(p, p.Final, "direct", "block")})
	cfg["outbounds"] = keep
	route["rules"] = rules
	route["rule_set"] = sets
	route["final"] = names.final
	return json.MarshalIndent(cfg, "", "  ")
}

// RoutingSummary 是给日志与错误用的简短描述。
func (p RoutingPlan) String() string {
	return fmt.Sprintf("%d 条规则 · %d 个策略组 · 兜底 %s", len(p.Rules), len(p.Groups), p.Final)
}
