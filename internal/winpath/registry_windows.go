//go:build windows

package winpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const backupKey = `Software\mpx`

// Apply 对用户 PATH 做一次 add 或 remove。任何一步出错都不写 PATH,原值不变。
// 返回 changed=false 表示本来就是想要的样子。
func Apply(op, dir string) (changed bool, backup string, err error) {
	if dir == "" {
		return false, "", errors.New("目录为空")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, "", fmt.Errorf("打开 HKCU\\Environment 失败: %w", err)
	}
	defer k.Close()
	// GetStringValue 不展开 %VAR%,缓冲区不够会自己加大,没有长度上限。
	cur, typ, err := k.GetStringValue("Path")
	switch {
	case errors.Is(err, registry.ErrNotExist):
		cur, typ = "", registry.EXPAND_SZ
	case err != nil:
		return false, "", fmt.Errorf("读不出用户 PATH,没有改动: %w", err)
	case typ != registry.SZ && typ != registry.EXPAND_SZ:
		return false, "", fmt.Errorf("用户 PATH 的类型不是字符串(%d),没有改动", typ)
	}
	var next string
	switch op {
	case "add":
		next, changed = Add(cur, dir)
	case "remove":
		next, changed = Remove(cur, dir)
	default:
		return false, "", fmt.Errorf("不认识的操作 %q", op)
	}
	if !changed {
		return false, "", nil
	}
	if backup, err = saveBackup(cur, typ); err != nil {
		return false, "", fmt.Errorf("备份原来的 PATH 失败,没有改动: %w", err)
	}
	if err = setString(k, typ, next); err != nil {
		return false, backup, fmt.Errorf("写用户 PATH 失败: %w", err)
	}
	if got, _, rerr := k.GetStringValue("Path"); rerr != nil || got != next {
		_ = setString(k, typ, cur) // 读回来不对:写回原值
		return false, backup, errors.New("写完读回来的 PATH 不对,已恢复原值")
	}
	broadcast()
	return true, backup, nil
}

func setString(k registry.Key, typ uint32, v string) error {
	if typ == registry.SZ {
		return k.SetStringValue("Path", v)
	}
	return k.SetExpandStringValue("Path", v)
}

// saveBackup 把原值存两份:HKCU\Software\mpx 的 PathBackup(同类型)和时间,
// 以及 %LOCALAPPDATA%\mpx\path-backup\path-<时间>.txt(每次一份,不覆盖)。
func saveBackup(cur string, typ uint32) (string, error) {
	ts := time.Now().Format("20060102-150405")
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "mpx", "path-backup")
	if os.Getenv("LOCALAPPDATA") == "" {
		return "", errors.New("没有 LOCALAPPDATA")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file := filepath.Join(dir, "path-"+ts+".txt")
	if err := os.WriteFile(file, []byte(cur), 0o600); err != nil {
		return "", err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, backupKey, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	if typ == registry.SZ {
		err = k.SetStringValue("PathBackup", cur)
	} else {
		err = k.SetExpandStringValue("PathBackup", cur)
	}
	if err != nil {
		return "", err
	}
	if err := k.SetStringValue("PathBackupTime", time.Now().Format(time.RFC3339)); err != nil {
		return "", err
	}
	return file, nil
}

// broadcast 通知已开的程序(资源管理器等)重新读环境变量;5 秒超时,不卡安装。
func broadcast() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("SendMessageTimeoutW")
	env, _ := syscall.UTF16PtrFromString("Environment")
	var res uintptr
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&res)))
}
