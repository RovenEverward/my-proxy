package render

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

// ---- EndpointKey ----

// TestEndpointKeyManaged 托管端点继续用三个 ID 作身份。
func TestEndpointKeyManaged(t *testing.T) {
	ep := Endpoint{NodeID: 3, InboundID: 5, HostID: 7}
	if got := string(ep.Key()); got != "m:3:5:7" {
		t.Errorf("Key() = %q, want m:3:5:7", got)
	}
}

// TestEndpointKeyExternal 外部端点用 source id 与连接参数指纹;同名不同连接参数
// 必须得到不同 Key。
func TestEndpointKeyExternal(t *testing.T) {
	a := Endpoint{SourceID: 7, SourceKey: "abc", Remark: "x"}
	b := Endpoint{SourceID: 7, SourceKey: "def", Remark: "x"}
	c := Endpoint{SourceID: 8, SourceKey: "abc"}
	if a.Key() == b.Key() {
		t.Error("不同连接参数的外部端点共享了 Key")
	}
	if a.Key() == c.Key() {
		t.Error("不同来源同指纹共享了 Key")
	}
	if !strings.HasPrefix(string(a.Key()), "s:7:") {
		t.Errorf("外部 Key = %q", a.Key())
	}
}

// ---- Compatible ----

func okEndpoint() Endpoint {
	return Endpoint{
		SourceID: 9, SourceKey: "k", Remark: "ext",
		Address: "ext.example.com", Port: 443,
		Protocol: model.ProtocolVLESS, UUID: "11111111-2222-3333-4444-555555555555",
		Network: model.NetworkTCP, Security: model.SecurityTLS, SNI: "a.com",
	}
}

