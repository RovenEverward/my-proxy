package render

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 测试辅助一律带 sc 前缀:本包还有别的 agent 在并发写测试文件,避免重名。

// scNow 是测试里固定的「现在」,避免用真实时钟导致断言漂移。
var scNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// scActiveUser 造一个可用用户;Key 固定,断言里能用 model.DeriveUUID 反推。
func scActiveUser(username string) model.User {
	return model.User{
		Username: username,
		Key:      "key-" + username,
		Enabled:  true,
	}
}

// scDecode 把渲染结果解析回 map,顺带断言它是合法 JSON 且顶层键齐全。
func scDecode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, raw)
	}
	for _, key := range []string{"log", "api", "stats", "policy", "inbounds", "outbounds", "routing"} {
		if _, ok := cfg[key]; !ok {
			t.Fatalf("顶层缺少 %q: %s", key, raw)
		}
	}
	return cfg
}

// scDig 按路径取值:字符串是 map 的键,整数是数组的下标。
func scDig(t *testing.T, root any, path ...any) any {
	t.Helper()
	cur := root
	for depth, step := range path {
		switch key := step.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("路径 %v 第 %d 步:当前值不是对象,而是 %T", path, depth, cur)
			}
			v, ok := m[key]
			if !ok {
				t.Fatalf("路径 %v 第 %d 步:对象里没有键 %q(实际键 %v)", path, depth, key, scKeys(m))
			}
			cur = v
		case int:
			s, ok := cur.([]any)
			if !ok {
				t.Fatalf("路径 %v 第 %d 步:当前值不是数组,而是 %T", path, depth, cur)
			}
			if key < 0 || key >= len(s) {
				t.Fatalf("路径 %v 第 %d 步:下标 %d 越界(长度 %d)", path, depth, key, len(s))
			}
			cur = s[key]
		default:
			t.Fatalf("路径 %v 第 %d 步:不支持的路径元素类型 %T", path, depth, step)
		}
	}
	return cur
}

// scKeys 列出对象的键,只用于报错信息。
func scKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// scDigEqual 断言路径上的值精确等于 want(不是「非空」)。
func scDigEqual(t *testing.T, root any, want any, path ...any) {
	t.Helper()
	got := scDig(t, root, path...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("路径 %v = %#v,期望 %#v", path, got, want)
	}
}

// scEmails 取出入站 clients 的 email 列表。
func scEmails(t *testing.T, cfg map[string]any, inboundIndex int) []string {
	t.Helper()
	clients := scDig(t, cfg, "inbounds", inboundIndex, "settings", "clients").([]any)
	out := make([]string, 0, len(clients))
	for i, c := range clients {
		m, ok := c.(map[string]any)
		if !ok {
			t.Fatalf("clients[%d] 不是对象: %T", i, c)
		}
		email, ok := m["email"].(string)
		if !ok {
			t.Fatalf("clients[%d] 缺 email: %v", i, m)
		}
		out = append(out, email)
	}
	return out
}

// scCheck 是一条「路径 → 期望值」的断言。
type scCheck struct {
	path []any
	want any
}

// scWGPub 取用户的 wireguard 公钥,与 serverconfig 里逐用户派生是同一份逻辑。
func scWGPub(key string) string {
	_, pub, err := model.DeriveWGKeypair(key)
	if err != nil {
		panic(err)
	}
	return pub
}

// scWGClientAddr 同 wgClientAddr:断言里用它反推,而不是把派生结果抄进来——
// 抄进来的常量只能证明「现在是这个值」,证明不了「和客户端拿的是同一个」。
func scWGClientAddr(serverAddr, key string) string {
	return wgClientAddr(serverAddr, key)
}

