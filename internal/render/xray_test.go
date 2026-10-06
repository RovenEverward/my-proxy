package render

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// realRealityPub 是一个真实的 X25519 公钥(43 字符 base64 RawURL)。
//
// 不能拿 "public-key" 这种占位串当公钥:真实 xray 会去解码它,拿占位串校验时
// 报的是 `invalid "password": ...`,看起来像字段名写错,实际是值不合法。
// 实测 `publicKey` 与 `password` 两个键名这版都认。
const realRealityPub = "3ofQKNd7CYn1Oef2aB11do2LVErA2smY31lczNCF_Rk"

func xrayEndpoints() []Endpoint {
	return []Endpoint{
		{Remark: "香港 tcp", Address: "hk.example.com", Port: 443, Protocol: model.ProtocolVLESS,
			UUID: "11111111-1111-4111-8111-111111111111", Network: model.NetworkTCP, Security: model.SecurityNone},
		{Remark: "日本 ws", Address: "jp.example.com", Port: 443, Protocol: model.ProtocolVLESS,
			UUID: "22222222-2222-4222-8222-222222222222", Network: model.NetworkWS, Path: "/ws", Host: "jp.example.com",
			Security: model.SecurityTLS, SNI: "jp.example.com", ALPN: []string{"h2", "http/1.1"}, Fingerprint: "chrome"},
		{Remark: "新加坡 grpc", Address: "sg.example.com", Port: 443, Protocol: model.ProtocolVLESS,
			UUID: "33333333-3333-4333-8333-333333333333", Network: model.NetworkGRPC, Path: "grpcsvc",
			Security: model.SecurityTLS, SNI: "sg.example.com", Fingerprint: "chrome"},
		{Remark: "美国 reality", Address: "us.example.com", Port: 443, Protocol: model.ProtocolVLESS,
			UUID: "44444444-4444-4444-8444-444444444444", Network: model.NetworkTCP, Flow: "xtls-rprx-vision",
			Security: model.SecurityReality, SNI: "www.microsoft.com", Fingerprint: "chrome",
			RealityPub: realRealityPub, RealitySID: "d4834461"},
		{Remark: "德国 vmess", Address: "de.example.com", Port: 8443, Protocol: model.ProtocolVMess,
			UUID: "55555555-5555-4555-8555-555555555555", Network: model.NetworkWS, Path: "/vm",
			Security: model.SecurityTLS, SNI: "de.example.com"},
		{Remark: "英国 trojan", Address: "uk.example.com", Port: 443, Protocol: model.ProtocolTrojan,
			Password: "uk-pass", Network: model.NetworkTCP, Security: model.SecurityTLS, SNI: "uk.example.com"},
		{Remark: "法国 trojan", Address: "fr.example.com", Port: 8443, Protocol: model.ProtocolTrojan,
			Password: "fr-pass", Network: model.NetworkWS, Path: "/tj", Security: model.SecurityNone},
	}
}

// 每个端点一份完整配置,这是 xray 客户端习惯的形态。
func TestXrayRendersOneConfigPerEndpoint(t *testing.T) {
	eps := xrayEndpoints()
	data, err := Xray(eps, nil)
	if err != nil {
		t.Fatalf("Xray: %v", err)
	}

	var configs []map[string]any
	if err := json.Unmarshal(data, &configs); err != nil {
		t.Fatalf("产物不是 JSON 数组: %v", err)
	}
	if len(configs) != len(eps) {
		t.Fatalf("应当每个端点一份配置,得到 %d 份", len(configs))
	}

	for i, cfg := range configs {
		if got, _ := cfg["remarks"].(string); got != eps[i].Remark {
			t.Errorf("第 %d 份的 remarks 是 %q,期望 %q", i, got, eps[i].Remark)
		}
		outbounds, ok := cfg["outbounds"].([]any)
		if !ok || len(outbounds) < 3 {
			t.Fatalf("第 %d 份的 outbounds 应当含节点与底稿的两条,实际 %v", i, cfg["outbounds"])
		}
		node, _ := outbounds[0].(map[string]any)
		if got, _ := node["protocol"].(string); got != string(eps[i].Protocol) {
			t.Errorf("第 %d 份的节点协议是 %q,期望 %q", i, got, eps[i].Protocol)
		}
		if got, _ := node["tag"].(string); got != eps[i].Remark {
			t.Errorf("第 %d 份的节点 tag 是 %q,期望 %q", i, got, eps[i].Remark)
		}
		// 底稿里的 direct / block 必须还在,否则 routing 规则引用的出口就没了。
		if !hasOutboundTag(outbounds, "direct") || !hasOutboundTag(outbounds, "block") {
			t.Errorf("第 %d 份丢掉了底稿的 direct / block 出口", i)
		}
		// 本地入口与路由必须还在,不然这份配置在客户端里什么都做不了。
		for _, key := range []string{"inbounds", "routing", "policy", "stats"} {
			if _, ok := cfg[key]; !ok {
				t.Errorf("第 %d 份缺少 %q", i, key)
			}
		}
	}
}

