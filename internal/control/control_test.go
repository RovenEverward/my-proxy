package control

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// fakeRunner 按脚本文本里的关键字给答案,并记下每条跑过的脚本。
//
// 它认的是 node 包拼出来的那几条命令的字面片段。片段要是改了,这个假 Runner 会
// 立刻失效并报「没有为这条命令准备答案」,不会静默地让测试一直绿下去。
//
// 上锁是因为 Task.Run 在另一个 goroutine 里跑,测试这边同时要查它跑过什么。
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	// replies 的键是关键字,值是这条命令的输出;命中第一个包含关键字的规则。
	replies []reply
	// failOn 命中关键字的命令返回错误。
	failOn string
	// stdin 记下每条命令收到的标准输入。
	stdin []string
}

type reply struct {
	keyword string
	out     string
}

func (f *fakeRunner) answer(script string, stdin []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answerLocked(script, stdin)
}

// answerLocked 要求调用方已经持有 f.mu。
func (f *fakeRunner) answerLocked(script string, stdin []byte) (string, error) {
	f.calls = append(f.calls, script)
	f.stdin = append(f.stdin, string(stdin))
	if f.failOn != "" && strings.Contains(script, f.failOn) {
		return "", fmt.Errorf("假节点拒绝了这条命令: %s", f.failOn)
	}
	for _, r := range f.replies {
		if strings.Contains(script, r.keyword) {
			return r.out, nil
		}
	}
	return "", fmt.Errorf("假节点没有为这条命令准备答案: %s", script)
}

func (f *fakeRunner) Run(_ context.Context, _ model.Node, script string) (string, error) {
	return f.answer(script, nil)
}

func (f *fakeRunner) RunWithInput(_ context.Context, _ model.Node, script string, stdin []byte) (string, error) {
	return f.answer(script, stdin)
}

// ran 报告是否跑过含该关键字的命令。
func (f *fakeRunner) ran(keyword string) bool {
	return f.indexOf(keyword) >= 0
}

func (f *fakeRunner) indexOf(keyword string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if strings.Contains(c, keyword) {
			return i
		}
	}
	return -1
}

// callList 返回一份命令序列的拷贝,给失败信息用。
func (f *fakeRunner) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// stdinFor 返回含该关键字的那条命令收到的标准输入。
func (f *fakeRunner) stdinFor(keyword string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if strings.Contains(c, keyword) && i < len(f.stdin) {
			return f.stdin[i]
		}
	}
	return ""
}

// inbounduserReply / trafficReply 是真实的 `xray api` 输出形状。
func inbounduserReply(names ...string) string {
	if len(names) == 0 {
		return `{"success": true, "users": []}`
	}
	var sb strings.Builder
	sb.WriteString(`{"success": true, "users": [`)
	for i, n := range names {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"email": %q, "level": 0}`, n)
	}
	sb.WriteString(`]}`)
	return sb.String()
}

func trafficReply(pairs ...any) string {
	var sb strings.Builder
	sb.WriteString(`{"stat": [`)
	for i := 0; i+2 < len(pairs); i += 3 {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"name": "user>>>%s>>>traffic>>>%s", "value": "%d"}`,
			pairs[i], pairs[i+1], pairs[i+2])
	}
	sb.WriteString(`]}`)
	return sb.String()
}

func newTestControl(t *testing.T, r *fakeRunner) (*Control, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatalf("开库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, r, slog.New(slog.DiscardHandler)), st
}

func addUser(t *testing.T, st *store.Store, u model.User) model.User {
	t.Helper()
	if err := st.CreateUser(&u); err != nil {
		t.Fatalf("建用户 %s: %v", u.Username, err)
	}
	got, err := st.GetUser(u.Username)
	if err != nil {
		t.Fatalf("读回用户 %s: %v", u.Username, err)
	}
	return *got
}

func activeUser(name string) model.User {
	return model.User{Username: name, Key: "key-" + name, Enabled: true, CreatedAt: time.Now()}
}

func addNode(t *testing.T, st *store.Store, name, addr string) model.Node {
	t.Helper()
	n := model.Node{Name: name, Address: addr, APIPort: 10085}
	if err := st.CreateNode(&n); err != nil {
		t.Fatalf("建节点 %s: %v", name, err)
	}
	got, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("读回节点 %s: %v", name, err)
	}
	return *got
}

