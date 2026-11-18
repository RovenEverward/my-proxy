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
	if err := c.deploySingBox(ctx, n, time.Now(), true); err != nil {
		return files.Version, err
	}
	return files.Version, nil
}

func (c *Control) deploySingBox(ctx context.Context, n model.Node, now time.Time, force bool) error {
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
	if !force && hash == d.DeployedHash {
		return nil
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
