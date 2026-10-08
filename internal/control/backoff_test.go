package control

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// fakeClock 是可拨的时钟:退避封顶 5 分钟,真等就没法测;心跳里的「多久之前」
// 也只有拨表才测得准。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Now()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// scriptedRunner 记下每条脚本,并能在两轮之间切换「节点通不通」。
//
// 它按关键字数调用次数:退避要验的是「什么时候真的又去碰了节点」,只看有没有
// 报错区分不出「跳过了」和「碰了但失败了」。
type scriptedRunner struct {
	mu     sync.Mutex
	calls  []string
	failOn string
}

func (r *scriptedRunner) setFailOn(keyword string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failOn = keyword
}

func (r *scriptedRunner) count(keyword string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if strings.Contains(c, keyword) {
			n++
		}
	}
	return n
}

func (r *scriptedRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	return r.RunWithInput(ctx, n, script, nil)
}

func (r *scriptedRunner) RunWithInput(_ context.Context, _ model.Node, script string, _ []byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, script)
	if r.failOn != "" && strings.Contains(script, r.failOn) {
		return "", fmt.Errorf("假节点拒绝了这条命令: %s", r.failOn)
	}
	switch {
	case strings.Contains(script, "statsquery"):
		return trafficReply(), nil
	case strings.Contains(script, "inbounduser"):
		return inbounduserReply(), nil
	case strings.Contains(script, "api adu"), strings.Contains(script, "api rmu"):
		return "", nil
	}
	return "", fmt.Errorf("假节点没有为这条命令准备答案: %s", script)
}

// timeAdvancingRunner 每次执行都把假时钟往前拨:模拟一条慢命令实际耗掉的时间。
type timeAdvancingRunner struct {
	clk  *fakeClock
	step time.Duration
}

func (r timeAdvancingRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	return r.RunWithInput(ctx, n, script, nil)
}

func (r timeAdvancingRunner) RunWithInput(_ context.Context, _ model.Node, script string, _ []byte) (string, error) {
	r.clk.advance(r.step)
	if strings.Contains(script, "statsquery") {
		return trafficReply(), nil
	}
	return "", nil
}

// taskWithClock 造一个时钟可拨的 Task,把库与假时钟一并交回。
func taskWithClock(t *testing.T, r node.Runner, logger *slog.Logger) (*Task, *store.Store, *fakeClock) {
	t.Helper()
	c, st := newLoggingControl(t, r, logger)
	task := NewTask(c, st, logger, 30*time.Second)
	clk := newFakeClock()
	task.now = clk.Now
	task.startedAt = clk.Now()
	return task, st, clk
}

// 采样失败的节点不该每轮都花掉一个采样预算:失败后跳过,窗口过后再试,成功即清零。
func TestNodeBacksOffAfterFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	r := &scriptedRunner{failOn: "statsquery"}
	task, st, clk := taskWithClock(t, r, logger)
	addNode(t, st, "hk", "node.example.com")

	// 第一轮:真的去采了一次,失败后进入退避(base = 30 秒)。
	task.Tick(context.Background())
	if got := r.count("statsquery"); got != 1 {
		t.Fatalf("第一轮该真去采一次,实际 %d 次", got)
	}
	snap := task.Snapshot(clk.Now())
	if len(snap.Backoffs) != 1 || snap.Backoffs[0].Failures != 1 {
		t.Fatalf("失败后该出现在退避列表里:%+v", snap.Backoffs)
	}
	if want := clk.Now().Add(30 * time.Second); !snap.Backoffs[0].SkipUntil.Equal(want) {
		t.Fatalf("首次退避该等 base=30s: got %s, want %s", snap.Backoffs[0].SkipUntil, want)
	}

	// 窗口内再跑两轮:一次都不该碰节点,「跳过」只记一条日志。
	clk.advance(time.Second)
	task.Tick(context.Background())
	clk.advance(time.Second)
	task.Tick(context.Background())
	if got := r.count("statsquery"); got != 1 {
		t.Fatalf("退避窗口里不该再碰节点,采样了 %d 次", got)
	}
	if got := strings.Count(logs.String(), "节点在退避中"); got != 1 {
		t.Fatalf("跳过的轮次只该记一次日志,记了 %d 次", got)
	}

	// 窗口过后重试:还是不通,退避翻倍到 60 秒。
	clk.advance(40 * time.Second) // 累计 42 秒,已过 30 秒的窗口
	task.Tick(context.Background())
	if got := r.count("statsquery"); got != 2 {
		t.Fatalf("窗口过后该再试一次,实际 %d 次", got)
	}
	snap = task.Snapshot(clk.Now())
	if len(snap.Backoffs) != 1 || snap.Backoffs[0].Failures != 2 {
		t.Fatalf("连续失败次数该累加:%+v", snap.Backoffs)
	}
	if wait := snap.Backoffs[0].SkipUntil.Sub(clk.Now()); wait != time.Minute {
		t.Fatalf("第二次失败该等 60 秒(翻倍),实际 %s", wait)
	}

	// 节点恢复:成功一轮即清零,不再被跳过。
	r.setFailOn("")
	clk.advance(2 * time.Minute)
	task.Tick(context.Background())
	if got := r.count("statsquery"); got != 3 {
		t.Fatalf("退避结束后该再试,实际 %d 次", got)
	}
	if snap := task.Snapshot(clk.Now()); len(snap.Backoffs) != 0 {
		t.Fatalf("成功一次就该清零,仍有退避:%+v", snap.Backoffs)
	}
	if !strings.Contains(logs.String(), "退避清零") {
		t.Fatalf("恢复时该记一条日志,日志:%s", logs.String())
	}
	if snap := task.Snapshot(clk.Now()); snap.Ticks != 5 {
		t.Fatalf("心跳该记下跑过的轮数,实际 %d", snap.Ticks)
	}
}

