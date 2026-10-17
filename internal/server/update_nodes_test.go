package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/xrayrelease"
)

// seedXrayNode 建一台只有 xray 核心的节点并写入观察到的版本。
//
// 没有入站、也没有 sing-box 下发记录的节点,updateCorePresent 恰好只给它出
// xray 一行;version 就落在 NodeStatus.Version 那格,updateNodes 从那里读当前
// 版本。idx 只用来拼出不重复的地址。
func seedXrayNode(t *testing.T, e *env, idx int, name, version string) model.Node {
	t.Helper()
	n := model.Node{Name: name, Address: fmt.Sprintf("203.0.113.%d", idx)}
	if err := e.st.CreateNode(&n); err != nil {
		t.Fatalf("建节点 %s: %v", name, err)
	}
	if err := e.st.UpsertNodeStatus(model.NodeStatus{NodeID: n.ID, Reachable: true, Version: version}); err != nil {
		t.Fatalf("写节点 %s 的状态: %v", name, err)
	}
	return n
}

// upgradeCalls 记假实现收到了哪些调用。任务 goroutine 在写、测试在断言,加锁。
type upgradeCalls struct {
	mu       sync.Mutex
	resolved []string
	upgraded []string
}

func (c *upgradeCalls) snapshot() (resolved, upgraded []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.resolved...), append([]string(nil), c.upgraded...)
}

// fakeXrayUpgrade 把 xray 那条路的两个实现点换成假的:不问 GitHub、不 ssh,只记账。
//
// failAt 里列的节点在升级那一步报错;block 非空时每次升级都先等它关上,用来把
// 任务摁在 running 状态里(409 用例靠它)。
func fakeXrayUpgrade(e *env, failAt map[string]error, block <-chan struct{}) *upgradeCalls {
	calls := &upgradeCalls{}
	e.s.resolveXray = func(_ context.Context, n model.Node, _ string) (string, string, error) {
		calls.mu.Lock()
		calls.resolved = append(calls.resolved, n.Name)
		calls.mu.Unlock()
		return "https://example.com/xray-linux-64.zip", strings.Repeat("0", 64), nil
	}
	e.s.upgradeXray = func(_ context.Context, n model.Node, _, _, _ string) (node.UpgradeResult, error) {
		if block != nil {
			<-block
		}
		calls.mu.Lock()
		calls.upgraded = append(calls.upgraded, n.Name)
		calls.mu.Unlock()
		if err, ok := failAt[n.Name]; ok {
			return node.UpgradeResult{}, err
		}
		// From 故意给一个和库里观察值不同的数:ok 事件里的 from 应当以它为准。
		return node.UpgradeResult{From: "25.1.1"}, nil
	}
	return calls
}

// fetchNodeUpdate 拉一次进度并解码,顺手把原始正文也交回来(分页用例要看大小)。
func fetchNodeUpdate(t *testing.T, e *env, since int64) (model.UpdateNodesProgress, []byte) {
	t.Helper()
	code, body := e.api("GET", fmt.Sprintf("/api/update/nodes?since=%d", since), nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update/nodes?since=%d: %d %s", since, code, body)
	}
	var p model.UpdateNodesProgress
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("解析进度: %v\n%s", err, body)
	}
	return p, body
}