func addInbound(t *testing.T, st *store.Store, nodeID int64, tag string, port int) model.Inbound {
	t.Helper()
	in := model.Inbound{NodeID: nodeID, Tag: tag, Protocol: model.ProtocolVLESS, Port: port, Network: model.NetworkWS}
	if err := st.CreateInbound(&in); err != nil {
		t.Fatalf("建入站 %s: %v", tag, err)
	}
	got, err := st.GetInbound(in.ID)
	if err != nil {
		t.Fatalf("读回入站 %s: %v", tag, err)
	}
	return *got
}

// planChanges 是收敛逻辑的全部决策,对着名单穷举。
func TestPlanChanges(t *testing.T) {
	now := time.Now()
	const inID = int64(7)

	past := now.Add(-time.Hour)
	later := now.Add(time.Hour)

	users := []model.User{
		{Username: "alice", Enabled: true, CreatedAt: now},
		{Username: "bob", Enabled: true, CreatedAt: now},
		{Username: "carol", Enabled: false, CreatedAt: now},                                  // 停用
		{Username: "dave", Enabled: true, ExpireAt: &past, CreatedAt: now},                   // 过期
		{Username: "erin", Enabled: true, ExpireAt: &later, CreatedAt: now},                  // 未过期
		{Username: "frank", Enabled: true, DataLimit: 100, UsedTraffic: 100, CreatedAt: now}, // 超限
		{Username: "grace", Enabled: true, InboundIDs: []int64{99}, CreatedAt: now},          // 只授权别的入站
		{Username: "heidi", Enabled: true, InboundIDs: []int64{inID}, CreatedAt: now},        // 只授权这个入站
	}
	// 期望名单:alice bob erin heidi(按名字排序)
	wantNames := map[string]bool{"alice": true, "bob": true, "erin": true, "heidi": true}

	t.Run("节点上一个人都没有时要补齐期望名单", func(t *testing.T) {
		add, remove := planChanges(users, inID, nil, now)
		got := map[string]bool{}
		for _, u := range add {
			got[u.Username] = true
		}
		if len(got) != len(wantNames) {
			t.Fatalf("应当补 %v,实际补 %v", keys(wantNames), keys(got))
		}
		for name := range wantNames {
			if !got[name] {
				t.Errorf("%s 应当在补齐名单里", name)
			}
		}
		if len(remove) != 0 {
			t.Errorf("不该摘人,实际摘了 %v", remove)
		}
		// 顺序必须稳定,否则每轮下发的次序都不一样。
		for i := 1; i < len(add); i++ {
			if add[i-1].Username > add[i].Username {
				t.Fatalf("补齐名单没有按用户名排序: %v", names(add))
			}
		}
	})

	t.Run("两边一致时什么都不做", func(t *testing.T) {
		add, remove := planChanges(users, inID, []string{"alice", "bob", "erin", "heidi"}, now)
		if len(add) != 0 || len(remove) != 0 {
			t.Fatalf("应当无事可做,实际 add=%v remove=%v", names(add), remove)
		}
	})

	t.Run("过期与超限的人要从节点上摘掉", func(t *testing.T) {
		// 节点上还留着已经失效的人 —— 这正是需要收敛的场景。
		add, remove := planChanges(users, inID, []string{"alice", "bob", "erin", "heidi", "dave", "frank", "carol", "grace"}, now)
		if len(add) != 0 {
			t.Errorf("不该补人,实际补了 %v", names(add))
		}
		if len(remove) != 4 {
			t.Fatalf("应当摘掉 dave frank carol grace 四个人,实际 %v", remove)
		}
		for _, name := range remove {
			if wantNames[name] {
				t.Errorf("%s 是有效用户,不该被摘掉", name)
			}
		}
	})

	t.Run("未知的陌生名字要被摘掉", func(t *testing.T) {
		// 手工在节点上加过的人,控制面下一轮把它收回来。
		_, remove := planChanges(users, inID, []string{"alice", "bob", "erin", "heidi", "手加的"}, now)
		if len(remove) != 1 || remove[0] != "手加的" {
			t.Fatalf("应当摘掉「手加的」,实际 %v", remove)
		}
	})

	t.Run("入站上没有用户时全部补齐", func(t *testing.T) {
		add, remove := planChanges(users, inID, []string{}, now)
		if len(add) != len(wantNames) || len(remove) != 0 {
			t.Fatalf("实际 add=%v remove=%v", names(add), remove)
		}
	})
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func names(us []model.User) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.Username)
	}
	return out
}

