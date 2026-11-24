package server

import (
	"context"
	"net/http"
	"strconv"
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
