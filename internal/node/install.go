package node

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// Core 是要装上节点的那套 xray:三个文件都已躺在控制面的本地磁盘上。
//
// 走路径不走内容:一个核心加两份数据接近 65MB,推送本来就是一个文件一个文件过去
// 的,没必要三个都读进内存。
type Core struct {
	Version string
	Xray    string
	GeoIP   string
	GeoSite string
}

// Platform 问节点自己是什么系统、什么架构。
//
// 问节点而不是用控制面的 GOOS/GOARCH:两者经常不在一个平台上(控制面可能就跑在
// 使用者的笔记本上),核心装错架构在节点上是一启动就退。
func Platform(ctx context.Context, r Runner, n model.Node) (goos, goarch string, err error) {
	out, err := r.Run(ctx, n, "uname -s -m")
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return "", "", fmt.Errorf("读不出节点 %s 的平台: %q", n.Name, strings.TrimSpace(out))
	}
	if goos, err = platformOS(fields[0]); err != nil {
		return "", "", err
	}
	if goarch, err = platformArch(fields[1]); err != nil {
		return "", "", err
	}
	return goos, goarch, nil
}

func platformOS(s string) (string, error) {
	switch strings.ToLower(s) {
	case "linux":
		return "linux", nil
	case "darwin":
		return "darwin", nil
	case "freebsd":
		return "freebsd", nil
	}
	return "", fmt.Errorf("节点上是不认识的系统 %s", s)
}

func platformArch(s string) (string, error) {
	// uname -m 给的是机器自己的叫法(x86_64、aarch64、armv7l),和 Go 的名字
	// (amd64、arm64、arm)不是一套。
	switch strings.ToLower(s) {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	case "armv7l", "armv7", "arm":
		return "arm", nil
	case "i386", "i686":
		return "386", nil
	case "riscv64":
		return "riscv64", nil
	case "s390x":
		return "s390x", nil
	case "ppc64le":
		return "ppc64le", nil
	case "loongarch64":
		return "loong64", nil
	}
	return "", fmt.Errorf("节点上是不认识的架构 %s", s)
}

// InstallCore 把一套 xray 装到节点上,并给它配好自己的 systemd 服务。
//
// 装和更新是同一件事:同一条命令跑第二遍就是更新。换核心的顺序是「新核心先以
// .new 落盘 → 拿节点上现有的配置校验一遍 → 通过了才顶替上去」,于是新核心跑不
// 起来的时候,旧的那份一个字节都没动。
//
// 它不启动服务:配置由 Deploy 写好、验过、再重启。那是同一个动作的下一步,分成
// 两半是因为「换核心」和「下发配置」各自都可能单独用到。
func InstallCore(ctx context.Context, r Runner, n model.Node, core Core) error {
	dir := path.Dir(XrayBinPath)

	if _, err := r.Run(ctx, n, "mkdir -p "+shellQuote(dir)); err != nil {
		return fmt.Errorf("在节点 %s 上建 %s: %w", n.Name, dir, err)
	}

	// geodata 先走:核心去自己的同目录找它们,待会儿校验配置就要用。
	for _, f := range []struct{ src, name string }{
		{core.GeoIP, "geoip.dat"},
		{core.GeoSite, "geosite.dat"},
	} {
		content, err := os.ReadFile(f.src)
		if err != nil {
			return err
		}
		if err := PutFile(ctx, r, n, path.Join(dir, f.name), content); err != nil {
			return fmt.Errorf("推送 %s 到节点 %s: %w", f.name, n.Name, err)
		}
	}

	// 新核心先落在旁边,不直接覆盖正在跑的那份。
	content, err := os.ReadFile(core.Xray)
	if err != nil {
		return err
	}
	if err := PutFile(ctx, r, n, XrayBinPath+".new", content); err != nil {
		return fmt.Errorf("推送 xray 核心到节点 %s: %w", n.Name, err)
	}
	// PutFile 走的是 umask 077,核心必须自己再补一次可执行位。
	if _, err := r.Run(ctx, n, "chmod 755 "+shellQuote(XrayBinPath+".new")); err != nil {
		return err
	}

	if err := verifyNewCore(ctx, r, n, XrayBinPath+".new"); err != nil {
		_, _ = r.Run(ctx, n, "rm -f "+shellQuote(XrayBinPath+".new"))
		return err
	}
	if _, err := r.Run(ctx, n, "mv -f "+shellQuote(XrayBinPath+".new")+" "+shellQuote(XrayBinPath)); err != nil {
		return err
	}

	if _, err := writeUnit(ctx, r, n); err != nil {
		return err
	}
	if _, err := r.Run(ctx, n, "systemctl daemon-reload"); err != nil {
		return fmt.Errorf("在节点 %s 上重载 systemd: %w", n.Name, err)
	}
	if _, err := r.Run(ctx, n, "systemctl enable "+shellQuote(n.ServiceNameOrDefault())); err != nil {
		return fmt.Errorf("在节点 %s 上设开机自启: %w", n.Name, err)
	}
	return nil
}

// verifyNewCore 拿新核心去校验节点上现有的配置。
//
// 配置还不存在(全新机器)就跳过这一步 —— 没什么可验的,装完由 Deploy 用刚写下
// 的那份去验。校验只解析配置、不绑定端口,所以它不会和正在跑的进程抢端口。
func verifyNewCore(ctx context.Context, r Runner, n model.Node, bin string) error {
	cfg := shellQuote(n.ConfigPathOrDefault())
	out, err := r.Run(ctx, n, fmt.Sprintf("if [ -f %s ]; then %s -test -config %s; fi",
		cfg, shellQuote(bin), cfg))
	if err != nil {
		return fmt.Errorf("新核心通不过节点 %s 上现有配置的校验,已放弃替换: %w: %s",
			n.Name, err, strings.TrimSpace(out))
	}
	return nil
}

// unitTemplate 是节点上 mpx 那份 systemd 单元。
//
// 服务名与配置路径都取自节点记录,不写死:同一台机器上可能还住着别人的 xray,
// 单元名一样就会互相 restart。两个路径里都不允许有空白(见 server 的校验),
// 所以能直接拼进 ExecStart —— systemd 是按空白切分参数的。
const unitTemplate = `[Unit]
Description=mpx xray node
Documentation=https://github.com/RovenEverward/my-proxy
After=network.target nss-lookup.target

[Service]
Type=simple
ExecStart=%s run -config %s
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`

// writeUnit 写下节点上那份单元文件,返回它的路径。
func writeUnit(ctx context.Context, r Runner, n model.Node) (string, error) {
	p := "/etc/systemd/system/" + n.ServiceNameOrDefault() + ".service"
	body := fmt.Sprintf(unitTemplate, XrayBinPath, n.ConfigPathOrDefault())
	if err := PutFile(ctx, r, n, p, []byte(body)); err != nil {
		return "", fmt.Errorf("写节点 %s 的单元文件: %w", n.Name, err)
	}
	// PutFile 走 umask 077,单元文件按惯例给 644。
	if _, err := r.Run(ctx, n, "chmod 644 "+shellQuote(p)); err != nil {
		return "", err
	}
	return p, nil
}
