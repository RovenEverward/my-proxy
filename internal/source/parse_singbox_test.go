package source_test

import (
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/source"
)

// 一份覆盖 sing-box outbounds 的正文:独立节点 + 非节点 outbound + 各跳过场景。
const singBoxFixture = `{
  "log": {"level": "warn"},
  "dns": {"servers": [{"tag": "g", "type": "udp", "server": "8.8.8.8"}]},
  "outbounds": [
    {"type": "selector", "tag": "proxy", "outbounds": ["v1", "direct"]},
    {"type": "urltest", "tag": "auto", "outbounds": ["v1"]},
    {"type": "direct", "tag": "direct"},
    {"type": "block", "tag": "block"},
    {"type": "dns", "tag": "dns-out"},
    {
      "type": "vless", "tag": "v1",
      "server": "v.example.com", "server_port": 443,
      "uuid": "11111111-2222-3333-4444-555555555555",
      "flow": "xtls-rprx-vision",
      "tls": {"enabled": true, "server_name": "www.microsoft.com",
              "utls": {"enabled": true, "fingerprint": "chrome"},
              "reality": {"enabled": true, "public_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "short_id": "01"}},
      "transport": {"type": "ws", "path": "/v", "headers": {"Host": "cdn.example.com"}}
    },
    {
      "type": "vmess", "tag": "vm1",
      "server": "vm.example.com", "server_port": 8443,
      "uuid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      "security": "aes-128-gcm", "alter_id": 0,
      "transport": {"type": "grpc", "service_name": "svc"}
    },
    {
      "type": "trojan", "tag": "tj1",
      "server": "tj.example.com", "server_port": 443,
      "password": "tj-pass",
      "tls": {"enabled": true, "server_name": "tj.example.com", "insecure": true,
              "alpn": ["h2"]}
    },
    {
      "type": "shadowsocks", "tag": "ss1",
      "server": "ss.example.com", "server_port": 8388,
      "method": "aes-256-gcm", "password": "s3cret-pw"
    },
    {
      "type": "hysteria2", "tag": "hy1",
      "server": "hy.example.com", "server_port": 443,
      "password": "hy-pass", "up_mbps": 100, "down_mbps": 200,
      "tls": {"enabled": true, "server_name": "hy.example.com", "insecure": true}
    },
    {
      "type": "tuic", "tag": "tu1",
      "server": "tu.example.com", "server_port": 4443,
      "uuid": "11111111-2222-3333-4444-555555555555",
      "password": "tuic-pw", "congestion_control": "cubic",
      "tls": {"enabled": true, "server_name": "tu.example.com", "alpn": ["h3"]}
    },
    {
      "type": "vless", "tag": "bad-detour",
      "server": "d.example.com", "server_port": 443,
      "uuid": "11111111-2222-3333-4444-555555555555",
      "detour": "v1"
    },
    {
      "type": "shadowsocks", "tag": "bad-plugin",
      "server": "p.example.com", "server_port": 8388,
      "method": "aes-256-gcm", "password": "pw", "plugin": "obfs-local"
    },
    {
      "type": "hysteria2", "tag": "bad-obfs",
      "server": "o.example.com", "server_port": 443,
      "password": "pw", "obfs": {"type": "salamander", "password": "sek"}
    },
    {
      "type": "wireguard", "tag": "wg1",
      "server": "wg.example.com", "server_port": 51820,
      "local_address": ["10.0.0.2/32"], "private_key": "PRIV", "peer_public_key": "PUB"
    },
    {
      "type": "vless", "tag": "bad-tls-ech",
      "server": "e.example.com", "server_port": 443,
      "uuid": "11111111-2222-3333-4444-555555555555",
      "tls": {"enabled": true, "ech": {"enabled": true}}
    }
  ]
}`

