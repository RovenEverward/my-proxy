package render

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 订阅镜像渲染:节点配置里多一个把公开端口转给控制面的入站。
// xray 用 dokodemo-door,sing-box 用 direct(override_*);两条路径共用一个
// upstream(host:port),拆不开时直接报错 —— 指错上游的镜像比没有镜像更糟。

func TestServerConfigMirrorInbound(t *testing.T) {
	n := model.Node{ID: 1, Name: "jp", Address: "206.237.120.219", APIPort: 10085}
	mirrors := []model.SubMirror{{ID: 7, NodeID: 1, Port: 18080, Upstream: "203.0.113.1:8081"}}

	raw, err := ServerConfig(n, nil, nil, mirrors, time.Now())
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
			Port     int    `json:"port"`
			Settings struct {
				Address string `json:"address"`
				Port    int    `json:"port"`
				Network string `json:"network"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("解析: %v", err)
	}
	found := false
	for _, in := range cfg.Inbounds {
		if in.Tag == "sub-mirror-18080" {
			found = true
			if in.Protocol != "dokodemo-door" || in.Port != 18080 {
				t.Fatalf("镜像入站形状不对: %+v", in)
			}
			if in.Settings.Address != "203.0.113.1" || in.Settings.Port != 8081 || in.Settings.Network != "tcp" {
				t.Fatalf("上游转发不对: %+v", in.Settings)
			}
		}
	}
	if !found {
		t.Fatalf("没渲染出镜像入站")
	}
}

func TestSingBoxServerConfigMirrorInbound(t *testing.T) {
	n := model.Node{ID: 1}
	d := model.CoreDeployment{NodeID: 1, Core: model.CoreSingBox, APIPort: 10085}
	// 至少一个 QUIC 入站,否则 SingBoxServerConfig 走空配置路径。
	ins := []model.Inbound{{
		ID: 1, NodeID: 1, Tag: "hy2", Protocol: model.ProtocolHysteria2,
		Port: 8444, Network: model.NetworkUDP, Security: model.SecurityTLS,
		CertFile: "/c.pem", KeyFile: "/k.pem",
	}}
	mirrors := []model.SubMirror{{ID: 7, NodeID: 1, Port: 18080, Upstream: "203.0.113.1:8081"}}

	raw, err := SingBoxServerConfig(n, d, ins, nil, mirrors, time.Now())
	if err != nil {
		t.Fatalf("渲染: %v", err)
	}
	var cfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("解析: %v", err)
	}
	found := false
	for _, in := range cfg.Inbounds {
		if in["tag"] == "sub-mirror-18080" {
			found = true
			if in["type"] != "direct" || in["override_address"] != "203.0.113.1" || in["override_port"] != float64(8081) {
				t.Fatalf("sing-box 镜像入站不对: %v", in)
			}
		}
	}
	if !found {
		t.Fatalf("没渲染出 sing-box 镜像入站")
	}
}

func TestMirrorBadUpstream(t *testing.T) {
	n := model.Node{ID: 1, APIPort: 10085}
	mirrors := []model.SubMirror{{NodeID: 1, Port: 18080, Upstream: "not-a-hostport"}}
	if _, err := ServerConfig(n, nil, nil, mirrors, time.Now()); err == nil {
		t.Fatalf("上游不是 host:port 应当报错")
	}
}
