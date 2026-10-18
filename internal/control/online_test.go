package control

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// xray 26 的真实在线输出形状:先一条 statsgetallonlineusers 给出在线的计数名,
// 再一次 SSH 里每人一段 statsonlineiplist,两段之间用分隔标记隔开(格式见
// internal/node/online_test.go)。
//
// 分隔标记按字面写死而不是引用 node 包里的常量:这里同时是在钉住「节点那边输出
// 什么形状」,两边任何一边改了,这条测试就该红。
const onlineSepText = "@@MPX-ONLINE@@"

func onlineReplies(usersLine string, chunks ...string) []reply {
	ips := ""
	if len(chunks) > 0 {
		ips = strings.Join(chunks, onlineSepText+"\n") + onlineSepText + "\n"
	}
	return []reply{
		{keyword: "statsUserOnline", out: usersLine},
		{keyword: "statsonlineiplist", out: ips},
	}
}

// nodeFailRunner 让某一台节点上的每条命令都失败,其余节点照常交给 fakeRunner。
//
// 需要它是因为 fakeRunner 只看脚本文本:两台节点同一批命令的脚本可以一模一样
// (statsquery 里只有 API 端口),认名字的是节点,不是脚本。
type nodeFailRunner struct {
	*fakeRunner
	failNode string
}

func (r *nodeFailRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	if n.Name == r.failNode {
		return "", errors.New("假节点不通:" + n.Name)
	}
	return r.fakeRunner.Run(ctx, n, script)
}

func (r *nodeFailRunner) RunWithInput(ctx context.Context, n model.Node, script string, stdin []byte) (string, error) {
	if n.Name == r.failNode {
		return "", errors.New("假节点不通:" + n.Name)
	}
	return r.fakeRunner.RunWithInput(ctx, n, script, stdin)
}

// 成功采样的那一轮要记下采样时间,失败的不记。
func TestTickMarksNodeSampled(t *testing.T) {
	r := &nodeFailRunner{failNode: "bad", fakeRunner: &fakeRunner{replies: []reply{
		{keyword: "statsquery", out: trafficReply()},
		{keyword: "inbounduser", out: inbounduserReply()},
	}}}
	c, st := newLoggingControl(t, r, slog.New(slog.DiscardHandler))
	task := NewTask(c, st, nil, time.Hour)

	good := addNode(t, st, "good", "good.example.com")
	addInbound(t, st, good.ID, "vless-ws", 8443)
	bad := addNode(t, st, "bad", "bad.example.com")
	addInbound(t, st, bad.ID, "bad", 8444)

	task.Tick(context.Background())

	times, err := st.NodeSampledTimes()
	if err != nil {
		t.Fatalf("读采样时间: %v", err)
	}
	if _, ok := times[good.ID]; !ok {
		t.Fatalf("采样成功却没有记下采样时间:%v", times)
	}
	if _, ok := times[bad.ID]; ok {
		t.Fatalf("采样失败不该记采样时间:%v", times)
	}
}

