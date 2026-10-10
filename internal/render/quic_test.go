package render

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

func quicFixture() (model.Node, []model.Inbound, []model.User) {
	node := model.Node{ID: 1, Name: "QUIC", Address: "127.0.0.1"}
	ins := []model.Inbound{
		{ID: 1, NodeID: 1, Tag: "hy2", Protocol: model.ProtocolHysteria2, Port: 19231, Network: model.NetworkUDP, Security: model.SecurityTLS, SNI: "localhost", ALPN: []string{"h3"}, CertFile: "cert.pem", KeyFile: "key.pem"},
		{ID: 2, NodeID: 1, Tag: "tuic", Protocol: model.ProtocolTUIC, Port: 19232, Network: model.NetworkUDP, Security: model.SecurityTLS, SNI: "localhost", ALPN: []string{"h3"}, CertFile: "cert.pem", KeyFile: "key.pem"},
		{ID: 3, NodeID: 1, Tag: "vless", Protocol: model.ProtocolVLESS, Port: 19239},
	}
	users := []model.User{scActiveUser("bob"), scActiveUser("alice"), {Username: "disabled", Key: "key-disabled"}}
	return node, ins, users
}

func TestQUICResolveAndFormats(t *testing.T) {
	node, ins, users := quicFixture()
	eps := Resolve(node, ins[:2], nil, &users[1])
	for i, ep := range eps {
		if ep.Network != model.NetworkUDP || ep.Security != model.SecurityTLS || ep.Password != model.DerivePassword(users[1].Key) {
			t.Fatalf("凭据/传输不一致: %+v", ep)
		}
		if i == 0 && ep.UUID != "" {
			t.Fatal("HY2 不应使用 UUID 认证")
		}
		if i == 1 && ep.UUID != model.DeriveUUID(users[1].Key) {
			t.Fatal("TUIC 缺 UUID")
		}
		ep.AllowInsecure = true
		ep.Fingerprint = "chrome"
		ep.Mux = true
		raw, err := ProbeClient(ep, 19234+i)
		if err != nil {
			t.Fatal(err)
		}
		cfg := decodeConfig(t, raw)
		ob := findOutbound(t, cfg, "probe")
		if len(outboundsOf(t, cfg)) != 1 || cfg["dns"] != nil {
			t.Fatal("probe 应为最小配置")
		}
		tls := ob["tls"].(map[string]any)
		if tls["insecure"] != true || tls["utls"] != nil || ob["multiplex"] != nil || ob["transport"] != nil {
			t.Fatalf("QUIC 混入 TCP 字段: %s", raw)
		}
		if i == 1 && ob["congestion_control"] != "bbr" {
			t.Fatal("TUIC 默认拥塞必须为 bbr")
		}
		full, err := SingBox([]Endpoint{ep}, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		assertSkeleton(t, decodeConfig(t, full), []string{ep.Remark})
		meta, err := Clash([]Endpoint{ep}, true, []byte("rule-providers: {}\nproxy-groups: []\nrules: [MATCH,DIRECT]\n"))
		if err != nil {
			t.Fatal(err)
		}
		var cm map[string]any
		if err := yaml.Unmarshal(meta, &cm); err != nil {
			t.Fatal(err)
		}
		p := cm["proxies"].([]any)[0].(map[string]any)
		if p["sni"] != "localhost" || p["network"] != nil || p["client-fingerprint"] != nil {
			t.Fatalf("mihomo QUIC 字段: %s", meta)
		}
		if i == 1 && p["congestion-controller"] != "bbr" {
			t.Fatal("mihomo 缺拥塞控制")
		}
		if _, err := Clash([]Endpoint{ep}, false, nil); err == nil || !strings.Contains(err.Error(), "QUIC") {
			t.Fatal("原版 Clash 应明确拒绝 QUIC")
		}
		if _, err := Xray([]Endpoint{ep}, nil); err == nil || !strings.Contains(err.Error(), "QUIC") {
			t.Fatal("Xray 应明确拒绝 QUIC")
		}
		links := Links([]Endpoint{ep})
		if len(links) != 1 {
			t.Fatal("QUIC 链接缺失")
		}
		link, err := url.Parse(links[0])
		if err != nil {
			t.Fatal(err)
		}
		if link.Scheme != string(ep.Protocol) || link.Host != "127.0.0.1:"+[]string{"19231", "19232"}[i] {
			t.Fatal(links)
		}
		if i == 0 && (link.User.Username() != ep.Password || link.Query().Get("insecure") != "1") {
			t.Fatal(links)
		}
		if i == 1 {
			pass, _ := link.User.Password()
			if pass != ep.Password || link.User.Username() != ep.UUID {
				t.Fatal(links)
			}
		}
	}
	ep := eps[1]
	ep.Address = "2001:db8::1"
	ep.Password = "pass:@/?# +"
	ep.Remark = "节点 #1"
	parsed, err := url.Parse(Links([]Endpoint{ep})[0])
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := parsed.User.Password()
	if parsed.Hostname() != ep.Address || pass != ep.Password || parsed.Fragment != ep.Remark {
		t.Fatal(parsed)
	}
}

func TestQUICServerFiltering(t *testing.T) {
	n, ins, users := quicFixture()
	users[0].InboundIDs = []int64{2}
	users = append(users, model.User{Username: "limited", Key: "limited", Enabled: true, DataLimit: 1, UsedTraffic: 1}, model.User{Username: "expired", Key: "expired", Enabled: true, ExpireAt: &scNow})
	// 用更晚的判定时刻覆盖过期过滤。
	now := scNow.Add(1)
	raw, err := SingBoxServerConfig(n, model.CoreDeployment{NodeID: n.ID, Core: model.CoreSingBox}, ins, users, now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := decodeConfig(t, raw)
	inbound := cfg["inbounds"].([]any)
	if len(inbound) != 2 {
		t.Fatalf("核心过滤失败: %s", raw)
	}
	hy := inbound[0].(map[string]any)["users"].([]any)
	tu := inbound[1].(map[string]any)["users"].([]any)
	if len(hy) != 1 || len(tu) != 2 {
		t.Fatalf("授权/状态过滤失败: %s", raw)
	}
	if hy[0].(map[string]any)["password"] != model.DerivePassword(users[1].Key) || hy[0].(map[string]any)["uuid"] != nil {
		t.Fatal("HY2 凭据错误")
	}
	if tu[0].(map[string]any)["uuid"] != model.DeriveUUID(users[1].Key) {
		t.Fatal("TUIC 凭据错误")
	}
	stats := cfg["experimental"].(map[string]any)["v2ray_api"].(map[string]any)
	if stats["listen"] != "127.0.0.1:10086" || !reflect.DeepEqual(stats["stats"].(map[string]any)["users"], []any{"alice", "bob"}) {
		t.Fatal(stats)
	}
	again, err := SingBoxServerConfig(n, model.CoreDeployment{}, ins, users, now)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("配置不稳定")
	}
	xr, err := ServerConfig(n, ins, users, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(xr), "hysteria2") || strings.Contains(string(xr), "tuic") {
		t.Fatal("Xray 服务端应跳过第二核心入站")
	}
}

func TestQUICRejectInvalid(t *testing.T) {
	n, ins, users := quicFixture()
	for _, mutate := range []func(*model.Inbound){
		func(in *model.Inbound) { in.Security = "" }, func(in *model.Inbound) { in.Security = model.SecurityReality },
		func(in *model.Inbound) { in.CertFile = "" }, func(in *model.Inbound) { in.KeyFile = "" }, func(in *model.Inbound) { in.Network = model.NetworkTCP }, func(in *model.Inbound) { in.Port = 0 },
	} {
		bad := ins[0]
		mutate(&bad)
		if _, err := SingBoxServerConfig(n, model.CoreDeployment{}, []model.Inbound{bad}, users, scNow); err == nil {
			t.Fatalf("接受无效入站: %+v", bad)
		}
	}
	ep := Resolve(n, ins[1:2], nil, &users[0])[0]
	for _, mutate := range []func(*Endpoint){func(ep *Endpoint) { ep.Security = model.SecurityNone }, func(ep *Endpoint) { ep.Network = model.NetworkTCP }, func(ep *Endpoint) { ep.Password = "" }, func(ep *Endpoint) { ep.UUID = "" }, func(ep *Endpoint) { ep.CongestionControl = "unknown" }} {
		bad := ep
		mutate(&bad)
		if _, err := ProbeClient(bad, 19234); err == nil {
			t.Fatal("接受无效 QUIC 端点")
		}
		if _, err := Clash([]Endpoint{bad}, true, nil); err == nil {
			t.Fatal("mihomo 接受无效 QUIC 端点")
		}
	}
	if _, err := ProbeClient(ep, 0); err == nil {
		t.Fatal("接受无效探测端口")
	}
}

// 显式指定目录才导出这次真实核心验证所用的渲染产物。
func TestQUICExportRuntimeFixtures(t *testing.T) {
	dir := os.Getenv("QUIC_RENDER_DIR")
	if dir == "" {
		t.Skip("设置 QUIC_RENDER_DIR 导出真实核心验证产物")
	}
	n, ins, users := quicFixture()
	for i := range ins[:2] {
		ins[i].CertFile = filepath.Join(dir, "cert.pem")
		ins[i].KeyFile = filepath.Join(dir, "key.pem")
	}
	write := func(name string, raw []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := SingBoxServerConfig(n, model.CoreDeployment{APIPort: 19233}, ins, users, scNow)
	write("render-server.json", raw, err)
	eps := Resolve(n, ins[:2], nil, &users[1])
	for i, ep := range eps {
		ep.AllowInsecure = true
		ep.Remark += "-" + string(ep.Protocol)
		eps[i] = ep
		raw, err := ProbeClient(ep, 19234+i)
		write(string(ep.Protocol)+"-render-probe.json", raw, err)
		template := []byte("mixed-port: " + []string{"19236", "19237"}[i] + "\nlog-level: warning\nrule-providers: {}\nproxy-groups: []\nrules:\n  - MATCH," + ep.Remark + "\n")
		raw, err = Clash([]Endpoint{ep}, true, template)
		write(string(ep.Protocol)+"-render-mihomo.yaml", raw, err)
	}
	raw, err = SingBox(eps, nil)
	write("render-full-singbox.json", raw, err)
	raw, err = Clash(eps, true, nil)
	write("render-full-mihomo.yaml", raw, err)
	write("render-links.txt", []byte(strings.Join(Links(eps), "\n")+"\n"), nil)
}
