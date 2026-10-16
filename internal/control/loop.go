package control

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
	"github.com/RovenEverward/my-proxy/internal/updatecheck"
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

	// alerter 是巡检发现问题时往哪儿喊。默认是日志 + webhook(见 defaultAlerter),
	// 留成接口也让测试能换成记账的假实现。
	alerter Alerter

	// mu 保护下面这一组「现在怎么样」的运行状态。它们只在内存里,不落库。
	mu        sync.Mutex
	startedAt time.Time
	ticks     uint64
	lastTick  tickRecord
	nodeFail  map[int64]*nodeBackoff

	// armed 记着每台节点最近一轮报出过哪些告警 Key(nodeID → key 集合)。
	//
	// 记它只为一件事:条件消失时能销。告警器自己只记得"报过没报过",不知道条件
	// 还在不在;而"上一轮报过、这一轮没报"正是条件消失的定义。
	//
	// 按节点分开存,因为"这一轮没报"必须是相对**查过**的那一轮说的:节点在退避里、
	// 或者这一轮还没轮到它时,它的记录要保持原样 —— 那是"没问",不是"好了"。
	armed map[int64]map[string]struct{}

	// updates 查「有没有比现在更新的发布」。它自己带静默期,所以挂在这里、
	// 每轮问一次是安全的;结论不落库,重启后重问一遍即可。
	updates *updatecheck.Checker
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
	t := &Task{
		control:   c,
		store:     st,
		logger:    logger,
		interval:  interval,
		now:       time.Now,
		startedAt: now,
		nodeFail:  map[int64]*nodeBackoff{},
		armed:     map[int64]map[string]struct{}{},
	}
	t.alerter = t.defaultAlerter()
	t.updates = updatecheck.New(buildinfo.Repo(), updatecheck.DefaultInterval)
	return t
}

// defaultAlerter 拼出开箱即用的告警出口:日志一份,配了 webhook 地址再发一份。
//
// 在这里拼而不是让调用方(serve)拼,是因为"忘了接 webhook"是一种没人会发现的
// 故障:告警照常进日志,只有坐到 webhook 那一头等消息的人才察觉收不到。默认
// 接好,配地址即生效。
func (t *Task) defaultAlerter() Alerter {
	return NewFanoutAlerter(
		NewLogAlerter(t.logger),
		NewWebhookAlerter(t.webhookURL, t.logger),
	)
}

// webhookURL 现取设置里的 webhook 地址;读不到当没配。
//
// 每次现取而不是缓存在 Task 上:地址可以在运行中被改,缓存就得有人负责失效,
// 而这点查询开销只会发生在有告警/有通知的时候。
func (t *Task) webhookURL() string {
	s, err := t.store.GetSettings()
	if err != nil {
		return ""
	}
	return s.WebhookURL
}