// TestServerConfig 是主表:逐字段断言各协议 / 传输 / 安全层的渲染结果。
func TestServerConfig(t *testing.T) {
	realityInbound := model.Inbound{
		ID: 1, Tag: "reality-in", Protocol: model.ProtocolVLESS, Port: 443,
		Network: model.NetworkTCP, Security: model.SecurityReality,
		SNI: "www.microsoft.com", RealityDest: "www.microsoft.com:443",
		RealityPriv: "REALITY-PRIVATE-KEY", ShortIDs: []string{"0123abcd", "deadbeef"},
		Flow: "xtls-rprx-vision",
	}
	wsTLSInbound := model.Inbound{
		ID: 2, Tag: "ws-in", Protocol: model.ProtocolVLESS, Port: 8443,
		Network: model.NetworkWS, Security: model.SecurityTLS, WSPath: "/ws",
		SNI: "cdn.example.com", ALPN: []string{"h2", "http/1.1"},
		CertFile: "/etc/xray/cert.pem", KeyFile: "/etc/xray/key.pem",
	}

	tests := []struct {
		name     string
		node     model.Node
		inbounds []model.Inbound
		users    []model.User
		checks   []scCheck
	}{
		{
			name:     "vless tcp reality",
			node:     model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{realityInbound},
			users:    []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "tag"}, "reality-in"},
				{[]any{"inbounds", 1, "listen"}, "0.0.0.0"},
				{[]any{"inbounds", 1, "port"}, float64(443)},
				{[]any{"inbounds", 1, "protocol"}, "vless"},
				{[]any{"inbounds", 1, "streamSettings", "network"}, "tcp"},
				{[]any{"inbounds", 1, "streamSettings", "security"}, "reality"},
				{[]any{"inbounds", 1, "streamSettings", "tcpSettings"}, map[string]any{}},
				{[]any{"inbounds", 1, "streamSettings", "realitySettings", "privateKey"}, "REALITY-PRIVATE-KEY"},
				{[]any{"inbounds", 1, "streamSettings", "realitySettings", "serverNames"}, []any{"www.microsoft.com"}},
				{[]any{"inbounds", 1, "streamSettings", "realitySettings", "shortIds"}, []any{"0123abcd", "deadbeef"}},
				{[]any{"inbounds", 1, "streamSettings", "realitySettings", "dest"}, "www.microsoft.com:443"},
				{[]any{"inbounds", 1, "streamSettings", "realitySettings", "xver"}, float64(0)},
				{[]any{"inbounds", 1, "streamSettings", "realitySettings", "show"}, false},
				{[]any{"inbounds", 1, "settings", "decryption"}, "none"},
				{[]any{"inbounds", 1, "settings", "clients", 0, "email"}, "alice"},
				{[]any{"inbounds", 1, "settings", "clients", 0, "id"}, model.DeriveUUID("key-alice")},
				{[]any{"inbounds", 1, "settings", "clients", 0, "flow"}, "xtls-rprx-vision"},
				{[]any{"inbounds", 1, "sniffing", "enabled"}, true},
				{[]any{"inbounds", 1, "sniffing", "destOverride"}, []any{"http", "tls", "quic"}},
				{[]any{"inbounds", 1, "sniffing", "routeOnly"}, true},
			},
		},
		{
			name:     "vless ws tls",
			node:     model.Node{ID: 1, APIPort: 12345},
			inbounds: []model.Inbound{wsTLSInbound},
			users:    []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "streamSettings", "network"}, "ws"},
				{[]any{"inbounds", 1, "streamSettings", "security"}, "tls"},
				{[]any{"inbounds", 1, "streamSettings", "wsSettings", "path"}, "/ws"},
				{[]any{"inbounds", 1, "streamSettings", "tlsSettings", "serverName"}, "cdn.example.com"},
				{[]any{"inbounds", 1, "streamSettings", "tlsSettings", "alpn"}, []any{"h2", "http/1.1"}},
				{[]any{"inbounds", 1, "streamSettings", "tlsSettings", "certificates"}, []any{
					map[string]any{"certificateFile": "/etc/xray/cert.pem", "keyFile": "/etc/xray/key.pem"},
				}},
				// 该入站没有 flow,clients 条目里不应出现 flow 键。
				{[]any{"inbounds", 1, "settings", "clients", 0}, map[string]any{
					"id": model.DeriveUUID("key-alice"), "email": "alice",
				}},
			},
		},
		{
			name: "vmess grpc none",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 3, Protocol: model.ProtocolVMess, Port: 2053,
				Network: model.NetworkGRPC, Security: model.SecurityNone, GRPCService: "grpc-svc",
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				// Tag 为空时按「协议-端口」拼。
				{[]any{"inbounds", 1, "tag"}, "vmess-2053"},
				{[]any{"inbounds", 1, "protocol"}, "vmess"},
				{[]any{"inbounds", 1, "streamSettings", "network"}, "grpc"},
				{[]any{"inbounds", 1, "streamSettings", "security"}, "none"},
				{[]any{"inbounds", 1, "streamSettings", "grpcSettings", "serviceName"}, "grpc-svc"},
				{[]any{"inbounds", 1, "settings", "clients", 0}, map[string]any{
					"id": model.DeriveUUID("key-alice"), "email": "alice", "alterId": float64(0),
				}},
			},
		},
		{
			name: "trojan tcp none",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 4, Tag: "trojan-in", Protocol: model.ProtocolTrojan, Port: 443,
				Network: model.NetworkTCP, Security: model.SecurityNone,
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "protocol"}, "trojan"},
				{[]any{"inbounds", 1, "settings", "clients", 0}, map[string]any{
					"password": model.DerivePassword("key-alice"), "email": "alice",
				}},
			},
		},
		{
			name: "ws path defaults to root",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 5, Tag: "ws-default", Protocol: model.ProtocolVLESS, Port: 80,
				Network: model.NetworkWS, Security: model.SecurityNone,
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "streamSettings", "wsSettings", "path"}, "/"},
			},
		},
		{
			// 传输与安全层的零值按 xray 默认的 tcp / none 处理,不报错。
			name: "empty network and security default to tcp and none",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 6, Tag: "defaults", Protocol: model.ProtocolVLESS, Port: 80,
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "streamSettings", "network"}, "tcp"},
				{[]any{"inbounds", 1, "streamSettings", "security"}, "none"},
				{[]any{"inbounds", 1, "streamSettings", "tcpSettings"}, map[string]any{}},
			},
		},
		{
			name: "shadowsocks-2022 tcp",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 7, Tag: "ss2022", Protocol: model.ProtocolShadowsocks, Port: 18443,
				Network: model.NetworkTCP, Security: model.SecurityNone,
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "protocol"}, "shadowsocks"},
				{[]any{"inbounds", 1, "settings", "method"}, "2022-blake3-aes-128-gcm"},
				{[]any{"inbounds", 1, "settings", "password"}, "2kbTQpLqQGxva91SzTnB4g=="},
				{[]any{"inbounds", 1, "settings", "clients", 0}, map[string]any{
					"password": model.DeriveSS2022Key("key-alice"), "email": "alice",
				}},
			},
		},
		{
			name: "wireguard",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 8, Tag: "wg", Protocol: model.ProtocolWireguard, Port: 51820,
				WGSecretKey: "WG-SERVER-PRIVATE-KEY", WGAddress: "10.0.0.1/24", WGMTU: 1420,
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "protocol"}, "wireguard"},
				{[]any{"inbounds", 1, "settings", "secretKey"}, "WG-SERVER-PRIVATE-KEY"},
				// xray 只收 /32 接口地址,网段的前缀不能带下来。
				{[]any{"inbounds", 1, "settings", "address"}, []any{"10.0.0.1/32"}},
				{[]any{"inbounds", 1, "settings", "mtu"}, float64(1420)},
				{[]any{"inbounds", 1, "settings", "peers", 0, "publicKey"}, scWGPub("key-alice")},
				// allowedIPs 与 wgClientAddr 派生的客户端地址一致。
				{[]any{"inbounds", 1, "settings", "peers", 0, "allowedIPs"}, []any{scWGClientAddr("10.0.0.1/24", "key-alice")}},
			},
		},
		{
			name: "vless xhttp none",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 9, Tag: "xh", Protocol: model.ProtocolVLESS, Port: 443,
				Network: model.NetworkXHTTP, Security: model.SecurityNone,
				XHTTPPath: "/x", XHTTPHost: "cdn.example.com", XHTTPMode: "packet-up",
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "streamSettings", "network"}, "xhttp"},
				{[]any{"inbounds", 1, "streamSettings", "xhttpSettings", "path"}, "/x"},
				{[]any{"inbounds", 1, "streamSettings", "xhttpSettings", "host"}, "cdn.example.com"},
				{[]any{"inbounds", 1, "streamSettings", "xhttpSettings", "mode"}, "packet-up"},
			},
		},
		{
			name: "vless splithttp none",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 10, Tag: "sh", Protocol: model.ProtocolVLESS, Port: 444,
				Network: model.NetworkSplitHTTP, Security: model.SecurityNone,
				XHTTPPath: "/s", XHTTPHost: "cdn2.example.com",
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "streamSettings", "network"}, "splithttp"},
				{[]any{"inbounds", 1, "streamSettings", "splithttpSettings", "path"}, "/s"},
				{[]any{"inbounds", 1, "streamSettings", "splithttpSettings", "host"}, "cdn2.example.com"},
			},
		},
		{
			name: "vless httpupgrade none",
			node: model.Node{ID: 1, APIPort: 10085},
			inbounds: []model.Inbound{{
				ID: 11, Tag: "hu", Protocol: model.ProtocolVLESS, Port: 445,
				Network: model.NetworkHTTPUpgrade, Security: model.SecurityNone,
				XHTTPPath: "/u", XHTTPHost: "cdn3.example.com",
			}},
			users: []model.User{scActiveUser("alice")},
			checks: []scCheck{
				{[]any{"inbounds", 1, "streamSettings", "network"}, "httpupgrade"},
				{[]any{"inbounds", 1, "streamSettings", "httpupgradeSettings", "path"}, "/u"},
				{[]any{"inbounds", 1, "streamSettings", "httpupgradeSettings", "host"}, "cdn3.example.com"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := ServerConfig(tt.node, tt.inbounds, tt.users, scNow)
			if err != nil {
				t.Fatalf("ServerConfig 返回错误: %v", err)
			}
			cfg := scDecode(t, raw)
			for _, c := range tt.checks {
				scDigEqual(t, cfg, c.want, c.path...)
			}
		})
	}
}

