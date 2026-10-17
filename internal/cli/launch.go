package cli

import "strings"

// DesktopArgs 摊派进程启动参数,给桌面构建一个「不带参数就开窗」的默认命令。
//
// 桌面版安装包里的可执行文件被系统启动时通常不带参数;macOS 从 Finder/Dock
// 启动还会塞一个 -psn_ 开头的位置参数(LaunchServices 的进程序号),那不是用户
// 敲的命令,与「无参数」同等对待。其余情况一律原样:桌面构建仍然可以当 CLI 用,
// 用户明确给了子命令或 flag(哪怕是 mpx ui)就照用户的来。
//
// 纯函数,main 在进入 cobra 之前调它;非桌面构建直接原样返回。
func DesktopArgs(desktop bool, args []string) []string {
	if !desktop {
		return args
	}
	if len(args) == 0 || (len(args) == 1 && strings.HasPrefix(args[0], "-psn_")) {
		return []string{"app"}
	}
	return args
}
