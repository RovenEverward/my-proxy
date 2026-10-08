package control

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// DefaultInterval 是后台任务的默认间隔。
//
// 30 秒的取舍:配额超限最坏晚 30 秒才被摘掉,这点延迟对"按流量计费"没有影响;
// 而每轮要对每个节点跑几条 ssh + xray api,间隔再短就只是给节点添负担。
const DefaultInterval = 30 * time.Second

// 节点连续失败后的退避:第一次等 backoffBase,之后每失败一次翻一倍,封顶 backoffMax。
//
// base 取 30 秒正好是一个默认周期:一台不通的节点失败一次就跳过下一轮,不再每
// 30 秒掏一个采样预算;继续不通就依次跳过 2、4、8 分钟(封顶 5 分钟),不去反复
// 敲一扇已经关上的门。成功一次即清零,恢复的节点不会因为旧账被继续跳过。
//
// 为什么跳过是安全的:批 1 之后采样读的是节点的累计值,跳过的几轮不会丢账 ——
// 下次采到仍然从上次留下的基准往后算,少采只是把增量并到下一次;而节点本身不可达
// 时收敛也做不了任何事,跳过不会让节点上的名单更不正确。
const (
	backoffBase = 30 * time.Second
	backoffMax  = 5 * time.Minute
)

// Task 是控制面的后台周期任务。
type Task struct {
	control  *Control
	store    *store.Store
	logger   *slog.Logger
	interval time.Duration
	// now 可注入:退避窗口和心跳都要能在测试里拨表,而不是真等 5 分钟。
	now func() time.Time

	// mu 保护下面这一组「现在怎么样」的运行状态。它们只在内存里,不落库。
	mu        sync.Mutex
	startedAt time.Time
	ticks     uint64
	lastTick  tickRecord
	nodeFail  map[int64]*nodeBackoff
}

// tickRecord 是最近一轮的时间信息。
type tickRecord struct {
	at       time.Time
	duration time.Duration
}

// nodeBackoff 是一台节点当前的失败退避状态。
type nodeBackoff struct {
	name      string
	failures  int
	skipUntil time.Time
	// skipLogged 标记这个退避窗口已经记过一条「跳过」日志:掉线几小时会跳过
	// 十几轮,每轮刷一行同样的话只会把日志冲成噪音。
	skipLogged bool
}

func NewTask(c *Control, st *store.Store, logger *slog.Logger, interval time.Duration) *Task {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	now := time.Now()
	return &Task{
		control:   c,
		store:     st,
		logger:    logger,
		interval:  interval,
		now:       time.Now,
		startedAt: now,
		nodeFail:  map[int64]*nodeBackoff{},
	}
}

// Run 阻塞地跑周期任务,直到 ctx 结束。
//
// 先立刻跑一轮再等第一个 tick:控制面重启后不该有长达一个间隔的空窗期,那段时间
// 里到期和超限的用户还挂在节点上。
func (t *Task) Run(ctx context.Context) {
	t.Tick(ctx)

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.Tick(ctx)
		}
	}
}

// Tick 跑一轮,分三步,顺序是有意的:
//
//  1. 采样。先把节点上的计数收进数据库,超限的用户这一轮就能被判定出来。
//  2. 重置。采样之后再做周期重置,刚过去的那个周期的用量才不会被算进新周期。
//     重置只动 users.used_traffic,traffic_samples 不动,所以区间用量仍然算得回来。
//  3. 收敛名单。用重置并采样后的状态决定谁该在节点上,该加的加、该摘的摘。
//
// 正在退避的节点整轮跳过;采样就失败的节点这一轮不再收敛 —— 它已经不通了,收敛
// 试了也只是再等一个预算。一台节点失败不影响其余节点:循环继续往下走。
func (t *Task) Tick(ctx context.Context) {
	started := t.now()
	defer t.finishTick(started)

	nodes, err := t.store.ListNodes()
	if err != nil {
		t.logger.Error("读取节点列表失败", "error", err)
		return
	}

	// 这一轮不再处理该节点的标记。采样失败与「本来就在退避」都进这里。
	skip := make(map[int64]bool, len(nodes))

	for _, n := range nodes {
		if t.inBackoff(n, started) {
			skip[n.ID] = true
			continue
		}
		if err := t.control.SampleTraffic(ctx, n, started); err != nil {
			t.logger.Warn("采样节点流量失败", "node", n.Name, "error", err)
			t.recordFailure(n, started)
			skip[n.ID] = true
		}
	}

	t.ResetDueUsers(started)

	for _, n := range nodes {
		if skip[n.ID] {
			continue
		}
		if err := t.control.ReconcileUsers(ctx, n, started); err != nil {
			t.logger.Warn("同步节点用户失败", "node", n.Name, "error", err)
			t.recordFailure(n, started)
			continue
		}
		t.recordSuccess(n)
	}
}

