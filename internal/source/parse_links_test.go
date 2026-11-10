package source_test

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/source"
)

// TestLinkVLESSReality vless URI 的参数全量保真。
func TestLinkVLESSReality(t *testing.T) {
	body := "vless://" + fakeUUID + "@1.2.3.4:443?security=reality&type=tcp&flow=xtls-rprx-vision" +
		"&sni=www.microsoft.com&fp=chrome&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0123abcd#name%20one"
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	ep := res.Endpoints[0]
	if ep.Protocol != model.ProtocolVLESS || ep.UUID != fakeUUID ||
		ep.Security != model.SecurityReality || ep.RealityPub != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" ||
		ep.RealitySID != "0123abcd" || ep.Flow != "xtls-rprx-vision" ||
		ep.SNI != "www.microsoft.com" || ep.Fingerprint != "chrome" ||
		ep.Remark != "name one" {
		t.Errorf("ep = %+v", ep)
	}
}

// TestLinkVLESSWS ws 传输参数、alpn 与 insecure 保真。
func TestLinkVLESSWS(t *testing.T) {
	body := "vless://" + fakeUUID + "@cdn.example.com:8443?type=ws&path=%2Fws&host=cdn.example.com" +
		"&security=tls&sni=real.example.com&alpn=h2%2Chttp%2F1.1&allowInsecure=1#x"
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	ep := res.Endpoints[0]
	if ep.Network != model.NetworkWS || ep.Path != "/ws" || ep.Host != "cdn.example.com" ||
		ep.SNI != "real.example.com" || len(ep.ALPN) != 2 || !ep.AllowInsecure {
		t.Errorf("ep = %+v", ep)
	}
}

// TestLinkVMessJSON vmess:// 的 JSON 体各字段。
func TestLinkVMessJSON(t *testing.T) {
	payload := `{"v":"2","ps":"vm node","add":"vm.example.com","port":"443","id":"` + fakeVMessID +
		`","aid":"2","scy":"aes-128-gcm","net":"ws","type":"none","host":"cdn.example.com","path":"/vm","tls":"tls","sni":"vm.example.com","alpn":"h2"}`
	body := "vmess://" + base64.StdEncoding.EncodeToString([]byte(payload))
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 1 {
		t.Fatalf("Issues = %+v", res.Issues)
	}
	ep := res.Endpoints[0]
	if ep.Remark != "vm node" || ep.Address != "vm.example.com" || ep.Port != 443 ||
		ep.VMessAlterID != 2 || ep.VMessSecurity != "aes-128-gcm" ||
		ep.Network != model.NetworkWS || ep.Host != "cdn.example.com" || ep.Path != "/vm" ||
		ep.Security != model.SecurityTLS || ep.SNI != "vm.example.com" {
		t.Errorf("ep = %+v", ep)
	}
}

// TestLinkVMessLegacyFields vmess JSON 里常见但无法保留的伪装字段要跳过。
func TestLinkVMessLegacyFields(t *testing.T) {
	payload := `{"ps":"bad","add":"vm.example.com","port":443,"id":"` + fakeVMessID +
		`","aid":0,"net":"kcp","type":"utp","tls":"none"}`
	body := "vmess://" + base64.StdEncoding.EncodeToString([]byte(payload))
	res, err := source.Parse([]byte(body))
	if err == nil {
		t.Fatal("kcp 伪装不该收进来")
	}
	if findIssue(res.Issues, "bad") == nil {
		t.Errorf("Issues = %+v", res.Issues)
	}
}

// TestLinkTrojan trojan URI:密码在 userinfo,TLS 默认开。
func TestLinkTrojan(t *testing.T) {
	body := "trojan://p%40ss%3Aword@tj.example.com:443?security=tls&sni=tj.example.com&allowInsecure=1#tj"
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	ep := res.Endpoints[0]
	if ep.Protocol != model.ProtocolTrojan || ep.Password != "p@ss:word" ||
		ep.Security != model.SecurityTLS || ep.SNI != "tj.example.com" || !ep.AllowInsecure {
		t.Errorf("ep = %+v", ep)
	}
}

