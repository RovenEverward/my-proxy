// Command mpx 是 xray 多节点管理的命令行入口。
//
// 一个二进制干两件事:`mpx serve` 跑控制面,其余子命令是通过 /api 操作它的
// 客户端。两边共用同一套领域模型,所以不需要两个程序去保持同步。
package main

import (
	"os"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/cli"
)

func main() {
	// 桌面构建不带参数启动(或只带 macOS 的 -psn_*)时摊派成 `mpx app`,先于
	// cobra 做;普通构建与给了参数的情况原样进入命令行。
	os.Exit(cli.Execute(cli.DesktopArgs(buildinfo.Desktop(), os.Args[1:])...))
}
