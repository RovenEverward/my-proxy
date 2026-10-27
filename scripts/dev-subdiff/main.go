// dev-subdiff 只向调用者返回字节差异是否完全属于已批准的默认订阅变更。
package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

func field(n *yaml.Node, key string) *yaml.Node {
	if n.Kind == yaml.DocumentNode {
		return field(n.Content[0], key)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func equal(a, b any) bool { return reflect.DeepEqual(a, b) }
func decode(n *yaml.Node) any {
	var v any
	if n == nil {
		return nil
	}
	_ = n.Decode(&v)
	return v
}
func domestic(v any) bool {
	return equal(v, []any{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"})
}
func copyComments(a, b *yaml.Node) {
	a.HeadComment = b.HeadComment
	a.LineComment = b.LineComment
	a.FootComment = b.FootComment
	if a.Kind == yaml.MappingNode && b.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(a.Content); i += 2 {
			for j := 0; j+1 < len(b.Content); j += 2 {
				if a.Content[i].Value == b.Content[j].Value {
					copyComments(a.Content[i], b.Content[j])
					copyComments(a.Content[i+1], b.Content[j+1])
				}
			}
		}
	} else if len(a.Content) == len(b.Content) {
		for i := range a.Content {
			copyComments(a.Content[i], b.Content[i])
		}
	}
}

func clash(before, after []byte) bool {
	var a, b yaml.Node
	if yaml.Unmarshal(before, &a) != nil || yaml.Unmarshal(after, &b) != nil {
		return false
	}
	// 只接受同一个编码器能够逐字节还原的输出，避免掩盖格式变化。
	round, _ := yaml.Marshal(&a)
	if !bytes.Equal(round, before) {
		return false
	}

	// DNS 的代理引用必须实际出现在最终订阅中。
	names := map[string]bool{}
	if groups := field(&b, "proxy-groups"); groups != nil {
		for _, g := range groups.Content {
			if n := field(g, "name"); n != nil {
				names[n.Value] = true
			}
		}
	}
	if dns := field(&b, "dns"); dns != nil {
		if servers := field(dns, "nameserver"); servers != nil {
			for _, server := range servers.Content {
				u, e := url.Parse(server.Value)
				if e != nil || (u.Fragment != "" && !names[u.Fragment]) {
					return false
				}
			}
		}
	}
	ad, bd := field(&a, "dns"), field(&b, "dns")
	if ad != nil && bd != nil {
		ns, ps := field(ad, "nameserver"), field(ad, "proxy-server-nameserver")
		bn, bp := field(bd, "nameserver"), field(bd, "proxy-server-nameserver")
		want := []any{"https://1.1.1.1/dns-query#手动切换", "https://8.8.8.8/dns-query#手动切换"}
		if (domestic(decode(ns)) || equal(decode(ns), []any{"https://1.1.1.1/dns-query#自动选择", "https://8.8.8.8/dns-query#自动选择"})) && equal(decode(bn), want) && ns != nil {
			*ns = *bn
		}
		if ps != nil && domestic(decode(bp)) {
			old := decode(ps)
			if equal(old, []any{"119.29.29.29", "223.5.5.5"}) || domestic(old) {
				*ps = *bp
			}
		}
		policy := field(ad, "nameserver-policy")
		if policy != nil {
			for i := 0; i+1 < len(policy.Content); i += 2 {
				if policy.Content[i].Value == "+.linux.do" && policy.Content[i+1].Value == "https://xxx.ddd.oaifree.com/query-dns" {
					policy.Content = append(policy.Content[:i], policy.Content[i+2:]...)
					break
				}
			}
		}
		// 整段 DNS 注释随已批准的模板调整；值仍由上面的精确规则约束。
		copyComments(ad, bd)

	}
	ag, bg := field(&a, "proxy-groups"), field(&b, "proxy-groups")
	if ag != nil && bg != nil && len(ag.Content) == len(bg.Content) {
		for i, g := range ag.Content {
			h := bg.Content[i]
			if !equal(decode(field(g, "name")), decode(field(h, "name"))) || decode(field(g, "type")) != "select" {
				continue
			}
			x, y := field(g, "proxies"), field(h, "proxies")
			var old, new []string
			if x == nil || y == nil || x.Decode(&old) != nil || y.Decode(&new) != nil || len(old) != len(new) || len(old) < 2 {
				continue
			}
			if old[0] == "自动选择" && new[0] == "手动切换" {
				expected := append([]string(nil), old...)
				for j, v := range expected {
					if v == "手动切换" {
						expected = append([]string{v}, append(expected[:j], expected[j+1:]...)...)
						break
					}
				}
				if equal(expected, new) {
					*x = *y
				}
			}
		}
	}
	result, _ := yaml.Marshal(&a)
	return bytes.Equal(result, after)
}
func singbox(before, after []byte) bool {
	var a, b map[string]any
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil {
		return false
	}
	round, _ := json.MarshalIndent(a, "", "  ")
	if !bytes.Equal(round, before) {
		return false
	}
	ao, _ := a["outbounds"].([]any)
	bo, _ := b["outbounds"].([]any)
	if len(ao) == len(bo) {
		for i, o := range ao {
			x, ok := o.(map[string]any)
			y, yes := bo[i].(map[string]any)
			if !ok || !yes || x["tag"] != "proxy" || y["tag"] != "proxy" || x["type"] != "selector" || y["type"] != "selector" {
				continue
			}
			old, _ := x["outbounds"].([]any)
			new, _ := y["outbounds"].([]any)
			if len(old) > 1 && old[0] == "auto" && equal(append(append([]any{}, old[1:]...), "auto"), new) && x["default"] == "auto" && y["default"] == nil {
				x["outbounds"] = new
				delete(x, "default")
			}
		}
	}
	ar, _ := a["route"].(map[string]any)
	br, _ := b["route"].(map[string]any)
	if equal(ar, br) && ar != nil {
		ad, _ := a["dns"].(map[string]any)
		bd, _ := b["dns"].(map[string]any)
		as, _ := ad["servers"].([]any)
		bs, _ := bd["servers"].([]any)
		if len(as) == len(bs) {
			for i, s := range as {
				x, ok := s.(map[string]any)
				y, yes := bs[i].(map[string]any)
				if ok && yes && x["detour"] == "proxy" && y["detour"] == ar["final"] {
					x["detour"] = y["detour"]
				}
			}
		}
	}
	result, _ := json.MarshalIndent(a, "", "  ")
	return bytes.Equal(result, after)
}
func main() {
	if len(os.Args) != 4 {
		os.Exit(2)
	}
	a, e := os.ReadFile(os.Args[2])
	if e != nil {
		os.Exit(2)
	}
	b, e := os.ReadFile(os.Args[3])
	if e != nil {
		os.Exit(2)
	}
	approved := bytes.Equal(a, b)
	if !approved {
		switch strings.TrimSpace(os.Args[1]) {
		case "clash-meta":
			approved = clash(a, b)
		case "sing-box":
			approved = singbox(a, b)
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]bool{"approved": approved})
}
