package render

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

const (
	linkTestUUID      = "11111111-2222-3333-4444-555555555555"
	linkTestVMessUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

// TestLinksExact 用精确字符串比对 vless / trojan 分享链接。
func TestLinksExact(t *testing.T) {
	cases := []struct {
		name string
		ep   Endpoint
		want string
	}{
		{
			name: "vless+tcp+reality",
			ep: Endpoint{
				Remark: "Reality", Address: "1.2.3.4", Port: 443,
				Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
				Network: model.NetworkTCP, Security: model.SecurityReality,
				SNI: "www.microsoft.com", Fingerprint: "chrome",
				RealityPub: "PBK_PUBLIC_KEY", RealitySID: "0123abcd",
			},
			want: "vless://" + linkTestUUID + "@1.2.3.4:443?" +
				"security=reality&type=tcp&sni=www.microsoft.com&fp=chrome&pbk=PBK_PUBLIC_KEY&sid=0123abcd#Reality",
		},
		{
			name: "vless+ws+tls",
			ep: Endpoint{
				Remark: "ws tls", Address: "example.com", Port: 8443,
				Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
				Network: model.NetworkWS, Path: "/ws", Host: "cdn.example.com",
				Security: model.SecurityTLS, SNI: "example.com", Fingerprint: "chrome",
				ALPN: []string{"h2", "http/1.1"},
			},
			want: "vless://" + linkTestUUID + "@example.com:8443?" +
				"security=tls&type=ws&path=%2Fws&host=cdn.example.com&sni=example.com&fp=chrome&alpn=h2%2Chttp%2F1.1#ws%20tls",
		},
		{
			name: "vless+grpc+tls",
			ep: Endpoint{
				Remark: "grpc", Address: "1.2.3.4", Port: 2053,
				Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
				Network: model.NetworkGRPC, Path: "grpcsvc", Host: "authority.example.com",
				Security: model.SecurityTLS, SNI: "sni.example.com", Fingerprint: "chrome",
			},
			want: "vless://" + linkTestUUID + "@1.2.3.4:2053?" +
				"security=tls&type=grpc&serviceName=grpcsvc&authority=authority.example.com&mode=gun&sni=sni.example.com&fp=chrome#grpc",
		},
		{
			name: "vless+tcp+tls+flow",
			ep: Endpoint{
				Remark: "vision", Address: "1.2.3.4", Port: 443,
				Protocol: model.ProtocolVLESS, UUID: linkTestUUID, Flow: "xtls-rprx-vision",
				Network: model.NetworkTCP, Security: model.SecurityTLS,
				SNI: "example.com", Fingerprint: "chrome",
			},
			want: "vless://" + linkTestUUID + "@1.2.3.4:443?" +
				"security=tls&type=tcp&flow=xtls-rprx-vision&sni=example.com&fp=chrome#vision",
		},
		{
			name: "trojan+tcp+tls",
			ep: Endpoint{
				Remark: "tj", Address: "9.9.9.9", Port: 443,
				Protocol: model.ProtocolTrojan, Password: "p@ss:w/rd",
				Network: model.NetworkTCP, Security: model.SecurityTLS,
				SNI: "tj.example.com", AllowInsecure: true,
			},
			want: "trojan://p%40ss:w%2Frd@9.9.9.9:443?" +
				"security=tls&type=tcp&sni=tj.example.com&allowInsecure=1#tj",
		},
		{
			name: "trojan+grpc+tls",
			ep: Endpoint{
				Remark: "TJ gRPC", Address: "tj.example.com", Port: 443,
				Protocol: model.ProtocolTrojan, Password: "pass",
				Network: model.NetworkGRPC, Path: "svc", Host: "authority.tj.com",
				Security: model.SecurityTLS, SNI: "tj.example.com",
				ALPN: []string{"h2"}, Fingerprint: "firefox",
			},
			want: "trojan://pass@tj.example.com:443?" +
				"security=tls&type=grpc&serviceName=svc&authority=authority.tj.com&mode=gun&sni=tj.example.com&fp=firefox&alpn=h2#TJ%20gRPC",
		},
		{
			name: "trojan+tcp+none 不带安全层参数",
			ep: Endpoint{
				Remark: "plain", Address: "1.2.3.4", Port: 8443,
				Protocol: model.ProtocolTrojan, Password: "1234",
				Network: model.NetworkTCP, Security: model.SecurityNone,
			},
			want: "trojan://1234@1.2.3.4:8443?security=none&type=tcp#plain",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Links([]Endpoint{tc.ep})
			if len(got) != 1 {
				t.Fatalf("Links() 条数 = %d,想要 1", len(got))
			}
			if got[0] != tc.want {
				t.Errorf("Links() =\n  %q\nwant\n  %q", got[0], tc.want)
			}
		})
	}
}