// waitNodeUpdateFinish 轮询进度接口直到任务离开 running。任务在后台 goroutine
// 里跑,这里只等终态,不等固定时间。
func waitNodeUpdateFinish(t *testing.T, e *env) model.UpdateNodesProgress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, _ := fetchNodeUpdate(t, e, 0)
		if p.State == model.UpdateNodesDone || p.State == model.UpdateNodesFailed {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务一直没跑完,现在:%+v", p)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// allNodeUpdateEvents 按 since 增量把事件翻完,拼成整个列表。终态已到时事件不再
// 增长,这个循环一定会停。
func allNodeUpdateEvents(t *testing.T, e *env) []model.UpdateNodeEvent {
	t.Helper()
	var got []model.UpdateNodeEvent
	var since int64
	for {
		p, _ := fetchNodeUpdate(t, e, since)
		if len(p.Events) == 0 {
			return got
		}
		got = append(got, p.Events...)
		since = p.Events[len(p.Events)-1].Seq
	}
}

// 一切顺利时:逐台 start/ok,全部换到推荐版本,整轮 done。
func TestUpdateNodesAllSuccess(t *testing.T) {
	e := newEnv(t)
	seedXrayNode(t, e, 1, "hk", "25.1.1")
	seedXrayNode(t, e, 2, "jp", "24.12.31")
	calls := fakeXrayUpgrade(e, nil, nil)

	code, body := e.api("POST", "/api/update/nodes", nil)
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/update/nodes: %d %s", code, body)
	}

	p := waitNodeUpdateFinish(t, e)
	if p.State != model.UpdateNodesDone {
		t.Fatalf("state = %s, 想要 done", p.State)
	}
	if p.Job != 1 {
		t.Errorf("job = %d, 第一轮该是 1", p.Job)
	}

	events := allNodeUpdateEvents(t, e)
	want := []struct {
		node, status, from string
	}{
		{"hk", "start", "25.1.1"},
		{"hk", "ok", "25.1.1"},
		{"jp", "start", "24.12.31"},
		{"jp", "ok", "25.1.1"},
	}
	if len(events) != len(want) {
		t.Fatalf("事件数 = %d, 想要 %d:%+v", len(events), len(want), events)
	}
	for i, w := range want {
		ev := events[i]
		if ev.Seq != int64(i+1) {
			t.Errorf("第 %d 条的 seq = %d, 想要 %d", i, ev.Seq, i+1)
		}
		if ev.Node != w.node || ev.Status != w.status {
			t.Errorf("第 %d 条 = %s/%s, 想要 %s/%s", i, ev.Node, ev.Status, w.node, w.status)
		}
		// start 的 from 是库里观察到的旧版本;ok 的 from 是节点回报的实际版本。
		if ev.From != w.from {
			t.Errorf("%s %s 的 from = %q, 想要 %q", ev.Node, ev.Status, ev.From, w.from)
		}
		if ev.To != xrayrelease.RecommendedVersion {
			t.Errorf("%s %s 的 to = %q, 想要推荐版本 %q", ev.Node, ev.Status, ev.To, xrayrelease.RecommendedVersion)
		}
	}

	_, upgraded := calls.snapshot()
	if strings.Join(upgraded, ",") != "hk,jp" {
		t.Errorf("升级顺序 = %v, 想按节点 id 先 hk 后 jp", upgraded)
	}
}

// 已是目标版本的节点只记一条 skip,连摘要都不去取。
func TestUpdateNodesSkipsWhenAlreadyAtTarget(t *testing.T) {
	e := newEnv(t)
	// 不带 v:节点上核心自己报的版本没有 v 前缀,同一版不能被误判成要升级。
	seedXrayNode(t, e, 1, "hk", strings.TrimPrefix(xrayrelease.RecommendedVersion, "v"))
	calls := fakeXrayUpgrade(e, nil, nil)

	code, body := e.api("POST", "/api/update/nodes", nil)
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/update/nodes: %d %s", code, body)
	}

	p := waitNodeUpdateFinish(t, e)
	if p.State != model.UpdateNodesDone {
		t.Fatalf("state = %s, 想要 done", p.State)
	}

	events := allNodeUpdateEvents(t, e)
	if len(events) != 1 || events[0].Status != "skip" || events[0].Node != "hk" {
		t.Fatalf("事件 = %+v, 想要 hk 的一条 skip", events)
	}
	if events[0].Msg != "已是目标版本" {
		t.Errorf("skip 的 msg = %q, 想要说清原因", events[0].Msg)
	}
	resolved, upgraded := calls.snapshot()
	if len(resolved) != 0 || len(upgraded) != 0 {
		t.Errorf("已是目标版本还动了节点: resolve=%v upgrade=%v", resolved, upgraded)
	}
}