// TestServerConfig_UserFiltering 断言只有现在可用的用户进配置。
func TestServerConfig_UserFiltering(t *testing.T) {
	past := scNow.Add(-time.Hour)
	future := scNow.Add(time.Hour)

	users := []model.User{
		{Username: "active-user", Key: "k-active", Enabled: true, ExpireAt: &future},
		{Username: "disabled-user", Key: "k-disabled", Enabled: false},
		{Username: "expired-user", Key: "k-expired", Enabled: true, ExpireAt: &past},
		{Username: "limited-user", Key: "k-limited", Enabled: true, DataLimit: 100, UsedTraffic: 100},
		{Username: "other-inbound-user", Key: "k-other", Enabled: true, InboundIDs: []int64{99}},
	}

	node := model.Node{ID: 1, APIPort: 10085}
	inbounds := []model.Inbound{{
		ID: 7, Tag: "vless-7", Protocol: model.ProtocolVLESS, Port: 443,
		Network: model.NetworkTCP, Security: model.SecurityNone,
	}}

	raw, err := ServerConfig(node, inbounds, users, scNow)
	if err != nil {
		t.Fatalf("ServerConfig 返回错误: %v", err)
	}
	cfg := scDecode(t, raw)

	got := scEmails(t, cfg, 1)
	want := []string{"active-user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("clients email = %v,期望 %v", got, want)
	}
	if n := len(scDig(t, cfg, "inbounds", 1, "settings", "clients").([]any)); n != 1 {
		t.Errorf("clients 条目数 = %d,期望 1", n)
	}

	// 不能只断言「数量是 1」:把被筛掉的用户名整体在原文里找一遍更有说服力。
	for _, name := range []string{"disabled-user", "expired-user", "limited-user", "other-inbound-user"} {
		if strings.Contains(string(raw), name) {
			t.Errorf("输出里不应出现被筛掉的用户 %q:\n%s", name, raw)
		}
	}
	if strings.Contains(string(raw), model.DeriveUUID("k-disabled")) {
		t.Errorf("输出里不应出现停用用户的 UUID:\n%s", raw)
	}
}