// TestCompatibleBasics 常见组合在六个格式下的取舍。
func TestCompatibleBasics(t *testing.T) {
	formats := []model.ClientFormat{
		model.FormatLinks, model.FormatBase64, model.FormatClash,
		model.FormatClashMeta, model.FormatSingBox, model.FormatXray,
	}
	t.Run("vless+tcp+tls 全格式可用", func(t *testing.T) {
		for _, f := range formats {
			if err := Compatible(okEndpoint(), f); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
	})
	t.Run("hysteria2 只有 meta/sing-box/links 可用", func(t *testing.T) {
		ep := okEndpoint()
		ep.Protocol = model.ProtocolHysteria2
		ep.UUID = ""
		ep.Password = "pw"
		ep.Network = model.NetworkUDP
		ep.Security = model.SecurityTLS
		want := map[model.ClientFormat]bool{
			model.FormatLinks: true, model.FormatBase64: true,
			model.FormatClash: false, model.FormatClashMeta: true,
			model.FormatSingBox: true, model.FormatXray: false,
		}
		for _, f := range formats {
			err := Compatible(ep, f)
			if want[f] && err != nil {
				t.Errorf("%s: %v", f, err)
			}
			if !want[f] && err == nil {
				t.Errorf("%s: 期望不兼容", f)
			}
		}
	})
	t.Run("splithttp 只有 xray 与链接可用", func(t *testing.T) {
		ep := okEndpoint()
		ep.Network = model.NetworkSplitHTTP
		want := map[model.ClientFormat]bool{
			model.FormatLinks: true, model.FormatBase64: true, model.FormatXray: true,
		}
		for _, f := range formats {
			err := Compatible(ep, f)
			if want[f] && err != nil {
				t.Errorf("%s: %v", f, err)
			}
			if !want[f] && err == nil {
				t.Errorf("%s: 期望不兼容", f)
			}
		}
	})
	t.Run("reality 原版 clash 不可用", func(t *testing.T) {
		ep := okEndpoint()
		ep.Security = model.SecurityReality
		ep.RealityPub = "PUB"
		if err := Compatible(ep, model.FormatClash); err == nil {
			t.Error("原版 Clash 应拒绝 REALITY")
		}
		if err := Compatible(ep, model.FormatClashMeta); err != nil {
			t.Errorf("meta: %v", err)
		}
		// sing-box 的 reality 必须带 uTLS 指纹;没给指纹如实拒绝。
		if err := Compatible(ep, model.FormatSingBox); err == nil {
			t.Error("sing-box 应拒绝无指纹的 REALITY")
		}
		ep.Fingerprint = "chrome"
		if err := Compatible(ep, model.FormatSingBox); err != nil {
			t.Errorf("sing-box: %v", err)
		}
	})
	t.Run("xray 自定义 id 合法", func(t *testing.T) {
		ep := okEndpoint()
		ep.Protocol = model.ProtocolVLESS
		ep.UUID = "not-a-uuid" // <30 字节非 UUID,文档明确允许。
		for _, f := range []model.ClientFormat{
			model.FormatLinks, model.FormatClashMeta, model.FormatSingBox, model.FormatXray,
		} {
			if err := Compatible(ep, f); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
		ep.UUID = "我爱🍉老师1314" // 官方示例,UTF-8 21 字节。
		if err := Compatible(ep, model.FormatXray); err != nil {
			t.Errorf("xray: %v", err)
		}
		ep.UUID = strings.Repeat("x", 40) // 超 30 字节又非 UUID。
		if err := Compatible(ep, model.FormatXray); err == nil {
			t.Error("xray 应拒绝超长的非 UUID id")
		}
	})
}

// TestCompatibleSSMethod classic method 全格式可用;ss-2022 进不了原版 Clash。
func TestCompatibleSSMethod(t *testing.T) {
	ep := okEndpoint()
	ep.Protocol = model.ProtocolShadowsocks
	ep.UUID = ""
	ep.Password = "pw"
	ep.Security = model.SecurityNone
	ep.SSMethod = "aes-256-gcm"
	for _, f := range []model.ClientFormat{
		model.FormatLinks, model.FormatClash, model.FormatClashMeta,
		model.FormatSingBox, model.FormatXray,
	} {
		if err := Compatible(ep, f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	ep.SSMethod = "2022-blake3-aes-128-gcm"
	if err := Compatible(ep, model.FormatClash); err == nil {
		t.Error("原版 Clash 不认 2022-blake3 method")
	}
	if err := Compatible(ep, model.FormatClashMeta); err != nil {
		t.Errorf("meta 应接受 ss-2022: %v", err)
	}
	// ss 不能带传输层:ws 的 ss 一律不可表达。
	ep.SSMethod = "aes-256-gcm"
	ep.Network = model.NetworkWS
	for _, f := range []model.ClientFormat{
		model.FormatLinks, model.FormatClash, model.FormatClashMeta,
		model.FormatSingBox, model.FormatXray,
	} {
		if err := Compatible(ep, f); err == nil {
			t.Errorf("%s: ws 的 ss 应不兼容", f)
		}
	}
}

// TestCompatibleVMessAlterID alterId 只能去原版 Clash 与分享链接。
func TestCompatibleVMessAlterID(t *testing.T) {
	ep := okEndpoint()
	ep.Protocol = model.ProtocolVMess
	ep.Security = model.SecurityNone
	ep.VMessAlterID = 4
	ep.VMessSecurity = "aes-128-gcm"
	for _, f := range []model.ClientFormat{
		model.FormatClashMeta, model.FormatSingBox, model.FormatXray,
	} {
		if err := Compatible(ep, f); err == nil {
			t.Errorf("%s: alterId>0 应不兼容", f)
		}
	}
	for _, f := range []model.ClientFormat{model.FormatLinks, model.FormatClash} {
		if err := Compatible(ep, f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

// ---- 外部字段渲染 ----

// TestClashExternalSSAndVMess 外部的 SS method 与 VMess 参数写进 Clash 输出,
// 托管默认不变由既有测试锁定,这里只看外部字段生效。
func TestClashExternalSSAndVMess(t *testing.T) {
	eps := []Endpoint{
		{Remark: "ss", Address: "1.1.1.1", Port: 8388, Protocol: model.ProtocolShadowsocks,
			SSMethod: "aes-256-gcm", Password: "pw", Network: model.NetworkTCP},
		{Remark: "vm", Address: "2.2.2.2", Port: 443, Protocol: model.ProtocolVMess,
			UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", VMessSecurity: "aes-128-gcm",
			Network: model.NetworkTCP},
	}
	body, err := Clash(eps, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]any{}
	for _, p := range doc.Proxies {
		byName[p["name"].(string)] = p
	}
	if got := byName["ss"]["cipher"]; got != "aes-256-gcm" {
		t.Errorf("ss cipher = %v", got)
	}
	if got := byName["vm"]["cipher"]; got != "aes-128-gcm" {
		t.Errorf("vmess cipher = %v", got)
	}
}

// TestSingBoxExternalFields sing-box 输出外部 SS method 与 VMess 参数。
func TestSingBoxExternalFields(t *testing.T) {
	eps := []Endpoint{
		{Remark: "ss", Address: "1.1.1.1", Port: 8388, Protocol: model.ProtocolShadowsocks,
			SSMethod: "chacha20-ietf-poly1305", Password: "pw", Network: model.NetworkTCP},
		{Remark: "vm", Address: "2.2.2.2", Port: 443, Protocol: model.ProtocolVMess,
			UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", VMessSecurity: "none",
			Network: model.NetworkTCP},
	}
	body, err := SingBox(eps, nil)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	byTag := map[string]map[string]any{}
	for _, o := range cfg.Outbounds {
		if tag, _ := o["tag"].(string); tag != "" {
			byTag[tag] = o
		}
	}
	if got := byTag["ss"]["method"]; got != "chacha20-ietf-poly1305" {
		t.Errorf("ss method = %v", got)
	}
	if got := byTag["vm"]["security"]; got != "none" {
		t.Errorf("vmess security = %v", got)
	}
}

// TestXrayExternalFields xray 输出外部 SS method 与 VMess 参数。
func TestXrayExternalFields(t *testing.T) {
	eps := []Endpoint{
		{Remark: "ss", Address: "1.1.1.1", Port: 8388, Protocol: model.ProtocolShadowsocks,
			SSMethod: "aes-128-gcm", Password: "pw", Network: model.NetworkTCP},
		{Remark: "vm", Address: "2.2.2.2", Port: 443, Protocol: model.ProtocolVMess,
			UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", VMessSecurity: "chacha20-poly1305",
			Network: model.NetworkTCP},
	}
	body, err := Xray(eps, nil)
	if err != nil {
		t.Fatal(err)
	}
	var cfgs []map[string]any
	if err := json.Unmarshal(body, &cfgs); err != nil {
		t.Fatal(err)
	}
	var ssServer, vmUser map[string]any
	for _, c := range cfgs {
		out := c["outbounds"].([]any)[0].(map[string]any)
		settings := out["settings"].(map[string]any)
		if out["protocol"] == "shadowsocks" {
			ssServer = settings["servers"].([]any)[0].(map[string]any)
		}
		if out["protocol"] == "vmess" {
			vmUser = settings["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)
		}
	}
	if got := ssServer["method"]; got != "aes-128-gcm" {
		t.Errorf("xray ss method = %v", got)
	}
	if got := vmUser["security"]; got != "chacha20-poly1305" {
		t.Errorf("xray vmess security = %v", got)
	}
}

// TestSSShareLink 外部 SS 节点产出 SIP002 ss:// 链接。
func TestSSShareLink(t *testing.T) {
	ep := Endpoint{
		Remark: "ss node", Address: "203.0.113.1", Port: 8388,
		Protocol: model.ProtocolShadowsocks, SSMethod: "aes-256-gcm", Password: "p@ss",
		Network: model.NetworkTCP,
	}
	got := Links([]Endpoint{ep})
	if len(got) != 1 {
		t.Fatalf("Links() = %v", got)
	}
	link := got[0]
	if !strings.HasPrefix(link, "ss://") {
		t.Fatalf("link = %q", link)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	// userinfo 是 method:password 的 base64,解回来必须等于原值。
	raw, err := base64.RawURLEncoding.DecodeString(u.User.String())
	if err != nil {
		t.Fatalf("userinfo 不是 base64: %v", u.User)
	}
	if string(raw) != "aes-256-gcm:p@ss" {
		t.Errorf("userinfo 解出 %q", raw)
	}
	if u.Host != "203.0.113.1:8388" || u.Fragment != "ss node" {
		t.Errorf("host/fragment = %q / %q", u.Host, u.Fragment)
	}
}

// TestSSLinkSkippedWithoutMethod 托管 ss 端点没有 SSMethod,行为保持原样:
// 不出 ss:// 链接。
func TestSSLinkSkippedWithoutMethod(t *testing.T) {
	ep := Endpoint{Remark: "ss", Protocol: model.ProtocolShadowsocks, Address: "9.9.9.9", Port: 1}
	if got := Links([]Endpoint{ep}); len(got) != 0 {
		t.Errorf("托管 ss 不该出链接: %v", got)
	}
}

// TestVMessLinkUsesExternalFields vmess 链接带外部 aid / scy。
func TestVMessLinkUsesExternalFields(t *testing.T) {
	ep := Endpoint{
		Remark: "vm", Address: "5.6.7.8", Port: 8080,
		Protocol: model.ProtocolVMess, UUID: linkTestVMessUUID,
		VMessAlterID: 4, VMessSecurity: "aes-128-gcm",
		Network: model.NetworkTCP, Security: model.SecurityNone,
	}
	got := Links([]Endpoint{ep})
	if len(got) != 1 {
		t.Fatalf("Links() = %v", got)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got[0], "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"aid":"4"`) || !strings.Contains(s, `"scy":"aes-128-gcm"`) {
		t.Errorf("vmess JSON 没带外部参数: %s", s)
	}
}

// ---- SingBoxRouting 用 Key ----

// TestSingBoxRoutingExternalTags 两个不同名的外部端点身份互相独立:组成员按
// Key 对上各自的 tag,不会因为三个托管 ID 都是 0 而互相覆盖。
func TestSingBoxRoutingExternalTags(t *testing.T) {
	mk := func(key, remark string) Endpoint {
		return Endpoint{
			SourceID: 9, SourceKey: key, Remark: remark,
			Address: "ext-" + key + ".example.com", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: "11111111-2222-3333-4444-555555555555",
			Network: model.NetworkTCP, Security: model.SecurityTLS, SNI: "a.com",
		}
	}
	eps := []Endpoint{mk("aaa", "外 A"), mk("bbb", "外 B")}
	p := RoutingPlan{
		Groups: []RoutingGroup{
			{ProxyGroup: model.ProxyGroup{ID: 1, Name: "混合", Mode: model.GroupManual},
				Members: []Endpoint{eps[0], eps[1]}},
			{ProxyGroup: model.ProxyGroup{ID: 2, Name: "只有B", Mode: model.GroupManual},
				Members: []Endpoint{eps[1]}},
		},
		Final: "group:1",
	}
	b, err := SingBoxRouting(eps, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := decodeConfig(t, b)
	mixed := findOutbound(t, cfg, "混合")["outbounds"].([]any)
	onlyB := findOutbound(t, cfg, "只有B")["outbounds"].([]any)
	if len(mixed) != 2 || len(onlyB) != 1 {
		t.Fatalf("组成员 = %v / %v", mixed, onlyB)
	}
	// 组的成员 tag 必须各指各的节点,不能都落到同一个 tag。
	if mixed[0] == mixed[1] {
		t.Fatalf("两个外部端点映射到了同一个 tag: %v", mixed)
	}
	if onlyB[0] != mixed[1] {
		t.Errorf("只有B 的成员 = %v, 想与混合组第二个相同 %v", onlyB, mixed)
	}
	// 组里引用的 tag 都必须存在。
	tags := map[string]bool{}
	for _, o := range cfg["outbounds"].([]any) {
		m := o.(map[string]any)
		tags[m["tag"].(string)] = true
	}
	for _, member := range append(mixed, onlyB...) {
		if !tags[member.(string)] {
			t.Errorf("组引用了不存在的 tag %v", member)
		}
	}
}

// decodeConfig / findOutbound 已在 singbox_test.go / routing_test.go 里用过,
// 这里复用同名 helper(若无则在各自文件定义)。
