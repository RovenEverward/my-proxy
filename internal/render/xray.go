// xray.go 把端点渲染成 xray 客户端配置。
//
// 与 sing-box 不同,xray 的客户端习惯是「一个节点一份完整配置」:用户在客户端里
// 导入的是一个配置列表,每份配置自带 inbounds(socks/http 本地入口)、dns、routing
// 和一条指向该节点的 outbound。所以这里的产物是一个 JSON 数组,数组里每一项都是
// 一份可以独立启用的配置。
//
// 底稿(全局模板)提供除节点出口以外的全部内容;本文件只负责把端点变成 outbound,
// 并用 Remarks 标出它是哪个节点。
package render

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// xrayDefaultTemplate 是没给底稿时用的内置客户端配置。
//
// 它经 go:embed 编进二进制,运行时不需要任何外部文件。
//
//go:embed templates/xray-default.json
var xrayDefaultTemplate []byte

// Xray 渲染 xray 客户端配置列表。
//
// templateJSON 是底稿(通常是控制面里的全局模板);为 nil 或空时用内置默认。
// 返回的是一份 JSON 数组,每个可用入口一项。
func Xray(eps []Endpoint, templateJSON []byte) ([]byte, error) {
	raw := templateJSON
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = xrayDefaultTemplate
	}

	var tpl map[string]any
	if err := json.Unmarshal(raw, &tpl); err != nil {
		return nil, fmt.Errorf("xray: 解析模板: %w", err)
	}
	if tpl == nil {
		tpl = map[string]any{}
	}
	cleanJSON(tpl)

	// 底稿里已有的出口:direct / block 之类。节点出口排在它们前面,client 默认走
	// 第一条,所以顺序不能反。
	base, err := xrayTemplateOutbounds(tpl["outbounds"])
	if err != nil {
		return nil, err
	}

	used := make(map[string]struct{}, len(eps)+len(base))
	for _, name := range base {
		used[name] = struct{}{}
	}

	configs := make([]any, 0, len(eps))
	for _, ep := range eps {
		tag := uniqueTag(ep.Remark, ep.Address, used)
		node, err := xrayNode(ep, tag)
		if err != nil {
			return nil, err
		}
		cfg := make(map[string]any, len(tpl)+2)
		for k, v := range tpl {
			if k == "outbounds" {
				continue
			}
			cfg[k] = v
		}
		cfg["remarks"] = tag
		outbounds := append([]any{node}, tpl["outbounds"].([]any)...)
		if ep.Fragment {
			outbounds = applyFragment(outbounds, tag)
		}
		cfg["outbounds"] = outbounds
		configs = append(configs, cfg)
	}

	out, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("xray: 序列化配置: %w", err)
	}
	return out, nil
}

// applyFragment 给节点出站挂上分片拨号链。
//
// xray 的分片用「出站链」做:代理出站(VLESS 等)的 sockopt.dialerProxy 指向
// 一条带 fragment settings 的 freedom 出站,底层 TCP 由那条 freedom 发出,
// ClientHello 在它拨号时被切片。所以这里做两件事:
//  1. 新增一条 tag 为 "<proxy-tag>-fragment" 的 freedom 出站,带切片参数;
//  2. 给节点出站(nodeTag,即 outbounds[0])挂 sockopt.dialerProxy 指向它。
//
// dialerProxy 在运行时才解析,`xray -test` 不会校验引用的 tag 是否存在;
// 指向不存在的 tag 只在拨号时报错。所以必须同时加链尾,不能只挂引用。
func applyFragment(outbounds []any, nodeTag string) []any {
	fragTag := nodeTag + "-fragment"
	// 模板里也许已经有这条链尾,有就不再加(用户手工配的分片链优先)。
	found := false
	for _, o := range outbounds {
		obj, ok := o.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := obj["tag"].(string); t == fragTag {
			found = true
			break
		}
	}
	if !found {
		outbounds = append(outbounds, map[string]any{
			"tag":      fragTag,
			"protocol": "freedom",
			"settings": map[string]any{
				"fragment": map[string]any{
					"packets":  "tlshello",
					"length":   "50-100",
					"interval": "10-20",
				},
			},
		})
	}

	// 给节点出站挂 sockopt.dialerProxy,指向上面那条 fragment 出站。
	node, ok := outbounds[0].(map[string]any)
	if !ok {
		return outbounds
	}
	sockopt, _ := node["sockopt"].(map[string]any)
	if sockopt == nil {
		sockopt = map[string]any{}
	}
	sockopt["dialerProxy"] = fragTag
	node["sockopt"] = sockopt
	return outbounds
}

