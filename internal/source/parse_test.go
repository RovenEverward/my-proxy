package source_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/source"
)

// 夹具凭证统一用占位值;断言里用它们检查泄漏。
const (
	fakeUUID     = "11111111-2222-3333-4444-555555555555"
	fakeVMessID  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	fakePassword = "s3cret-pw"
)

// findIssue 找某个位置或名字上的跳过原因。
func findIssue(issues []source.Issue, name string) *source.Issue {
	for i := range issues {
		if issues[i].Name == name {
			return &issues[i]
		}
	}
	return nil
}

// assertNoSecrets 全有效输出可以带凭证,但 Issue 和 error 不行。
func assertNoSecrets(t *testing.T, res source.Result, err error, secrets ...string) {
	t.Helper()
	for _, is := range res.Issues {
		for _, s := range secrets {
			if strings.Contains(is.Reason, s) {
				t.Errorf("Issue %q 的 Reason 泄露了 %q: %q", is.Name, s, is.Reason)
			}
		}
	}
	if err != nil {
		for _, s := range secrets {
			if strings.Contains(err.Error(), s) {
				t.Errorf("error 泄露了 %q: %v", s, err)
			}
		}
	}
}

func byRemark(eps []render.Endpoint, name string) *render.Endpoint {
	for i := range eps {
		if eps[i].Remark == name {
			return &eps[i]
		}
	}
	return nil
}

// TestParseRejectsEmpty 空正文不是订阅。
func TestParseRejectsEmpty(t *testing.T) {
	for _, body := range [][]byte{nil, {}, []byte("   \n\t ")} {
		if _, err := source.Parse(body); err == nil {
			t.Errorf("空正文应报错")
		}
	}
}

// TestParseRejectsGarbage 既不是 YAML/JSON 容器也不是链接的正文要报错。
func TestParseRejectsGarbage(t *testing.T) {
	_, err := source.Parse([]byte("<html><body>not a subscription</body></html>"))
	if err == nil {
		t.Fatal("应报错")
	}
	if !errors.Is(err, source.ErrUnrecognized) {
		t.Errorf("err = %v, 想 ErrUnrecognized", err)
	}
}

// TestParseAllInvalid 结构能认、但一个节点都不能完整表达时:报错 + 报告。
func TestParseAllInvalid(t *testing.T) {
	body := "ss://bm90LWEtbGluaw==\nvmess://bm90LWpzb24=\n"
	res, err := source.Parse([]byte(body))
	if err == nil {
		t.Fatal("全无效应报错")
	}
	if len(res.Endpoints) != 0 {
		t.Errorf("Endpoints = %v", res.Endpoints)
	}
	if len(res.Issues) != 2 {
		t.Fatalf("Issues = %v", res.Issues)
	}
	assertNoSecrets(t, res, err, fakePassword, fakeUUID)
}

// TestParseClashContainer 顶层识别 clash:proxies 段。
func TestParseClashContainer(t *testing.T) {
	body := []byte("mixed-port: 7890\nproxies:\n  - {name: a, type: ss, server: h.example.com, port: 8388, cipher: aes-256-gcm, password: " + fakePassword + "}\nrules:\n  - MATCH,PROXY\n")
	res, err := source.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "clash" {
		t.Errorf("Format = %q", res.Format)
	}
	if len(res.Endpoints) != 1 {
		t.Fatalf("Endpoints = %v", res.Endpoints)
	}
	// DNS / rules / proxy-providers 等上游配置段不导入——它们根本不进入解析视野。
	ep := res.Endpoints[0]
	if ep.SSMethod != "aes-256-gcm" || ep.Password != fakePassword || ep.Port != 8388 {
		t.Errorf("ep = %+v", ep)
	}
}

// TestParseLinksPlain 明文链接列表:每行一条。
func TestParseLinksPlain(t *testing.T) {
	body := "vless://" + fakeUUID + "@v.example.com:443?security=tls&sni=v.example.com&type=tcp#vless-one\n" +
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:"+fakePassword)) + "@ss.example.com:8388#ss-one\n"
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "links" {
		t.Errorf("Format = %q", res.Format)
	}
	if len(res.Endpoints) != 2 {
		t.Fatalf("Endpoints = %+v", res.Endpoints)
	}
	v := byRemark(res.Endpoints, "vless-one")
	if v == nil || v.Protocol != model.ProtocolVLESS || v.UUID != fakeUUID || v.Security != model.SecurityTLS {
		t.Errorf("vless ep = %+v", v)
	}
	s := byRemark(res.Endpoints, "ss-one")
	if s == nil || s.SSMethod != "aes-256-gcm" || s.Password != fakePassword || s.Port != 8388 {
		t.Errorf("ss ep = %+v", s)
	}
}