// TestLinkSS SIP002 两种写法:base64 userinfo 与明文 userinfo 都要解。
func TestLinkSS(t *testing.T) {
	cred := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:" + fakePassword))
	for _, link := range []string{
		"ss://" + cred + "@ss.example.com:8388#b64",
		"ss://" + cred + "@ss.example.com:8388",
		"ss://aes-256-gcm:" + fakePassword + "@ss2.example.com:8388#plain",
	} {
		res, err := source.Parse([]byte(link))
		if err != nil {
			t.Fatalf("%q: %v", link, err)
		}
		if len(res.Endpoints) != 1 {
			t.Fatalf("%q 解不出节点: %+v", link, res.Issues)
		}
	}
	// 带 plugin 的 ss 链接必须跳过,不丢字段;全无效时 Parse 报错但报告还在。
	body := "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:pw")) +
		"@ss.example.com:8388?plugin=obfs-local%3Bobfs%3Dtls#pluginned"
	res, err := source.Parse([]byte(body))
	if !errors.Is(err, source.ErrNoEndpoints) {
		t.Fatalf("err = %v, want ErrNoEndpoints", err)
	}
	if len(res.Endpoints) != 0 || findIssue(res.Issues, "pluginned") == nil {
		t.Errorf("plugin ss 应跳过: eps=%+v issues=%+v", res.Endpoints, res.Issues)
	}
}

// TestLinkHysteria2 hy2 URI:密码、sni、insecure、alpn;带 obfs 的跳过。
func TestLinkHysteria2(t *testing.T) {
	body := "hysteria2://" + fakePassword + "@hy.example.com:443?sni=hy.example.com&insecure=1&alpn=h3#hy2\n"
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	ep := res.Endpoints[0]
	if ep.Protocol != model.ProtocolHysteria2 || ep.Password != fakePassword ||
		ep.SNI != "hy.example.com" || !ep.AllowInsecure || ep.Security != model.SecurityTLS {
		t.Errorf("ep = %+v", ep)
	}
	// obfs 表达不了:跳过(全无效时报 ErrNoEndpoints,报告仍在)。
	res2, err := source.Parse([]byte("hy2://pw@x.example.com:443?obfs=salamander&obfs-password=sek#o"))
	if !errors.Is(err, source.ErrNoEndpoints) {
		t.Fatalf("err = %v, want ErrNoEndpoints", err)
	}
	if len(res2.Endpoints) != 0 || findIssue(res2.Issues, "o") == nil {
		t.Errorf("obfs hy2 应跳过: eps=%+v issues=%+v", res2.Endpoints, res2.Issues)
	}
	// 多端口跳端口订阅表达不了:跳过。
	res3, err := source.Parse([]byte("hy2://pw@x.example.com:443,1000-2000?mport=443,1000-2000#m"))
	if !errors.Is(err, source.ErrNoEndpoints) {
		t.Fatalf("err = %v, want ErrNoEndpoints", err)
	}
	if len(res3.Endpoints) != 0 {
		t.Errorf("多端口 hy2 应跳过: %+v", res3.Endpoints)
	}
}

// TestLinkTUIC tuic URI:uuid:password 双段 userinfo + 拥塞控制。
func TestLinkTUIC(t *testing.T) {
	body := "tuic://" + fakeUUID + ":" + fakePassword + "@tuic.example.com:4443?congestion_control=cubic&sni=t.example.com&alpn=h3#t"
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	ep := res.Endpoints[0]
	if ep.Protocol != model.ProtocolTUIC || ep.UUID != fakeUUID || ep.Password != fakePassword ||
		ep.CongestionControl != "cubic" || ep.SNI != "t.example.com" {
		t.Errorf("ep = %+v", ep)
	}
}

// TestLinkUnknownQuery 链接里出现认不得又影响连接的参数:跳过该条,不丢参数硬发。
func TestLinkUnknownQuery(t *testing.T) {
	body := "vless://" + fakeUUID + "@v.example.com:443?security=tls&mystery-param=1#mystery\n" +
		"vless://" + fakeUUID + "@ok.example.com:443?security=tls#fine"
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 1 || res.Endpoints[0].Address != "ok.example.com" {
		t.Fatalf("Endpoints = %+v", res.Endpoints)
	}
	if findIssue(res.Issues, "mystery") == nil {
		t.Error("未知参数链接应跳过")
	}
}

// TestLinkGarbageLines 非链接行静默跳过,不是节点也不算 issue;
// 像链接但解不开的进 issue。
func TestLinkGarbageLines(t *testing.T) {
	body := "# comment line\njust some text\nnot-a-link\n" +
		"vless://" + fakeUUID + "@v.example.com:443?security=tls#ok\n"
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 1 {
		t.Fatalf("Endpoints = %+v", res.Endpoints)
	}
	if len(res.Issues) != 0 {
		t.Errorf("普通文本不该进 Issues: %+v", res.Issues)
	}
}
