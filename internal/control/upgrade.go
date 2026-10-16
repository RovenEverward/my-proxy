package control

import (
	"context"
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/xrayrelease"
)

// ResolveXray 算出某台节点该下哪个 xray 资产、以及它的官方 sha256。
//
// 架构问节点自己(uname -m),不猜控制面的 GOARCH:两者经常不是一台机器。
// 资产名只认 linux —— 一键升级面向的是节点,节点就是 linux;认不出的架构在
// AssetName 那里直接报错,不会把错架构的核心推上去。
func (c *Control) ResolveXray(ctx context.Context, n model.Node, version string) (assetURL, sha256 string, err error) {
	arch, err := node.NodeArch(ctx, c.runner, n)
	if err != nil {
		return "", "", err
	}
	asset, err := xrayrelease.AssetName("linux", arch)
	if err != nil {
		return "", "", err
	}
	sha256, err = xrayrelease.Digest(ctx, version, asset)
	if err != nil {
		return "", "", err
	}
	return xrayrelease.DownloadURL(version, asset), sha256, nil
}

// UpgradeXray 让节点自己下载并换上指定版本的 xray,失败由 node 层回滚。
//
// 和 InstallNode 用同一个安装预算、同一把节点锁:它和一次推送安装是同一类动作,
// 不该和别的针对这台节点的操作并行。
func (c *Control) UpgradeXray(ctx context.Context, n model.Node, version, assetURL, sha256 string) (node.UpgradeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, installBudget)
	defer cancel()
	unlock := c.lockNode(n.ID)
	defer unlock()
	res, err := node.UpgradeXray(ctx, c.runner, n, version, assetURL, sha256)
	if err != nil {
		return res, fmt.Errorf("升级节点 %s 的 xray: %w", n.Name, err)
	}
	return res, nil
}