// SetAlerter 换掉告警出口。
//
// 默认是日志 + webhook(见 defaultAlerter);传 nil 恢复默认。留这个口子是让
// 测试能用一个记账的实现去断言"报了几次、什么时候销的"。
func (t *Task) SetAlerter(a Alerter) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a == nil {
		a = t.defaultAlerter()
	}
	t.alerter = a
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
// 单核心节点保留整节点退避。混合节点不能用一个核心的故障跳过另一个核心：
// 每轮分别尝试采样和收敛，错误仍记录退避状态，但该状态不阻止健康核心处理用户。
//
// 结账之后还有两件"顺便做"的事:给用户发提醒、抽查节点状态。两者都在 finishTick
// 之后,理由同 sweepNodeStatus 上的注释 —— 它们的耗时不该算进"一轮多久"。
// 提醒排在巡检之前:提醒是给人看的,而巡检本来就每 10 轮才做一次。
func (t *Task) Tick(ctx context.Context) {
	started := t.now()

	nodes, err := t.store.ListNodes()
	if err != nil {
		t.logger.Error("读取节点列表失败", "error", err)
		t.finishTick(started)
		return
	}

	// 单核心继续整节点退避；混合核心故障不能阻断另一个核心的撤销。
	skip := make(map[int64]bool, len(nodes))
	mixed := make(map[int64]bool, len(nodes))
	failed := make(map[int64]bool, len(nodes))
	for _, n := range nodes {
		mixed[n.ID] = t.control.hasMultipleCores(n)
		if !mixed[n.ID] && t.inBackoff(n, started) {
			skip[n.ID] = true
			continue
		}
		if err := t.control.SampleTraffic(ctx, n, started); err != nil {
			t.logger.Warn("采样节点流量失败", "node", n.Name, "error", err)
			t.recordFailure(n, started)
			failed[n.ID] = true
			skip[n.ID] = !mixed[n.ID]
		}
	}

	t.ResetDueUsers(started)

	for _, n := range nodes {
		if skip[n.ID] {
			continue
		}
		if err := t.control.ReconcileUsers(ctx, n, started); err != nil {
			t.logger.Warn("同步节点用户失败", "node", n.Name, "error", err)
			if !failed[n.ID] {
				t.recordFailure(n, started)
			}
			continue
		}
		if !failed[n.ID] {
			t.recordSuccess(n)
		}
	}

	// 先结这一轮,再巡检。
	//
	// 顺序是有意的:巡检要 ssh 出去问每台节点,它的耗时**不该算进"一轮多久"**。
	// 心跳回答的是"后台任务跟不跟得上 30 秒的周期",而巡检本来就每 10 轮才做
	// 一次、天生比周期慢 —— 把它算进去,心跳会在每个第 10 轮跳一次、报出一个
	// 并不存在的"落后",而真正需要警惕的(采样+收敛本身变慢)反而被掩盖。
	t.finishTick(started)
	t.notifyUsers(ctx, t.now())
	t.checkUpdates(ctx)
	t.sweepNodeStatus(ctx, nodes, t.now())
}

// checkUpdates 顺手问一次有没有新发布。
//
// 它和 notifyUsers、sweepNodeStatus 一样排在 finishTick 之后:一次 HTTP 往返,
// 慢起来能有几秒,不该算进"一轮多久"的心跳里。Checker 自己带静默期,所以每轮
// 调用不等于每轮发请求 —— 绝大多数轮次只是一次内存里的比较。
//
// 失败只记日志:更新检查连不上 GitHub 是常态(控制面本来就可能跑在没有外网的
// 机器上),不该因此每 30 秒刷一条警告。错误会留在 Snapshot 里,`mpx status`
// 想看的时候能看到。
func (t *Task) checkUpdates(ctx context.Context) {
	res := t.updates.Check(ctx, false)
	if res.Error != "" {
		t.logger.Debug("检查更新失败", "error", res.Error)
		return
	}
	if res.Available() {
		t.logger.Info("有新的发布", "当前", res.Current, "最新", res.Latest, "落后", res.Behind, "发布页", res.ReleaseURL)
	}
}

// sweepEvery 是巡检的间隔:每多少个 Tick 查一次节点状态。
//
// 10 个 Tick(默认 30 秒周期下 = 5 分钟)的理由是这三件事的频率完全不同:
//   - 用户增删要"马上"生效,那是收敛的活,30 秒一轮;
//   - 流量采样是计费依据,也要跟着周期走;
//   - 而服务在不在跑、证书还有几天过期、配置有没有被人改过,这三件事的变化速度
//     是"天"级的。每 30 秒 ssh 上去问一次,一分钟 2 次往返换来的信息几乎总是
//     和上一轮一样,而每次都要占一条连接、一个 openssl 进程。
//
// 5 分钟也压得住真出事的场景:服务挂了到被发现,最坏也只延迟 5 分钟,而这批的
// 定位本来就是"发现并告诉你",不是自动处置。再稀疏(比如 1 小时)就开始不像
// 监控了,证书临期这类事的提前量本来就只有几天。
const sweepEvery = 10

// sweepBudget 是整轮巡检的上限。
//
// 一台节点的检查有自己的预算(checkBudget 30 秒),但一轮要查十几台;只按台数
// 算的话,几台慢节点能把后台的正事(采样、到期、收敛)压住好几分钟 —— 巡检是
// "顺便看看",不该让计费那边等。超出的节点这一轮跳过,5 分钟后下一轮继续。
const sweepBudget = 45 * time.Second

