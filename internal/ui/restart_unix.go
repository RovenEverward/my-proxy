//go:build !windows

package ui

import (
	"os"
	"syscall"
)

// restartSelf 用刚装好的二进制原地替换当前进程:同一个 PID、原命令行、原环境,
// 只加一个指向交接文件的环境变量。exe 是升级时实际写入的新二进制路径(见
// applyCLIUpdate),必须由调用方传进来 —— 升级后旧二进制已改名成 <exe>.prev,
// 这时再问 selfExecutable/os.Executable 会指回旧版。做成包级变量,是为了测试里
// 换成只记录不动作的替身,不真把测试进程换掉。
var restartSelf = func(stateFile, exe string) error {
	return syscall.Exec(stripPrevSuffix(exe), os.Args, restartEnviron(stateFile))
}