// finishTick 记一轮的心跳,并在这一轮超过周期时告警。
//
// 「这一轮超过了周期」是永久落后的早期信号:单轮超时只是慢,轮轮超时意味着任务
// 永远追不上,得在它变成静默停摆之前说出来。
func (t *Task) finishTick(started time.Time) {
	duration := t.now().Sub(started)

	t.mu.Lock()
	t.ticks++
	t.lastTick = tickRecord{at: started, duration: duration}
	t.mu.Unlock()

	if duration > t.interval {
		t.logger.Warn("后台任务这一轮超过了周期,再这样下去会一直落后",
			"耗时", duration.Round(time.Millisecond), "周期", t.interval)
	}
}

// inBackoff 报告该节点是否还在退避窗口里,并在窗口内第一次被跳过时记一条日志。
func (t *Task) inBackoff(n model.Node, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	b := t.nodeFail[n.ID]
	if b == nil || !now.Before(b.skipUntil) {
		return false
	}
	if !b.skipLogged {
		b.skipLogged = true
		t.logger.Info("节点在退避中,这轮跳过",
			"node", n.Name, "连续失败", b.failures, "跳过到", b.skipUntil.Format(time.RFC3339))
	}
	return true
}

// recordFailure 记一次节点失败并推后下一次尝试。
func (t *Task) recordFailure(n model.Node, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	b := t.nodeFail[n.ID]
	if b == nil {
		b = &nodeBackoff{}
		t.nodeFail[n.ID] = b
	}
	b.name = n.Name
	b.failures++
	b.skipUntil = now.Add(backoffWait(b.failures))
	// 新窗口要重新记一次日志:上一个窗口那行说的已经是旧时间了。
	b.skipLogged = false

	t.logger.Warn("节点连续失败,退避后再试",
		"node", n.Name, "连续失败", b.failures, "暂停到", b.skipUntil.Format(time.RFC3339))
}

// recordSuccess 成功一轮就把退避清零:恢复的节点不该按旧账继续被跳过。
func (t *Task) recordSuccess(n model.Node) {
	t.mu.Lock()
	defer t.mu.Unlock()

	b := t.nodeFail[n.ID]
	if b == nil || b.failures == 0 {
		return
	}
	delete(t.nodeFail, n.ID)
	t.logger.Info("节点已恢复,退避清零", "node", n.Name)
}

// backoffWait 是第 failures 次连续失败后要等多久:backoffBase * 2^(n-1),封顶 backoffMax。
func backoffWait(failures int) time.Duration {
	wait := backoffMax
	// 2^10 * 30 秒已经远超封顶,再往上左移只会溢出成负数。
	if shift := failures - 1; shift >= 0 && shift < 10 {
		if w := backoffBase << shift; w < wait {
			wait = w
		}
	}
	return wait
}

// Snapshot 返回控制面此刻的自报状态,给 GET /api/status 用。
//
// 这些都是内存里的「现在怎么样」,不落库:控制面重启后从零再算,库里的账一件
// 不少;把启动时间、上一轮耗时写进库,只是多一份会过期的真相。
func (t *Task) Snapshot(now time.Time) model.ControlStatus {
	t.mu.Lock()
	defer t.mu.Unlock()

	st := model.ControlStatus{
		StartedAt:       t.startedAt,
		UptimeSeconds:   now.Sub(t.startedAt).Seconds(),
		IntervalSeconds: t.interval.Seconds(),
		Ticks:           t.ticks,
		Backoffs:        []model.NodeBackoff{},
	}
	if !t.lastTick.at.IsZero() {
		at := t.lastTick.at
		st.LastTickAt = &at
		st.LastTickSeconds = t.lastTick.duration.Seconds()
	}
	for id, b := range t.nodeFail {
		if !now.Before(b.skipUntil) {
			continue // 窗口已过,下一轮就会重试,不算"正在退避"
		}
		st.Backoffs = append(st.Backoffs, model.NodeBackoff{
			NodeID:    id,
			NodeName:  b.name,
			Failures:  b.failures,
			SkipUntil: b.skipUntil,
		})
	}
	sort.Slice(st.Backoffs, func(i, j int) bool { return st.Backoffs[i].NodeID < st.Backoffs[j].NodeID })
	return st
}

// ResetDueUsers 把走到重置点的用户的本周期用量归零。
//
// 一个用户写失败只记日志:其余用户该重置还是要重置,不能因为一条记录卡住整轮。
func (t *Task) ResetDueUsers(now time.Time) {
	users, err := t.store.ListUsers()
	if err != nil {
		t.logger.Error("读取用户列表失败", "error", err)
		return
	}
	for i := range users {
		u := &users[i]
		if !u.ResetDue(now) {
			continue
		}
		at := u.AdvanceReset(now)
		if err := t.store.ResetUserTraffic(u.Username, at); err != nil {
			t.logger.Warn("重置用户流量失败", "user", u.Username, "error", err)
			continue
		}
		t.logger.Info("周期重置用户流量",
			"user", u.Username, "strategy", u.ResetStrategy, "next_at", at)
	}
}