// 「先加后减」是刻意的:中途失败时名单只多不少,不会把人锁在外面。
func TestReconcileAddsBeforeRemoving(t *testing.T) {
	r := &fakeRunner{replies: []reply{
		{keyword: "inbounduser", out: inbounduserReply("alice", "已过期的人")},
		{keyword: "api adu", out: "Added 1 user(s)\n"},
		{keyword: "api rmu", out: "Removed 1 user(s)\n"},
	}}
	c, st := newTestControl(t, r)

	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "vless-ws", 8443)
	addUser(t, st, activeUser("alice"))
	addUser(t, st, activeUser("bob"))

	if err := c.ReconcileUsers(context.Background(), n, time.Now()); err != nil {
		t.Fatalf("ReconcileUsers: %v", err)
	}

	addAt := r.indexOf("api adu")
	removeAt := r.indexOf("api rmu")
	if addAt < 0 {
		t.Fatalf("没有添加任何用户,跑过的命令:%v", r.callList())
	}
	if removeAt < 0 {
		t.Fatalf("没有摘掉过期用户,跑过的命令:%v", r.callList())
	}
	if addAt > removeAt {
		t.Fatalf("先摘后加了(adu 在第 %d 条,rmu 在第 %d 条):中途失败会让人连不上", addAt, removeAt)
	}
}

// 添加失败时不再往下摘人:名单宁可多留几个,也不能在补不上的时候还缩水。
func TestReconcileStopsWhenAddFails(t *testing.T) {
	r := &fakeRunner{
		failOn: "api adu",
		replies: []reply{
			{keyword: "inbounduser", out: inbounduserReply("alice", "已过期的人")},
			{keyword: "api rmu", out: "Removed 1 user(s)\n"},
		},
	}
	c, st := newTestControl(t, r)

	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "vless-ws", 8443)
	addUser(t, st, activeUser("alice"))
	addUser(t, st, activeUser("bob"))

	if err := c.ReconcileUsers(context.Background(), n, time.Now()); err == nil {
		t.Fatal("添加失败时应当把错误报上去")
	}
	if r.ran("api rmu") {
		t.Fatal("添加都失败了,不该继续摘人")
	}
}

// 一个入站坏了不该拖住别的入站。
func TestReconcileKeepsGoingAfterOneInboundFails(t *testing.T) {
	r := &brokenInboundRunner{bad: "坏入站"}
	c, st := newTestControl(t, r.raw())

	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "坏入站", 8443)
	addInbound(t, st, n.ID, "好入站", 8444)
	addUser(t, st, activeUser("alice"))

	err := c.ReconcileUsers(context.Background(), n, time.Now())
	if err == nil {
		t.Fatal("有入站失败时应当把错误报上去")
	}
	if !strings.Contains(err.Error(), "坏入站") {
		t.Fatalf("错误里应当点名是哪个入站: %v", err)
	}
	if !r.raw().ran("好入站") {
		t.Fatal("坏入站失败后应当继续处理好入站")
	}
}

// brokenInboundRunner 只对某个 tag 的 inbounduser 报错。
type brokenInboundRunner struct {
	bad string
	f   fakeRunner
}

func (b *brokenInboundRunner) raw() *fakeRunner { return &b.f }

func (b *brokenInboundRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	if strings.Contains(script, "inbounduser") && strings.Contains(script, b.bad) {
		b.f.calls = append(b.f.calls, script)
		return "", fmt.Errorf("这个入站读不出来")
	}
	if strings.Contains(script, "inbounduser") {
		b.f.calls = append(b.f.calls, script)
		return inbounduserReply("alice"), nil
	}
	return b.f.Run(ctx, n, script)
}

func (b *brokenInboundRunner) RunWithInput(ctx context.Context, n model.Node, script string, stdin []byte) (string, error) {
	b.f.stdin = append(b.f.stdin, string(stdin))
	return b.Run(ctx, n, script)
}

