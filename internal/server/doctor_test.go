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

// 漂移的节点检查项要带 node_id 与 ui_action=apply,好让界面在旁边放「应用到节点」;
// Detail 文案一字不改 —— CLI 的 mpx doctor 还在按原文输出。正常节点只带 node_id。
func TestDoctorNodeChecksCarryNodeIDAndApplyAction(t *testing.T) {
	ops := &fakeNodeOps{}
	e := newEnvWith(t, ops)
	normal := e.createNode("normal", "normal.example.com")
	drift := e.createNode("drift", "drift.example.com")
	if err := e.st.SetNodeDeployed(normal.ID, "same", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetNodeDeployed(drift.ID, "deployed", time.Now()); err != nil {
		t.Fatal(err)
	}
	// 两台共用一份现场读数:normal 与下发一致,drift 被改过。
	ops.check = &model.NodeStatus{NodeID: normal.ID, Reachable: true, Active: true, ObservedHash: "same"}

	code, raw := e.api("GET", "/api/doctor", nil)
	if code != 200 {
		t.Fatalf("doctor: %d %s", code, raw)
	}
	report := decodeJSON[model.DoctorReport](t, raw)

	got := findCheck(t, report, "节点 normal")
	if got.NodeID != normal.ID {
		t.Errorf("正常节点 node_id: got %d want %d", got.NodeID, normal.ID)
	}
	if got.UIAction != "" {
		t.Errorf("正常节点不该给 ui_action: %q", got.UIAction)
	}

	drifted := findCheck(t, report, "节点 drift")
	if drifted.NodeID != drift.ID {
		t.Errorf("漂移节点 node_id: got %d want %d", drifted.NodeID, drift.ID)
	}
	if drifted.UIAction != "apply" {
		t.Errorf("漂移节点该给 ui_action=apply: %q", drifted.UIAction)
	}
	if drifted.Status != model.DoctorWarn || drifted.Detail != "配置和下发的不一致(节点上被改过)" {
		t.Errorf("漂移节点文案/结论被改了: %+v", drifted)
	}
}

// 连不上的节点走早返回:带 node_id,但不带 ui_action —— 界面没法替用户下发。
func TestDoctorUnreachableNodeHasNodeIDWithoutApplyAction(t *testing.T) {
	ops := &fakeNodeOps{check: &model.NodeStatus{Reachable: false, Error: "dial tcp: i/o timeout"}}
	e := newEnvWith(t, ops)
	n := e.createNode("down", "down.example.com")

	code, raw := e.api("GET", "/api/doctor", nil)
	if code != 200 {
		t.Fatalf("doctor: %d %s", code, raw)
	}
	report := decodeJSON[model.DoctorReport](t, raw)
	c := findCheck(t, report, "节点 down")
	if c.NodeID != n.ID {
		t.Errorf("连不上的节点 node_id: got %d want %d", c.NodeID, n.ID)
	}
	if c.UIAction != "" {
		t.Errorf("连不上的节点不该给 ui_action: %q", c.UIAction)
	}
}
