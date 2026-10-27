package render

import (
	"bytes"
	"net/url"
	"reflect"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

// clashTestEndpoints 覆盖三种协议 × 三种传输,以及 tls / reality / none。
func clashTestEndpoints() []Endpoint {
	return []Endpoint{
		{
			Remark: "reality-vless", Address: "1.2.3.4", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID, Flow: "xtls-rprx-vision",
			Network: model.NetworkTCP, Security: model.SecurityReality,
			SNI: "www.microsoft.com", Fingerprint: "chrome",
			RealityPub: "PBK_PUBLIC_KEY", RealitySID: "0123abcd",
		},
		{
			Remark: "ws-vless", Address: "example.com", Port: 8443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkWS, Path: "/ws", Host: "cdn.example.com",
			Security: model.SecurityTLS, SNI: "example.com", Fingerprint: "chrome",
			ALPN: []string{"h2", "http/1.1"},
		},
		{
			Remark: "grpc-vless", Address: "1.2.3.4", Port: 2053,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkGRPC, Path: "grpcsvc", Host: "authority.example.com",
			Security: model.SecurityTLS, SNI: "sni.example.com",
		},
		{
			Remark: "vmess-ws", Address: "1.2.3.4", Port: 8443,
			Protocol: model.ProtocolVMess, UUID: linkTestVMessUUID,
			Network: model.NetworkWS, Path: "/vm", Host: "vm.example.com",
			Security: model.SecurityTLS, SNI: "vm.example.com",
		},
		{
			Remark: "vmess-tcp", Address: "5.6.7.8", Port: 8080,
			Protocol: model.ProtocolVMess, UUID: linkTestVMessUUID,
			Network: model.NetworkTCP, Security: model.SecurityNone,
		},
		{
			Remark: "tcp-trojan", Address: "9.9.9.9", Port: 443,
			Protocol: model.ProtocolTrojan, Password: "tjpass",
			Network: model.NetworkTCP, Security: model.SecurityTLS,
			SNI: "tj.example.com", AllowInsecure: true,
		},
		{
			Remark: "grpc-trojan", Address: "tj.example.com", Port: 443,
			Protocol: model.ProtocolTrojan, Password: "tjpass",
			Network: model.NetworkGRPC, Path: "svc", Host: "authority.tj.com",
			Security: model.SecurityTLS, SNI: "tj.example.com",
		},
		{
			Remark: "ss2022", Address: "ss.example.com", Port: 8388,
			Protocol: model.ProtocolShadowsocks, Password: "AAECAwQFBgcICQoLDA0ODw==",
			Network: model.NetworkTCP, Security: model.SecurityNone,
		},
		{
			Remark: "wg", Address: "wg.example.com", Port: 51820,
			Protocol:     model.ProtocolWireguard,
			WGPrivateKey: "V2whiAfjuJKAfimCWSgogDW7OesBIJSISZGjiRWRmX8",
			WGPublicKey:  "fh0bUIH0q9EjI8syMy2iDT1pwocGBoFcbWmauu1wc3o",
			WGAddress:    "10.0.0.7/32",
			Network:      model.NetworkTCP, Security: model.SecurityNone,
		},
		{
			Remark: "xhttp-vless", Address: "xh.example.com", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkXHTTP, Path: "/x", Host: "cdn.example.com", Mode: "packet-up",
			Security: model.SecurityTLS, SNI: "xh.example.com",
		},
		{
			Remark: "httpupgrade-vless", Address: "hu.example.com", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkHTTPUpgrade, Path: "/u", Host: "cdn2.example.com",
			Security: model.SecurityTLS, SNI: "hu.example.com",
		},
	}
}

// clashProxies 渲染并取回节点列表。
func clashProxies(t *testing.T, eps []Endpoint, meta bool) []map[string]any {
	t.Helper()
	raw, err := Clash(eps, meta, nil)
	if err != nil {
		t.Fatalf("Clash() 报错: %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("yaml 解析失败: %v\n%s", err, raw)
	}
	return doc.Proxies
}

// TestClashStructure 覆盖顶层结构:能被 yaml 解析,节点数与策略组、规则齐全。
// meta=false 是原来的最小输出(2 组 3 规则);meta=true 走完整模板。
func TestClashStructure(t *testing.T) {
	eps := clashTestEndpoints()

	t.Run("clash", func(t *testing.T) {
		raw, err := Clash(eps, false, nil)
		if err != nil {
			t.Fatalf("Clash() 报错: %v", err)
		}

		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("yaml 解析失败: %v\n%s", err, raw)
		}

		proxies, ok := doc["proxies"].([]any)
		if !ok {
			t.Fatalf("proxies 不是列表: %#v", doc["proxies"])
		}
		if len(proxies) != len(eps) {
			t.Errorf("proxies 条数 = %d, want %d", len(proxies), len(eps))
		}

		groups, ok := doc["proxy-groups"].([]any)
		if !ok || len(groups) != 2 {
			t.Fatalf("proxy-groups = %#v, want 2 个", doc["proxy-groups"])
		}

		proxyGroup, _ := groups[0].(map[string]any)
		if proxyGroup["name"] != "PROXY" || proxyGroup["type"] != "select" {
			t.Errorf("第一个策略组 = %#v, want PROXY/select", groups[0])
		}
		selectNames, _ := proxyGroup["proxies"].([]any)
		wantSelect := []any{"AUTO", "DIRECT", "reality-vless", "ws-vless", "grpc-vless",
			"vmess-ws", "vmess-tcp", "tcp-trojan", "grpc-trojan",
			"ss2022", "wg", "xhttp-vless", "httpupgrade-vless"}
		if len(selectNames) != len(wantSelect) {
			t.Fatalf("PROXY 成员 = %#v, want %#v", selectNames, wantSelect)
		}
		for i, want := range wantSelect {
			if selectNames[i] != want {
				t.Errorf("PROXY[%d] = %v, want %v", i, selectNames[i], want)
			}
		}

		autoGroup, _ := groups[1].(map[string]any)
		if autoGroup["name"] != "AUTO" || autoGroup["type"] != "url-test" {
			t.Errorf("第二个策略组 = %#v, want AUTO/url-test", groups[1])
		}
		if autoGroup["url"] != "http://www.gstatic.com/generate_204" {
			t.Errorf("AUTO.url = %v", autoGroup["url"])
		}
		if autoGroup["interval"] != 300 {
			t.Errorf("AUTO.interval = %#v, want 300", autoGroup["interval"])
		}
		autoNames, _ := autoGroup["proxies"].([]any)
		if len(autoNames) != len(eps) {
			t.Errorf("AUTO 成员条数 = %d, want %d", len(autoNames), len(eps))
		}

		rules, ok := doc["rules"].([]any)
		if !ok || len(rules) != 3 {
			t.Fatalf("rules = %#v, want 3 条", doc["rules"])
		}
		wantRules := []any{"GEOIP,LAN,DIRECT", "GEOIP,CN,DIRECT", "MATCH,PROXY"}
		for i, want := range wantRules {
			if rules[i] != want {
				t.Errorf("rules[%d] = %v, want %v", i, rules[i], want)
			}
		}
	})

	t.Run("clash-meta", func(t *testing.T) {
		raw, err := Clash(eps, true, nil)
		if err != nil {
			t.Fatalf("Clash() 报错: %v", err)
		}

		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("yaml 解析失败: %v\n%s", err, raw)
		}

		proxies, ok := doc["proxies"].([]any)
		if !ok {
			t.Fatalf("proxies 不是列表: %#v", doc["proxies"])
		}
		if len(proxies) != len(eps) {
			t.Errorf("proxies 条数 = %d, want %d", len(proxies), len(eps))
		}
		// 注入的节点与最小版同源:name 齐全即可。
		names := map[string]bool{}
		for _, p := range proxies {
			names[p.(map[string]any)["name"].(string)] = true
		}
		for _, want := range []string{"reality-vless", "ws-vless", "grpc-vless",
			"vmess-ws", "vmess-tcp", "tcp-trojan", "grpc-trojan"} {
			if !names[want] {
				t.Errorf("proxies 缺节点 %q", want)
			}
		}

		// 模板段落齐整:rule-providers / proxy-groups / rules / dns。
		if _, ok := doc["rule-providers"].(map[string]any); !ok {
			t.Errorf("meta 版应有 rule-providers")
		}
		groups, ok := doc["proxy-groups"].([]any)
		if !ok || len(groups) < 15 {
			t.Fatalf("meta 版 proxy-groups = %d 个, want >= 15", len(groups))
		}
		groupNames := map[string]bool{}
		for _, g := range groups {
			groupNames[g.(map[string]any)["name"].(string)] = true
		}
		for _, want := range []string{"手动切换", "自动选择", "全球代理", "广告拦截",
			"本地直连", "人工智能", "谷歌", "苹果", "微软", "社交", "流媒体", "游戏",
			"开发", "国内媒体", "漏网之鱼"} {
			if !groupNames[want] {
				t.Errorf("meta 版缺策略组 %q", want)
			}
		}

		rules, ok := doc["rules"].([]any)
		if !ok || len(rules) < 100 {
			t.Errorf("meta 版 rules = %d 条, want >= 100", len(rules))
		}

		dns, ok := doc["dns"].(map[string]any)
		if !ok {
			t.Errorf("meta 版应有 dns 段")
		} else {
			if _, ok := dns["fake-ip-filter"].([]any); !ok {
				t.Errorf("dns 缺 fake-ip-filter")
			}
			if _, ok := dns["nameserver-policy"].(map[string]any); !ok {
				t.Errorf("dns 缺 nameserver-policy")
			}
		}
	})
}