// 流量只累加非零的那部分,并且一个人写失败不该拖住其余几百个人。
func TestSampleTrafficSkipsZerosAndSurvivesBadUser(t *testing.T) {
	r := &fakeRunner{replies: []reply{
		{keyword: "statsquery", out: trafficReply(
			"alice", "uplink", 100, "alice", "downlink", 900,
			"bob", "uplink", 0, "bob", "downlink", 0,
			"没这个人", "uplink", 5, "没这个人", "downlink", 5,
			"carol", "uplink", 7, "carol", "downlink", 3,
		)},
	}}
	c, st := newTestControl(t, r)

	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "vless-ws", 8443)
	addUser(t, st, activeUser("alice"))
	addUser(t, st, activeUser("bob"))
	addUser(t, st, activeUser("carol"))

	if err := c.SampleTraffic(context.Background(), n); err != nil {
		t.Fatalf("SampleTraffic: %v", err)
	}

	alice, _ := st.GetUser("alice")
	if alice.UsedTraffic != 1000 {
		t.Errorf("alice 应当是 1000,实际 %d", alice.UsedTraffic)
	}
	bob, _ := st.GetUser("bob")
	if bob.UsedTraffic != 0 {
		t.Errorf("上下行都是 0 的不该产生记录,实际 %d", bob.UsedTraffic)
	}
	// carol 排在「没这个人」后面,前面那个用户不存在也不能让后面的停止累计。
	carol, _ := st.GetUser("carol")
	if carol.UsedTraffic != 10 {
		t.Errorf("carol 应当是 10,实际 %d(前面某个用户写失败就中断了)", carol.UsedTraffic)
	}
}

// 采集是「读走即清零」的,所以脚本里必须带 -reset,否则流量会被重复累加。
func TestSampleTrafficUsesReset(t *testing.T) {
	r := &fakeRunner{replies: []reply{
		{keyword: "statsquery", out: trafficReply("alice", "uplink", 1, "alice", "downlink", 1)},
	}}
	c, st := newTestControl(t, r)
	n := addNode(t, st, "hk", "node.example.com")
	addUser(t, st, activeUser("alice"))

	if err := c.SampleTraffic(context.Background(), n); err != nil {
		t.Fatalf("SampleTraffic: %v", err)
	}
	if !r.ran("-reset") {
		t.Fatalf("statsquery 没有带 -reset,流量会被重复累加:%v", r.callList())
	}
}

// 下发是「备份 → 写 → 校验 → 重启」,顺序错了就没有可回滚的东西。
func TestDeployNodeBacksUpThenValidatesThenRestarts(t *testing.T) {
	r := &fakeRunner{replies: []reply{
		{keyword: "-test -config", out: "Configuration OK.\n"},
		{keyword: "cat >", out: ""},
		{keyword: "cp -f", out: ""},
		{keyword: "systemctl", out: ""},
	}}
	c, st := newTestControl(t, r)

	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "vless-ws", 8443)
	addUser(t, st, activeUser("alice"))

	if err := c.DeployNode(context.Background(), n, time.Now()); err != nil {
		t.Fatalf("DeployNode: %v", err)
	}

	backupAt := r.indexOf(".bak")
	testAt := r.indexOf("-test -config")
	restartAt := r.indexOf("systemctl restart")
	if backupAt < 0 {
		t.Fatalf("没有备份原配置:%v", r.callList())
	}
	if testAt < 0 {
		t.Fatalf("没有校验新配置:%v", r.callList())
	}
	if restartAt < 0 {
		t.Fatalf("没有重启 xray:%v", r.callList())
	}
	if backupAt > testAt {
		t.Fatalf("先校验后备份了:校验失败时没有可回滚的东西")
	}
	if testAt > restartAt {
		t.Fatalf("先重启后校验了")
	}
}

// 配置渲染不出来时,一个字节都不该往节点上写。
func TestDeployNodeWritesNothingWhenRenderFails(t *testing.T) {
	r := &fakeRunner{}
	c, st := newTestControl(t, r)

	n := addNode(t, st, "hk", "node.example.com")
	// 入站没填协议,渲染必然失败。
	in := model.Inbound{NodeID: n.ID, Tag: "坏入站", Port: 8443}
	if err := st.CreateInbound(&in); err != nil {
		t.Fatalf("建入站: %v", err)
	}
	addUser(t, st, activeUser("alice"))

	if err := c.DeployNode(context.Background(), n, time.Now()); err == nil {
		t.Fatal("渲染失败时应当报错")
	}
	if len(r.callList()) != 0 {
		t.Fatalf("渲染失败却已经动了节点:%v", r.callList())
	}
}