// sweepNodeStatus 抽查节点状态:查一遍、落库、该喊的喊。
//
// 它**绝不**能让一轮变长 —— 这是接进 Tick 时最容易犯的错。所以:
//  1. 只有轮到的那一轮才做(每 sweepEvery 轮,第一次也做);
//  2. 每台节点各用 checkBudget 包住自己的上下文,一台卡住不牵连别的;
//  3. 整轮另有一个 sweepBudget 的上限,慢节点多了也不会把采样/收敛压住几分钟;
//  4. 已经在退避里的节点直接跳过 —— 上一轮刚失败过,现在再问一遍只会让它
//     失败得更快,而且它的状态库里已经有了(就是那条 Reachable=false)。
//
// 第一次也做:控制面刚起来时 `mpx node list` 不该空着五分钟没人知道为什么。
//
// 注意查询失败也**不进退避**:退避是给"收敛失败"用的,它会让节点这一轮完全
// 不参与采样和同步;而"状态没问到"只是少了一条信息,不该让一个正常服务着的
// 节点因为 ssh 抖动就被停掉采样。用各自的预算和上限分开处理。
func (t *Task) sweepNodeStatus(ctx context.Context, nodes []model.Node, now time.Time) {
	t.mu.Lock()
	ticks := t.ticks
	t.mu.Unlock()
	// ticks 从 1 开始(第一轮在 Tick 结束时就已经结了),所以 1 就是"第一次"。
	if ticks != 1 && ticks%sweepEvery != 0 {
		return
	}

	sctx, cancel := context.WithTimeout(ctx, sweepBudget)
	defer cancel()

	checked := 0
	for _, n := range nodes {
		if sctx.Err() != nil {
			t.logger.Warn("这一轮巡检超时,剩下的节点留到下一轮",
				"已检查", checked, "共", len(nodes))
			return
		}
		if t.inBackoff(n, now) {
			continue
		}
		st, err := t.control.CheckNode(sctx, n, now)
		if err != nil {
			t.logger.Warn("检查节点状态失败", "node", n.Name, "error", err)
			continue
		}
		if err := t.store.UpsertNodeStatus(st); err != nil {
			t.logger.Warn("写入节点状态失败", "node", n.Name, "error", err)
			continue
		}
		t.raiseNodeAlerts(n, st, now)
		checked++
	}
}

// raiseNodeAlerts 按一份状态决定这一轮报什么,并把上一轮报过、这一轮不再成立的销掉。
//
// 做法是"报一轮、比一轮":这一轮该报的 Key 收进 current,再和这台节点上一轮的
// current 比 —— 少了的就是条件消失了。这样"报"和"销"共用同一份判断,不会出现
// 两处各拼一遍 Key 而拼得不一样(证书告警就栽在这上面:报的时候用证书路径拼,
// 销的时候用了空串,于是永远销不掉)。
func (t *Task) raiseNodeAlerts(n model.Node, st model.NodeStatus, now time.Time) {
	current := map[string]struct{}{}
	report := func(kind model.Kind, extra, msg string) {
		key := alertKey(kind, n.ID, extra)
		current[key] = struct{}{}
		t.alerter.Alert(model.Alert{
			Key:      key,
			Kind:     kind,
			NodeID:   n.ID,
			NodeName: n.Name,
			Message:  fmt.Sprintf("节点 %s:%s", n.Name, msg),
			At:       now,
		})
	}

	if !st.Reachable {
		// 连不上时只报这一件:服务在不在跑、配置对不对、证书还有几天,这时全都
		// 无从得知,拿上一轮的值继续报只会把一条故障说成三条。
		report(model.KindNodeUnreachable, "", "节点连不上:"+st.Error)

		// 而且**不销**别的:看不到和好了是两件事。把上一轮的记录原样留着,等看得
		// 见了再判 —— 少了这一步,一次网络抖动就会在日志里留下"证书已恢复"。
		t.mu.Lock()
		remains := t.armed[n.ID]
		if remains == nil {
			remains = map[string]struct{}{}
		}
		for key := range current {
			remains[key] = struct{}{}
		}
		t.armed[n.ID] = remains
		t.mu.Unlock()
		return
	}

	if !st.Active {
		report(model.KindNodeInactive, "", "核心服务没有在跑")
	}

	// 从没下发过不算漂移:没有基准可比,这一批的漂移定义就是"和我们推上去的那
	// 份不一样"。接入一台本来就在跑的机器时正是这个状态(README 里那条路径),
	// 那时报漂移是假消息。
	if n.Deployed() && st.ObservedHash != "" && st.ObservedHash != n.DeployedHash {
		report(model.KindNodeDrift, "",
			"节点上的配置和我们下发的不一样(可能是你自己改的,也可能是别人改的)")
	}

	// 证书临期,每张一个 Key。按路径而不是按入站:一个文件被多个入站共用是常态
	// (那时它只该报一次),而同一个入站换了证书文件是两件事 —— 旧的那张已经不
	// 用了,它对应的 Key 该销掉。
	for _, c := range st.Certificates {
		if c.NotAfter != nil && c.DaysLeft > model.CertWarnDays {
			continue
		}
		msg := fmt.Sprintf("证书 %s 还有 %d 天到期", c.Path, c.DaysLeft)
		if c.NotAfter == nil {
			// 读不到到期时间:openssl 没装,或者文件已经不在。报出来,因为这两种
			// 情况都会让"什么时候到期"这个问题失去答案 —— 而失去答案比"还有
			// 100 天"危险得多。
			msg = "证书到期时间读不到:" + c.Error
		}
		report(model.KindCertExpiring, c.Path, msg)
	}

	// 上一轮报过、这一轮没再报的,就是好了。
	t.mu.Lock()
	previous := t.armed[n.ID]
	t.armed[n.ID] = current
	t.mu.Unlock()
	for key := range previous {
		if _, still := current[key]; !still {
			t.alerter.Resolve(key, now)
		}
	}
}

