// Package control 是「让节点状态和数据库一致」的那一层。
//
// 它把三样东西接在一起:数据库(期望状态)、渲染器(把期望状态变成配置)、节点
// (实际状态)。放在 server 和 task 之外的包,是因为 HTTP 接口和定时任务用的是
// 同一套动作 —— 两边各写一份,迟早会有一边漏掉某个步骤。
package control

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// 单节点操作的预算:一个节点在一轮动作里最多花多久。
//
// 这是「上限」而不是「预期耗时」:超了就把这一节点这一轮记成失败,继续下一个 ——
// 一台节点卡住不该让其余节点这一轮什么都收不到。每个数都比 node.DefaultTimeout
// 紧得多,因为调用方知道这类命令要多久,兜底值只是保险。
const (
	// sampleBudget 是采样一台节点的上限:节点上就一条 statsquery。20 秒必须留在
	// 30 秒的周期之内,慢过周期就会让后台任务永久落后。
	sampleBudget = 20 * time.Second
	// reconcileBudget 是收敛一台节点的上限:可能连着加很多用户,每条 xray api
	// 约 0.3 秒,几十条也就十几秒;60 秒是余量,不是常态。
	reconcileBudget = 60 * time.Second
	// deployBudget 是 node sync 下发一台节点的上限:推一份几十 KB 的配置 +
	// xray -test + 重启 + 等它起来,两分钟足够。
	deployBudget = 2 * time.Minute
	// installBudget 是 node install 的上限:要推 65MB(核心加两份 geodata),
	// 实测约 40 秒,慢链路按 100KB/s 算约 11 分钟。它和 CLI 侧的
	// installTimeout(15 分钟)对齐:HTTP 请求还在等的时候,不该先在节点这一层断掉。
	installBudget = 15 * time.Minute
)

// Control 持有跨节点动作需要的依赖。
type Control struct {
	store    *store.Store
	runner   node.Runner
	logger   *slog.Logger
	cacheDir string
}

// New 接起这一层。
//
// cacheDir 是控制面存 xray 发布包的地方。它得是一个能长久留存、并且控制面进程有
// 权写的目录 —— 下发给节点的核心从这里推出去,所以不能是每次启动就没的临时目录。
func New(st *store.Store, r node.Runner, logger *slog.Logger, cacheDir string) *Control {
	if logger == nil {
		logger = slog.Default()
	}
	return &Control{store: st, runner: r, logger: logger, cacheDir: cacheDir}
}

// DeployNode 渲染该节点的完整配置并下发,然后重启 xray。
//
// 用于「结构性」变更:新增入站、改端口、换证书。这类改动 xray 只能重启才生效。
func (c *Control) DeployNode(ctx context.Context, n model.Node, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, deployBudget)
	defer cancel()

	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return err
	}
	users, err := c.store.ListUsers()
	if err != nil {
		return err
	}
	cfg, err := render.ServerConfig(n, inbounds, users, now)
	if err != nil {
		return fmt.Errorf("渲染节点 %s 的配置失败: %w", n.Name, err)
	}
	if err := node.Deploy(ctx, c.runner, n, cfg); err != nil {
		return fmt.Errorf("下发到节点 %s 失败: %w", n.Name, err)
	}
	return nil
}

