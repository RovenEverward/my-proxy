package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func nodeStatusPath(id int64) string {
	return fmt.Sprintf("/api/nodes/%d", id)
}

func findCheck(t *testing.T, report model.DoctorReport, name string) model.DoctorCheck {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("自检里没有 %q 这条:%+v", name, report.Checks)
	return model.DoctorCheck{}
}

// 列表里带着缓存的状态,而且不会顺手去查节点 —— 一千台节点的一条 list 要是各
// ssh 一次,这条命令就没法用了。
func TestNodeListCarriesCachedStatus(t *testing.T) {
	ops := &fakeNodeOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")

	st := model.NodeStatus{
		NodeID: n.ID, CheckedAt: time.Now(), Reachable: true, Active: true, Version: "26.3.27",
	}
	if err := e.st.UpsertNodeStatus(st); err != nil {
		t.Fatalf("写状态: %v", err)
	}

	code, data := e.api("GET", "/api/nodes", nil)
	if code != http.StatusOK {
		t.Fatalf("列表: got %d %s", code, data)
	}
	views := decodeJSON[[]model.NodeView](e.t, data)
	if len(views) != 1 {
		t.Fatalf("节点数 = %d, want 1", len(views))
	}
	if views[0].Status == nil {
		t.Fatal("列表里没带状态")
	}
	if views[0].Status.Version != "26.3.27" {
		t.Errorf("版本 = %q, want 26.3.27", views[0].Status.Version)
	}
	if ops.checked != 0 {
		t.Errorf("列表去查了节点 %d 次,应该一次都不查", ops.checked)
	}
}

// "还没查过"和"查过但连不上"是两件事:前者 Status 为空,后者 Status 里带着
// 那句错误。混成一个,看的人就分不清是没数据还是真出事了。
func TestGetNodeOnlyChecksWhenAskedTo(t *testing.T) {
	ops := &fakeNodeOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")

	code, data := e.api("GET", nodeStatusPath(n.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("读节点: got %d %s", code, data)
	}
	if view := decodeJSON[model.NodeView](e.t, data); view.Status != nil {
		t.Fatalf("还没查过就有状态: %+v", view.Status)
	}
	if ops.checked != 0 {
		t.Errorf("不带 fresh 的读查了节点 %d 次", ops.checked)
	}

	code, data = e.api("GET", nodeStatusPath(n.ID)+"?fresh=1", nil)
	if code != http.StatusOK {
		t.Fatalf("现场查: got %d %s", code, data)
	}
	view := decodeJSON[model.NodeView](e.t, data)
	if view.Status == nil || !view.Status.Reachable {
		t.Fatalf("fresh=1 没给出状态: %+v", view.Status)
	}
	if ops.checked != 1 {
		t.Errorf("fresh=1 查了节点 %d 次, want 1", ops.checked)
	}
	if view.Status.NodeID != n.ID {
		t.Errorf("状态挂在了节点 %d 上, want %d", view.Status.NodeID, n.ID)
	}

	// 现场查的结果要落库:紧接着那次"读缓存"看到的就是它,这样"刚查过但列表
	// 里还是旧的"不会发生。
	code, data = e.api("GET", nodeStatusPath(n.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("再读: got %d %s", code, data)
	}
	if view := decodeJSON[model.NodeView](e.t, data); view.Status == nil {
		t.Error("现场查的结果没落库")
	}
	if ops.checked != 1 {
		t.Errorf("第二次读又查了一遍节点(共 %d 次)", ops.checked)
	}
}

// 健康的一轮:核心在跑、配置也是刚下发的,这台节点不该有任何问题。
func TestDoctorPassesOnAHealthyNode(t *testing.T) {
	ops := &fakeNodeOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	// fakeNodeOps 回的 ObservedHash 就是 "abcdef";基准写同一个值,才不算漂移。
	if err := e.st.SetNodeDeployed(n.ID, "abcdef", time.Now()); err != nil {
		t.Fatalf("记下发基准: %v", err)
	}

	code, data := e.api("GET", "/api/doctor", nil)
	if code != http.StatusOK {
		t.Fatalf("自检: got %d %s", code, data)
	}
	report := decodeJSON[model.DoctorReport](e.t, data)
	c := findCheck(t, report, "节点 hk")
	if c.Status != model.DoctorOK {
		t.Errorf("健康节点被判成 %s:%s", c.Status, c.Detail)
	}
	if report.HasFailure() {
		t.Errorf("有 fail 的检查:%+v", report.Checks)
	}
}

// 没下发过配置是"提醒",不是"故障":节点还在正常服务,只是控制面不知道它上面
// 跑的是哪一份。判成 fail 会让人以为节点坏了。
func TestDoctorWarnsWhenNothingWasEverDeployed(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	e.createNode("hk", "node.example.com")

	code, data := e.api("GET", "/api/doctor", nil)
	if code != http.StatusOK {
		t.Fatalf("自检: got %d %s", code, data)
	}
	report := decodeJSON[model.DoctorReport](e.t, data)
	c := findCheck(t, report, "节点 hk")
	if c.Status != model.DoctorWarn {
		t.Errorf("从没下发过的节点被判成 %s, want warn", c.Status)
	}
	if report.HasFailure() {
		t.Errorf("只是没下发过,不该有 fail:%+v", report.Checks)
	}
}

// 节点连不上要判 fail,并且给一句能照着做的事 —— 只有结论没有下一步的自检,
// 用的人还得回来问一遍。
func TestDoctorFailsOnUnreachableNode(t *testing.T) {
	ops := &fakeNodeOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	// NodeID 要跟着节点:状态是按 node_id 落库的,对不上就写不进去,自检会
	// 变成"检查失败"而不是"连不上"。
	ops.check = &model.NodeStatus{
		NodeID:    n.ID,
		Reachable: false,
		Error:     "ssh: connect to host node.example.com port 22: Connection refused",
	}

	code, data := e.api("GET", "/api/doctor", nil)
	if code != http.StatusOK {
		t.Fatalf("自检: got %d %s", code, data)
	}
	report := decodeJSON[model.DoctorReport](e.t, data)
	c := findCheck(t, report, "节点 hk")
	if c.Status != model.DoctorFail {
		t.Errorf("连不上的节点被判成 %s, want fail", c.Status)
	}
	if !report.HasFailure() {
		t.Error("有节点连不上,自检却说没问题")
	}
	if c.Hint == "" {
		t.Errorf("连不上却没给下一步该怎么办:%s", c.Detail)
	}
}