func hasOutboundTag(outbounds []any, want string) bool {
	for _, o := range outbounds {
		if obj, ok := o.(map[string]any); ok {
			if tag, _ := obj["tag"].(string); tag == want {
				return true
			}
		}
	}
	return false
}

// flow 只在有 TLS 层时出现:写在 security=none 的出口上 xray 会直接拒绝启动。
func TestXrayFlowOnlyWithTLS(t *testing.T) {
	base := Endpoint{Address: "a.example.com", Port: 443, Protocol: model.ProtocolVLESS,
		UUID: "11111111-1111-4111-8111-111111111111", Network: model.NetworkTCP, Flow: "xtls-rprx-vision"}

	cases := []struct {
		security model.Security
		wantFlow bool
	}{
		{model.SecurityTLS, true},
		{model.SecurityReality, true},
		{model.SecurityNone, false},
	}
	for _, tc := range cases {
		ep := base
		ep.Security = tc.security
		ep.SNI = "a.example.com"
		ep.RealityPub = realRealityPub
		ep.RealitySID = "sid"

		data, err := Xray([]Endpoint{ep}, nil)
		if err != nil {
			t.Fatalf("security=%s: %v", tc.security, err)
		}
		node := firstXrayNode(t, data)
		settings, _ := node["settings"].(map[string]any)
		vnext, _ := settings["vnext"].([]any)
		users, _ := vnext[0].(map[string]any)["users"].([]any)
		user, _ := users[0].(map[string]any)

		_, hasFlow := user["flow"]
		if hasFlow != tc.wantFlow {
			t.Errorf("security=%s: flow 出现=%v,期望 %v", tc.security, hasFlow, tc.wantFlow)
		}
	}
}

// 重名与空名都要消歧:xray 的出口靠 tag 认人,空 tag 会让配置起不来。
func TestXrayRemarksAreUniqueAndNonEmpty(t *testing.T) {
	eps := []Endpoint{
		{Remark: "香港", Address: "a.example.com", Port: 443, Protocol: model.ProtocolTrojan, Password: "p"},
		{Remark: "香港", Address: "b.example.com", Port: 443, Protocol: model.ProtocolTrojan, Password: "p"},
		{Remark: "", Address: "c.example.com", Port: 443, Protocol: model.ProtocolTrojan, Password: "p"},
		{Remark: "", Address: "", Port: 443, Protocol: model.ProtocolTrojan, Password: "p"},
	}
	data, err := Xray(eps, nil)
	if err != nil {
		t.Fatalf("Xray: %v", err)
	}
	var configs []map[string]any
	if err := json.Unmarshal(data, &configs); err != nil {
		t.Fatalf("产物不是 JSON 数组: %v", err)
	}
	seen := map[string]bool{}
	for i, cfg := range configs {
		name, _ := cfg["remarks"].(string)
		if name == "" {
			t.Fatalf("第 %d 份的 remarks 为空", i)
		}
		if seen[name] {
			t.Fatalf("第 %d 份的 remarks 与前面重名: %q", i, name)
		}
		seen[name] = true
	}
}