// TestParseBase64Links base64 包一层仍是链接列表。
func TestParseBase64Links(t *testing.T) {
	plain := "trojan://" + fakePassword + "@tj.example.com:443?sni=tj.example.com#tj\n"
	body := base64.StdEncoding.EncodeToString([]byte(plain))
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "base64-links" {
		t.Errorf("Format = %q", res.Format)
	}
	ep := byRemark(res.Endpoints, "tj")
	if ep == nil || ep.Password != fakePassword || ep.Security != model.SecurityTLS {
		t.Errorf("trojan ep = %+v", ep)
	}
}

// TestParseSingBoxContainer sing-box JSON 的 outbounds 段。
func TestParseSingBoxContainer(t *testing.T) {
	body := `{"outbounds":[
		{"type":"selector","tag":"proxy","outbounds":["a"]},
		{"type":"vless","tag":"a","server":"v.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `","flow":"xtls-rprx-vision",
		 "tls":{"enabled":true,"server_name":"www.microsoft.com",
		 "utls":{"enabled":true,"fingerprint":"chrome"},
		 "reality":{"enabled":true,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"ab"}}}
	]}`
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "sing-box" {
		t.Errorf("Format = %q", res.Format)
	}
	if len(res.Endpoints) != 1 {
		t.Fatalf("Endpoints = %+v", res.Endpoints)
	}
	ep := res.Endpoints[0]
	if ep.Remark != "a" || ep.Security != model.SecurityReality ||
		ep.RealityPub != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" ||
		ep.RealitySID != "ab" || ep.Fingerprint != "chrome" || ep.Flow != "xtls-rprx-vision" {
		t.Errorf("ep = %+v", ep)
	}
}

// TestParseFieldValidation 核心在构造期就拒绝或必然失效的字段(tuic UUID、
// ss-2022 密钥、reality 公钥/short id)在 Parse 阶段进 Issue,合法同行保留。
func TestParseFieldValidation(t *testing.T) {
	body := `{"outbounds":[
		{"type":"tuic","tag":"bad-uuid","server":"t.example.com","server_port":443,
		 "uuid":"not-a-uuid","password":"pw","tls":{"enabled":true}},
		{"type":"shadowsocks","tag":"bad-ss22","server":"s.example.com","server_port":8388,
		 "method":"2022-blake3-aes-128-gcm","password":"badkey"},
		{"type":"shadowsocks","tag":"good-ss22","server":"s.example.com","server_port":8388,
		 "method":"2022-blake3-aes-128-gcm","password":"MTIzNDU2Nzg5MDEyMzQ1Ng=="},
		{"type":"vless","tag":"bad-pbk","server":"v.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `",
		 "tls":{"enabled":true,"utls":{"enabled":true,"fingerprint":"chrome"},
		 "reality":{"enabled":true,"public_key":"not-base64!!!","short_id":"ab"}}},
		{"type":"vless","tag":"bad-sid","server":"v.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `",
		 "tls":{"enabled":true,"utls":{"enabled":true,"fingerprint":"chrome"},
		 "reality":{"enabled":true,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"zz"}}},
		{"type":"vless","tag":"long-sid","server":"v.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `",
		 "tls":{"enabled":true,"utls":{"enabled":true,"fingerprint":"chrome"},
		 "reality":{"enabled":true,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"0123456789abcdef0123"}}},
		{"type":"trojan","tag":"bad-trojan-reality","server":"j.example.com","server_port":443,
		 "password":"pw",
		 "tls":{"enabled":true,"utls":{"enabled":true,"fingerprint":"chrome"},
		 "reality":{"enabled":true,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}},
		{"type":"vless","tag":"good-reality","server":"v.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `",
		 "tls":{"enabled":true,"utls":{"enabled":true,"fingerprint":"chrome"},
		 "reality":{"enabled":true,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"0123456789abcdef"}}}
	]}`
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 2 {
		t.Fatalf("Endpoints = %+v", res.Endpoints)
	}
	for _, name := range []string{"bad-uuid", "bad-ss22", "bad-pbk", "bad-sid", "long-sid", "bad-trojan-reality"} {
		if findIssue(res.Issues, name) == nil {
			t.Errorf("%s 应进 Issue", name)
		}
	}
	assertNoSecrets(t, res, nil, "badkey", "pw")
}

// TestParseDedupeAndKey 同源同参数去重;指纹与备注、顺序无关。
func TestParseDedupeAndKey(t *testing.T) {
	node := func(name, server string) string {
		return "  - {name: " + name + ", type: ss, server: " + server + ", port: 8388, cipher: aes-256-gcm, password: " + fakePassword + "}\n"
	}
	body := []byte("proxies:\n" + node("名字甲", "h.example.com") + node("名字乙", "h.example.com") + node("名字丙", "other.example.com"))
	res, err := source.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 2 {
		t.Fatalf("同参数节点没去重: %+v", res.Endpoints)
	}
	if res.Endpoints[0].SourceKey == "" || res.Endpoints[1].SourceKey == "" {
		t.Fatal("SourceKey 为空")
	}
	if res.Endpoints[0].SourceKey == res.Endpoints[1].SourceKey {
		t.Error("不同服务器共享了 SourceKey")
	}
	if res.Endpoints[0].Remark != "名字甲" {
		t.Errorf("去重应保留先到的名字, got %q", res.Endpoints[0].Remark)
	}

	// 换名字与顺序,同一连接参数的指纹必须一致。
	body2 := []byte("proxies:\n" + node("名字丙", "other.example.com") + node("另一个名字", "h.example.com"))
	res2, err := source.Parse(body2)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, ep := range res2.Endpoints {
		got[ep.Address] = ep.SourceKey
	}
	if got["h.example.com"] != res.Endpoints[0].SourceKey {
		t.Error("SourceKey 受名字或顺序影响")
	}
	if got["other.example.com"] != res.Endpoints[1].SourceKey {
		t.Error("SourceKey 受名字或顺序影响")
	}
}

// TestParseDedupeKeepsMuxDifference smux 开关影响客户端拨号行为,进指纹:
// 同参数节点一条开 mux 一条不开,不该被当成重复。
func TestParseDedupeKeepsMuxDifference(t *testing.T) {
	body := `{"outbounds":[
		{"type":"vless","tag":"a","server":"h.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `"},
		{"type":"vless","tag":"b","server":"h.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `","multiplex":{"enabled":true,"protocol":"smux"}},
		{"type":"vless","tag":"c","server":"h.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `","multiplex":{"enabled":true,"protocol":"smux"}}
	]}`
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 2 {
		t.Fatalf("mux 差异被误去重: %+v", res.Endpoints)
	}
	muxOn, muxOff := 0, 0
	for _, ep := range res.Endpoints {
		if ep.Mux {
			muxOn++
		} else {
			muxOff++
		}
	}
	if muxOn != 1 || muxOff != 1 {
		t.Errorf("Endpoints = %+v", res.Endpoints)
	}
}

// TestParseDedupeKeepsALPNOrder ALPN 原序参与指纹:顺序是客户端宣告的
// 协商优先级,顺序不同的节点是不同连接,不该合并。
func TestParseDedupeKeepsALPNOrder(t *testing.T) {
	body := `{"outbounds":[
		{"type":"vless","tag":"a","server":"h.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `","tls":{"enabled":true,"server_name":"h.example.com","alpn":["h2","h3"]}},
		{"type":"vless","tag":"b","server":"h.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `","tls":{"enabled":true,"server_name":"h.example.com","alpn":["h3","h2"]}},
		{"type":"vless","tag":"c","server":"h.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `","tls":{"enabled":true,"server_name":"h.example.com","alpn":["h2","h3"]}}
	]}`
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 2 {
		t.Fatalf("ALPN 顺序差异被误去重: %+v", res.Endpoints)
	}
}

// TestParseSourceKeyStableAcrossFormats 同一连接从 clash 与 sing-box 两种
// 容器解出来,SourceKey 相同——指纹长在连接参数上,不长在容器上。
func TestParseSourceKeyStableAcrossFormats(t *testing.T) {
	clashBody := []byte("proxies:\n  - {name: n, type: ss, server: h.example.com, port: 8388, cipher: aes-256-gcm, password: " + fakePassword + "}\n")
	singBody := []byte(`{"outbounds":[{"type":"shadowsocks","tag":"n","server":"h.example.com","server_port":8388,"method":"aes-256-gcm","password":"` + fakePassword + `"}]}`)
	a, err := source.Parse(clashBody)
	if err != nil {
		t.Fatal(err)
	}
	b, err := source.Parse(singBody)
	if err != nil {
		t.Fatal(err)
	}
	if a.Endpoints[0].SourceKey != b.Endpoints[0].SourceKey {
		t.Errorf("同连接跨容器指纹不同: %q vs %q", a.Endpoints[0].SourceKey, b.Endpoints[0].SourceKey)
	}
}