// TestServerConfig_ClientsSortedByUsername 断言客户端按用户名排序。
func TestServerConfig_ClientsSortedByUsername(t *testing.T) {
	users := []model.User{
		scActiveUser("zoe"),
		scActiveUser("Alice"),
		scActiveUser("bob"),
	}
	inbounds := []model.Inbound{{
		ID: 1, Tag: "vless-1", Protocol: model.ProtocolVLESS, Port: 443,
		Network: model.NetworkTCP, Security: model.SecurityNone,
	}}
	raw, err := ServerConfig(model.Node{APIPort: 10085}, inbounds, users, scNow)
	if err != nil {
		t.Fatalf("ServerConfig 返回错误: %v", err)
	}
	cfg := scDecode(t, raw)
	want := []string{"Alice", "bob", "zoe"} // Go 字符串比较按字节,大写在前
	if got := scEmails(t, cfg, 1); !reflect.DeepEqual(got, want) {
		t.Errorf("clients email = %v,期望 %v", got, want)
	}
}

// TestServerConfig_Deterministic 断言同一输入多次调用字节一致,且与用户顺序无关。
func TestServerConfig_Deterministic(t *testing.T) {
	inbounds := []model.Inbound{
		{
			ID: 1, Tag: "vless-1", Protocol: model.ProtocolVLESS, Port: 443,
			Network: model.NetworkTCP, Security: model.SecurityReality,
			SNI: "www.microsoft.com", RealityDest: "www.microsoft.com:443",
			RealityPriv: "priv", ShortIDs: []string{"abcd"},
		},
		{
			ID: 2, Tag: "trojan-2", Protocol: model.ProtocolTrojan, Port: 8443,
			Network: model.NetworkWS, Security: model.SecurityTLS, WSPath: "/t",
			SNI: "cdn.example.com", CertFile: "/c.pem", KeyFile: "/k.pem",
		},
	}
	usersA := []model.User{scActiveUser("zoe"), scActiveUser("alice"), scActiveUser("bob")}
	usersB := []model.User{scActiveUser("bob"), scActiveUser("zoe"), scActiveUser("alice")}

	first, err := ServerConfig(model.Node{APIPort: 10085}, inbounds, usersA, scNow)
	if err != nil {
		t.Fatalf("ServerConfig 返回错误: %v", err)
	}
	second, err := ServerConfig(model.Node{APIPort: 10085}, inbounds, usersA, scNow)
	if err != nil {
		t.Fatalf("ServerConfig 返回错误: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("同一输入两次输出不一致:\n%s\n---\n%s", first, second)
	}

	reordered, err := ServerConfig(model.Node{APIPort: 10085}, inbounds, usersB, scNow)
	if err != nil {
		t.Fatalf("ServerConfig 返回错误: %v", err)
	}
	if string(first) != string(reordered) {
		t.Errorf("用户顺序不同导致输出不同:\n%s\n---\n%s", first, reordered)
	}
}

// TestServerConfig_APIPort 覆盖 API 入站的固定形态与默认端口。
func TestServerConfig_APIPort(t *testing.T) {
	tests := []struct {
		name     string
		apiPort  int
		wantPort float64
	}{
		{name: "显式端口", apiPort: 20000, wantPort: 20000},
		{name: "为零时用 10085", apiPort: 0, wantPort: 10085},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := ServerConfig(model.Node{ID: 1, APIPort: tt.apiPort}, nil, nil, scNow)
			if err != nil {
				t.Fatalf("ServerConfig 返回错误: %v", err)
			}
			cfg := scDecode(t, raw)

			// 没有业务入站时,只应剩下 API 入站。
			inbounds := scDig(t, cfg, "inbounds").([]any)
			if len(inbounds) != 1 {
				t.Fatalf("入站数 = %d,期望 1", len(inbounds))
			}
			apiInbound := scDig(t, cfg, "inbounds", 0).(map[string]any)
			wantInbound := map[string]any{
				"tag":      "api",
				"listen":   "127.0.0.1",
				"port":     tt.wantPort,
				"protocol": "dokodemo-door",
				"settings": map[string]any{"address": "127.0.0.1"},
			}
			if !reflect.DeepEqual(apiInbound, wantInbound) {
				t.Errorf("API 入站 = %#v,期望 %#v", apiInbound, wantInbound)
			}
		})
	}
}