// TestLinksBracketIPv6 覆盖裸 IPv6 进 vless / trojan 链接后带方括号,
// 并且 host:port 能被 net.SplitHostPort 拆回。IPv4 与域名不变由
// TestLinksExact 的精确字符串锁定。
func TestLinksBracketIPv6(t *testing.T) {
	cases := []Endpoint{
		{
			Remark: "v6 vless", Address: "2001:db8::1", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkTCP, Security: model.SecurityNone,
		},
		{
			Remark: "v6 trojan", Address: "2001:db8::1", Port: 8443,
			Protocol: model.ProtocolTrojan, Password: "pw",
			Network: model.NetworkTCP, Security: model.SecurityNone,
		},
	}
	for _, ep := range cases {
		link := Links([]Endpoint{ep})[0]
		if !strings.Contains(link, "@[2001:db8::1]:") {
			t.Errorf("%s 链接里的 IPv6 没加方括号: %s", ep.Protocol, link)
		}
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("%s 链接解析失败: %v", ep.Protocol, err)
		}
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			t.Fatalf("%s 链接的 %q 无法拆成 host:port: %v", ep.Protocol, u.Host, err)
		}
		if host != "2001:db8::1" || port != strconv.Itoa(ep.Port) {
			t.Errorf("%s 拆出 (%q, %q), want (2001:db8::1, %d)", ep.Protocol, host, port, ep.Port)
		}
	}
}

