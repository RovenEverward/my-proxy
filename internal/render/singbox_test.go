package render

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 测试里反复用的固定值,避免每处都写一遍长 UUID。
const (
	testUUID     = "11111111-2222-3333-4444-555555555555"
	testPassword = "trojan-pass"
)

// decode 把渲染结果解回通用结构,顺带验证输出是合法 JSON。
func decodeConfig(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, data)
	}
	return cfg
}

// outboundsOf 取出 outbounds 数组。
func outboundsOf(t *testing.T, cfg map[string]any) []any {
	t.Helper()
	obs, ok := cfg["outbounds"].([]any)
	if !ok {
		t.Fatalf("输出里没有 outbounds 数组: %v", cfg["outbounds"])
	}
	return obs
}

// findOutbound 按 tag 找 outbound;找不到或不是对象都直接失败。
func findOutbound(t *testing.T, cfg map[string]any, tag string) map[string]any {
	t.Helper()
	for _, item := range outboundsOf(t, cfg) {
		ob, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if ob["tag"] == tag {
			return ob
		}
	}
	t.Fatalf("outbounds 里找不到 tag %q", tag)
	return nil
}

// tagsOf 按顺序返回 outbounds 的 tag,缺 tag 的记为 ""。
func tagsOf(cfg map[string]any) []string {
	obs, _ := cfg["outbounds"].([]any)
	tags := make([]string, 0, len(obs))
	for _, item := range obs {
		ob, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := ob["tag"].(string)
		tags = append(tags, tag)
	}
	return tags
}

// jsonValue 把一段 JSON 文本解成通用结构,便于和渲染结果整体比对。
func jsonValue(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("测试用的期望值不是合法 JSON: %v\n%s", err, s)
	}
	return m
}

// dump 用于失败时打印可读的 JSON。
func dump(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "<无法序列化>"
	}
	return string(b)
}

// assertSkeleton 校验每个配置都该有的骨架:proxy selector、auto urltest、
// direct、block,以及 route.final。
func assertSkeleton(t *testing.T, cfg map[string]any, nodeTags []string) {
	t.Helper()

	proxy := findOutbound(t, cfg, singboxProxyTag)
	if proxy["type"] != "selector" {
		t.Errorf("proxy 不是 selector: %v", proxy["type"])
	}
	if _, ok := proxy["default"]; ok {
		t.Errorf("手动 selector 不应固定默认组：%v", proxy["default"])
	}
	wantProxy := append(stringsToAny(nodeTags), singboxAutoTag)
	if !reflect.DeepEqual(proxy["outbounds"], wantProxy) {
		t.Errorf("selector 的 outbounds = %v, 期望 %v", proxy["outbounds"], wantProxy)
	}

	auto := findOutbound(t, cfg, singboxAutoTag)
	if auto["type"] != "urltest" {
		t.Errorf("auto 不是 urltest: %v", auto["type"])
	}
	if auto["url"] != singboxTestURL {
		t.Errorf("urltest 的 url = %v, 期望 %q", auto["url"], singboxTestURL)
	}
	if auto["interval"] != singboxTestInterval {
		t.Errorf("urltest 的 interval = %v, 期望 %q", auto["interval"], singboxTestInterval)
	}
	if !reflect.DeepEqual(auto["outbounds"], stringsToAny(nodeTags)) {
		t.Errorf("urltest 的 outbounds = %v, 期望 %v", auto["outbounds"], nodeTags)
	}

	for _, tag := range []string{singboxDirectTag, singboxBlockTag} {
		if ob := findOutbound(t, cfg, tag); ob["type"] != tag {
			t.Errorf("%s 的 type = %v", tag, ob["type"])
		}
	}

	route, ok := cfg["route"].(map[string]any)
	if !ok {
		t.Fatalf("route 不是对象: %v", cfg["route"])
	}
	final, _ := route["final"].(string)
	if final == "" {
		t.Errorf("route.final 为空,应当指向一个真实存在的出口")
	}
}

func stringsToAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// TestSingBoxNode 逐个覆盖支持的协议与传输组合,并整体比对生成的 outbound。
func TestSingBoxNode(t *testing.T) {
	cases := []struct {
		name string
		ep   Endpoint
		want string
	}{
		{
			name: "vless-ws-tls",
			ep: Endpoint{
				Remark:  "香港 01",
				Address: "hk.example.com",
				Port:    443,

				Protocol: model.ProtocolVLESS,
				UUID:     testUUID,
				Flow:     "xtls-rprx-vision",

				Network: model.NetworkWS,
				Path:    "/ws",
				Host:    "cdn.example.com",

				Security:    model.SecurityTLS,
				SNI:         "hk.example.com",
				ALPN:        []string{"h2", "http/1.1"},
				Fingerprint: "chrome",
			},
			want: `{
				"type": "vless",
				"tag": "香港 01",
				"server": "hk.example.com",
				"server_port": 443,
				"uuid": "` + testUUID + `",
				"flow": "xtls-rprx-vision",
				"tls": {
					"enabled": true,
					"server_name": "hk.example.com",
					"alpn": ["h2", "http/1.1"],
					"utls": {"enabled": true, "fingerprint": "chrome"}
				},
				"transport": {"type": "ws", "path": "/ws", "headers": {"Host": "cdn.example.com"}}
			}`,
		},
		{
			name: "vless-tcp-reality",
			ep: Endpoint{
				Remark:  "日本 02",
				Address: "jp.example.com",
				Port:    8443,

				Protocol: model.ProtocolVLESS,
				UUID:     testUUID,
				Flow:     "xtls-rprx-vision",

				Network: model.NetworkTCP,

				Security:    model.SecurityReality,
				SNI:         "www.microsoft.com",
				Fingerprint: "chrome",
				RealityPub:  "PUBKEY-abc123",
				RealitySID:  "ab12",
			},
			want: `{
				"type": "vless",
				"tag": "日本 02",
				"server": "jp.example.com",
				"server_port": 8443,
				"uuid": "` + testUUID + `",
				"flow": "xtls-rprx-vision",
				"tls": {
					"enabled": true,
					"server_name": "www.microsoft.com",
					"utls": {"enabled": true, "fingerprint": "chrome"},
					"reality": {"enabled": true, "public_key": "PUBKEY-abc123", "short_id": "ab12"}
				}
			}`,
		},
		{
			name: "vless-grpc-tls",
			ep: Endpoint{
				Remark:  "新加坡 03",
				Address: "sg.example.com",
				Port:    443,

				Protocol: model.ProtocolVLESS,
				UUID:     testUUID,

				Network: model.NetworkGRPC,
				Path:    "grpc-svc",

				Security:    model.SecurityTLS,
				SNI:         "sg.example.com",
				Fingerprint: "chrome",
			},
			want: `{
				"type": "vless",
				"tag": "新加坡 03",
				"server": "sg.example.com",
				"server_port": 443,
				"uuid": "` + testUUID + `",
				"tls": {
					"enabled": true,
					"server_name": "sg.example.com",
					"utls": {"enabled": true, "fingerprint": "chrome"}
				},
				"transport": {"type": "grpc", "service_name": "grpc-svc"}
			}`,
		},
		{
			name: "vmess-ws",
			ep: Endpoint{
				Remark:  "美国 04",
				Address: "us.example.com",
				Port:    80,

				Protocol: model.ProtocolVMess,
				UUID:     testUUID,

				Network: model.NetworkWS,
				Path:    "/vm",

				Security: model.SecurityNone,
			},
			want: `{
				"type": "vmess",
				"tag": "美国 04",
				"server": "us.example.com",
				"server_port": 80,
				"uuid": "` + testUUID + `",
				"security": "auto",
				"alter_id": 0,
				"transport": {"type": "ws", "path": "/vm"}
			}`,
		},
		{
			name: "trojan-tcp-tls",
			ep: Endpoint{
				Remark:  "德国 05",
				Address: "de.example.com",
				Port:    443,

				Protocol: model.ProtocolTrojan,
				Password: testPassword,

				Network: model.NetworkTCP,

				Security:    model.SecurityTLS,
				SNI:         "de.example.com",
				ALPN:        []string{"h2"},
				Fingerprint: "firefox",
			},
			want: `{
				"type": "trojan",
				"tag": "德国 05",
				"server": "de.example.com",
				"server_port": 443,
				"password": "` + testPassword + `",
				"tls": {
					"enabled": true,
					"server_name": "de.example.com",
					"alpn": ["h2"],
					"utls": {"enabled": true, "fingerprint": "firefox"}
				}
			}`,
		},
		{
			// trojan 即使底层标着 none 也要开 TLS,否则客户端握手就失败。
			name: "trojan-tcp-none-仍带tls",
			ep: Endpoint{
				Remark:  "英国 06",
				Address: "uk.example.com",
				Port:    8443,

				Protocol: model.ProtocolTrojan,
				Password: testPassword,

				Network:  model.NetworkTCP,
				Security: model.SecurityNone,
				SNI:      "uk.example.com",
			},
			want: `{
				"type": "trojan",
				"tag": "英国 06",
				"server": "uk.example.com",
				"server_port": 8443,
				"password": "` + testPassword + `",
				"tls": {"enabled": true, "server_name": "uk.example.com"}
			}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := SingBox([]Endpoint{tc.ep}, nil)
			if err != nil {
				t.Fatalf("SingBox 返回错误: %v", err)
			}
			cfg := decodeConfig(t, out)

			got := findOutbound(t, cfg, tc.ep.Remark)
			want := jsonValue(t, tc.want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("outbound 不一致\n got: %s\nwant: %s", dump(got), dump(want))
			}

			assertSkeleton(t, cfg, []string{tc.ep.Remark})
		})
	}
}

// wireguard 在 sing-box 里是 endpoint 而不是 outbound:它有地址和 key,没有 server/port。
// 渲染放在 endpoints[],同时把 tag 追加进 selector,selector 指向 endpoint tag。
func TestSingBoxWireguardEndpoint(t *testing.T) {
	ep := Endpoint{
		Remark: "wg 节点", Address: "wg.example.com", Port: 51820,
		Protocol:     model.ProtocolWireguard,
		WGPrivateKey: "V2whiAfjuJKAfimCWSgogDW7OesBIJSISZGjiRWRmX8",
		WGPublicKey:  "fh0bUIH0q9EjI8syMy2iDT1pwocGBoFcbWmauu1wc3o",
		WGAddress:    "10.0.0.7/32",
	}
	out, err := SingBox([]Endpoint{ep}, nil)
	if err != nil {
		t.Fatalf("SingBox 返回错误: %v", err)
	}
	cfg := decodeConfig(t, out)

	eps, ok := cfg["endpoints"].([]any)
	if !ok || len(eps) != 1 {
		t.Fatalf("endpoints = %#v", cfg["endpoints"])
	}
	wg, ok := eps[0].(map[string]any)
	if !ok {
		t.Fatalf("endpoints[0] 不是对象: %#v", eps[0])
	}
	if wg["type"] != "wireguard" || wg["tag"] != "wg-0" {
		t.Errorf("endpoint 顶层字段 = %#v", wg)
	}
	if wg["address"].([]any)[0] != "10.0.0.7/32" {
		t.Errorf("address = %#v", wg["address"])
	}
	// sing-box 只收带填充的标准 base64,IR 里是无填充 RawURL。
	if wg["private_key"] != "V2whiAfjuJKAfimCWSgogDW7OesBIJSISZGjiRWRmX8=" {
		t.Errorf("private_key = %v", wg["private_key"])
	}
	peers, _ := wg["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("peers = %#v", wg["peers"])
	}
	peer := peers[0].(map[string]any)
	if peer["public_key"] != "fh0bUIH0q9EjI8syMy2iDT1pwocGBoFcbWmauu1wc3o=" {
		t.Errorf("public_key = %v", peer["public_key"])
	}

	// selector 的 outbounds 必须把 wg endpoint 的 tag 带上,否则选不中。
	proxy := findOutbound(t, cfg, singboxProxyTag)
	if !reflect.DeepEqual(proxy["outbounds"], []any{"wg-0", singboxAutoTag}) {
		t.Errorf("selector.outbounds = %v", proxy["outbounds"])
	}
}

// mux 和 fragment 是订阅级的开关:渲染时写进对应的 outbound 层。
func TestSingBoxMuxAndFragment(t *testing.T) {
	ep := Endpoint{
		Remark: "mux-frag", Address: "us.example.com", Port: 443,
		Protocol: model.ProtocolVLESS, UUID: testUUID,
		Network: model.NetworkTCP, Security: model.SecurityTLS,
		SNI: "us.example.com",
		Mux: true, Fragment: true,
	}
	out, err := SingBox([]Endpoint{ep}, nil)
	if err != nil {
		t.Fatalf("SingBox 返回错误: %v", err)
	}
	cfg := decodeConfig(t, out)
	ob := findOutbound(t, cfg, "mux-frag")

	mux, ok := ob["multiplex"].(map[string]any)
	if !ok {
		t.Fatalf("multiplex 缺失: %#v", ob)
	}
	if mux["enabled"] != true || mux["protocol"] != "smux" || mux["max_connections"] != float64(4) {
		t.Errorf("multiplex = %#v", mux)
	}
}

// TestSingBoxRealityFields 精确断言 REALITY 的公钥与 shortId 原样进了 tls.reality。
func TestSingBoxRealityFields(t *testing.T) {
	ep := Endpoint{
		Remark:  "REALITY 节点",
		Address: "r.example.com",
		Port:    443,

		Protocol: model.ProtocolVLESS,
		UUID:     testUUID,
		Network:  model.NetworkTCP,

		Security:   model.SecurityReality,
		SNI:        "www.microsoft.com",
		RealityPub: "PUBKEY-abc123",
		RealitySID: "deadbeef",
	}

	out, err := SingBox([]Endpoint{ep}, nil)
	if err != nil {
		t.Fatalf("SingBox 返回错误: %v", err)
	}
	cfg := decodeConfig(t, out)

	tls, ok := findOutbound(t, cfg, ep.Remark)["tls"].(map[string]any)
	if !ok {
		t.Fatalf("没有 tls 块")
	}
	reality, ok := tls["reality"].(map[string]any)
	if !ok {
		t.Fatalf("tls 里没有 reality 块: %s", dump(tls))
	}
	if reality["public_key"] != ep.RealityPub {
		t.Errorf("public_key = %v, 期望 %q", reality["public_key"], ep.RealityPub)
	}
	if reality["short_id"] != ep.RealitySID {
		t.Errorf("short_id = %v, 期望 %q", reality["short_id"], ep.RealitySID)
	}
	if reality["enabled"] != true {
		t.Errorf("reality.enabled = %v, 期望 true", reality["enabled"])
	}
}

// TestSingBoxKeepsTemplateKeys 校验自定义模板的其余顶层键与多余 outbound 都还在,
// 同时节点确实进了 outbounds。
func TestSingBoxKeepsTemplateKeys(t *testing.T) {
	tmpl := `{
		"log": {"level": "debug"},
		"dns": {"servers": [{"tag": "dns-x", "address": "local"}]},
		"experimental": {"cache_file": {"enabled": true, "store_rdrc": true}},
		"outbounds": [
			{"type": "selector", "tag": "proxy", "outbounds": null},
			{"type": "urltest", "tag": "auto", "outbounds": null},
			{"type": "direct", "tag": "direct"},
			{"type": "block", "tag": "block"},
			{"type": "dns", "tag": "dns-out"}
		],
		"route": {"rules": [{"protocol": "dns", "outbound": "dns-out"}], "auto_detect_interface": true}
	}`

	eps := []Endpoint{
		{
			Remark: "节点 A", Address: "a.example.com", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: testUUID,
			Network: model.NetworkWS, Path: "/a", Security: model.SecurityTLS, SNI: "a.example.com",
		},
		{
			Remark: "节点 B", Address: "b.example.com", Port: 443,
			Protocol: model.ProtocolVMess, UUID: testUUID,
			Network: model.NetworkTCP, Security: model.SecurityTLS, SNI: "b.example.com",
		},
	}

	out, err := SingBox(eps, []byte(tmpl))
	if err != nil {
		t.Fatalf("SingBox 返回错误: %v", err)
	}
	cfg := decodeConfig(t, out)

	// 模板里原有的顶层键原样保留。
	if got := cfg["log"]; !reflect.DeepEqual(got, jsonValue(t, `{"level": "debug"}`)) {
		t.Errorf("log 被改动: %s", dump(got))
	}
	if got := cfg["dns"]; !reflect.DeepEqual(got, jsonValue(t, `{"servers": [{"tag": "dns-x", "address": "local"}]}`)) {
		t.Errorf("dns 被改动: %s", dump(got))
	}
	exp := findOutbound(t, cfg, "dns-out") // 模板独有的出口要留下
	if exp["type"] != "dns" {
		t.Errorf("dns-out 被替换成了 %v", exp["type"])
	}
	if got := cfg["experimental"]; !reflect.DeepEqual(got, jsonValue(t, `{"cache_file": {"enabled": true, "store_rdrc": true}}`)) {
		t.Errorf("experimental 被改动: %s", dump(got))
	}

	// route 里原有的规则保留,final 由渲染补上。
	route := cfg["route"].(map[string]any)
	if route["final"] != singboxProxyTag {
		t.Errorf("route.final = %v, 期望 %q", route["final"], singboxProxyTag)
	}
	if !reflect.DeepEqual(route["rules"], jsonValue(t, `{"rules": [{"protocol": "dns", "outbound": "dns-out"}]}`)["rules"]) {
		t.Errorf("route.rules 被改动: %s", dump(route["rules"]))
	}

	// 节点进 outbounds,且 tag 不与四个固定出口重复。
	wantTags := []string{
		singboxProxyTag, singboxAutoTag,
		"节点 A", "节点 B",
		singboxDirectTag, singboxBlockTag,
		"dns-out",
	}
	if got := tagsOf(cfg); !reflect.DeepEqual(got, wantTags) {
		t.Errorf("outbounds 的 tag 顺序 = %v, 期望 %v", got, wantTags)
	}
	assertSkeleton(t, cfg, []string{"节点 A", "节点 B"})

	if got := findOutbound(t, cfg, "节点 A")["type"]; got != "vless" {
		t.Errorf("节点 A 的 type = %v, 期望 vless", got)
	}
	if got := findOutbound(t, cfg, "节点 B")["type"]; got != "vmess" {
		t.Errorf("节点 B 的 type = %v, 期望 vmess", got)
	}
}

// TestSingBoxUniqueTags 校验重名 Remark 会被消歧,输出里没有重复 tag。
func TestSingBoxUniqueTags(t *testing.T) {
	mk := func(addr string) Endpoint {
		return Endpoint{
			Remark: "同名声", Address: addr, Port: 443,
			Protocol: model.ProtocolVLESS, UUID: testUUID,
			Network: model.NetworkTCP, Security: model.SecurityTLS, SNI: addr,
		}
	}
	eps := []Endpoint{mk("a.example.com"), mk("b.example.com"), mk("c.example.com"), mk("d.example.com")}
	eps[3].Remark = "别的名字"

	out, err := SingBox(eps, nil)
	if err != nil {
		t.Fatalf("SingBox 返回错误: %v", err)
	}
	cfg := decodeConfig(t, out)

	wantTags := []string{
		singboxProxyTag, singboxAutoTag,
		"同名声", "同名声-2", "同名声-3", "别的名字",
		singboxDirectTag, singboxBlockTag,
		"全球代理", "广告拦截", "本地直连", "人工智能", "谷歌", "苹果", "微软",
		"社交", "流媒体", "游戏", "开发", "国内媒体", "漏网之鱼",
	}
	got := tagsOf(cfg)
	if !reflect.DeepEqual(got, wantTags) {
		t.Errorf("outbounds 的 tag 顺序 = %v, 期望 %v", got, wantTags)
	}

	seen := map[string]bool{}
	for _, tag := range got {
		if seen[tag] {
			t.Errorf("tag %q 重复", tag)
		}
		seen[tag] = true
	}
	assertSkeleton(t, cfg, []string{"同名声", "同名声-2", "同名声-3", "别的名字"})
}

// TestSingBoxDefaultTemplate 校验内置默认模板的结构与 outbounds 数量。
func TestSingBoxDefaultTemplate(t *testing.T) {
	ep := Endpoint{
		Remark: "默认模板节点", Address: "d.example.com", Port: 443,
		Protocol: model.ProtocolVLESS, UUID: testUUID,
		Network: model.NetworkTCP, Security: model.SecurityTLS, SNI: "d.example.com",
	}

	out, err := SingBox([]Endpoint{ep}, nil)
	if err != nil {
		t.Fatalf("SingBox 返回错误: %v", err)
	}
	cfg := decodeConfig(t, out)

	// 内置默认模板:proxy / auto / 节点 / direct / block,外加按主题分组的
	// selector(全球代理 / 广告拦截 / 本地直连 / 人工智能 / 谷歌 / 苹果 / 微软 /
	// 社交 / 流媒体 / 游戏 / 开发 / 国内媒体 / 漏网之鱼)。
	if got := len(outboundsOf(t, cfg)); got != 18 {
		t.Errorf("outbounds 数量 = %d, 期望 18: %v", got, tagsOf(cfg))
	}
	for _, key := range []string{"log", "dns", "inbounds", "route", "experimental"} {
		if _, ok := cfg[key]; !ok {
			t.Errorf("默认模板缺少顶层键 %q", key)
		}
	}
	// 主题组存在且引用的是稳定出口,而不是节点名。
	for _, tag := range []string{"全球代理", "广告拦截", "本地直连", "人工智能", "谷歌",
		"苹果", "微软", "社交", "流媒体", "游戏", "开发", "国内媒体", "漏网之鱼"} {
		ob := findOutbound(t, cfg, tag)
		if ob["type"] != "selector" {
			t.Errorf("%s 的 type = %v, 期望 selector", tag, ob["type"])
		}
	}
	// route.rule_set 存在,且至少带广告与国内 IP 两组规则。
	route := cfg["route"].(map[string]any)
	ruleSets, ok := route["rule_set"].([]any)
	if !ok || len(ruleSets) == 0 {
		t.Errorf("默认模板 route 应有 rule_set")
	}
	ruleSetTags := map[string]bool{}
	for _, rs := range ruleSets {
		if m, ok := rs.(map[string]any); ok {
			ruleSetTags[m["tag"].(string)] = true
		}
	}
	for _, want := range []string{"category-ads-all", "easylist", "cn"} {
		if !ruleSetTags[want] {
			t.Errorf("rule_set 缺 %q", want)
		}
	}
	assertSkeleton(t, cfg, []string{ep.Remark})
}

// 模板与实际订阅的 selector 默认链不能落到测速组；用户仍可主动选择测速组。
func TestSingBoxDefaultManualChains(t *testing.T) {
	eps := routingEndpoints()
	for _, routing := range []bool{false, true} {
		name := "模板原样输出"
		if routing {
			name = "实际订阅"
		}
		t.Run(name, func(t *testing.T) {
			var raw []byte
			var err error
			if routing {
				raw, err = SingBoxRouting(eps, seedPlan(t, eps), nil)
			} else {
				raw, err = SingBox(eps, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg := decodeConfig(t, raw)
			byTag := map[string]map[string]any{}
			for _, item := range outboundsOf(t, cfg) {
				ob := item.(map[string]any)
				byTag[ob["tag"].(string)] = ob
			}
			for tag, ob := range byTag {
				if ob["type"] != "selector" {
					continue
				}
				next := tag
				for depth := 0; ; depth++ {
					if next == singboxProxyTag || next == "手动切换" || next == singboxDirectTag || next == singboxBlockTag {
						break
					}
					if depth >= len(byTag) {
						t.Fatalf("%s 默认链有环", tag)
					}
					current := byTag[next]
					if current == nil || current["type"] != "selector" {
						t.Errorf("%s 默认链落到非手动组 %s", tag, next)
						break
					}
					target, _ := current["default"].(string)
					if target == "" {
						candidates, _ := current["outbounds"].([]any)
						if len(candidates) == 0 {
							t.Fatalf("%s 缺少候选", next)
						}
						target = candidates[0].(string)
					}
					next = target
				}
			}
			assertSkeleton(t, cfg, []string{"Daily", "Media"})
			if routing {
				final := findOutbound(t, cfg, FinalGroupName)
				if got := final["outbounds"].([]any)[0]; got != "手动切换" {
					t.Errorf("兜底首项 = %v，想要手动切换", got)
				}
			} else if got := findOutbound(t, cfg, "全球代理")["default"]; got != singboxProxyTag {
				t.Errorf("全球代理默认 = %v，想要手动 selector", got)
			}
		})
	}
}

// 没有节点时保留空测速入口，不添加新的直连兜底。
func TestSingBoxEmptyManualSelector(t *testing.T) {
	raw, err := SingBox(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSkeleton(t, decodeConfig(t, raw), nil)
}

// TestSingBoxDeterministic 校验同一输入多次渲染字节一致。
func TestSingBoxDeterministic(t *testing.T) {
	eps := []Endpoint{
		{Remark: "A", Address: "a.example.com", Port: 443, Protocol: model.ProtocolVLESS, UUID: testUUID,
			Network: model.NetworkWS, Path: "/ws", Host: "h.example.com", Security: model.SecurityTLS, SNI: "a.example.com", ALPN: []string{"h2"}},
		{Remark: "B", Address: "b.example.com", Port: 8443, Protocol: model.ProtocolTrojan, Password: testPassword,
			Network: model.NetworkGRPC, Path: "svc", Security: model.SecurityReality, SNI: "www.microsoft.com", RealityPub: "PBK", RealitySID: "sid"},
	}
	tmpl := []byte(`{"log": {"level": "info"}, "route": {}}`)

	first, err := SingBox(eps, tmpl)
	if err != nil {
		t.Fatalf("SingBox 返回错误: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := SingBox(eps, tmpl)
		if err != nil {
			t.Fatalf("第 %d 次调用返回错误: %v", i+2, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("第 %d 次输出与第一次不一致\nfirst: %s\nagain: %s", i+2, first, again)
		}
	}
	// 输出应当是缩进过的 JSON。
	if !bytes.Contains(first, []byte("\n  \"log\"")) {
		t.Errorf("输出没有用两个空格缩进:\n%s", first)
	}
}

// TestSingBoxErrors 校验坏模板与不支持的输入会报错,而不是产出坏配置。
func TestSingBoxErrors(t *testing.T) {
	ep := Endpoint{Remark: "A", Address: "a.example.com", Port: 443,
		Protocol: model.ProtocolVLESS, UUID: testUUID, Network: model.NetworkTCP, Security: model.SecurityTLS}

	cases := []struct {
		name string
		eps  []Endpoint
		tmpl []byte
	}{
		{name: "模板不是JSON", eps: []Endpoint{ep}, tmpl: []byte("{")},
		{name: "outbounds不是数组", eps: []Endpoint{ep}, tmpl: []byte(`{"outbounds": "x"}`)},
		{name: "route不是对象", eps: []Endpoint{ep}, tmpl: []byte(`{"route": []}`)},
		{name: "协议不支持", eps: []Endpoint{{Remark: "S", Protocol: model.Protocol("trojan-go"), Network: model.NetworkTCP}}},
		{name: "传输不支持", eps: []Endpoint{{Remark: "H", Protocol: model.ProtocolVLESS, Network: model.Network("hysteria2")}}},
		{name: "安全层不支持", eps: []Endpoint{{Remark: "X", Protocol: model.ProtocolVLESS, Network: model.NetworkTCP, Security: model.Security("xtls")}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SingBox(tc.eps, tc.tmpl); err == nil {
				t.Fatalf("期望返回错误,实际成功了")
			}
		})
	}
}