// TestServerConfig_Skeleton 断言 log / api / stats / policy / outbounds / routing 落在该在的位置。
func TestServerConfig_Skeleton(t *testing.T) {
	raw, err := ServerConfig(model.Node{ID: 1, APIPort: 10085}, nil, nil, scNow)
	if err != nil {
		t.Fatalf("ServerConfig 返回错误: %v", err)
	}
	cfg := scDecode(t, raw)

	scDigEqual(t, cfg, map[string]any{"loglevel": "warning"}, "log")
	scDigEqual(t, cfg, map[string]any{
		"tag":      "api",
		"services": []any{"HandlerService", "StatsService"},
	}, "api")
	scDigEqual(t, cfg, map[string]any{}, "stats")
	scDigEqual(t, cfg, map[string]any{
		"0": map[string]any{"statsUserUplink": true, "statsUserDownlink": true},
	}, "policy", "levels")
	scDigEqual(t, cfg, map[string]any{
		"statsInboundUplink": true, "statsInboundDownlink": true,
	}, "policy", "system")
	scDigEqual(t, cfg, []any{
		map[string]any{
			"protocol": "freedom", "tag": "direct",
			"settings": map[string]any{"domainStrategy": "UseIPv4v6"},
		},
		map[string]any{"protocol": "blackhole", "tag": "block"},
	}, "outbounds")
	scDigEqual(t, cfg, []any{
		map[string]any{"type": "field", "inboundTag": []any{"api"}, "outboundTag": "api"},
		map[string]any{"type": "field", "ip": []any{"geoip:private"}, "outboundTag": "block"},
	}, "routing", "rules")
}

