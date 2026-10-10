// Package control 是「让节点状态和数据库一致」的那一层。
//
// 它把三样东西接在一起:数据库(期望状态)、渲染器(把期望状态变成配置)、节点
// (实际状态)。放在 server 和 task 之外的包,是因为 HTTP 接口和定时任务用的是
// 同一套动作 —— 两边各写一份,迟早会有一边漏掉某个步骤。
package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// 每核心操作预算。混合节点分别创建上下文，故障核心不能耗尽健康核心的预算。
// 这是上限而不是预期耗时；两核心均到达上限时，一轮可能超过后台采样周期。
const (
	// sampleBudget 是一个核心采样的上限，通常只有一次 statsquery。
	sampleBudget = 20 * time.Second
	// reconcileBudget 是一个核心收敛的上限:可能连着加很多用户,每条 xray api
	// 约 0.3 秒,几十条也就十几秒;60 秒是余量,不是常态。
	reconcileBudget = 60 * time.Second
	// deployBudget 是 node sync 下发一个核心的上限:推一份几十 KB 的配置 +
	// xray -test + 重启 + 等它起来,两分钟足够。
	deployBudget = 2 * time.Minute
	// installBudget 是 node install 的上限:要推 65MB(核心加两份 geodata),
	// 实测约 40 秒,慢链路按 100KB/s 算约 11 分钟。它和 CLI 侧的
	// installTimeout(15 分钟)对齐:HTTP 请求还在等的时候,不该先在节点这一层断掉。
	installBudget = 15 * time.Minute
)

// Control 持有跨节点动作需要的依赖。
type Control struct {
	store          *store.Store
	runner         node.Runner
	logger         *slog.Logger
	cacheDir       string
	capabilitySlot chan struct{}
	// Internal overrides keep deadline-exhaustion regression tests fast.
	sampleTimeout    time.Duration
	reconcileTimeout time.Duration
}

// New 接起这一层。
//
// cacheDir 是控制面存 xray 发布包的地方。它得是一个能长久留存、并且控制面进程有
// 权写的目录 —— 下发给节点的核心从这里推出去,所以不能是每次启动就没的临时目录。
func New(st *store.Store, r node.Runner, logger *slog.Logger, cacheDir string) *Control {
	if logger == nil {
		logger = slog.Default()
	}
	return &Control{store: st, runner: r, logger: logger, cacheDir: cacheDir, capabilitySlot: make(chan struct{}, 1)}
}

// DeployNode 下发节点所需的独立核心配置。QUIC 配置变化时采样并重启
// sing-box；Xray 结构性变更仍使用原有配置验证与重启流程。
func (c *Control) DeployNode(ctx context.Context, n model.Node, now time.Time) error {
	unlock := c.lockNode(n.ID)
	defer unlock()
	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return err
	}
	d, err := c.store.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		return err
	}
	xrayRequired, _ := coreRequirements(n, *d, inbounds)
	quicCtx, cancelQUIC := context.WithTimeout(ctx, deployBudget)
	quicErr := c.deploySingBox(quicCtx, n, now, false)
	cancelQUIC()
	ctx, cancelXray := context.WithTimeout(ctx, deployBudget)
	defer cancelXray()
	// Final QUIC accounting may change a shared user's quota status.
	users, err := c.store.ListUsers()
	if err != nil {
		return err
	}
	if !xrayRequired {
		return quicErr
	}
	cfg, err := render.ServerConfig(n, inbounds, users, now)
	if err != nil {
		return errors.Join(quicErr, fmt.Errorf("渲染节点 %s 的配置失败: %w", n.Name, err))
	}
	if err := node.Deploy(ctx, c.runner, n, cfg); err != nil {
		return errors.Join(quicErr, fmt.Errorf("下发到节点 %s 失败: %w", n.Name, err))
	}

	// 下发成功才记基准,失败不记:写下去的是"节点上现在跑的是这一份"这个断言,
	// 而失败的部署可能连文件都没传完。记了一个没真正生效的哈希,下一次检查就会
	// 报出一个不存在的漂移 —— 假漂移比没有漂移更坏,它会让真的那次被忽略。
	//
	// 存渲染内容的 sha256,与节点上 `sha256sum config.json` 得到的是同一种东西:
	// node.Deploy 原样写入 cfg,两边算的是同一串字节。
	if err := c.store.SetNodeDeployed(n.ID, hashOf(cfg), now); err != nil {
		return errors.Join(quicErr, fmt.Errorf("记录节点 %s 的下发哈希失败: %w", n.Name, err))
	}
	return quicErr
}

// hashOf 是配置内容的 sha256,十六进制小写 —— 与节点上 sha256sum 的输出同格式,
// 这样两边的字符串可以直接比。
func hashOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// OnlineIPs 现场问节点:此刻每个用户连着几个 IP。
//
// 这是顺路读一份 xray 已经在算的统计(statsonlineiplist),不是另起一份账本。
// 不落库、不缓存 —— 在线名单过期得太快,存它没意义;要趋势请调用方自己轮询。
func (c *Control) OnlineIPs(ctx context.Context, n model.Node) (map[string][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()
	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return nil, err
	}
	if len(model.InboundsForCore(inbounds, model.CoreSingBox)) > 0 {
		return nil, fmt.Errorf("sing-box 不提供单用户在线 IP API; 含 QUIC 的节点无法返回完整在线名单")
	}
	return node.OnlineIPs(ctx, c.runner, n)
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
	unlock := c.lockNode(n.ID)
	defer unlock()
	return c.sampleCores(ctx, n, now)
}

func (c *Control) sampleXrayTraffic(ctx context.Context, n model.Node, now time.Time) error {
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
			if err := c.store.ApplyCoreTrafficSample(sample, model.CoreXray); err != nil {
				c.logger.Warn("写流量采样失败", "node", n.Name, "user", name, "error", err)
			}
			continue
		}
		if delta.Uplink == 0 && delta.Downlink == 0 {
			continue
		}

		// 历史基准与配额账本同一事务:任一失败都可在下一轮按旧基准重试。
		if err := c.store.ApplyCoreTrafficSample(sample, model.CoreXray); err != nil {
			c.logger.Warn("写流量采样和用户账本失败", "node", n.Name, "user", name, "error", err)
		}
	}
	return nil
}

// ReconcileUsers 让节点上的人员名单收敛到数据库里的期望状态。
//
// Xray 使用动态增删用户；sing-box 缺少动态修改接口，名单变化时先采样
// 再重启第二核心。名单不变不会重启。两核心的服务与配置独立。
func (c *Control) ReconcileUsers(ctx context.Context, n model.Node, now time.Time) error {
	unlock := c.lockNode(n.ID)
	defer unlock()
	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return err
	}
	var firstErr error
	quicCtx, cancelQUIC := context.WithTimeout(ctx, c.coreReconcileBudget())
	if err := c.deploySingBox(quicCtx, n, now, false); err != nil {
		firstErr = err
	}
	cancelQUIC()
	xrayCtx, cancelXray := context.WithTimeout(ctx, c.coreReconcileBudget())
	defer cancelXray()
	users, err := c.store.ListUsers()
	if err != nil {
		return err
	}
	for _, in := range model.InboundsForCore(inbounds, model.CoreXray) {
		if err := c.reconcileInbound(xrayCtx, n, in, users, now); err != nil {
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