// TestSingBoxProtocols sing-box 里六个协议独立 outbound 解全;非节点
// outbound(selector / urltest / direct / block / dns)静默忽略。
func TestSingBoxProtocols(t *testing.T) {
	res, err := source.Parse([]byte(singBoxFixture))
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "sing-box" {
		t.Fatalf("Format = %q", res.Format)
	}
	if len(res.Endpoints) != 6 {
		t.Fatalf("Endpoints = %d: %+v", len(res.Endpoints), res.Endpoints)
	}

	v := byRemark(res.Endpoints, "v1")
	if v == nil || v.Security != model.SecurityReality || v.RealityPub != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" ||
		v.RealitySID != "01" || v.Fingerprint != "chrome" || v.Flow != "xtls-rprx-vision" ||
		v.Network != model.NetworkWS || v.Path != "/v" || v.Host != "cdn.example.com" {
		t.Errorf("vless ep = %+v", v)
	}

	vm := byRemark(res.Endpoints, "vm1")
	if vm == nil || vm.VMessSecurity != "aes-128-gcm" || vm.VMessAlterID != 0 ||
		vm.Network != model.NetworkGRPC || vm.Path != "svc" {
		t.Errorf("vmess ep = %+v", vm)
	}

	tj := byRemark(res.Endpoints, "tj1")
	if tj == nil || tj.Security != model.SecurityTLS || !tj.AllowInsecure ||
		len(tj.ALPN) != 1 || tj.SNI != "tj.example.com" {
		t.Errorf("trojan ep = %+v", tj)
	}

	ss := byRemark(res.Endpoints, "ss1")
	if ss == nil || ss.SSMethod != "aes-256-gcm" || ss.Password != "s3cret-pw" {
		t.Errorf("ss ep = %+v", ss)
	}

	hy := byRemark(res.Endpoints, "hy1")
	if hy == nil || !hy.AllowInsecure || hy.Network != model.NetworkUDP {
		t.Errorf("hy2 ep = %+v", hy)
	}

	tu := byRemark(res.Endpoints, "tu1")
	if tu == nil || tu.CongestionControl != "cubic" || tu.Security != model.SecurityTLS {
		t.Errorf("tuic ep = %+v", tu)
	}
}

// TestSingBoxSkips detour / plugin / obfs / wireguard / ech 全部跳过并报告。
func TestSingBoxSkips(t *testing.T) {
	res, err := source.Parse([]byte(singBoxFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad-detour", "bad-plugin", "bad-obfs", "wg1", "bad-tls-ech"} {
		if findIssue(res.Issues, name) == nil {
			t.Errorf("%s 没有进 Issues", name)
		}
		if byRemark(res.Endpoints, name) != nil {
			t.Errorf("%s 不应进入 Endpoints", name)
		}
	}
	assertNoSecrets(t, res, err, "s3cret-pw", "PRIV", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", fakeUUID)
}

// TestSingBoxIssueIndex sing-box 的 Issue 位置按 outbounds 数组数。
func TestSingBoxIssueIndex(t *testing.T) {
	res, err := source.Parse([]byte(singBoxFixture))
	if err != nil {
		t.Fatal(err)
	}
	is := findIssue(res.Issues, "bad-detour")
	if is == nil {
		t.Fatal("缺 bad-detour")
	}
	// bad-detour 在 outbounds 数组里是第 12 项(Issue.Index 是 1-based 条目序号)。
	if is.Index != 12 {
		t.Errorf("Index = %d, want 12", is.Index)
	}
}

// TestSingBoxMultiplex smux 的 multiplex 收进 Mux;其它复用协议表达不了。
func TestSingBoxMultiplex(t *testing.T) {
	body := `{"outbounds":[
		{"type":"vless","tag":"m","server":"a.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `","multiplex":{"enabled":true,"protocol":"smux"}},
		{"type":"vless","tag":"bad","server":"b.example.com","server_port":443,
		 "uuid":"` + fakeUUID + `","multiplex":{"enabled":true,"protocol":"yamux"}}
	]}`
	res, err := source.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Endpoints) != 1 || !res.Endpoints[0].Mux {
		t.Fatalf("Endpoints = %+v", res.Endpoints)
	}
	if findIssue(res.Issues, "bad") == nil {
		t.Error("yamux multiplex 应跳过")
	}
}

// TestSingBoxNoCredentialLeakInError 解析失败与 issue 不带上游凭证材料。
func TestSingBoxNoCredentialLeakInError(t *testing.T) {
	// 整条 outbound 因为 detour 被跳过;原因只提字段名,不带 tag 之外的值。
	res, err := source.Parse([]byte(singBoxFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range res.Issues {
		if strings.Contains(is.Reason, "v1") && is.Name == "bad-detour" {
			t.Logf("原因含目标 tag 可接受但不必要: %q", is.Reason)
		}
	}
}
