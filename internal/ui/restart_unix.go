//go:build !windows

package ui

import (
	"os"
	"syscall"
)

// restartSelf 用刚装好的二进制原地替换当前进程:同一个 PID、原命令行、原环境,
// 只加一个指向交接文件的环境变量。做成包级变量,是为了测试里换成只记录不动作的
// 替身,不真把测试进程换掉。
var restartSelf = func(stateFile string) error {
	exe, err := selfExecutable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, restartEnviron(stateFile))
}