// 采样能过、名单这步失败的节点同样退避:这台节点通,但它的 xray API 有问题,
// 每轮都去问一遍没有意义。
func TestNodeBacksOffOnReconcileFailure(t *testing.T) {
	r := &scriptedRunner{failOn: "inbounduser"}
	task, st, clk := taskWithClock(t, r, slog.New(slog.DiscardHandler))
	n := addNode(t, st, "hk", "node.example.com")
	addInbound(t, st, n.ID, "vless-ws", 8443)

	task.Tick(context.Background())
	if r.count("statsquery") != 1 || r.count("inbounduser") != 1 {
		t.Fatalf("第一轮该采样并查名单:statsquery=%d inbounduser=%d",
			r.count("statsquery"), r.count("inbounduser"))
	}
	if snap := task.Snapshot(clk.Now()); len(snap.Backoffs) != 1 {
		t.Fatalf("收敛失败也该退避:%+v", snap.Backoffs)
	}

	clk.advance(time.Second)
	task.Tick(context.Background())
	if got := r.count("statsquery"); got != 1 {
		t.Fatalf("退避窗口里连采样都不该做,实际 %d 次", got)
	}
	if got := r.count("inbounduser"); got != 1 {
		t.Fatalf("退避窗口里不该再查名单,实际 %d 次", got)
	}
}

// 一轮超过周期是永久落后的早期信号,必须留下 Warn,同时把耗时记进心跳。
func TestTickWarnsWhenSlowerThanInterval(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	clk := newFakeClock()
	// 一条命令就把时钟拨过 30 秒的周期。
	c, st := newLoggingControl(t, timeAdvancingRunner{clk: clk, step: 40 * time.Second}, logger)
	task := NewTask(c, st, logger, 30*time.Second)
	task.now = clk.Now
	task.startedAt = clk.Now()
	addNode(t, st, "hk", "node.example.com")

	task.Tick(context.Background())

	if !strings.Contains(logs.String(), "超过了周期") {
		t.Fatalf("超过周期该打 Warn,日志:%s", logs.String())
	}
	if got := task.Snapshot(clk.Now()).LastTickSeconds; got != 40 {
		t.Fatalf("上一轮耗时该记成 40 秒,实际 %v", got)
	}
}

// 心跳:没跑过时明确说是"还没跑过",跑过之后有开始时间与耗时。
func TestSnapshotReportsHeartbeat(t *testing.T) {
	task, st, clk := taskWithClock(t, &scriptedRunner{}, slog.New(slog.DiscardHandler))

	before := task.Snapshot(clk.Now())
	if before.Ticks != 0 || before.LastTickAt != nil {
		t.Fatalf("一轮都没跑过时该说没跑过:%+v", before)
	}
	if before.IntervalSeconds != 30 {
		t.Fatalf("周期该如实报出: %v", before.IntervalSeconds)
	}
	// Backoffs 要是空切片:JSON 里 null 和 [] 是两种形状,CLI 不该被迫区分它们。
	if before.Backoffs == nil {
		t.Fatal("没有退避节点时该给空切片,不是 nil")
	}
	if !before.StartedAt.Equal(clk.Now()) {
		t.Fatalf("启动时间该是建任务那一刻: got %s, want %s", before.StartedAt, clk.Now())
	}

	addNode(t, st, "hk", "node.example.com")
	task.Tick(context.Background())

	snap := task.Snapshot(clk.Now())
	if snap.Ticks != 1 {
		t.Fatalf("Tick 次数该累加: %d", snap.Ticks)
	}
	if snap.LastTickAt == nil || !snap.LastTickAt.Equal(clk.Now()) {
		t.Fatalf("最后 Tick 的开始时间该记下: %+v", snap.LastTickAt)
	}
}

// 退避等待是 base * 2^(n-1),封顶 5 分钟。
func TestBackoffWaitDoublesAndCaps(t *testing.T) {
	cases := map[int]time.Duration{
		1:  30 * time.Second,
		2:  time.Minute,
		3:  2 * time.Minute,
		4:  4 * time.Minute,
		5:  5 * time.Minute,
		6:  5 * time.Minute,
		20: 5 * time.Minute,
	}
	for failures, want := range cases {
		if got := backoffWait(failures); got != want {
			t.Errorf("第 %d 次失败该等 %s,实际 %s", failures, want, got)
		}
	}
}
