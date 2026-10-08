package control

import (
	"context"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// Tick 的三步顺序是有意的,顺序错了会表现成「刚重置完就被摘掉」或「这一轮超的
// 流量要等下一轮才生效」。这两条用行为差异来钉死顺序,不断言命令的行号。
//
// 第一条:重置必须发生在收敛之前。
//
// alice 已经超了本周期配额,但她这个周期正好到期该重置。若先收敛后重置,她在收敛
// 那一刻还是超限状态,会被从节点上摘掉;先重置的话她已归零,名单不动。
func TestTickResetsBeforeReconciling(t *testing.T) {
	r := &fakeRunner{replies: []reply{
		{keyword: "statsquery", out: trafficReply()},
		{keyword: "inbounduser", out: inbounduserReply("alice")},
		{keyword: "api adu", out: "Added 1 user(s)\n"},
		{keyword: "api rmu", out: "Removed 1 user(s)\n"},
	}}
	c, st := newTestControl(t, r)
	task := NewTask(c, st, nil, time.Minute)

	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "vless-ws", 8443)

	anchor := time.Now().Add(-48 * time.Hour)
	addUser(t, st, model.User{
		Username: "alice", Key: "key-alice", Enabled: true, CreatedAt: anchor,
		DataLimit: 100, UsedTraffic: 150, ResetStrategy: model.ResetDaily, TrafficResetAt: &anchor,
	})

	task.Tick(context.Background())

	if r.ran("api rmu") {
		t.Fatalf("重置在收敛之后跑了:alice 刚归零就被摘掉。命令序列:%v", r.callList())
	}
	alice, err := st.GetUser("alice")
	if err != nil {
		t.Fatalf("读回 alice: %v", err)
	}
	if alice.UsedTraffic != 0 {
		t.Fatalf("到期该重置却没有:已用流量仍是 %d", alice.UsedTraffic)
	}
}

// 第二条:采样必须发生在收敛之前。
//
// bob 在数据库里还没超限,但节点上这一轮的计数把他推过了线。先采样才知道他超了,
// 这一轮就能摘掉;若先收敛,得等到下一轮。
func TestTickSamplesBeforeReconciling(t *testing.T) {
	r := &fakeRunner{replies: []reply{
		{keyword: "statsquery", out: trafficReply("bob", "uplink", 300, "bob", "downlink", 300)},
		{keyword: "inbounduser", out: inbounduserReply("bob")},
		{keyword: "api adu", out: "Added 1 user(s)\n"},
		{keyword: "api rmu", out: "Removed 1 user(s)\n"},
	}}
	c, st := newTestControl(t, r)
	task := NewTask(c, st, nil, time.Minute)

	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "vless-ws", 8443)
	addUser(t, st, model.User{
		Username: "bob", Key: "key-bob", Enabled: true, CreatedAt: time.Now(), DataLimit: 100,
	})

	task.Tick(context.Background())

	if !r.ran("api rmu") {
		t.Fatalf("采样在收敛之后跑了:这一轮超限的 bob 要等下一轮才被摘掉。命令序列:%v", r.callList())
	}
	if r.ran("-reset") {
		t.Fatalf("采样又清零了:清零后节点上就没有第二份读数,账再也拼不回来:%v", r.callList())
	}
}

// 一个节点连不上,不该让其余节点这一轮什么都收不到。
func TestTickKeepsGoingAfterOneNodeFails(t *testing.T) {
	r := &fakeRunner{replies: []reply{
		{keyword: "statsquery", out: trafficReply()},
		{keyword: "inbounduser", out: inbounduserReply("alice")},
		{keyword: "api adu", out: "Added 1 user(s)\n"},
		// 第一台节点的查看名单故意失败
		{keyword: "inbounduser -tag='坏节点'", out: ""},
	}}
	// 让「坏节点」这个入站的查看失败
	r.failOn = "坏节点"
	c, st := newTestControl(t, r)
	task := NewTask(c, st, nil, time.Minute)

	good := addNode(t, st, "good", "good.example.com")
	addInbound(t, st, good.ID, "vless-ws", 8443)

	bad := addNode(t, st, "bad", "bad.example.com")
	addInbound(t, st, bad.ID, "坏节点", 8444)

	addUser(t, st, activeUser("alice"))

	task.Tick(context.Background())

	// 好节点的入站仍然被查过(顺序取决于 id,所以两台都在)
	if !r.ran("good") && !r.ran("vless-ws") {
		t.Fatalf("坏节点失败后没有继续处理好节点:%v", r.callList())
	}
}

