package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/singboxbuild"
)

var coreLocks sync.Map

func (c *Control) lockNode(id int64) func() {
	key := struct {
		control *Control
		node    int64
	}{c, id}
	m, _ := coreLocks.LoadOrStore(key, new(sync.Mutex))
	mutex := m.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func (c *Control) InstallNodeCore(ctx context.Context, n model.Node, core model.Core, version string) (string, error) {
	if core == model.CoreXray {
		return c.InstallNode(ctx, n, version)
	}
	if core != model.CoreSingBox {
		return "", fmt.Errorf("unsupported core %q", core)
	}
	ctx, cancel := context.WithTimeout(ctx, installBudget)
	defer cancel()
	unlock := c.lockNode(n.ID)
	defer unlock()
	goos, arch, err := node.Platform(ctx, c.runner, n)
	if err != nil {
		return "", err
	}
	files, err := singboxbuild.Fetch(ctx, singboxbuild.Request{Version: version, OS: goos, Arch: arch, Dir: c.cacheDir})
	if err != nil {
		return "", err
	}
	d, err := c.store.GetCoreDeployment(n.ID, core)
	if err != nil {
		return "", err
	}
	// Updating a running binary must use the same accounting boundary as config
	// changes. Installation replaces the on-disk executable, not the running one.
	if err := node.InstallSingBox(ctx, c.runner, n, *d, files.Binary); err != nil {
		return "", err
	}
	if err := c.deploySingBox(ctx, n, time.Now(), true, false); err != nil {
		return files.Version, err
	}
	return files.Version, nil
}

func (c *Control) deploySingBox(ctx context.Context, n model.Node, now time.Time, force, verifyRemote bool) error {
	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return err
	}
	d, err := c.store.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		return err
	}
	if len(model.InboundsForCore(inbounds, model.CoreSingBox)) == 0 && d.DeployedHash == "" {
		return nil
	}
	users, err := c.store.ListUsers()
	if err != nil {
		return err
	}
	cfg, err := render.SingBoxServerConfig(n, *d, inbounds, users, now)
	if err != nil {
		return err
	}
	hash := hashOf(cfg)
	// 真要下发时证书必须在 sing-box check 之前就位:校验会打开配置里的
	// certificate_path / key_path,缺文件的 sync 死在校验,服务还没动。
	// certsEnsured 标记"这一轮已经查过",免得 verifyRemote 分支先补完、
	// 走到下发前又查一遍 —— 一条 ssh 也是钱。
	certsEnsured := false
	certsReload := false
	if !force && hash == d.DeployedHash {
		if !verifyRemote {
			// 无 ssh 的快路径(周期收敛):渲染结果与"最后一次下发成功"的
			// 基准一致就跳过。失败的下发会把基准清掉(见下方错误分支),
			// 所以 == 等价于"磁盘上确实是这一份"。
			return nil
		}
		// 结构级下发(node sync / 镜像增删)本来就发一把 ssh,顺手对一次
		// 磁盘哈希:失败的下发、人工改文件都可能让磁盘与基准脱节,那正是
		// node sync 该修的东西。对不上或读不到都继续往下走。
		//
		// 先把证书补齐再比对:配置没变但有人在节点上删了证书时,光对
		// 哈希会判"不用发",而 sing-box check 会因为缺文件挂掉 —
		// node sync 是唯一会来修它的入口,不能漏。
		//
		// 托管证书换过也要继续往下走:证书变化不进 config.json 的哈希,
		// 磁盘配置对得上不代表服务已经读到新证书(进程里还是启动时加载
		// 的那张)。补自签不算 —— 进程内存里那张与磁盘一样能撑握手。
		var err error
		if certsReload, err = node.EnsureTLSCertificates(ctx, c.runner, n, model.InboundsForCore(inbounds, model.CoreSingBox)); err != nil {
			return err
		}
		certsEnsured = true
		if !certsReload {
			if onDisk, err := node.ConfigHash(ctx, c.runner, n, *d); err == nil && onDisk == hash {
				return nil
			}
		}
	}
	if !certsEnsured {
		var err error
		if certsReload, err = node.EnsureTLSCertificates(ctx, c.runner, n, model.InboundsForCore(inbounds, model.CoreSingBox)); err != nil {
			return err
		}
	}
	active, err := node.CoreIsActive(ctx, c.runner, n, *d)
	if err != nil {
		return err
	}
	var beforeStop func() error
	if active {
		beforeStop = func() error {
			at := time.Now()
			if err := c.sampleSingBox(ctx, n, *d, at); err != nil {
				return err
			}
			freshUsers, err := c.store.ListUsers()
			if err != nil {
				return err
			}
			fresh, err := render.SingBoxServerConfig(n, *d, inbounds, freshUsers, at)
			if err != nil {
				return err
			}
			freshHash := hashOf(fresh)
			if freshHash != hash {
				if err := node.StageSingBoxConfig(ctx, c.runner, n, *d, fresh); err != nil {
					return err
				}
				hash = freshHash
			}
			return nil
		}
	}
	afterStop := func() error { return c.store.ResetCoreTrafficBaselines(n.ID, model.CoreSingBox, time.Now()) }
	if err := node.DeploySingBox(ctx, c.runner, n, *d, cfg, beforeStop, afterStop); err != nil {
		// apply 推进到写正式配置之后才失败的(StaleDeployError):磁盘上的
		// 配置不再是记过的那份基准。清掉它,下一次下发不会被"hash 相同"挡
		// 在门口,巡检也会如实报漂移。采样等更早的失败没动磁盘,基准照留。
		var stale *node.StaleDeployError
		if errors.As(err, &stale) {
			if cerr := c.store.ClearCoreDeployed(n.ID, model.CoreSingBox); cerr != nil {
				c.logger.Warn("清 sing-box 下发基准失败", "node", n.Name, "error", cerr)
			}
		}
		return err
	}
	return c.store.SetCoreDeployed(n.ID, model.CoreSingBox, hash, now)
}

