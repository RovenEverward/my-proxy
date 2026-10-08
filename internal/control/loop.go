package control

import (
	"context"
	"log/slog"
	"time"

	"github.com/RovenEverward/my-proxy/internal/store"
)

// DefaultInterval 是后台任务的默认间隔。
//
// 30 秒的取舍:配额超限最坏晚 30 秒才被摘掉,这点延迟对"按流量计费"没有影响;
// 而每轮要对每个节点跑几条 ssh + xray api,间隔再短就只是给节点添负担。
const DefaultInterval = 30 * time.Second

// Task 是控制面的后台周期任务。
type Task struct {
	control  *Control
	store    *store.Store
	logger   *slog.Logger
	interval time.Duration
}

func NewTask(c *Control, st *store.Store, logger *slog.Logger, interval time.Duration) *Task {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Task{control: c, store: st, logger: logger, interval: interval}
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
func (t *Task) Tick(ctx context.Context) {
	now := time.Now()

	nodes, err := t.store.ListNodes()
	if err != nil {
		t.logger.Error("读取节点列表失败", "error", err)
		return
	}

	for _, n := range nodes {
		if err := t.control.SampleTraffic(ctx, n, now); err != nil {
			t.logger.Warn("采样节点流量失败", "node", n.Name, "error", err)
		}
	}

	t.ResetDueUsers(now)

	for _, n := range nodes {
		if err := t.control.ReconcileUsers(ctx, n, now); err != nil {
			t.logger.Warn("同步节点用户失败", "node", n.Name, "error", err)
		}
	}
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