// 默认 DNS 在模板原样输出和实际订阅中都必须指向存在的代理组。
func TestClashDefaultDNS(t *testing.T) {
	eps := clashTestEndpoints()
	cases := []struct {
		name   string
		render func() ([]byte, error)
	}{
		{"模板原样输出", func() ([]byte, error) { return Clash(eps, true, nil) }},
		{"实际订阅", func() ([]byte, error) { return ClashRouting(eps, seedPlan(t, eps), nil) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := c.render()
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			groups := map[string]bool{}
			for _, g := range doc["proxy-groups"].([]any) {
				groups[g.(map[string]any)["name"].(string)] = true
			}
			dns, ok := doc["dns"].(map[string]any)
			if !ok {
				t.Fatal("缺少 DNS 配置")
			}
			domestic := []any{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"}
			wantDNS := map[string]any{
				"default-nameserver":      []any{"119.29.29.29", "223.5.5.5"},
				"proxy-server-nameserver": domestic,
				"nameserver-policy":       map[string]any{"geosite:cn,private,apple": domestic},
			}
			for key, want := range wantDNS {
				if !reflect.DeepEqual(dns[key], want) {
					t.Errorf("dns.%s = %#v, want %#v", key, dns[key], want)
				}
			}
			servers, ok := dns["nameserver"].([]any)
			wantServers := []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}
			if !ok || len(servers) != len(wantServers) {
				t.Fatalf("nameserver = %#v，想要两条国外 DoH", dns["nameserver"])
			}
			for i, server := range servers {
				address, ok := server.(string)
				if !ok {
					t.Fatalf("DoH 地址不是字符串：%#v", server)
				}
				u, err := url.Parse(address)
				if err != nil {
					t.Fatalf("DoH 地址解析失败：%v", err)
				}
				if u.Fragment == "" || !groups[u.Fragment] {
					t.Errorf("DoH 指向不存在的代理组 %q", u.Fragment)
				}
				u.Fragment = ""
				if got := u.String(); got != wantServers[i] {
					t.Errorf("nameserver[%d] = %q, want %q", i, got, wantServers[i])
				}
			}
		})
	}
}

