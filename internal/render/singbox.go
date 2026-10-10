// singbox.go 把端点渲染成 sing-box 客户端配置。
//
// sing-box 的配置里,节点只是 outbounds 的一部分,其余(log / dns / inbounds /
// route / experimental)随用户环境差别很大,所以这里不自己拼一份完整配置,而是
// 在底稿上注入 outbounds:底稿通常是控制面里的全局模板,本文件只负责把端点变成
// outbound,并把 proxy / auto / direct / block 这四个固定 tag 换成与本次节点匹配
// 的版本,再补一个可选的 route.final。
package render

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

const (
	// singboxProxyTag 是给用户在客户端里手动切节点用的 selector。
	singboxProxyTag = "proxy"
	// singboxAutoTag 是按延迟自动选路的 urltest;selector 默认指向它。
	singboxAutoTag = "auto"
	// singboxDirectTag 与 singboxBlockTag 是路由规则按 tag 引用的直连与拦截出口。
	singboxDirectTag = "direct"
	singboxBlockTag  = "block"

	// singboxTestURL 与 singboxTestInterval 是 urltest 探活用的地址与间隔。
	singboxTestURL      = "http://www.gstatic.com/generate_204"
	singboxTestInterval = "5m"
)

// singboxDefaultTemplate 是没给底稿时用的内置客户端配置。
//
// 它经 go:embed 编进二进制,运行时不需要任何外部文件;里面的
// proxy / auto / direct / block 只是占位,渲染时会被替换掉。
//
//go:embed templates/singbox-default.json
var singboxDefaultTemplate []byte

// SingBox 渲染 sing-box 客户端配置。
//
// templateJSON 是底稿(通常是控制面里的全局模板);为 nil 或空时用内置默认。
// 端点作为 outbounds 注入底稿,底稿里已有的同名 outbounds 会被替换。
func SingBox(eps []Endpoint, templateJSON []byte) ([]byte, error) {
	raw := templateJSON
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = singboxDefaultTemplate
	}

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("sing-box: 解析模板: %w", err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	cleanJSON(cfg)

	// 先渲染节点:重名 tag 在这里就已经消歧,后面的 selector / urltest 直接用结果。
	// wireguard 端点不走 outbound(1.13 起单独顶层段),先拆出来。
	var wgEps []Endpoint
	var proxyEps []Endpoint
	for _, ep := range eps {
		if ep.Protocol == model.ProtocolWireguard {
			wgEps = append(wgEps, ep)
		} else {
			proxyEps = append(proxyEps, ep)
		}
	}

	nodes, tags, err := singboxNodes(proxyEps)
	if err != nil {
		return nil, err
	}

	outbounds, err := singboxOutbounds(cfg["outbounds"], nodes, tags)
	if err != nil {
		return nil, err
	}
	cfg["outbounds"] = outbounds

	// wireguard 是 endpoint 不是 outbound:sing-box 1.13 删掉了 outbound 形态,
	// 让 endpoint 通过路由规则引流。端点之间靠 tag 区分,不与 outbound tag 冲突。
	// 每个 wireguard endpoint 进 selector 的候选,让用户能手动选;不进 urltest
	// (endpoint 不参与 outbound 测速)。
	wgTags := make([]any, 0, len(wgEps))
	for i, ep := range wgEps {
		epTag := fmt.Sprintf("wg-%d", i)
		cfg["endpoints"] = append([]any{singboxWireguardEndpoint(ep, epTag)},
			toAnySlice(cfg["endpoints"])...)
		wgTags = append(wgTags, epTag)
	}
	// wireguard endpoint 也要能被 selector 选中:把 tag 混进 proxy 的候选列表。
	// endpoint 不在 outbound 数组里,但 selector 的候选可以指到 endpoint。
	if outs, ok := cfg["outbounds"].([]any); ok {
		for _, o := range outs {
			if m, ok := o.(map[string]any); ok && m["tag"] == singboxProxyTag {
				if cands, ok := m["outbounds"].([]any); ok {
					m["outbounds"] = append(cands, wgTags...)
				}
			}
		}
	}

	if err := singboxEnsureFinal(cfg); err != nil {
		return nil, err
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sing-box: 序列化配置: %w", err)
	}
	return out, nil
}

// cleanJSON 就地清掉 json.Marshal 不认识的类型,把 []byte 转成字符串。
//
// 底稿来自 JSON 解码,正常不会出现这类值;这里只做一层防御,免得以后换来源时
// 整个渲染要拖到序列化那一步才报错。返回清理后的值,便于处理数组元素。
func cleanJSON(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case map[string]any:
		for k, e := range t {
			t[k] = cleanJSON(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = cleanJSON(e)
		}
		return t
	default:
		return v
	}
}