// TestLinksVMessJSON 解开 vmess 链接的 base64,精确比对 JSON 文本。
func TestLinksVMessJSON(t *testing.T) {
	cases := []struct {
		name     string
		ep       Endpoint
		wantJSON string
	}{
		{
			name: "vmess+ws+tls",
			ep: Endpoint{
				Remark: "vmess ws", Address: "1.2.3.4", Port: 443,
				Protocol: model.ProtocolVMess, UUID: linkTestVMessUUID,
				Network: model.NetworkWS, Path: "/vm", Host: "vm.example.com",
				Security: model.SecurityTLS, SNI: "vm.example.com",
				ALPN: []string{"h2"},
			},
			wantJSON: `{"add":"1.2.3.4","aid":"0","alpn":"h2","host":"vm.example.com",` +
				`"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","net":"ws","path":"/vm",` +
				`"port":443,"ps":"vmess ws","scy":"auto","sni":"vm.example.com",` +
				`"tls":"tls","type":"none","v":"2"}`,
		},
		{
			name: "vmess+tcp+none",
			ep: Endpoint{
				Remark: "vm", Address: "5.6.7.8", Port: 8080,
				Protocol: model.ProtocolVMess, UUID: linkTestVMessUUID,
				Network: model.NetworkTCP, Security: model.SecurityNone,
			},
			wantJSON: `{"add":"5.6.7.8","aid":"0","id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",` +
				`"net":"tcp","port":8080,"ps":"vm","scy":"auto","tls":"none","type":"none","v":"2"}`,
		},
		{
			name: "vmess+grpc+reality",
			ep: Endpoint{
				Remark: "vm reality", Address: "1.2.3.4", Port: 443,
				Protocol: model.ProtocolVMess, UUID: linkTestVMessUUID,
				Network: model.NetworkGRPC, Path: "gsvc", Host: "authority.example.com",
				Security: model.SecurityReality, SNI: "www.microsoft.com",
				Fingerprint: "chrome", RealityPub: "PUB", RealitySID: "sid1",
			},
			wantJSON: `{"add":"1.2.3.4","aid":"0","fp":"chrome","host":"authority.example.com",` +
				`"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","net":"grpc","path":"gsvc","pbk":"PUB",` +
				`"port":443,"ps":"vm reality","scy":"auto","sid":"sid1","sni":"www.microsoft.com",` +
				`"tls":"reality","type":"none","v":"2"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Links([]Endpoint{tc.ep})
			if len(got) != 1 {
				t.Fatalf("Links() 条数 = %d,想要 1", len(got))
			}
			if !strings.HasPrefix(got[0], "vmess://") {
				t.Fatalf("链接前缀不对: %q", got[0])
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got[0], "vmess://"))
			if err != nil {
				t.Fatalf("base64 解码失败: %v", err)
			}
			if string(raw) != tc.wantJSON {
				t.Errorf("vmess JSON =\n  %s\nwant\n  %s", raw, tc.wantJSON)
			}
		})
	}
}

// TestLinksOmitsEmptyParams 覆盖空 RealitySID / 空 ALPN 不输出对应参数。
func TestLinksOmitsEmptyParams(t *testing.T) {
	t.Run("空 RealitySID 不输出 sid", func(t *testing.T) {
		link := Links([]Endpoint{{
			Remark: "r", Address: "1.2.3.4", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkTCP, Security: model.SecurityReality,
			SNI: "www.microsoft.com", Fingerprint: "chrome",
			RealityPub: "PUB", RealitySID: "",
		}})[0]
		if strings.Contains(link, "sid=") {
			t.Errorf("空 RealitySID 不应输出 sid: %s", link)
		}
		if !strings.Contains(link, "pbk=PUB") {
			t.Errorf("pbk 应当保留: %s", link)
		}
	})

	t.Run("空 ALPN 不输出 alpn", func(t *testing.T) {
		link := Links([]Endpoint{{
			Remark: "r", Address: "1.2.3.4", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkWS, Path: "/ws",
			Security: model.SecurityTLS, SNI: "example.com", Fingerprint: "chrome",
		}})[0]
		if strings.Contains(link, "alpn=") {
			t.Errorf("空 ALPN 不应输出 alpn: %s", link)
		}
	})

	t.Run("security=none 不带 sni/fp/pbk/sid", func(t *testing.T) {
		link := Links([]Endpoint{{
			Remark: "r", Address: "1.2.3.4", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkTCP, Security: model.SecurityNone,
			SNI: "example.com", Fingerprint: "chrome", RealityPub: "PUB", RealitySID: "sid",
		}})[0]
		for _, banned := range []string{"sni=", "fp=", "pbk=", "sid="} {
			if strings.Contains(link, banned) {
				t.Errorf("security=none 不应输出 %q: %s", banned, link)
			}
		}
	})

	t.Run("tcp 不带 path/host", func(t *testing.T) {
		link := Links([]Endpoint{{
			Remark: "r", Address: "1.2.3.4", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkTCP, Security: model.SecurityNone,
			Path: "/leftover", Host: "leftover.example.com",
		}})[0]
		if strings.Contains(link, "path=") || strings.Contains(link, "host=") {
			t.Errorf("tcp 不应输出 path/host: %s", link)
		}
	})
}

// TestLinksRemarkEscaping 覆盖备注的 URL 编码:空格是 %20,不是 +。
func TestLinksRemarkEscaping(t *testing.T) {
	link := Links([]Endpoint{{
		Remark: "a b+c/中", Address: "1.2.3.4", Port: 443,
		Protocol: model.ProtocolTrojan, Password: "pw",
		Network: model.NetworkTCP, Security: model.SecurityNone,
	}})[0]

	const wantFragment = "#a%20b%2Bc%2F%E4%B8%AD"
	if !strings.HasSuffix(link, wantFragment) {
		t.Errorf("备注编码 =\n  %s\nwant 后缀\n  %s", link, wantFragment)
	}
	// query 与 fragment 里都不该出现 form 编码的空格。
	if strings.Contains(link, "+") {
		t.Errorf("链接里不应出现 + : %s", link)
	}
}

// TestLinksSkipsUnsupported 覆盖不支持的协议被跳过而不是中断。
func TestLinksSkipsUnsupported(t *testing.T) {
	supported := Endpoint{
		Remark: "ok", Address: "1.2.3.4", Port: 443,
		Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
		Network: model.NetworkTCP, Security: model.SecurityNone,
	}
	eps := []Endpoint{
		{Remark: "ss", Protocol: model.Protocol("shadowsocks"), Address: "9.9.9.9", Port: 1},
		supported,
		{Remark: "hy2", Protocol: model.Protocol("hysteria2"), Address: "9.9.9.9", Port: 2},
	}

	got := Links(eps)
	if len(got) != 1 {
		t.Fatalf("Links() 条数 = %d,想要 1(其余被跳过)", len(got))
	}
	if want := Links([]Endpoint{supported})[0]; got[0] != want {
		t.Errorf("跳过后的链接 = %q, want %q", got[0], want)
	}
}

// TestLinksBase64 覆盖整体 base64 与前缀换行拼接一致。
func TestLinksBase64(t *testing.T) {
	eps := []Endpoint{
		{
			Remark: "one", Address: "1.2.3.4", Port: 443,
			Protocol: model.ProtocolVLESS, UUID: linkTestUUID,
			Network: model.NetworkTCP, Security: model.SecurityReality,
			SNI: "www.microsoft.com", Fingerprint: "chrome", RealityPub: "PUB",
		},
		{
			Remark: "two", Address: "5.6.7.8", Port: 8080,
			Protocol: model.ProtocolVMess, UUID: linkTestVMessUUID,
			Network: model.NetworkTCP, Security: model.SecurityNone,
		},
		{
			Remark: "three", Address: "9.9.9.9", Port: 8443,
			Protocol: model.ProtocolTrojan, Password: "pw",
			Network: model.NetworkWS, Path: "/t", Host: "t.example.com",
			Security: model.SecurityTLS, SNI: "t.example.com",
		},
	}

	raw, err := base64.StdEncoding.DecodeString(LinksBase64(eps))
	if err != nil {
		t.Fatalf("base64 解码失败: %v", err)
	}
	want := strings.Join(Links(eps), "\n")
	if string(raw) != want {
		t.Errorf("LinksBase64 解码 =\n%s\nwant\n%s", raw, want)
	}
}

// TestLinksBase64Empty 覆盖空输入。
func TestLinksBase64Empty(t *testing.T) {
	if got := LinksBase64(nil); got != "" {
		t.Errorf("LinksBase64(nil) = %q, want \"\"", got)
	}
	if got := Links(nil); len(got) != 0 {
		t.Errorf("Links(nil) = %v, want 空", got)
	}
}