func (c *Control) sampleSingBox(ctx context.Context, n model.Node, d model.CoreDeployment, now time.Time) error {
	generation, err := node.CoreGeneration(ctx, c.runner, n, d)
	if err != nil {
		return err
	}
	traffic, err := node.QuerySingBoxTraffic(ctx, c.runner, n, d)
	if err != nil {
		return err
	}
	confirmed, err := node.CoreGeneration(ctx, c.runner, n, d)
	if err != nil {
		return err
	}
	if confirmed != generation {
		return fmt.Errorf("sing-box restarted during traffic query; retry required")
	}
	users, err := c.store.ListUsers()
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(users))
	for _, u := range users {
		known[u.Username] = true
	}
	var errs []error
	for name, t := range traffic {
		// Deleted users still have counters in the running core. Their absence
		// must not prevent deploying the configuration that revokes them.
		if !known[name] {
			continue
		}
		sample := model.TrafficSample{Generation: generation, Core: model.CoreSingBox, Username: name, NodeID: n.ID, SampledAt: now, Uplink: t.Uplink, Downlink: t.Downlink}
		if err := c.store.ApplyCoreTrafficSample(sample, model.CoreSingBox); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Control) coreSampleBudget() time.Duration {
	if c.sampleTimeout > 0 {
		return c.sampleTimeout
	}
	return sampleBudget
}
func (c *Control) coreReconcileBudget() time.Duration {
	if c.reconcileTimeout > 0 {
		return c.reconcileTimeout
	}
	return reconcileBudget
}

func coreRequirements(n model.Node, d model.CoreDeployment, inbounds []model.Inbound) (xray, quic bool) {
	quic = len(model.InboundsForCore(inbounds, model.CoreSingBox)) > 0 || d.DeployedHash != ""
	xray = len(model.InboundsForCore(inbounds, model.CoreXray)) > 0 || n.DeployedHash != "" || (len(inbounds) == 0 && !quic)
	return
}

// hasMultipleCores conservatively keeps reconciliation active if metadata cannot
// be read: a bookkeeping error must not turn into a healthy-core revocation skip.
func (c *Control) hasMultipleCores(n model.Node) bool {
	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return true
	}
	d, err := c.store.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		return true
	}
	xray, quic := coreRequirements(n, *d, inbounds)
	return quic && xray
}

func (c *Control) sampleCores(ctx context.Context, n model.Node, now time.Time) error {
	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		return err
	}
	d, err := c.store.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		return err
	}
	var errs []error
	xray, quic := coreRequirements(n, *d, inbounds)
	// An existing deployment still needs sampling after its last inbound is removed.
	if quic {
		coreCtx, cancel := context.WithTimeout(ctx, c.coreSampleBudget())
		if err := c.sampleSingBox(coreCtx, n, *d, now); err != nil {
			errs = append(errs, err)
		}
		cancel()
	}
	if xray {
		coreCtx, cancel := context.WithTimeout(ctx, c.coreSampleBudget())
		if err := c.sampleXrayTraffic(coreCtx, n, now); err != nil {
			errs = append(errs, err)
		}
		cancel()
	}
	return errors.Join(errs...)
}
