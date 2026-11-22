package render

import (
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestResolveNamedRemarks(t *testing.T) {
	user := &model.User{Username: "alice", Key: "k"}
	node := model.Node{ID: 1, Name: "la-1", Address: "1.2.3.4"}
	reality := model.Inbound{ID: 10, NodeID: 1, Protocol: model.ProtocolVLESS, Port: 443, Network: model.NetworkTCP, Security: model.SecurityReality}
	ws := model.Inbound{ID: 11, NodeID: 1, Protocol: model.ProtocolVLESS, Port: 8443, Network: model.NetworkWS, Security: model.SecurityTLS}
	hy2 := model.Inbound{ID: 12, NodeID: 1, Protocol: model.ProtocolHysteria2, Port: 8444}
	hosts := map[int64][]model.Host{11: {{ID: 5, InboundID: 11, Remark: "{NODE_DISPLAY} 备用 / {NODE_NAME}"}}}

	eps := ResolveNamed(node, "🇺🇸 洛杉矶 A", []model.Inbound{reality, ws, hy2}, hosts, user)
	for _, ep := range eps {
		if ep.Protocol == model.ProtocolHysteria2 && !ep.AllowInsecure {
			t.Fatalf("hy2 AllowInsecure=false, want true for self-signed certs")
		}
	}

	// UC-27:挂了托管证书的 hy2 走正常 CA 校验,订阅不该再让客户端跳过。
	hy2c := hy2
	hy2c.ID = 14
	hy2c.CertPEM = "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n"
	for _, ep := range ResolveNamed(node, "", []model.Inbound{hy2c}, nil, user) {
		if ep.AllowInsecure {
			t.Fatal("挂了真实证书的 hy2 不该再 AllowInsecure")
		}
	}
	eps = DedupeRemarks(AddProtocolSuffix(eps))
	got := map[int64]string{}
	for _, ep := range eps {
		got[ep.InboundID] = ep.Remark
	}
	want := map[int64]string{
		10: "🇺🇸 洛杉矶 A · Reality",
		11: "🇺🇸 洛杉矶 A 备用 / la-1", // host 手写的 remark 不加后缀;{NODE_NAME} 仍是 name
		12: "🇺🇸 洛杉矶 A · Hy2",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("inbound %d: got %q want %q", id, got[id], w)
		}
	}

	// 单入口不加后缀;没有显示名退回 name。
	one := AddProtocolSuffix(ResolveNamed(node, "", []model.Inbound{reality}, nil, user))
	if len(one) != 1 || one[0].Remark != "la-1" {
		t.Fatalf("single: %+v", one)
	}
	// 同协议两个入口:后缀相同,交给 DedupeRemarks 加序号。
	r2 := reality
	r2.ID, r2.Port = 13, 444
	two := DedupeRemarks(AddProtocolSuffix(ResolveNamed(node, "🇺🇸 洛杉矶 A", []model.Inbound{reality, r2}, nil, user)))
	if two[0].Remark != "🇺🇸 洛杉矶 A · Reality" || two[1].Remark != "🇺🇸 洛杉矶 A · Reality 2" {
		t.Fatalf("dedupe: %q %q", two[0].Remark, two[1].Remark)
	}
}