// finishTick 记下这一轮的结束:次数、耗时,以及"耗时是否已经贴着周期"。
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
		Version:         buildinfo.Version(),
		Update:          t.updateInfo(),
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

// ControlUpdate 返回控制面自己这一份版本结论,给 GET /api/update 用。
//
// 结论来自同一个 updateInfo:接口与 /api/status 报的必须是同一件事,不能各算
// 各的 —— 两处对不上时,使用者没法知道该信哪个。这里不发任何网络请求,上游的
// 检查由后台任务按自己的节奏做。
func (t *Task) ControlUpdate() model.UpdatePart {
	return model.UpdatePartFrom(t.updateInfo())
}

// RefreshControlUpdate 强制向上游查一次,再给出与 ControlUpdate 同一套判据的结论。
//
// 给 GET /api/update?refresh=1 用:后台任务按静默期自己跑,但"刚发了新版想立刻
// 看到"需要一个不等静默期的手动入口。查询失败不返回 error —— Checker 会把失败
// 记进结论的 Error 字段,上一次的版本信息照常带回来。
func (t *Task) RefreshControlUpdate(ctx context.Context) model.UpdatePart {
	if t.updates != nil {
		last := t.updates.Snapshot().CheckedAt
		if last.IsZero() || time.Since(last) >= updatecheck.MinRefreshInterval {
			t.updates.Check(ctx, true)
		}
	}
	return t.ControlUpdate()
}

// updateInfo 把更新检查的结论翻成 API 的形状。
//
// 当前版本优先用编译进去的 buildinfo:定在二进制里的版本号不会因为运行环境换了
// 个配置文件就不一样。只有它为空(从源码直接编)时才退回查出来的那个。
func (t *Task) updateInfo() model.UpdateInfo {
	if t.updates == nil {
		return model.UpdateInfo{Current: buildinfo.Version()}
	}
	res := t.updates.Snapshot()
	out := model.UpdateInfo{
		Current:     res.Current,
		Latest:      res.Latest,
		Behind:      res.Behind,
		URL:         res.ReleaseURL,
		Notes:       res.Notes,
		PublishedAt: res.PublishedAt,
		Error:       res.Error,
	}
	// CheckedAt 在 checker 里是零值表示"没查过",这里翻成指针:API 上 null 比
	// "0001-01-01" 好认。
	if !res.CheckedAt.IsZero() {
		at := res.CheckedAt
		out.CheckedAt = &at
	}
	if buildinfo.Version() != "" {
		out.Current = buildinfo.Version()
	}
	return out
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
