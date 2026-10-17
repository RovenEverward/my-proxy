package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 界面显示的入口名必须就是订阅里的名字:没起名的跟随节点显示名,同一节点多个
// 默认名入口加协议后缀,手写 remark 原样。拿同一个用户的订阅端点逐个对照。
func TestSubscriptionNamesMatchSubscription(t *testing.T) {
	e := newEnv(t)
	n := model.Node{Name: "jp-1", Address: "198.51.100.7", SSHPort: 22}
	if err := e.st.CreateNode(&n); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SaveLocationMetadata("nodes", n.ID, model.LocationMetadata{Region: "JP", City: "东京", Letter: "A"}); err != nil {
		t.Fatal(err)
	}
	mk := func(in model.Inbound) model.Inbound {
		in.NodeID = n.ID
		in.Tag = in.TagOrDefault()
		if err := e.st.CreateInbound(&in); err != nil {
			t.Fatal(err)
		}
		return in
	}
	a := mk(model.Inbound{Protocol: model.ProtocolVLESS, Port: 443, Network: model.NetworkTCP, Security: model.SecurityNone})
	b := mk(model.Inbound{Protocol: model.ProtocolVLESS, Port: 2087, Network: model.NetworkWS, Security: model.SecurityNone, WSPath: "/ws"})
	named := model.Host{InboundID: b.ID, Remark: "备用线路", Address: "a.example.com"}
	if err := e.st.CreateHost(&named); err != nil {
		t.Fatal(err)
	}
	unnamed := model.Host{InboundID: b.ID, Address: "b.example.com"}
	if err := e.st.CreateHost(&unnamed); err != nil {
		t.Fatal(err)
	}

	user := &model.User{Username: "alice", Key: "k", Enabled: true}
	eps, err := e.s.endpointsFor(user)
	if err != nil {
		t.Fatal(err)
	}
	want := map[subNameKey]string{}
	for _, ep := range eps {
		want[subNameKey{ep.InboundID, ep.HostID}] = ep.Remark
	}

	code, body := e.api("GET", "/api/inbounds", nil)
	var ins []model.InboundView
	if code != http.StatusOK || json.Unmarshal(body, &ins) != nil {
		t.Fatalf("inbounds %d %s", code, body)
	}
	for _, in := range ins {
		if in.ID == a.ID && (in.SubName != want[subNameKey{a.ID, 0}] || !in.AutoName || in.SubName == "") {
			t.Fatalf("入站 %d 默认入口名 %q auto=%v,订阅里是 %q", in.ID, in.SubName, in.AutoName, want[subNameKey{a.ID, 0}])
		}
	}
	code, body = e.api("GET", "/api/inbounds/"+strconv.FormatInt(b.ID, 10)+"/hosts", nil)
	var hs []model.HostView
	if code != http.StatusOK || json.Unmarshal(body, &hs) != nil || len(hs) != 2 {
		t.Fatalf("hosts %d %s", code, body)
	}
	for _, h := range hs {
		w := want[subNameKey{b.ID, h.ID}]
		if h.SubName != w || w == "" {
			t.Fatalf("入口 %d 名字 %q,订阅里是 %q", h.ID, h.SubName, w)
		}
		if h.ID == named.ID && (h.AutoName || h.SubName != "备用线路") {
			t.Fatalf("手写名应原样且不算跟随: %+v", h)
		}
		if h.ID == unnamed.ID && (!h.AutoName || h.SubName == "") {
			t.Fatalf("没起名应跟随节点名: %+v", h)
		}
	}
}