// singboxNodes 渲染全部节点 outbound,并返回它们的 tag(与 outbound 一一对应)。
func singboxNodes(eps []Endpoint) ([]any, []string, error) {
	tags := make([]string, 0, len(eps))
	out := make([]any, 0, len(eps))
	used := make(map[string]struct{}, len(eps)+4)
	used[singboxProxyTag] = struct{}{}
	used[singboxAutoTag] = struct{}{}
	used[singboxDirectTag] = struct{}{}
	used[singboxBlockTag] = struct{}{}

	for _, ep := range eps {
		tag := uniqueTag(ep.Remark, ep.Address, used)
		node, err := singboxNode(ep, tag)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, node)
		tags = append(tags, tag)
	}
	return out, tags, nil
}

// singboxNode 把一个端点渲染成 outbound。
func singboxNode(ep Endpoint, tag string) (map[string]any, error) {
	if ep.Protocol.QUIC() {
		return singboxQUICNode(ep, tag)
	}
	// wireguard 不走 outbound(1.13 起是顶层 endpoints),这里只拦漏网的。
	if ep.Protocol == model.ProtocolWireguard {
		return nil, fmt.Errorf("sing-box: 端点 %q 的 wireguard 协议应走 endpoint 而非 outbound", tag)
	}
	if !ep.Protocol.Valid() {
		return nil, fmt.Errorf("sing-box: 端点 %q 的协议 %q 不受支持", tag, ep.Protocol)
	}
	if ep.Network != "" && !ep.Network.Valid() {
		return nil, fmt.Errorf("sing-box: 端点 %q 的传输 %q 不受支持", tag, ep.Network)
	}
	if ep.Security != "" && !ep.Security.Valid() {
		return nil, fmt.Errorf("sing-box: 端点 %q 的安全层 %q 不受支持", tag, ep.Security)
	}

	node := map[string]any{
		"type":        string(ep.Protocol),
		"tag":         tag,
		"server":      ep.Address,
		"server_port": ep.Port,
	}

	switch ep.Protocol {
	case model.ProtocolVLESS:
		node["uuid"] = ep.UUID
		if ep.Flow != "" {
			node["flow"] = ep.Flow
		}
	case model.ProtocolVMess:
		node["uuid"] = ep.UUID
		node["security"] = "auto"
		node["alter_id"] = 0
	case model.ProtocolTrojan:
		node["password"] = ep.Password
	case model.ProtocolShadowsocks:
		node["method"] = "2022-blake3-aes-128-gcm"
		node["password"] = ep.Password
	}

	if tls, ok := singboxTLS(ep); ok {
		node["tls"] = tls
	}
	// sing-box 的 shadowsocks 没有 transport 字段:ss-2022 只走裸 TCP,
	// 别的传输形态在服务端本来就很少见,这里直接报错,免得订阅里
	// 出一条连不上的节点。
	if ep.Protocol == model.ProtocolShadowsocks && ep.Network != "" && ep.Network != model.NetworkTCP {
		return nil, fmt.Errorf("sing-box: 端点 %q 的 shadowsocks 不支持传输 %q", tag, ep.Network)
	}
	if tr := singboxTransport(ep); tr != nil {
		node["transport"] = tr
	}

	// mux 是 outbound.multiplex:sing-box 的 mux 写在这一层,不在 stream 层。
	if ep.Mux {
		node["multiplex"] = map[string]any{"enabled": true, "protocol": "smux", "max_connections": 4}
	}
	return node, nil
}

// singboxWireguardEndpoint 把 wireguard 端点渲染成 sing-box 的 endpoints[] 项。
//
// sing-box 1.13 删掉了 wireguard outbound,用 endpoint 取代;endpoint 的引流靠
// route.final 指向 endpoint tag,而不是 outbound 列表。
func singboxWireguardEndpoint(ep Endpoint, tag string) map[string]any {
	// xray 侧的密钥是无填充 RawURL base64,sing-box 只认带填充的标准 base64。
	return map[string]any{
		"type":        "wireguard",
		"tag":         tag,
		"mtu":         1420,
		"address":     []string{ep.WGAddress},
		"private_key": paddedStdB64(ep.WGPrivateKey),
		"peers": []any{map[string]any{
			"address":     ep.Address,
			"port":        ep.Port,
			"public_key":  paddedStdB64(ep.WGPublicKey),
			"allowed_ips": []string{"0.0.0.0/0", "::/0"},
		}},
	}
}

