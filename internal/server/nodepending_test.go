package server

import (
	"strconv"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func pendingIDs(t *testing.T, e *env) []int64 {
	t.Helper()
	code, raw := e.api("GET", "/api/nodes/pending", nil)
	if code != 200 {
		t.Fatalf("pending: %d %s", code, raw)
	}
	var ids []int64
	for _, p := range decodeJSON[[]model.NodePending](t, raw) {
		ids = append(ids, p.NodeID)
	}
	return ids
}

func TestNodePendingLifecycle(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	a := e.createNode("a", "a.example.com")
	b := e.createNode("b", "b.example.com")
	if got := pendingIDs(t, e); len(got) != 0 {
		t.Fatalf("fresh: %v", got)
	}
	code, raw := e.api("POST", "/api/inbounds", model.Inbound{NodeID: a.ID, Protocol: model.ProtocolVLESS, Port: 8443, Network: model.NetworkWS, Security: model.SecurityNone, WSPath: "/ws"})
	if code != 201 {
		t.Fatalf("create: %d %s", code, raw)
	}
	in := decodeJSON[model.Inbound](t, raw)
	if got := pendingIDs(t, e); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("after create: %v", got)
	}
	if code, raw := e.api("POST", "/api/nodes/"+strconv.FormatInt(a.ID, 10)+"/sync", nil); code != 200 {
		t.Fatalf("sync: %d %s", code, raw)
	}
	if got := pendingIDs(t, e); len(got) != 0 {
		t.Fatalf("after sync: %v", got)
	}
	// 只改用户数上限:不用应用。
	in.MaxUsers = 5
	if code, raw := e.api("PUT", "/api/inbounds/"+strconv.FormatInt(in.ID, 10), in); code != 200 {
		t.Fatalf("update max: %d %s", code, raw)
	}
	if got := pendingIDs(t, e); len(got) != 0 {
		t.Fatalf("after max_users: %v", got)
	}
	in.Port = 8444
	if code, raw := e.api("PUT", "/api/inbounds/"+strconv.FormatInt(in.ID, 10), in); code != 200 {
		t.Fatalf("update port: %d %s", code, raw)
	}
	if got := pendingIDs(t, e); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("after port: %v", got)
	}
	if code, raw := e.api("POST", "/api/nodes/"+strconv.FormatInt(a.ID, 10)+"/sync", nil); code != 200 {
		t.Fatalf("sync: %d %s", code, raw)
	}
	if code, raw := e.api("DELETE", "/api/inbounds/"+strconv.FormatInt(in.ID, 10), nil); code != 200 {
		t.Fatalf("delete: %d %s", code, raw)
	}
	if got := pendingIDs(t, e); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("after delete: %v", got)
	}
	// 删掉节点,标记跟着没了。
	if code, raw := e.api("DELETE", "/api/nodes/"+strconv.FormatInt(a.ID, 10), nil); code != 200 && code != 204 {
		t.Fatalf("delete node: %d %s", code, raw)
	}
	if got := pendingIDs(t, e); len(got) != 0 {
		t.Fatalf("after node delete: %v", got)
	}
	_ = b
}
