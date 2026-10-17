//go:build windows

package ui

import (
	"os"
	"os/exec"
)

// restartSelf 在 Windows 上没法 exec 替换自己:用新二进制起一个子进程(带上交接
// 文件的环境变量),当前进程随后退出把端口让出来。做成包级变量,是为了测试里换成
// 只记录不动作的替身。
var restartSelf = func(stateFile string) error {
	exe, err := selfExecutable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = restartEnviron(stateFile)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := cmd.Start(); err != nil {
		return err
	}
	// 子进程已起,当前进程退出。新进程会重试绑定同一端口(见 cli.listenUI)。
	os.Exit(0)
	return nil
}
