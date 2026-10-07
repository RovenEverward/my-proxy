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

// SampleTraffic 采一次该节点上的用户流量并累加进数据库。
//
// 单个用户的写入失败只记日志、不中断整轮:一个用户的流量对不上,不该让其余
// 几百个人的用量也停止累计。
func (c *Control) SampleTraffic(ctx context.Context, n model.Node) error {
	traffic, err := node.QueryUserTraffic(ctx, c.runner, n)
	if err != nil {
		return err
	}
	for name, t := range traffic {
		if t.Uplink == 0 && t.Downlink == 0 {
			continue
		}
		if err := c.store.AddUserTraffic(name, t.Uplink, t.Downlink); err != nil {
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
