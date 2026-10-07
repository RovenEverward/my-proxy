package control

import (
	"context"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/xrayrelease"
)

// InstallNode 在节点上装好 mpx 那份 xray、配好它的 systemd 服务,然后下发一次配置
// 把它启动起来。
//
// 下载发生在控制面(xrayrelease 那层),推过去的只有三个文件 —— 于是节点上只要
// sshd、systemd 和 coreutils:不需要 curl、不需要 unzip,也不需要能连上 GitHub。
// 那些东西正是「在这台机器上手工装一遍」的全部成本,也正是不该让每台新机器都重复
// 一遍的东西。
//
// 装和更新是同一条路径:同一个版本再跑一遍就是把核心重新推一遍、重新校验一遍。
// 返回的是节点上回话的版本 —— 控制面以为装了什么不算数,节点上那句才是事实。
func (c *Control) InstallNode(ctx context.Context, n model.Node, version string) (string, error) {
	goos, goarch, err := node.Platform(ctx, c.runner, n)
	if err != nil {
		return "", err
	}
	files, err := xrayrelease.Fetch(ctx, xrayrelease.Request{
		Version: version,
		OS:      goos,
		Arch:    goarch,
		Dir:     c.cacheDir,
	})
	if err != nil {
		return "", err
	}
	core := node.Core{Version: files.Version, Xray: files.Xray, GeoIP: files.GeoIP, GeoSite: files.GeoSite}
	if err := node.InstallCore(ctx, c.runner, n, core); err != nil {
		return "", err
	}

	// 装完直接下发并启动:新机器上跑完这一条就有得用,不用再记得补一次 node sync。
	// 失败的返回里仍带上版本,好让调用方说清「核心装好了,是配置没起来」。
	installed := versionLabel(files.Version)
	if err := c.DeployNode(ctx, n, time.Now()); err != nil {
		return installed, err
	}

	// 顺手把节点上实际的版本读回来。读不到不算装失败 —— 核心已经在那儿了,这一句
	// 只是为了报得更准,不该让它把「装成功了」变成「装失败了」。
	if v, err := node.Version(ctx, c.runner, n); err == nil && v != "" {
		return v, nil
	}
	return installed, nil
}

// versionLabel 把 GitHub 那边的 tag 写法(v26.3.27)收成版本号本身(26.3.27)。
//
// 装的时候我们手上是 tag,装完节点上回话的是版本号;同一个输出里出现两种写法,
// 读的人就得自己换算一遍。
func versionLabel(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}