// toAnySlice 把 any 转 []any,失败时返回空切片。
func toAnySlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// singboxTLS 生成端点的 tls 块,不需要时返回 false。
//
// trojan 一律要 tls:即便底层标着 none,客户端不握手也连不上。
func singboxTLS(ep Endpoint) (map[string]any, bool) {
	if ep.Security == model.SecurityNone || ep.Security == "" {
		if ep.Protocol != model.ProtocolTrojan {
			return nil, false
		}
	}

	tls := map[string]any{
		"enabled":     true,
		"server_name": ep.SNI,
	}
	if len(ep.ALPN) > 0 {
		tls["alpn"] = ep.ALPN
	}
	// sing-box 要求 reality 必须带 utls;没设指纹时给 chrome,与 xray 侧的默认一致。
	fp := ep.Fingerprint
	if ep.Security == model.SecurityReality && fp == "" {
		fp = "chrome"
	}
	if fp != "" {
		tls["utls"] = map[string]any{
			"enabled":     true,
			"fingerprint": fp,
		}
	}
	if ep.Security == model.SecurityReality {
		tls["reality"] = map[string]any{
			"enabled":    true,
			"public_key": ep.RealityPub,
			"short_id":   ep.RealitySID,
		}
	}
	return tls, true
}

// singboxTransport 生成端点的 transport 块,tcp 返回 nil。
func singboxTransport(ep Endpoint) map[string]any {
	switch ep.Network {
	case model.NetworkWS:
		tr := map[string]any{"type": "ws", "path": ep.Path}
		if ep.Host != "" {
			tr["headers"] = map[string]any{"Host": ep.Host}
		}
		return tr
	case model.NetworkGRPC:
		return map[string]any{"type": "grpc", "service_name": ep.Path}
	case model.NetworkXHTTP, model.NetworkHTTPUpgrade:
		// sing-box 没有独立的 xhttp/httpupgrade 类型:都走 type=http 加 path/host。
		// 上游的 splithttp 同样没有对应类型,这里不映射——让订阅方在
		// sing-box 上看不到该节点,比悄悄降级成 plain http 好。
		tr := map[string]any{"type": "http"}
		if ep.Path != "" {
			tr["path"] = ep.Path
		}
		if ep.Host != "" {
			tr["host"] = ep.Host
		}
		return tr
	default:
		return nil
	}
}

// singboxOutbounds 拼出最终的 outbounds:selector、urltest、全部节点、direct、
// block,再接上底稿里其余的 outbound(如 dns-out)。
//
// 底稿里 tag 与生成结果相同的项被丢弃,由生成结果顶替,因此不会有重复 tag。
func singboxOutbounds(prev any, nodes []any, tags []string) ([]any, error) {
	nodeTags := make([]any, 0, len(tags))
	for _, tag := range tags {
		nodeTags = append(nodeTags, tag)
	}
	proxyOutbounds := make([]any, 0, len(tags)+1)
	proxyOutbounds = append(proxyOutbounds, singboxAutoTag)
	proxyOutbounds = append(proxyOutbounds, nodeTags...)

	out := make([]any, 0, len(nodes)+6)
	out = append(out,
		map[string]any{
			"type":      "selector",
			"tag":       singboxProxyTag,
			"outbounds": proxyOutbounds,
			"default":   singboxAutoTag,
		},
		map[string]any{
			"type":      "urltest",
			"tag":       singboxAutoTag,
			"outbounds": nodeTags,
			"url":       singboxTestURL,
			"interval":  singboxTestInterval,
		},
	)
	out = append(out, nodes...)
	out = append(out,
		map[string]any{"type": "direct", "tag": singboxDirectTag},
		map[string]any{"type": "block", "tag": singboxBlockTag},
	)

	// 已经用掉的 tag:四个固定 tag 加全部节点。
	seen := map[string]struct{}{
		singboxProxyTag:  {},
		singboxAutoTag:   {},
		singboxDirectTag: {},
		singboxBlockTag:  {},
	}
	for _, tag := range tags {
		seen[tag] = struct{}{}
	}

	switch prev := prev.(type) {
	case nil:
	case []any:
		for _, item := range prev {
			item, ok := item.(map[string]any)
			if !ok {
				continue
			}
			tag, _ := item["tag"].(string)
			if _, dup := seen[tag]; dup {
				continue
			}
			seen[tag] = struct{}{}
			out = append(out, item)
		}
	default:
		return nil, fmt.Errorf("sing-box: 模板的 outbounds 不是数组")
	}
	return out, nil
}

// singboxEnsureFinal 保证 route.final 指向存在的出口。
//
// 模板没写 final(或写成了空串)时补成 proxy,否则 sing-box 会因为没有默认出口
// 而拒绝启动。
func singboxEnsureFinal(cfg map[string]any) error {
	switch route := cfg["route"].(type) {
	case nil:
		cfg["route"] = map[string]any{"final": singboxProxyTag}
	case map[string]any:
		if final, _ := route["final"].(string); final == "" {
			route["final"] = singboxProxyTag
		}
	default:
		return fmt.Errorf("sing-box: 模板的 route 不是对象")
	}
	return nil
}