// TestServerConfig_OutboundIP 覆盖 direct 出站的 IP 版本偏好:每个取值翻成对应
// 的 domainStrategy;"asis" 不写 settings 键;blackhole 出站永远不带 settings。
func TestServerConfig_OutboundIP(t *testing.T) {
	cases := []struct {
		pref string
		want any // nil 表示 freedom 出站不带 settings 键
	}{
		{pref: "", want: "UseIPv4v6"},
		{pref: "v4v6", want: "UseIPv4v6"},
		{pref: "v4", want: "UseIPv4"},
		{pref: "v6", want: "UseIPv6"},
		{pref: "asis", want: nil},
	}
	for _, tc := range cases {
		t.Run("pref="+tc.pref, func(t *testing.T) {
			raw, err := ServerConfig(model.Node{ID: 1, APIPort: 10085, OutboundIP: tc.pref}, nil, nil, scNow)
			if err != nil {
				t.Fatalf("ServerConfig 返回错误: %v", err)
			}
			cfg := scDecode(t, raw)
			outbounds := scDig(t, cfg, "outbounds").([]any)
			direct, ok := outbounds[0].(map[string]any)
			if !ok {
				t.Fatalf("direct 出站不是对象: %T", outbounds[0])
			}
			if direct["protocol"] != "freedom" || direct["tag"] != "direct" {
				t.Fatalf("第一条出站不是 freedom/direct: %#v", direct)
			}
			if tc.want == nil {
				if _, ok := direct["settings"]; ok {
					t.Errorf("asis 不该写 settings,实际 %#v", direct["settings"])
				}
			} else {
				scDigEqual(t, cfg, tc.want, "outbounds", 0, "settings", "domainStrategy")
			}
			// blackhole 出站不能多出 settings。
			block, ok := outbounds[1].(map[string]any)
			if !ok {
				t.Fatalf("block 出站不是对象: %T", outbounds[1])
			}
			if _, ok := block["settings"]; ok {
				t.Errorf("blackhole 出站不该有 settings: %#v", block)
			}
		})
	}
}

// TestServerConfig_UnsupportedInput 断言不支持的协议 / 传输 / 安全层直接报错。
func TestServerConfig_UnsupportedInput(t *testing.T) {
	node := model.Node{APIPort: 10085}
	users := []model.User{scActiveUser("alice")}

	tests := []struct {
		name    string
		inbound model.Inbound
	}{
		{name: "不支持的协议", inbound: model.Inbound{ID: 1, Protocol: model.Protocol("vless-reality"), Port: 80}},
		{name: "不支持的传输", inbound: model.Inbound{ID: 1, Protocol: model.ProtocolVLESS, Network: model.Network("kcp"), Port: 80}},
		{name: "不支持的安全层", inbound: model.Inbound{ID: 1, Protocol: model.ProtocolVLESS, Security: model.Security("xtls"), Port: 80}},
		{name: "空协议", inbound: model.Inbound{ID: 1, Port: 80}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := ServerConfig(node, []model.Inbound{tt.inbound}, users, scNow)
			if err == nil {
				t.Fatalf("期望报错,却拿到了配置:\n%s", raw)
			}
			if raw != nil {
				t.Errorf("报错时不应返回配置: %s", raw)
			}
		})
	}
}
