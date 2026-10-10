package server

import (
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestDoctorQUICFormats(t *testing.T) {
	for _, protocol := range []model.Protocol{model.ProtocolHysteria2, model.ProtocolTUIC} {
		t.Run(string(protocol), func(t *testing.T) {
			ops := &fakeNodeOps{}
			e := newEnvWith(t, ops)
			n := e.createNode("quic", "node.example.com")
			code, raw := e.api("POST", "/api/inbounds", model.Inbound{NodeID: n.ID, Protocol: protocol, Port: 443, CertFile: "/cert.pem", KeyFile: "/key.pem"})
			if code != 201 {
				t.Fatalf("create QUIC: %d %s", code, raw)
			}
			in := decodeJSON[model.Inbound](t, raw)
			e.createUser("subscriber")
			if err := e.st.SetCoreDeployed(n.ID, model.CoreSingBox, "quic-hash", time.Now()); err != nil {
				t.Fatal(err)
			}
			ops.check = &model.NodeStatus{NodeID: n.ID, Reachable: true, Active: true, ObservedHash: "quic-hash", Cores: []model.CoreStatus{{Core: model.CoreSingBox, Active: true, StatsAvailable: true, ObservedHash: "quic-hash"}}}
			code, raw = e.api("GET", "/api/doctor", nil)
			if code != 200 {
				t.Fatalf("doctor: %d %s", code, raw)
			}
			report := decodeJSON[model.DoctorReport](t, raw)
			if report.HasFailure() {
				t.Fatalf("healthy QUIC failed: %+v", report.Checks)
			}
			for _, format := range []string{"links", string(model.FormatBase64), "clash-meta", "sing-box"} {
				if c := findCheck(t, report, "订阅 "+format); c.Status != model.DoctorOK {
					t.Fatalf("compatible %s: %+v", format, c)
				}
			}
			for _, format := range []string{"clash", "xray"} {
				if c := findCheck(t, report, "订阅 "+format); c.Status != model.DoctorWarn || !strings.Contains(c.Detail, "不支持当前 QUIC 候选") {
					t.Fatalf("incompatible %s: %+v", format, c)
				}
			}
			settings, err := e.st.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			settings.Templates = map[string]string{"sing-box": "{"}
			if err := e.st.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			code, raw = e.api("GET", "/api/doctor", nil)
			if code != 200 {
				t.Fatalf("doctor broken template: %d %s", code, raw)
			}
			report = decodeJSON[model.DoctorReport](t, raw)
			if c := findCheck(t, report, "订阅 sing-box"); c.Status != model.DoctorFail {
				t.Fatalf("broken compatible template hidden: %+v", c)
			}
			if err := e.st.DeleteInbound(in.ID); err != nil {
				t.Fatal(err)
			}
			settings.Templates = nil
			if err := e.st.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			code, raw = e.api("GET", "/api/doctor", nil)
			if code != 200 {
				t.Fatalf("doctor after deletion: %d %s", code, raw)
			}
			report = decodeJSON[model.DoctorReport](t, raw)
			if c := findCheck(t, report, "节点 quic"); c.Status != model.DoctorOK {
				t.Fatalf("last QUIC removal invented Xray requirement: %+v", c)
			}
			if err := e.st.SetNodeDeployed(n.ID, "xray-hash", time.Now()); err != nil {
				t.Fatal(err)
			}
			ops.check.ObservedHash = "unexpected-xray-hash"
			code, raw = e.api("GET", "/api/doctor", nil)
			if code != 200 {
				t.Fatalf("doctor historical Xray: %d %s", code, raw)
			}
			report = decodeJSON[model.DoctorReport](t, raw)
			if c := findCheck(t, report, "节点 quic"); c.Status != model.DoctorWarn || !strings.Contains(c.Detail, "配置和下发的不一致") {
				t.Fatalf("historical Xray drift lost: %+v", c)
			}
		})
	}
}