// xrayTemplateOutbounds 取出底稿里的出口并返回它们占用的 tag。
func xrayTemplateOutbounds(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("xray: 模板的 outbounds 不是数组")
	}
	tags := make([]string, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if tag, _ := obj["tag"].(string); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

// xrayNode 把一个端点渲染成 xray 的 outbound。
func xrayNode(ep Endpoint, tag string) (map[string]any, error) {
	if !ep.Protocol.Valid() {
		return nil, fmt.Errorf("xray: 端点 %q 的协议 %q 不受支持", tag, ep.Protocol)
	}
	if ep.Network != "" && !ep.Network.Valid() {
		return nil, fmt.Errorf("xray: 端点 %q 的传输 %q 不受支持", tag, ep.Network)
	}
	if ep.Security != "" && !ep.Security.Valid() {
		return nil, fmt.Errorf("xray: 端点 %q 的安全层 %q 不受支持", tag, ep.Security)
	}

	out := map[string]any{"tag": tag, "protocol": string(ep.Protocol)}

	server := map[string]any{"address": ep.Address, "port": ep.Port}
	switch ep.Protocol {
	case model.ProtocolVLESS:
		user := map[string]any{"id": ep.UUID, "encryption": "none"}
		// flow 只在 tls / reality 下有意义;写在 none 的入站上 xray 会直接报错。
		if flow := xrayFlow(ep); flow != "" {
			user["flow"] = flow
		}
		server["users"] = []any{user}
		out["settings"] = map[string]any{"vnext": []any{server}}
	case model.ProtocolVMess:
		server["users"] = []any{map[string]any{"id": ep.UUID, "security": "auto"}}
		out["settings"] = map[string]any{"vnext": []any{server}}
	case model.ProtocolTrojan:
		server["password"] = ep.Password
		out["settings"] = map[string]any{"servers": []any{server}}
	case model.ProtocolShadowsocks:
		server["method"] = "2022-blake3-aes-128-gcm"
		server["password"] = ep.Password
		out["settings"] = map[string]any{"servers": []any{server}}
	case model.ProtocolWireguard:
		// wireguard 是 L3:outbound 不走 vnext / servers,也没有 streamSettings。
		if ep.WGPrivateKey == "" || ep.WGPublicKey == "" || ep.WGAddress == "" {
			return nil, fmt.Errorf("xray: 端点 %q 的 wireguard 缺私钥/公钥/隧道地址", tag)
		}
		out["settings"] = map[string]any{
			"secretKey": ep.WGPrivateKey,
			"address":   []string{ep.WGAddress},
			"peers": []any{map[string]any{
				"publicKey": ep.WGPublicKey,
				"endpoint":  fmt.Sprintf("%s:%d", ep.Address, ep.Port),
				"keepAlive": 25,
			}},
		}
		return out, nil
	default:
		return nil, fmt.Errorf("xray: 端点 %q 的协议 %q 不受支持", tag, ep.Protocol)
	}

	out["streamSettings"] = xrayStreamSettings(ep)

	// mux 只在出站层:xray 的 mux 是 outbound.mux,与传输无关。
	if ep.Mux {
		out["mux"] = map[string]any{"enabled": true, "concurrency": 8}
	}
	return out, nil
}

// xrayFlow 只在有 TLS 层时返回 flow,否则一律为空。
func xrayFlow(ep Endpoint) string {
	if ep.Security != model.SecurityTLS && ep.Security != model.SecurityReality {
		return ""
	}
	return ep.Flow
}

// xrayStreamSettings 生成传输层与安全层配置。
func xrayStreamSettings(ep Endpoint) map[string]any {
	network := ep.Network
	if network == "" {
		network = model.NetworkTCP
	}
	stream := map[string]any{"network": string(network)}

	switch network {
	case model.NetworkWS:
		ws := map[string]any{}
		if ep.Path != "" {
			ws["path"] = ep.Path
		}
		// xray 的 wsSettings 读的是顶层 host 字段(header 里的 Host 由它转写)。
		if ep.Host != "" {
			ws["host"] = ep.Host
		}
		stream["wsSettings"] = ws
	case model.NetworkGRPC:
		grpc := map[string]any{}
		if ep.Path != "" {
			grpc["serviceName"] = ep.Path
		}
		if ep.Host != "" {
			grpc["authority"] = ep.Host
		}
		stream["grpcSettings"] = grpc
	case model.NetworkTCP:
		stream["tcpSettings"] = map[string]any{}
	case model.NetworkXHTTP, model.NetworkSplitHTTP, model.NetworkHTTPUpgrade:
		// 三个传输共一个字段集;键名各不同,由调用方按 transport 写。
		http := map[string]any{}
		if ep.Path != "" {
			http["path"] = ep.Path
		}
		if ep.Host != "" {
			http["host"] = ep.Host
		}
		if ep.Mode != "" && ep.Mode != "auto" {
			http["mode"] = ep.Mode
		}
		key := string(network) + "Settings"
		stream[key] = http
	}

	switch ep.Security {
	case model.SecurityTLS:
		tls := map[string]any{"serverName": ep.SNI, "allowInsecure": ep.AllowInsecure}
		if len(ep.ALPN) > 0 {
			tls["alpn"] = ep.ALPN
		}
		if ep.Fingerprint != "" {
			tls["fingerprint"] = ep.Fingerprint
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = tls
	case model.SecurityReality:
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"serverName":  ep.SNI,
			"fingerprint": ep.Fingerprint,
			"show":        false,
			"publicKey":   ep.RealityPub,
			"shortId":     ep.RealitySID,
			"spiderX":     "",
		}
	default:
		stream["security"] = "none"
	}
	return stream
}
