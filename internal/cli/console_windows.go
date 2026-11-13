//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// attachParentConsole 把以 -H windowsgui 构建的进程挂回启动它的控制台。
//
// 桌面版 Windows 二进制是 GUI 子系统进程,刚启动时没有任何控制台。用户从终端
// 敲 `mpx status` 之类子命令时,标准输出无处可去 —— GUI 进程不会自动继承父
// 进程的 cmd/PowerShell。AttachConsole(-1) 挂到父控制台之后,再把 CONOUT$ 重新
// 接回 stdout/stderr,子命令的输出就回到用户的终端里。
//
// 只对桌面构建带子命令的场景调用:无参数开窗口的路径不经过这里;普通 CLI
// 构建本身有控制台,也不需要它。attach 失败(父进程不是控制台程序,例如被
// 别的工具拉起)时保持原样,不报错 —— 有没有输出不会比这更糟。
func attachParentConsole() {
	// 已有有效 stdout(控制台继承、被重定向到文件或管道)就不动 —— 换到
	// CONOUT$ 反而会把 mpx status > out.txt 的输出偷回终端。
	if h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && h != 0 {
		return
	}
	// x/sys/windows 未导出 AttachConsole,沿用 winpath 的 LazyDLL 方式。
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	r, _, _ := kernel32.NewProc("AttachConsole").Call(attachParentProcess)
	if r == 0 {
		return
	}
	// 打开 CONOUT$ 并回接 stdout/stderr。GUI 进程刚挂到控制台时 Go 启动时
	// 记录的 os.Stdout/Stderr 仍指向原句柄,必须换掉才能被 cobra 写出。
	conout, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return
	}
	h, err := windows.CreateFile(conout, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return
	}
	f := os.NewFile(uintptr(h), "conout$")
	os.Stdout = f
	os.Stderr = f
}
