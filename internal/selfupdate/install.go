// 本文件处理「替换本机二进制、回退、状态文件」;下载与校验见 fetch.go。

package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNeedSudo 表示当前权限不足以替换本机二进制，需要以更高权限重跑。
var ErrNeedSudo = errors.New("需要更高权限才能替换本机二进制")

// versionTimeout 是运行 `--version` 自检的超时时间。
const versionTimeout = 15 * time.Second

// State 记录一次自更新流程的状态，供状态文件读写。
type State struct {
	Component  string    `json:"component"`
	State      string    `json:"state"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Result     string    `json:"result"`
	Trigger    string    `json:"trigger"`
	Error      string    `json:"error"`
}

// Install 用 bin 替换 exe 指向的本机二进制。
//
// exe 是当前可执行文件路径，调用方需先做 filepath.EvalSymlinks。
// 流程：同目录写 <exe>.new（0755）→ 运行 <exe>.new --version 自检（15s 超时）
// → 删除旧 <exe>.prev → exe 改名为 exe.prev → exe.new 改名为 exe。
//
// 该顺序在 Windows 上同样成立：正在运行的 exe 不能被覆盖，但可以被改名，
// 因此先把它挪走后，新文件就能占据原来的名字。
func Install(exe string, bin []byte, want string) error {
	newPath := exe + ".new"
	prevPath := exe + ".prev"

	if err := os.WriteFile(newPath, bin, 0o755); err != nil {
		return wrapWriteErr("写入", newPath, err)
	}

	out, err := runVersion(newPath)
	if err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("新二进制自检失败: %w", err)
	}
	if !versionMatches(out, want) {
		_ = os.Remove(newPath)
		return fmt.Errorf("新二进制版本输出中未找到 %q: %s", want, strings.TrimSpace(out))
	}

	if err := os.Remove(prevPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(newPath)
		return wrapWriteErr("删除", prevPath, err)
	}
	if err := os.Rename(exe, prevPath); err != nil {
		_ = os.Remove(newPath)
		return wrapWriteErr("重命名", exe, err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		// 新文件没能就位，把原文件改回去，尽量保持可用。
		_ = os.Rename(prevPath, exe)
		return wrapWriteErr("重命名", newPath, err)
	}
	return nil
}

// Rollback 把 exe 与其 .prev 互换，并运行互换后的 exe --version 返回输出（去空白）。
func Rollback(exe string) (string, error) {
	prevPath := exe + ".prev"
	if _, err := os.Stat(prevPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("没有可回退的版本: %s 不存在", prevPath)
		}
		return "", fmt.Errorf("检查 %s 失败: %w", prevPath, err)
	}

	// 借一个临时名完成互换：exe → tmp，prev → exe，tmp → prev。
	tmpPath := exe + ".rollback"
	if err := os.Rename(exe, tmpPath); err != nil {
		return "", wrapWriteErr("重命名", exe, err)
	}
	if err := os.Rename(prevPath, exe); err != nil {
		_ = os.Rename(tmpPath, exe)
		return "", wrapWriteErr("重命名", prevPath, err)
	}
	if err := os.Rename(tmpPath, prevPath); err != nil {
		return "", wrapWriteErr("重命名", tmpPath, err)
	}

	out, err := runVersion(exe)
	if err != nil {
		return strings.TrimSpace(out), fmt.Errorf("回退后的二进制自检失败: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// SaveState 把状态写入 path（0600）。先写临时文件再 rename，必要时创建目录。
func SaveState(path string, st State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建状态目录 %s 失败: %w", dir, err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}
	data = append(data, '\n')

	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建状态临时文件失败: %w", err)
	}
	tmpName := f.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("设置状态文件权限失败: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入状态文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭状态文件失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换状态文件失败: %w", err)
	}
	tmpName = "" // 已 rename，无需清理
	return nil
}

// LoadState 读取 path 处的状态。文件不存在时返回 ok=false 且 err=nil。
func LoadState(path string) (State, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, false, nil
		}
		return State{}, false, fmt.Errorf("读取状态文件失败: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, false, fmt.Errorf("解析状态文件失败: %w", err)
	}
	return st, true, nil
}

// runVersion 运行 path --version，返回合并后的输出（stdout+stderr）。
func runVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("运行 %s --version 超时", path)
	}
	if err != nil {
		return string(out), fmt.Errorf("运行 %s --version 失败: %w", path, err)
	}
	return string(out), nil
}

// wrapWriteErr 在权限不足时返回 ErrNeedSudo 包装的错误，提示用 sudo 重跑。
func wrapWriteErr(op, path string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w: 无法%s %s，请用 sudo 重跑 mpx update apply cli", ErrNeedSudo, op, path)
	}
	return fmt.Errorf("%s %s 失败: %w", op, path, err)
}

// versionMatches 要求 --version 输出的最后一个字段正好是 want。
//
// 不用子串匹配:want 是 v0.6.1 时,v0.6.10 也会包含它。
func versionMatches(out, want string) bool {
	f := strings.Fields(out)
	return len(f) > 0 && f[len(f)-1] == want
}