// TestClashCustomTemplate 覆盖 meta 版套用自定义模板:proxies 注入到自定义
// 模板的 rule-providers 之后,模板里的组与规则原样保留。
func TestClashCustomTemplate(t *testing.T) {
	eps := clashTestEndpoints()
	custom := `# 自定义模板
rule-providers:
  myrules:
    type: http
    behavior: classical
    url: https://example.com/myrules.yaml
    path: ./ruleset/myrules.yaml
    interval: 86400

proxy-groups:
  - name: 手动切换
    type: select
    include-all-proxies: true

rules:
  - DOMAIN-SUFFIX,example.com,手动切换
  - MATCH,手动切换
`
	raw, err := Clash(eps, true, []byte(custom))
	if err != nil {
		t.Fatalf("Clash(custom) 报错: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("yaml 解析失败: %v\n%s", err, raw)
	}

	// 自定义模板的 rule-providers 保留。
	rps, ok := doc["rule-providers"].(map[string]any)
	if !ok {
		t.Fatalf("自定义模板的 rule-providers 应保留: %#v", doc["rule-providers"])
	}
	if _, ok := rps["myrules"]; !ok {
		t.Errorf("自定义模板的 myrules provider 丢失: %#v", rps)
	}

	// proxies 注入且齐全。
	proxies, ok := doc["proxies"].([]any)
	if !ok || len(proxies) != len(eps) {
		t.Fatalf("proxies = %#v, want %d 个", doc["proxies"], len(eps))
	}

	// 自定义组保留(不是内置的 15 组)。
	groups, ok := doc["proxy-groups"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("自定义模板的 proxy-groups = %#v, want 1 个", doc["proxy-groups"])
	}
	if groups[0].(map[string]any)["name"] != "手动切换" {
		t.Errorf("自定义组名不对: %#v", groups[0])
	}

	rules, ok := doc["rules"].([]any)
	if !ok || len(rules) != 2 {
		t.Fatalf("自定义模板的 rules = %#v, want 2 条", doc["rules"])
	}
}

// TestClashMetaTemplateFallback 覆盖 meta 版传空模板时回落内置默认。
func TestClashMetaTemplateFallback(t *testing.T) {
	eps := clashTestEndpoints()
	raw, err := Clash(eps, true, nil)
	if err != nil {
		t.Fatalf("Clash(meta, nil) 报错: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("yaml 解析失败: %v", err)
	}
	if _, ok := doc["rule-providers"].(map[string]any); !ok {
		t.Error("空模板应回落内置默认(有 rule-providers)")
	}
	// 空串同样回落。
	raw2, err := Clash(eps, true, []byte(""))
	if err != nil {
		t.Fatalf("Clash(meta, 空串) 报错: %v", err)
	}
	var doc2 map[string]any
	if err := yaml.Unmarshal(raw2, &doc2); err != nil {
		t.Fatalf("yaml 解析失败: %v", err)
	}
	if _, ok := doc2["rule-providers"].(map[string]any); !ok {
		t.Error("空串模板应回落内置默认(有 rule-providers)")
	}
}

// TestClashProxyFields 逐个字段核对节点的渲染结果。
func TestClashProxyFields(t *testing.T) {
	byName := map[string]map[string]any{}
	for _, p := range clashProxies(t, clashTestEndpoints(), true) {
		name, _ := p["name"].(string)
		byName[name] = p
	}

	t.Run("vless+tcp+reality", func(t *testing.T) {
		p := byName["reality-vless"]
		if p == nil {
			t.Fatal("缺少 reality-vless")
		}
		if p["type"] != "vless" || p["server"] != "1.2.3.4" || p["port"] != 443 {
			t.Errorf("基本字段 = %#v", p)
		}
		if p["uuid"] != linkTestUUID {
			t.Errorf("uuid = %v", p["uuid"])
		}
		if p["network"] != "tcp" {
			t.Errorf("network = %v", p["network"])
		}
		if p["tls"] != true {
			t.Errorf("reality 也应 tls: true,得到 %#v", p["tls"])
		}
		if p["servername"] != "www.microsoft.com" {
			t.Errorf("servername = %v", p["servername"])
		}
		if p["flow"] != "xtls-rprx-vision" {
			t.Errorf("flow = %v", p["flow"])
		}
		if p["udp"] != true {
			t.Errorf("udp = %v", p["udp"])
		}
		if p["client-fingerprint"] != "chrome" {
			t.Errorf("client-fingerprint = %v", p["client-fingerprint"])
		}
		reality, ok := p["reality-opts"].(map[string]any)
		if !ok {
			t.Fatalf("reality-opts = %#v", p["reality-opts"])
		}
		if reality["public-key"] != "PBK_PUBLIC_KEY" {
			t.Errorf("public-key = %v", reality["public-key"])
		}
		if reality["short-id"] != "0123abcd" {
			t.Errorf("short-id = %v", reality["short-id"])
		}
	})

	t.Run("vless+ws+tls", func(t *testing.T) {
		p := byName["ws-vless"]
		if p == nil {
			t.Fatal("缺少 ws-vless")
		}
		if p["network"] != "ws" {
			t.Errorf("network = %v", p["network"])
		}
		if p["servername"] != "example.com" {
			t.Errorf("servername = %v", p["servername"])
		}
		opts, ok := p["ws-opts"].(map[string]any)
		if !ok {
			t.Fatalf("ws-opts = %#v", p["ws-opts"])
		}
		if opts["path"] != "/ws" {
			t.Errorf("ws-opts.path = %v", opts["path"])
		}
		headers, ok := opts["headers"].(map[string]any)
		if !ok || headers["Host"] != "cdn.example.com" {
			t.Errorf("ws-opts.headers = %#v", opts["headers"])
		}
		alpn, ok := p["alpn"].([]any)
		if !ok || len(alpn) != 2 || alpn[0] != "h2" || alpn[1] != "http/1.1" {
			t.Errorf("alpn = %#v", p["alpn"])
		}
		if _, ok := p["reality-opts"]; ok {
			t.Error("tls 节点不该有 reality-opts")
		}
	})

	t.Run("vless+grpc+tls", func(t *testing.T) {
		p := byName["grpc-vless"]
		if p == nil {
			t.Fatal("缺少 grpc-vless")
		}
		if p["network"] != "grpc" {
			t.Errorf("network = %v", p["network"])
		}
		opts, ok := p["grpc-opts"].(map[string]any)
		if !ok || opts["grpc-service-name"] != "grpcsvc" {
			t.Errorf("grpc-opts = %#v", p["grpc-opts"])
		}
	})

	t.Run("vmess+tcp+none", func(t *testing.T) {
		p := byName["vmess-tcp"]
		if p == nil {
			t.Fatal("缺少 vmess-tcp")
		}
		if p["type"] != "vmess" || p["uuid"] != linkTestVMessUUID {
			t.Errorf("vmess 字段 = %#v", p)
		}
		if p["alterId"] != 0 {
			t.Errorf("alterId = %#v, want 0", p["alterId"])
		}
		if p["cipher"] != "auto" {
			t.Errorf("cipher = %v", p["cipher"])
		}
		for _, absent := range []string{"tls", "servername", "alpn", "skip-cert-verify", "reality-opts"} {
			if _, ok := p[absent]; ok {
				t.Errorf("security=none 不该有 %s: %#v", absent, p[absent])
			}
		}
	})

	t.Run("trojan+tcp+tls", func(t *testing.T) {
		p := byName["tcp-trojan"]
		if p == nil {
			t.Fatal("缺少 tcp-trojan")
		}
		if p["type"] != "trojan" || p["password"] != "tjpass" {
			t.Errorf("trojan 字段 = %#v", p)
		}
		if _, ok := p["uuid"]; ok {
			t.Error("trojan 不该有 uuid")
		}
		if p["skip-cert-verify"] != true {
			t.Errorf("skip-cert-verify = %v", p["skip-cert-verify"])
		}
	})

	t.Run("trojan+grpc+tls", func(t *testing.T) {
		p := byName["grpc-trojan"]
		if p == nil {
			t.Fatal("缺少 grpc-trojan")
		}
		if p["network"] != "grpc" {
			t.Errorf("network = %v", p["network"])
		}
		opts, ok := p["grpc-opts"].(map[string]any)
		if !ok || opts["grpc-service-name"] != "svc" {
			t.Errorf("grpc-opts = %#v", p["grpc-opts"])
		}
	})

	t.Run("ss-2022", func(t *testing.T) {
		p := byName["ss2022"]
		if p == nil {
			t.Fatal("缺少 ss2022")
		}
		if p["type"] != "ss" {
			t.Errorf("type = %v", p["type"])
		}
		if p["cipher"] != "2022-blake3-aes-128-gcm" {
			t.Errorf("cipher = %v", p["cipher"])
		}
		if p["password"] != "AAECAwQFBgcICQoLDA0ODw==" {
			t.Errorf("password = %v", p["password"])
		}
	})

	t.Run("wireguard", func(t *testing.T) {
		p := byName["wg"]
		if p == nil {
			t.Fatal("缺少 wg")
		}
		if p["type"] != "wireguard" {
			t.Errorf("type = %v", p["type"])
		}
		// IR 里是 RawURL base64,mihomo 只收带填充的标准 base64。
		if p["private-key"] != "V2whiAfjuJKAfimCWSgogDW7OesBIJSISZGjiRWRmX8=" {
			t.Errorf("private-key = %v", p["private-key"])
		}
		if p["public-key"] != "fh0bUIH0q9EjI8syMy2iDT1pwocGBoFcbWmauu1wc3o=" {
			t.Errorf("public-key = %v", p["public-key"])
		}
		if p["ip"] != "10.0.0.7/32" {
			t.Errorf("ip = %v", p["ip"])
		}
		if _, ok := p["network"]; ok {
			t.Errorf("wireguard 是 L3,不该有 network 字段: %#v", p)
		}
	})

	t.Run("vless+xhttp", func(t *testing.T) {
		p := byName["xhttp-vless"]
		if p == nil {
			t.Fatal("缺少 xhttp-vless")
		}
		if p["network"] != "xhttp" {
			t.Errorf("network = %v", p["network"])
		}
		opts, ok := p["xhttp-opts"].(map[string]any)
		if !ok {
			t.Fatalf("xhttp-opts = %#v", p["xhttp-opts"])
		}
		if opts["path"] != "/x" || opts["host"] != "cdn.example.com" || opts["mode"] != "packet-up" {
			t.Errorf("xhttp-opts = %#v", opts)
		}
	})

	t.Run("vless+httpupgrade", func(t *testing.T) {
		p := byName["httpupgrade-vless"]
		if p == nil {
			t.Fatal("缺少 httpupgrade-vless")
		}
		if p["network"] != "httpupgrade" {
			t.Errorf("network = %v", p["network"])
		}
		opts, ok := p["httpupgrade-opts"].(map[string]any)
		if !ok {
			t.Fatalf("httpupgrade-opts = %#v", p["httpupgrade-opts"])
		}
		if opts["path"] != "/u" || opts["host"] != "cdn2.example.com" {
			t.Errorf("httpupgrade-opts = %#v", opts)
		}
	})
}

// TestClashMetaOnlyFields 覆盖 client-fingerprint 只在 meta 版出现。
func TestClashMetaOnlyFields(t *testing.T) {
	eps := clashTestEndpoints()

	metaRaw, err := Clash(eps, true, nil)
	if err != nil {
		t.Fatalf("Clash(meta) 报错: %v", err)
	}
	plainRaw, err := Clash(eps, false, nil)
	if err != nil {
		t.Fatalf("Clash() 报错: %v", err)
	}

	if !bytes.Contains(metaRaw, []byte("client-fingerprint")) {
		t.Error("meta 版应当输出 client-fingerprint")
	}
	if bytes.Contains(plainRaw, []byte("client-fingerprint")) {
		t.Error("非 meta 版不该输出 client-fingerprint")
	}
	// REALITY 结构两者一致,只是原版 Clash 不认。
	for _, raw := range [][]byte{metaRaw, plainRaw} {
		if !bytes.Contains(raw, []byte("reality-opts")) {
			t.Error("两种版本都应写出 reality-opts")
		}
	}
}

// TestClashOmitsEmptyRealityShortID 覆盖空 RealitySID 不输出 short-id。
func TestClashOmitsEmptyRealityShortID(t *testing.T) {
	raw, err := Clash([]Endpoint{{
		Remark: "r", Address: "1.2.3.4", Port: 443,
		Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
		Network: model.NetworkTCP, Security: model.SecurityReality,
		SNI: "www.microsoft.com", Fingerprint: "chrome", RealityPub: "PUB",
	}}, true, nil)
	if err != nil {
		t.Fatalf("Clash() 报错: %v", err)
	}
	if bytes.Contains(raw, []byte("short-id")) {
		t.Errorf("空 RealitySID 不该输出 short-id:\n%s", raw)
	}
}

// TestClashDeterministic 覆盖同一输入多次渲染字节一致。
func TestClashDeterministic(t *testing.T) {
	eps := clashTestEndpoints()
	for _, meta := range []bool{false, true} {
		first, err := Clash(eps, meta, nil)
		if err != nil {
			t.Fatalf("Clash() 报错: %v", err)
		}
		for i := 0; i < 5; i++ {
			next, err := Clash(eps, meta, nil)
			if err != nil {
				t.Fatalf("Clash() 报错: %v", err)
			}
			if !bytes.Equal(first, next) {
				t.Fatalf("meta=%v 第 %d 次渲染与首次不一致:\n%s\n---\n%s", meta, i, first, next)
			}
		}
	}
}

// TestClashEmpty 覆盖没有节点时的输出仍是一份可解析的订阅。
func TestClashEmpty(t *testing.T) {
	raw, err := Clash(nil, false, nil)
	if err != nil {
		t.Fatalf("Clash() 报错: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("yaml 解析失败: %v\n%s", err, raw)
	}
	proxies, ok := doc["proxies"].([]any)
	if !ok {
		t.Fatalf("proxies 不是列表: %#v", doc["proxies"])
	}
	if len(proxies) != 0 {
		t.Errorf("proxies = %#v, want 空", proxies)
	}
	if _, ok := doc["proxy-groups"].([]any); !ok {
		t.Error("空订阅也应有 proxy-groups")
	}
	if _, ok := doc["rules"].([]any); !ok {
		t.Error("空订阅也应有 rules")
	}
}

// TestClashMetaEmptyProxies 覆盖 meta 版模板在零节点时也能正常产出:
// proxies 段插进去但为空,模板其余部分原样带出。
func TestClashMetaEmptyProxies(t *testing.T) {
	raw, err := Clash(nil, true, nil)
	if err != nil {
		t.Fatalf("Clash(nil, true, nil) 报错: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("yaml 解析失败: %v\n%s", err, raw)
	}
	proxies, ok := doc["proxies"].([]any)
	if !ok {
		t.Fatalf("proxies 不是列表: %#v", doc["proxies"])
	}
	if len(proxies) != 0 {
		t.Errorf("proxies = %#v, want 空", proxies)
	}
	if _, ok := doc["rule-providers"].(map[string]any); !ok {
		t.Error("meta 版空订阅也应有 rule-providers")
	}
	if _, ok := doc["dns"].(map[string]any); !ok {
		t.Error("meta 版空订阅也应有 dns")
	}
}

// TestClashRejectsUnsupported 覆盖不支持的输入明确报错。
func TestClashRejectsUnsupported(t *testing.T) {
	cases := []struct {
		name string
		ep   Endpoint
	}{
		{
			name: "协议",
			ep: Endpoint{
				Protocol: model.Protocol("trojan-go"), Network: model.NetworkTCP,
				Security: model.SecurityNone, Address: "1.2.3.4", Port: 1,
			},
		},
		{
			name: "传输",
			ep: Endpoint{
				Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
				Network: model.Network("kcp"), Security: model.SecurityNone,
				Address: "1.2.3.4", Port: 1,
			},
		},
		{
			name: "安全层",
			ep: Endpoint{
				Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
				Network: model.NetworkTCP, Security: model.Security("xtls"),
				Address: "1.2.3.4", Port: 1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Clash([]Endpoint{tc.ep}, false, nil); err == nil {
				t.Fatal("应当报错,却返回 nil")
			}
		})
	}
}