// refreshOnline 用真实返回形状填快照,并且同一分钟内不再问。
func TestRefreshOnlineStoresSnapshotThenRateLimits(t *testing.T) {
	aliceChunk := `{"name":"user>>>alice>>>online","ips":{"198.51.100.7":1760000000,"203.0.113.9":1760000001}}`
	r := &fakeRunner{replies: onlineReplies(`{"users":["user>>>alice>>>online"]}`, aliceChunk)}
	c, st := newTestControl(t, r)
	n := addNode(t, st, "hk", "203.0.113.10")
	addInbound(t, st, n.ID, "vless-ws", 8443)

	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	c.refreshOnline(context.Background(), n, now)

	got := c.OnlineSnapshots()
	snap, ok := got[n.ID]
	if !ok {
		t.Fatalf("刷新后没有快照:%v", got)
	}
	want := map[string][]string{"alice": {"198.51.100.7", "203.0.113.9"}}
	if !reflect.DeepEqual(snap.Users, want) {
		t.Fatalf("快照 = %v,想要 %v", snap.Users, want)
	}
	if snap.NodeID != n.ID || !snap.At.Equal(now) {
		t.Fatalf("快照的节点或时间不对:%+v", snap)
	}

	// 一分钟内再刷:不发任何命令。
	before := len(r.callList())
	c.refreshOnline(context.Background(), n, now.Add(30*time.Second))
	if got := len(r.callList()); got != before {
		t.Fatalf("一分钟内不该再问节点,实际多发了 %d 条", got-before)
	}

	// 过了点才问,且拿到的新名单会替换旧的。
	bobChunk := `{"name":"user>>>bob>>>online","ips":{"198.51.100.8":1760000002}}`
	r.replies = onlineReplies(`{"users":["user>>>bob>>>online"]}`, bobChunk)
	c.refreshOnline(context.Background(), n, now.Add(61*time.Second))
	if got := len(r.callList()); got <= before {
		t.Fatalf("过了一分钟该再问一次节点,实际没有:%v", r.callList())
	}
	snap = c.OnlineSnapshots()[n.ID]
	if !reflect.DeepEqual(snap.Users, map[string][]string{"bob": {"198.51.100.8"}}) {
		t.Fatalf("快照没有换成新名单:%v", snap.Users)
	}
}

// 读失败要删掉旧快照,不能让一份过期的名单一直挂着。
func TestRefreshOnlineForgetsOnError(t *testing.T) {
	aliceChunk := `{"name":"user>>>alice>>>online","ips":{"198.51.100.7":1760000000}}`
	r := &fakeRunner{replies: onlineReplies(`{"users":["user>>>alice>>>online"]}`, aliceChunk)}
	c, st := newTestControl(t, r)
	n := addNode(t, st, "hk", "203.0.113.10")
	addInbound(t, st, n.ID, "vless-ws", 8443)

	now := time.Now()
	c.refreshOnline(context.Background(), n, now)
	if _, ok := c.OnlineSnapshots()[n.ID]; !ok {
		t.Fatal("第一次刷新应该有快照")
	}

	r.replies = nil
	r.failOn = "statsUserOnline"
	c.refreshOnline(context.Background(), n, now.Add(2*time.Minute))
	if _, ok := c.OnlineSnapshots()[n.ID]; ok {
		t.Fatal("读失败后旧快照该被删掉")
	}
}

// OnlineSnapshots 返回的是拷贝:改它不该影响下一次读到的内容。
func TestOnlineSnapshotsReturnsCopy(t *testing.T) {
	c, st := newTestControl(t, &fakeRunner{})
	n := addNode(t, st, "hk", "203.0.113.10")
	now := time.Now()
	c.onlineMu.Lock()
	c.online = map[int64]model.OnlineSnapshot{
		n.ID: {NodeID: n.ID, At: now, Users: map[string][]string{"alice": {"198.51.100.7"}}},
	}
	c.onlineMu.Unlock()

	first := c.OnlineSnapshots()
	first[n.ID].Users["alice"][0] = "203.0.113.99"
	first[n.ID].Users["intruder"] = []string{"203.0.113.1"}
	first[999] = model.OnlineSnapshot{}
	delete(first, n.ID)

	second := c.OnlineSnapshots()
	want := map[int64]model.OnlineSnapshot{
		n.ID: {NodeID: n.ID, At: now, Users: map[string][]string{"alice": {"198.51.100.7"}}},
	}
	if !reflect.DeepEqual(second, want) {
		t.Fatalf("改拷贝改到了内部状态:第二次 = %v,想要 %v", second, want)
	}

	c.ForgetOnline(n.ID)
	if _, ok := c.OnlineSnapshots()[n.ID]; ok {
		t.Fatal("ForgetOnline 之后不该还有这台节点的快照")
	}
}