// 底稿是用户提供的,里面多出来的出口与自定义路由要原样留住。
func TestXrayHonorsCustomTemplate(t *testing.T) {
	tpl := []byte(`{
	  "log": {"loglevel": "debug"},
	  "routing": {"rules": [{"domain": ["geosite:cn"], "outboundTag": "direct"}]},
	  "outbounds": [
	    {"tag": "direct", "protocol": "freedom"},
	    {"tag": "block", "protocol": "blackhole"},
	    {"tag": "warp", "protocol": "socks", "settings": {"servers": [{"address": "127.0.0.1", "port": 40000}]}}
	  ]
	}`)
	eps := []Endpoint{{Remark: "warp", Address: "a.example.com", Port: 443,
		Protocol: model.ProtocolTrojan, Password: "p", Security: model.SecurityTLS, SNI: "a.example.com"}}

	data, err := Xray(eps, tpl)
	if err != nil {
		t.Fatalf("Xray: %v", err)
	}
	var configs []map[string]any
	if err := json.Unmarshal(data, &configs); err != nil {
		t.Fatalf("产物不是 JSON 数组: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("应当只有一份配置,得到 %d 份", len(configs))
	}
	log, _ := configs[0]["log"].(map[string]any)
	if got, _ := log["loglevel"].(string); got != "debug" {
		t.Errorf("底稿的 log 没有被沿用: %v", configs[0]["log"])
	}
	outbounds, _ := configs[0]["outbounds"].([]any)
	for _, want := range []string{"direct", "block", "warp"} {
		if !hasOutboundTag(outbounds, want) {
			t.Errorf("底稿的出口 %q 丢了", want)
		}
	}
	// 节点名与底稿里的 warp 同名,必须错开,否则两个出口同名。
	if got, _ := configs[0]["remarks"].(string); got == "warp" {
		t.Errorf("节点名与底稿出口同名了: %q", got)
	}
}

func TestXrayRejectsBadInput(t *testing.T) {
	if _, err := Xray(nil, []byte("{ 不是 JSON")); err == nil {
		t.Error("坏模板应当报错")
	}
	if _, err := Xray(nil, []byte(`{"outbounds": {"tag": "x"}}`)); err == nil {
		t.Error("outbounds 不是数组时应当报错")
	}
	_, err := Xray([]Endpoint{{Remark: "坏协议", Protocol: model.Protocol("nope")}}, nil)
	if err == nil {
		t.Error("未知协议应当报错")
	}
}

func firstXrayNode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var configs []map[string]any
	if err := json.Unmarshal(data, &configs); err != nil {
		t.Fatalf("产物不是 JSON 数组: %v", err)
	}
	if len(configs) == 0 {
		t.Fatal("产物是空数组")
	}
	outbounds, _ := configs[0]["outbounds"].([]any)
	node, _ := outbounds[0].(map[string]any)
	return node
}

// 渲染出的客户端配置要能被真实 xray 接受 —— 单测里我们自己渲染自己解析,字段名
// 写错、某个键在这个版本改名都发现不了。
//
// REALITY 那个端点的公钥换成我们自己 GenerateRealityKeypair 产出的那一个:这样
// 服务端(用私钥)与客户端(用公钥)两半是同一套生成器出来的,对得上才算通。
func TestLiveXrayClientConfigAcceptedByXray(t *testing.T) {
	bin := liveXrayBinary(t)

	priv, pub, err := model.GenerateRealityKeypair()
	if err != nil {
		t.Fatalf("GenerateRealityKeypair: %v", err)
	}
	if priv == "" || pub == "" {
		t.Fatalf("生成器给出了空密钥: priv=%q pub=%q", priv, pub)
	}

	eps := xrayEndpoints()
	for i := range eps {
		if eps[i].Security == model.SecurityReality {
			eps[i].RealityPub = pub
		}
	}

	data, err := Xray(eps, nil)
	if err != nil {
		t.Fatalf("Xray: %v", err)
	}
	var configs []json.RawMessage
	if err := json.Unmarshal(data, &configs); err != nil {
		t.Fatalf("产物不是 JSON 数组: %v", err)
	}

	dir := t.TempDir()
	for i, cfg := range configs {
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, cfg, 0o600); err != nil {
			t.Fatalf("写第 %d 份配置: %v", i, err)
		}
		cmd := exec.Command(bin, "-test", "-config", path)
		// 底稿里的 geosite: / geoip: 规则要去可执行文件旁边读 .dat 文件。
		cmd.Dir = filepath.Dir(bin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("真实 xray 不接受第 %d 份客户端配置: %v\n%s\n--- 配置 ---\n%s", i, err, out, cfg)
		}
		if !bytes.Contains(out, []byte("Configuration OK.")) {
			t.Fatalf("第 %d 份:退出码是 0 却没报告配置正常:\n%s", i, out)
		}
	}
}

func liveXrayBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("MPX_XRAY_BIN"); p != "" {
		return p
	}
	p, err := exec.LookPath("xray")
	if err != nil {
		t.Skip("本机没有 xray:设 MPX_XRAY_BIN,或把 xray 放进 PATH 后重跑")
	}
	return p
}
