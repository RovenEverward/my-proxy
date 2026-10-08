package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// fixedStatus 交回一份写死的心跳,把 /api/status 的回答钉住。
type fixedStatus struct{ st model.ControlStatus }

func (f fixedStatus) Snapshot(time.Time) model.ControlStatus { return f.st }

func TestStatusReportsHeartbeatCountsAndBackoffs(t *testing.T) {
	e := newEnv(t)

	// 各来一条:数量该一一对上,而不是把某一类数错。
	n := model.Node{Name: "hk", Address: "node.example.com"}
	if err := e.st.CreateNode(&n); err != nil {
		t.Fatalf("建节点: %v", err)
	}
	in := model.Inbound{NodeID: n.ID, Tag: "vless-ws", Protocol: model.ProtocolVLESS,
		Port: 8443, Network: model.NetworkWS}
	if err := e.st.CreateInbound(&in); err != nil {
		t.Fatalf("建入站: %v", err)
	}
	if err := e.st.CreateHost(&model.Host{InboundID: in.ID, Remark: "主入口",
		Address: "a.example.com", Port: 443}); err != nil {
		t.Fatalf("建对外入口: %v", err)
	}
	if err := e.st.CreateUser(&model.User{Username: "alice", Key: "key-alice", Enabled: true}); err != nil {
		t.Fatalf("建用户: %v", err)
	}

	started := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	lastTick := started.Add(2 * time.Minute)
	e.s.SetStatusSource(fixedStatus{st: model.ControlStatus{
		StartedAt:       started,
		UptimeSeconds:   3600,
		IntervalSeconds: 30,
		Ticks:           42,
		LastTickAt:      &lastTick,
		LastTickSeconds: 1.5,
		Backoffs: []model.NodeBackoff{{
			NodeID: n.ID, NodeName: "hk", Failures: 3, SkipUntil: lastTick.Add(time.Minute),
		}},
	}})

	code, body := e.api("GET", "/api/status", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/status: got %d: %s", code, body)
	}

	// 字段名是 CLI 与其它调用方看到的东西,单独钉一遍,别只靠结构体往返。
	for _, key := range []string{
		"started_at", "uptime_seconds", "interval_seconds", "ticks",
		"last_tick_at", "last_tick_seconds", "counts", "backoffs",
		"nodes", "inbounds", "hosts", "users",
		"node_id", "node_name", "failures", "skip_until",
	} {
		if !strings.Contains(string(body), `"`+key+`"`) {
			t.Errorf("响应里缺少字段 %s:%s", key, body)
		}
	}

	var got model.ControlStatus
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析响应: %v: %s", err, body)
	}
	if !got.StartedAt.Equal(started) {
		t.Errorf("启动时间 = %s, 想要 %s", got.StartedAt, started)
	}
	if got.UptimeSeconds != 3600 || got.IntervalSeconds != 30 || got.Ticks != 42 {
		t.Errorf("心跳数值不对:%+v", got)
	}
	if got.LastTickAt == nil || !got.LastTickAt.Equal(lastTick) {
		t.Errorf("最后 Tick 时间 = %v, 想要 %s", got.LastTickAt, lastTick)
	}
	if got.LastTickSeconds != 1.5 {
		t.Errorf("上一轮耗时 = %v, 想要 1.5", got.LastTickSeconds)
	}
	want := model.Counts{Nodes: 1, Inbounds: 1, Hosts: 1, Users: 1}
	if got.Counts != want {
		t.Errorf("对象数量 = %+v, 想要 %+v", got.Counts, want)
	}
	if len(got.Backoffs) != 1 || got.Backoffs[0].Failures != 3 || got.Backoffs[0].NodeName != "hk" {
		t.Errorf("退避状态不对:%+v", got.Backoffs)
	}
}

// 后台任务没接上时也要回答:心跳给零值、数量照给,CLI 的 status 不该整个失败。
func TestStatusWithoutBackgroundTaskStillAnswersCounts(t *testing.T) {
	e := newEnv(t)
	if err := e.st.CreateNode(&model.Node{Name: "hk", Address: "node.example.com"}); err != nil {
		t.Fatalf("建节点: %v", err)
	}

	code, body := e.api("GET", "/api/status", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/status: got %d: %s", code, body)
	}
	// 响应是缩进过的 JSON,断言数值前先把空白去掉,免得被排版噎住。
	flat := strings.ReplaceAll(string(body), " ", "")
	if !strings.Contains(flat, `"ticks":0`) {
		t.Errorf("没跑过时 Tick 次数该是 0:%s", body)
	}
	// 空退避要是 [],不是 null —— 两种空会让调用方被迫写两套判断。
	if !strings.Contains(flat, `"backoffs":[]`) {
		t.Errorf("没有退避节点时该给空数组:%s", body)
	}
	if !strings.Contains(flat, `"nodes":1`) {
		t.Errorf("数量照给:%s", body)
	}
}

// 状态接口是管理接口,和其它 /api 一样要 token。
func TestStatusRequiresToken(t *testing.T) {
	e := newEnv(t)

	if code, _ := e.request("GET", "/api/status", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("不带 token 应当 401,实际 %d", code)
	}
	code, _ := e.request("GET", "/api/status", nil, map[string]string{"Authorization": "Bearer wrong"})
	if code != http.StatusUnauthorized {
		t.Fatalf("token 不对应当 401,实际 %d", code)
	}
	if code, _ := e.api("GET", "/api/status", nil); code != http.StatusOK {
		t.Fatalf("带对 token 应当 200,实际 %d", code)
	}
}