// 缓存说已是目标版本、现场却查到旧版本时,不能 skip:节点可能在两次巡检之间被
// 手工换过核心,跳不跳过要看现场。
func TestUpdateNodesUsesLiveVersionOverCached(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	// 缓存(上一轮巡检)说已经是推荐版本,照着它就会 skip。
	n := seedXrayNode(t, e, 1, "hk", strings.TrimPrefix(xrayrelease.RecommendedVersion, "v"))
	// 现场查到的却是旧版本:节点在两次巡检之间被换过核心。
	e.s.nodeOps.(*fakeNodeOps).check = &model.NodeStatus{NodeID: n.ID, Reachable: true, Version: "25.1.1"}
	calls := fakeXrayUpgrade(e, nil, nil)

	code, body := e.api("POST", "/api/update/nodes", nil)
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/update/nodes: %d %s", code, body)
	}

	p := waitNodeUpdateFinish(t, e)
	if p.State != model.UpdateNodesDone {
		t.Fatalf("state = %s, 想要 done", p.State)
	}

	events := allNodeUpdateEvents(t, e)
	if len(events) != 2 || events[0].Status != "start" || events[1].Status != "ok" {
		t.Fatalf("事件 = %+v, 现场是旧版本,应当走升级而不是 skip", events)
	}
	if events[0].From != "25.1.1" {
		t.Errorf("start 的 from = %q, 想要现场查到的 25.1.1", events[0].From)
	}
	if _, upgraded := calls.snapshot(); strings.Join(upgraded, ",") != "hk" {
		t.Errorf("被升级的节点 = %v, 想要 hk", upgraded)
	}
}

// 现场查不到时退回缓存:一次 SSH 不通不该把"已是目标版本"的节点又升一遍。
func TestUpdateNodesFallsBackToCachedWhenLiveCheckFails(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{checkErr: errors.New("ssh 连不上")})
	seedXrayNode(t, e, 1, "hk", strings.TrimPrefix(xrayrelease.RecommendedVersion, "v"))
	calls := fakeXrayUpgrade(e, nil, nil)

	code, body := e.api("POST", "/api/update/nodes", nil)
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/update/nodes: %d %s", code, body)
	}
	p := waitNodeUpdateFinish(t, e)
	if p.State != model.UpdateNodesDone {
		t.Fatalf("state = %s, 想要 done", p.State)
	}

	events := allNodeUpdateEvents(t, e)
	if len(events) != 1 || events[0].Status != "skip" {
		t.Fatalf("事件 = %+v, 现场查不到时应退回缓存并 skip", events)
	}
	if _, upgraded := calls.snapshot(); len(upgraded) != 0 {
		t.Errorf("查不到现场版本却动了节点: %v", upgraded)
	}
}

// 一台失败,后面的节点一个都不动,整轮 failed。
func TestUpdateNodesStopsAfterFirstFailure(t *testing.T) {
	e := newEnv(t)
	seedXrayNode(t, e, 1, "one", "25.1.1")
	seedXrayNode(t, e, 2, "two", "25.1.1")
	seedXrayNode(t, e, 3, "three", "25.1.1")
	calls := fakeXrayUpgrade(e, map[string]error{"two": errors.New("ssh 连不上")}, nil)

	code, body := e.api("POST", "/api/update/nodes", nil)
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/update/nodes: %d %s", code, body)
	}

	p := waitNodeUpdateFinish(t, e)
	if p.State != model.UpdateNodesFailed {
		t.Fatalf("state = %s, 想要 failed", p.State)
	}

	_, upgraded := calls.snapshot()
	if strings.Join(upgraded, ",") != "one,two" {
		t.Errorf("被碰过的节点 = %v, two 失败后 three 不该再被调用", upgraded)
	}

	events := allNodeUpdateEvents(t, e)
	want := []struct{ node, status string }{
		{"one", "start"}, {"one", "ok"},
		{"two", "start"}, {"two", "failed"},
	}
	if len(events) != len(want) {
		t.Fatalf("事件数 = %d, 想要 %d:%+v", len(events), len(want), events)
	}
	for i, w := range want {
		if events[i].Node != w.node || events[i].Status != w.status {
			t.Errorf("第 %d 条 = %s/%s, 想要 %s/%s", i, events[i].Node, events[i].Status, w.node, w.status)
		}
	}
	last := events[len(events)-1]
	if !strings.Contains(last.Msg, "ssh 连不上") {
		t.Errorf("失败原因没进事件: %q", last.Msg)
	}
}

