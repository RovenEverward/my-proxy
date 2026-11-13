package server

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// fakeBackoffOps 记下被调了几次、给什么答案。
type fakeBackoffOps struct {
	retries int
	gotNode model.Node
	err     error
	cleared []int64
}

func (f *fakeBackoffOps) RetryNodeBackoff(_ context.Context, n model.Node) error {
	f.retries++
	f.gotNode = n
	return f.err
}

func (f *fakeBackoffOps) ClearNodeBackoff(nodeID int64) {
	f.cleared = append(f.cleared, nodeID)
}

// 人工重试:经路由走到后台任务的重试口,通了回 200,不通把原因原样带回。
func TestRetryNodeBackoffRoute(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	n := e.createNode("hk", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/backoff/retry", n.ID)

	t.Run("没接后台任务通道时回 501", func(t *testing.T) {
		code, _ := e.api("POST", path, nil)
		if code != 501 {
			t.Fatalf("没接通道该 501,实际 %d", code)
		}
	})

	t.Run("试通回 200 并真的调了重试", func(t *testing.T) {
		ops := &fakeBackoffOps{}
		e.s.SetBackoffOps(ops)
		defer e.s.SetBackoffOps(nil)

		code, body := e.api("POST", path, nil)
		if code != 200 {
			t.Fatalf("重试成功该 200,实际 %d: %s", code, body)
		}
		if ops.retries != 1 || ops.gotNode.ID != n.ID {
			t.Fatalf("该重试到节点 %d 一次: %+v", n.ID, ops)
		}
	})

	t.Run("没试通把原因原样带回 502", func(t *testing.T) {
		e.s.SetBackoffOps(&fakeBackoffOps{err: fmt.Errorf("ssh: connect timeout")})
		defer e.s.SetBackoffOps(nil)

		code, body := e.api("POST", path, nil)
		if code != 502 {
			t.Fatalf("没试通该 502,实际 %d: %s", code, body)
		}
		if !bytes.Contains(body, []byte("connect timeout")) {
			t.Fatalf("原因该原样给回使用者: %s", body)
		}
	})

	t.Run("节点不存在回 404", func(t *testing.T) {
		e.s.SetBackoffOps(&fakeBackoffOps{})
		defer e.s.SetBackoffOps(nil)
		code, _ := e.api("POST", "/api/nodes/999/backoff/retry", nil)
		if code != 404 {
			t.Fatalf("不存在的节点该 404,实际 %d", code)
		}
	})
}

// fresh=1 现场确认可达 = 成功一次,该把退避清掉。
func TestFreshNodeStatusClearsBackoff(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	ops := &fakeBackoffOps{}
	e.s.SetBackoffOps(ops)
	n := e.createNode("hk", "node.example.com")

	code, _ := e.api("GET", fmt.Sprintf("/api/nodes/%d?fresh=1", n.ID), nil)
	if code != 200 {
		t.Fatalf("fresh 查询该 200,实际 %d", code)
	}
	if len(ops.cleared) != 1 || ops.cleared[0] != n.ID {
		t.Fatalf("可达确认该清一次退避: %v", ops.cleared)
	}
}
