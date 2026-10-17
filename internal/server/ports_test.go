package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/ports"
)

// portOps 在 fakeNodeOps 上加一份假的监听端口。
type portOps struct {
	fakeNodeOps
	tcp, udp map[int]string
	err      error
}

func (p *portOps) ListeningPorts(context.Context, model.Node) (map[int]string, map[int]string, error) {
	return p.tcp, p.udp, p.err
}

func TestInstallNodeStopsWhenInboundPortBusy(t *testing.T) {
	ops := &portOps{fakeNodeOps: fakeNodeOps{version: "v26.3.27"}, tcp: map[int]string{443: "nginx(进程 1234)", 22: "sshd(进程 1)"}}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	e.createInbound(n.ID, "", 443)

	code, data := e.api("POST", installPath(n, ""), nil)
	if code != http.StatusConflict {
		t.Fatalf("端口被占该回 409,得到 %d %s", code, data)
	}
	body := string(data)
	for _, want := range []string{"443 端口(TCP)已被 nginx(进程 1234)", "还没有改动这台节点", "ui_hint", "入站与入口"} {
		if !strings.Contains(body, want) {
			t.Errorf("响应缺 %q: %s", want, body)
		}
	}
	if ops.installs != 0 {
		t.Error("端口被占时不该开始装核心")
	}
}

func TestInstallNodeIgnoresCoreAndUnknown(t *testing.T) {
	cases := map[string]*portOps{
		"核心自己占着": {fakeNodeOps: fakeNodeOps{version: "v1"}, tcp: map[int]string{443: "xray(进程 9)"}},
		"看不出来":   {fakeNodeOps: fakeNodeOps{version: "v1"}, err: ports.ErrNoSS},
		"空着":     {fakeNodeOps: fakeNodeOps{version: "v1"}, tcp: map[int]string{22: "sshd(进程 1)"}},
	}
	for name, ops := range cases {
		e := newEnvWith(t, ops)
		n := e.createNode("hk", "node.example.com")
		e.createInbound(n.ID, "", 443)
		if code, data := e.api("POST", installPath(n, ""), nil); code != http.StatusOK {
			t.Errorf("%s: 该照常安装,得到 %d %s", name, code, data)
		}
	}
}

func TestNodePortsEndpoint(t *testing.T) {
	ops := &portOps{tcp: map[int]string{443: "caddy(进程 900)", 8443: "xray(进程 9)"}, udp: map[int]string{53: ports.Unknown}}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	code, data := e.api("GET", "/api/nodes/"+strconv.FormatInt(n.ID, 10)+"/ports", nil)
	if code != http.StatusOK {
		t.Fatalf("应 200,得到 %d %s", code, data)
	}
	var res struct {
		Known bool       `json:"known"`
		TCP   []PortView `json:"tcp"`
		UDP   []PortView `json:"udp"`
	}
	res = decodeJSON[struct {
		Known bool       `json:"known"`
		TCP   []PortView `json:"tcp"`
		UDP   []PortView `json:"udp"`
	}](t, data)
	if !res.Known || len(res.TCP) != 2 || res.TCP[0].Port != 443 || res.TCP[0].Core || !res.TCP[1].Core || len(res.UDP) != 1 {
		t.Errorf("结果不对: %s", data)
	}
	// 没接节点通道
	e2 := newEnv(t)
	n2 := e2.createNode("hk", "node.example.com")
	if code, _ := e2.api("GET", "/api/nodes/"+strconv.FormatInt(n2.ID, 10)+"/ports", nil); code != http.StatusNotImplemented {
		t.Errorf("没接节点通道该回 501,得到 %d", code)
	}
}
