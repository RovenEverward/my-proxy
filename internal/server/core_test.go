package server

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestQUICValidation(t *testing.T) {
	valid := func(p model.Protocol) model.Inbound {
		return model.Inbound{NodeID: 1, Protocol: p, Port: 443, CertFile: "/cert.pem", KeyFile: "/key.pem"}
	}
	for _, p := range []model.Protocol{model.ProtocolHysteria2, model.ProtocolTUIC} {
		in := valid(p)
		if msg := validateInbound(&in); msg != "" {
			t.Fatal(msg)
		}
		if in.Network != model.NetworkUDP || in.Security != model.SecurityTLS {
			t.Fatalf("defaults: %+v", in)
		}
		if p == model.ProtocolTUIC && in.CongestionControl != "bbr" {
			t.Fatalf("congestion: %+v", in)
		}
	}
	cases := []func(*model.Inbound){
		func(in *model.Inbound) { in.Network = model.NetworkTCP },
		func(in *model.Inbound) { in.Security = model.SecurityNone },
		func(in *model.Inbound) { in.CertFile = "" },
		func(in *model.Inbound) { in.KeyFile = "" },
		func(in *model.Inbound) { in.Flow = "xtls-rprx-vision" },
		func(in *model.Inbound) { in.Mux = true },
		func(in *model.Inbound) { in.Fragment = true },
		func(in *model.Inbound) { in.WSPath = "/ws" },
		func(in *model.Inbound) { in.GRPCService = "grpc" },
		func(in *model.Inbound) { in.XHTTPMode = "auto" },
		func(in *model.Inbound) { in.RealityDest = "example.com:443" },
		func(in *model.Inbound) { in.CongestionControl = "wrong" },
	}
	for i, mutate := range cases {
		in := valid(model.ProtocolTUIC)
		mutate(&in)
		if msg := validateInbound(&in); msg == "" {
			t.Fatalf("invalid case %d accepted", i)
		}
	}
	for _, cc := range []string{"bbr", "cubic", "new_reno"} {
		in := valid(model.ProtocolTUIC)
		in.CongestionControl = cc
		if msg := validateInbound(&in); msg != "" {
			t.Fatal(msg)
		}
	}
	in := valid(model.ProtocolHysteria2)
	in.CongestionControl = "bbr"
	if msg := validateInbound(&in); msg == "" {
		t.Fatal("hy2 congestion accepted")
	}
}

func TestCoreMetadataAPIIsolation(t *testing.T) {
	e := newEnv(t)
	n := model.Node{Name: "hk", Address: "203.0.113.1"}
	if err := e.st.CreateNode(&n); err != nil {
		t.Fatal(err)
	}
	route := fmt.Sprintf("/api/nodes/%d/cores/sing-box", n.ID)
	code, raw := e.api("GET", route, nil)
	if code != 200 {
		t.Fatalf("GET %d %s", code, raw)
	}
	var d model.CoreDeployment
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Core != model.CoreSingBox || d.APIPort != 10086 {
		t.Fatalf("defaults: %+v", d)
	}
	now := time.Now().UTC().Truncate(time.Second)
	d.APIPort = 11086
	if code, raw := e.api("PUT", route, d); code != 200 {
		t.Fatalf("configure before deploy: %d %s", code, raw)
	}
	if err := e.st.SetCoreDeployed(n.ID, model.CoreSingBox, "real-hash", now); err != nil {
		t.Fatal(err)
	}
	d.DeployedHash = "forged"
	d.NodeID = 999
	d.Core = model.CoreXray
	d.APIPort = 11086
	code, raw = e.api("PUT", route, d)
	if code != 200 {
		t.Fatalf("PUT %d %s", code, raw)
	}
	got, err := e.st.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeployedHash != "real-hash" || !got.DeployedAt.Equal(now) || got.APIPort != 11086 {
		t.Fatalf("PUT metadata: %+v", got)
	}
	xr, err := e.st.GetCoreDeployment(n.ID, model.CoreXray)
	if err != nil {
		t.Fatal(err)
	}
	if xr.DeployedHash != "" || xr.APIPort != 10085 {
		t.Fatalf("Xray changed: %+v", xr)
	}
	for i, change := range []func(*model.CoreDeployment){
		func(d *model.CoreDeployment) { d.APIPort = xr.APIPort },
		func(d *model.CoreDeployment) { d.ConfigPath = xr.ConfigPath },
		func(d *model.CoreDeployment) { d.ServiceName = xr.ServiceName + ".service" },
		func(d *model.CoreDeployment) { d.ConfigPath = "/etc/../etc/config.json" },
		func(d *model.CoreDeployment) { d.ServiceName = "bad/service" },
		func(d *model.CoreDeployment) { d.APIPort = -1 },
	} {
		bad := *got
		change(&bad)
		code, raw = e.api("PUT", route, bad)
		if code != 400 {
			t.Fatalf("invalid %d: %d %s", i, code, raw)
		}
	}
	changed := *got
	changed.APIPort = 12086
	code, raw = e.api("PUT", route, changed)
	if code != 409 {
		t.Fatalf("deployed metadata change: %d %s", code, raw)
	}
	n.APIPort = 11086
	code, raw = e.api("PUT", fmt.Sprintf("/api/nodes/%d", n.ID), n)
	if code != 400 {
		t.Fatalf("old node route isolation: %d %s", code, raw)
	}
	code, _ = e.api("GET", fmt.Sprintf("/api/nodes/%d/cores/invalid", n.ID), nil)
	if code != 400 {
		t.Fatalf("invalid core %d", code)
	}
	code, _ = e.api("GET", "/api/nodes/9999/cores/sing-box", nil)
	if code != 404 {
		t.Fatalf("missing node %d", code)
	}
}

func TestQUICInboundAPIPersistence(t *testing.T) {
	e := newEnv(t)
	n := model.Node{Name: "hk", Address: "203.0.113.1"}
	if err := e.st.CreateNode(&n); err != nil {
		t.Fatal(err)
	}
	in := model.Inbound{NodeID: n.ID, Protocol: model.ProtocolTUIC, Port: 443, CertFile: "/cert.pem", KeyFile: "/key.pem"}
	code, raw := e.api("POST", "/api/inbounds", in)
	if code != 201 {
		t.Fatalf("create %d %s", code, raw)
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.GetInbound(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Network != model.NetworkUDP || got.Security != model.SecurityTLS || got.CongestionControl != "bbr" {
		t.Fatalf("defaults persisted: %+v", got)
	}
	in.CongestionControl = "new_reno"
	code, raw = e.api("PUT", fmt.Sprintf("/api/inbounds/%d", in.ID), in)
	if code != 200 {
		t.Fatalf("update %d %s", code, raw)
	}
	got, err = e.st.GetInbound(in.ID)
	if err != nil || got.CongestionControl != "new_reno" {
		t.Fatalf("update persisted: %+v %v", got, err)
	}
}