// SampleTraffic 采一次该节点上的用户流量,算出相对上次采样的增量并累加进数据库。
//
// 读的是 xray 的累计计数(见 node.QueryUserTraffic),增量由「本次累计 − 上次
// 累计」得到,上次值从 traffic_samples 里读最近一行。每采到一笔变化就把这次的
// 累计值追加进历史表:它既是下次的基准,也是事后重算用量的依据。
//
// 累计值回退 = 节点上的 xray 重启过。这一笔不累加(负增量会把用户用量倒扣回去),
// 只打日志并把新的累计值存成基准,免得下一轮又按旧基准报同一个回退。
//
// 单个用户的写入失败只记日志、不中断整轮:一个用户的流量对不上,不该让其余
// 几百个人的用量也停止累计。
func (c *Control) SampleTraffic(ctx context.Context, n model.Node, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()

	traffic, err := node.QueryUserTraffic(ctx, c.runner, n)
	if err != nil {
		// 节点不可达:这一轮什么都不做,不写历史也不动基准。漏掉的这段时间不会
		// 丢账 —— 下一轮采到的累计值仍然是从上次留下的基准往后算。
		return err
	}

	last, err := c.store.LatestTrafficSamples(n.ID)
	if err != nil {
		return err
	}

	for name, t := range traffic {
		var prev *model.TrafficSample
		if p, ok := last[name]; ok {
			prev = &p
		}
		sample := model.TrafficSample{
			Username: name, NodeID: n.ID, SampledAt: now,
			Uplink: t.Uplink, Downlink: t.Downlink,
		}
		delta := sample.DeltaFrom(prev)

		if delta.Restarted {
			c.logger.Warn("节点流量计数回退,疑似 xray 重启过,本轮不累加",
				"node", n.Name, "user", name,
				"prev_uplink", prev.Uplink, "prev_downlink", prev.Downlink,
				"uplink", t.Uplink, "downlink", t.Downlink)
			// 新的累计值要存成基准:不存的话下一轮又拿旧基准算出同一个回退。
			if err := c.store.RecordTrafficSample(sample); err != nil {
				c.logger.Warn("写流量采样失败", "node", n.Name, "user", name, "error", err)
			}
			continue
		}
		if delta.Uplink == 0 && delta.Downlink == 0 {
			continue
		}

		// 先落历史再累加:采样是下一次的基准,也是账本。反过来写的话,历史写失败
		// 会让下一轮拿同一个基准再算一遍,把同一段流量加两次;而累加失败只是这一
		// 笔晚到,历史里有,能看出来也能重算。
		if err := c.store.RecordTrafficSample(sample); err != nil {
			c.logger.Warn("写流量采样失败", "node", n.Name, "user", name, "error", err)
			continue
		}
		if err := c.store.AddUserTraffic(name, delta.Uplink, delta.Downlink); err != nil {
			c.logger.Warn("累加用户流量失败", "node", n.Name, "user", name, "error", err)
		}
	}
	return nil
}

// ReconcileUsers 让节点上的人员名单收敛到数据库里的期望状态。
//
// 只做增删用户,不重启 xray:配额恢复、到期续费都会走到这里,若每次都重启,
// 节点上所有人的连接都会跟着断一次。
func (c *Control) ReconcileUsers(ctx context.Context, n model.Node, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, reconcileBudget)
	defer cancel()

	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return err
	}
	users, err := c.store.ListUsers()
	if err != nil {
		return err
	}

	var firstErr error
	for _, in := range inbounds {
		if err := c.reconcileInbound(ctx, n, in, users, now); err != nil {
			c.logger.Warn("同步入站人员失败", "node", n.Name, "inbound", in.TagOrDefault(), "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (c *Control) reconcileInbound(ctx context.Context, n model.Node, in model.Inbound, users []model.User, now time.Time) error {
	tag := in.TagOrDefault()

	actual, err := node.InboundUsers(ctx, c.runner, n, tag)
	if err != nil {
		return err
	}
	add, remove := planChanges(users, in.ID, actual, now)

	// 先加后减:新用户先能连上,再摘掉不该有的,中途失败也不会让名单空掉。
	for _, u := range add {
		if err := node.AddUser(ctx, c.runner, n, in, u, now); err != nil {
			return fmt.Errorf("添加用户 %s 失败: %w", u.Username, err)
		}
	}
	for _, name := range remove {
		if err := node.RemoveUser(ctx, c.runner, n, tag, name); err != nil {
			return fmt.Errorf("移除用户 %s 失败: %w", name, err)
		}
	}
	return nil
}

// planChanges 算出「该给这个入站加谁、该摘掉谁」。
//
// 无 IO 的纯函数:收敛逻辑最容易写错,把它和跑命令分开,才能只对着两个名单
// 穷举验证。
func planChanges(users []model.User, inboundID int64, actual []string, now time.Time) (add []model.User, remove []string) {
	want := expectedUsers(users, inboundID, now)

	actualSet := make(map[string]bool, len(actual))
	for _, name := range actual {
		actualSet[name] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, u := range want {
		wantSet[u.Username] = true
	}

	for _, u := range want {
		if !actualSet[u.Username] {
			add = append(add, u)
		}
	}
	for _, name := range actual {
		if !wantSet[name] {
			remove = append(remove, name)
		}
	}
	return add, remove
}

// expectedUsers 是「这个入站此刻应该有哪些人」,按用户名排序。
func expectedUsers(users []model.User, inboundID int64, now time.Time) []model.User {
	out := make([]model.User, 0, len(users))
	for _, u := range users {
		if u.Status(now) != model.StatusActive {
			continue
		}
		if !u.AllowsInbound(inboundID) {
			continue
		}
		out = append(out, u)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}