// 同一时刻只允许一轮:上一轮还挂着时再 POST 得到 409,而不是排队或合并。
func TestUpdateNodesRejectsSecondRunWhileBusy(t *testing.T) {
	e := newEnv(t)
	seedXrayNode(t, e, 1, "solo", "25.1.1")
	block := make(chan struct{})
	calls := fakeXrayUpgrade(e, nil, block)

	code, body := e.api("POST", "/api/update/nodes", nil)
	if code != http.StatusAccepted {
		t.Fatalf("第一次 POST: %d %s", code, body)
	}

	// 任务被 block 摁在 running 里,这次 POST 一定撞上"正在跑"。
	code, body = e.api("POST", "/api/update/nodes", nil)
	if code != http.StatusConflict {
		t.Fatalf("任务在跑时的 POST: %d %s, 想要 409", code, body)
	}
	if !strings.Contains(string(body), "已有节点核心升级任务在跑") {
		t.Errorf("409 的正文没说清原因: %s", body)
	}

	close(block)
	p := waitNodeUpdateFinish(t, e)
	if p.State != model.UpdateNodesDone {
		t.Fatalf("state = %s, 放行后该跑完 done", p.State)
	}
	if _, upgraded := calls.snapshot(); strings.Join(upgraded, ",") != "solo" {
		t.Errorf("被升级的节点 = %v, 想要 solo", upgraded)
	}
}

// 事件多了要分页:一次最多 20 条、整个响应压在 4KB 内,since 只取增量。
func TestUpdateNodesProgressPagesIncrementally(t *testing.T) {
	e := newEnv(t)
	// 11 台 × (start+ok) = 22 条事件,刚好跨过 20 条的上限。
	for i := 1; i <= 11; i++ {
		seedXrayNode(t, e, i, fmt.Sprintf("n%02d", i), "25.1.1")
	}
	fakeXrayUpgrade(e, nil, nil)

	code, body := e.api("POST", "/api/update/nodes", nil)
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/update/nodes: %d %s", code, body)
	}
	waitNodeUpdateFinish(t, e)

	first, raw := fetchNodeUpdate(t, e, 0)
	if len(first.Events) != nodeUpdateMaxEvents {
		t.Fatalf("第一页给了 %d 条, 想要上限 %d", len(first.Events), nodeUpdateMaxEvents)
	}
	if len(raw) >= 4096 {
		t.Errorf("第一页响应 %d 字节, 该压在 4KB 内", len(raw))
	}
	if first.Events[0].Seq != 1 || first.Events[len(first.Events)-1].Seq != nodeUpdateMaxEvents {
		t.Errorf("第一页的 seq 范围 = %d..%d, 想要 1..%d",
			first.Events[0].Seq, first.Events[len(first.Events)-1].Seq, nodeUpdateMaxEvents)
	}

	second, _ := fetchNodeUpdate(t, e, nodeUpdateMaxEvents)
	if len(second.Events) != 2 {
		t.Fatalf("第二页给了 %d 条, 想要剩下的 2 条:%+v", len(second.Events), second.Events)
	}
	if second.Events[0].Seq != 21 || second.Events[1].Seq != 22 {
		t.Errorf("第二页的 seq = %d,%d, 想要 21,22", second.Events[0].Seq, second.Events[1].Seq)
	}

	// 两页拼起来恰好是 1..22,不重不漏。
	var seqs []int64
	for _, ev := range append(first.Events, second.Events...) {
		seqs = append(seqs, ev.Seq)
	}
	for i, seq := range seqs {
		if seq != int64(i+1) {
			t.Fatalf("拼起来的第 %d 条 seq = %d, 想要 %d", i, seq, i+1)
		}
	}
}