// Run 必须先立刻跑一轮:控制面重启后不该有长达一个间隔的空窗,那段时间里到期和
// 超限的用户还挂在节点上。
func TestRunTicksImmediately(t *testing.T) {
	r := &fakeRunner{replies: []reply{
		{keyword: "statsquery", out: trafficReply()},
		{keyword: "inbounduser", out: inbounduserReply()},
	}}
	c, st := newTestControl(t, r)
	// 间隔给得很长:只有「立刻跑一轮」才能在取消前发生。
	task := NewTask(c, st, nil, time.Hour)

	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "vless-ws", 8443)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		task.Run(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !r.ran("statsquery") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 Run 没有返回")
	}

	if !r.ran("statsquery") {
		t.Fatalf("Run 没有立刻跑第一轮,要等满一个间隔:%v", r.callList())
	}
}

// 重置只动走到重置点的人;策略为 none 的人永远不动。
func TestResetDueUsersOnlyTouchesDueOnes(t *testing.T) {
	r := &fakeRunner{}
	c, st := newTestControl(t, r)
	task := NewTask(c, st, nil, time.Minute)

	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	// 已到期(每天重置,两天前重置过)
	dueAnchor := now.Add(-48 * time.Hour)
	addUser(t, st, model.User{
		Username: "due", Key: "k", Enabled: true, CreatedAt: dueAnchor,
		UsedTraffic: 500, LifetimeUsedTraffic: 700,
		ResetStrategy: model.ResetDaily, TrafficResetAt: &dueAnchor,
	})
	// 还没到期(每天重置,一小时前刚重置过)
	notYet := now.Add(-time.Hour)
	addUser(t, st, model.User{
		Username: "notyet", Key: "k", Enabled: true, CreatedAt: notYet,
		UsedTraffic: 500, ResetStrategy: model.ResetDaily, TrafficResetAt: &notYet,
	})
	// 不重置
	addUser(t, st, model.User{
		Username: "never", Key: "k", Enabled: true, CreatedAt: now.Add(-100 * 24 * time.Hour),
		UsedTraffic: 500, ResetStrategy: model.ResetNone,
	})

	task.ResetDueUsers(now)

	due, _ := st.GetUser("due")
	if due.UsedTraffic != 0 {
		t.Errorf("到期的用户该归零,实际 %d", due.UsedTraffic)
	}
	if due.TrafficResetAt == nil || !due.TrafficResetAt.After(now) {
		t.Errorf("重置后起算点应当严格晚于 now,实际 %v", due.TrafficResetAt)
	}
	if due.LifetimeUsedTraffic != 700 {
		t.Errorf("周期用量归零不该动累计用量,实际 %d", due.LifetimeUsedTraffic)
	}

	notyet, _ := st.GetUser("notyet")
	if notyet.UsedTraffic != 500 {
		t.Errorf("没到期的用户不该被动:实际 %d", notyet.UsedTraffic)
	}

	never, _ := st.GetUser("never")
	if never.UsedTraffic != 500 {
		t.Errorf("none 策略的用户永远不该被重置:实际 %d", never.UsedTraffic)
	}
}

// 控制面停机几天再起来时,一次跨过多个周期也只重置一次 —— 每次重置都伴随节点侧的
// 摘除与恢复,没必要把同一个用户重复折腾几遍。
func TestResetDueUsersDoesNotReplayMissedPeriods(t *testing.T) {
	r := &fakeRunner{}
	c, st := newTestControl(t, r)
	task := NewTask(c, st, nil, time.Minute)

	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addUser(t, st, model.User{
		Username: "alice", Key: "k", Enabled: true, CreatedAt: anchor,
		UsedTraffic: 10, ResetStrategy: model.ResetDaily, TrafficResetAt: &anchor,
	})

	// 隔了 100 天才恢复
	now := anchor.Add(100 * 24 * time.Hour)
	task.ResetDueUsers(now)

	alice, _ := st.GetUser("alice")
	if alice.UsedTraffic != 0 {
		t.Fatalf("该重置,实际用量 %d", alice.UsedTraffic)
	}
	if !alice.TrafficResetAt.After(now) {
		t.Fatalf("起算点应当跳到 now 之后,实际 %v", alice.TrafficResetAt)
	}
}
