package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const (
	oldScript = "#!/bin/sh\necho \"mpx v0.6.0\"\n"
	newScript = "#!/bin/sh\necho \"mpx v0.6.1\"\n"
)

// skipWindows 跳过只在类 Unix 上成立的用例（假二进制依赖 /bin/sh）。
func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("该用例依赖 shell 脚本假二进制，仅在非 Windows 上运行")
	}
}

// writeScript 写入一个可执行的假二进制脚本。
func writeScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("写入假二进制 %s 失败: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(data)
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("期望 %s 不存在, err=%v", path, err)
	}
}

func TestInstallSuccess(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "mpx")
	writeScript(t, exe, oldScript)

	if err := Install(exe, []byte(newScript), "v0.6.1"); err != nil {
		t.Fatalf("Install 失败: %v", err)
	}

	if got := readFile(t, exe); got != newScript {
		t.Errorf("exe 内容 = %q, 期望新脚本", got)
	}
	if got := readFile(t, exe+".prev"); got != oldScript {
		t.Errorf(".prev 内容 = %q, 期望旧脚本", got)
	}
	mustNotExist(t, exe+".new")
}

func TestInstallVersionMismatch(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "mpx")
	writeScript(t, exe, oldScript)

	badScript := "#!/bin/sh\necho \"mpx v0.5.0\"\n"
	err := Install(exe, []byte(badScript), "v0.6.1")
	if err == nil {
		t.Fatal("期望 Install 因版本不匹配报错")
	}
	if got := readFile(t, exe); got != oldScript {
		t.Errorf("失败后 exe 被改动, 内容 = %q", got)
	}
	mustNotExist(t, exe+".new")
}

func TestInstallReplacesExistingPrev(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "mpx")
	writeScript(t, exe, oldScript)

	// 预先放一个陈旧的 .prev，应被本次安装的旧版本覆盖。
	stalePrev := "#!/bin/sh\necho \"mpx v0.5.9\"\n"
	writeScript(t, exe+".prev", stalePrev)

	if err := Install(exe, []byte(newScript), "v0.6.1"); err != nil {
		t.Fatalf("Install 失败: %v", err)
	}
	if got := readFile(t, exe+".prev"); got != oldScript {
		t.Errorf(".prev 内容 = %q, 期望被替换为安装前的 exe", got)
	}
}

func TestInstallDirNotWritable(t *testing.T) {
	skipWindows(t)
	if os.Getuid() == 0 {
		t.Skip("以 root 运行时目录权限不生效")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "mpx")
	writeScript(t, exe, oldScript)

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("设置只读目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := Install(exe, []byte(newScript), "v0.6.1")
	if !errors.Is(err, ErrNeedSudo) {
		t.Fatalf("期望 ErrNeedSudo, 实际: %v", err)
	}
}

func TestRollbackRoundTrip(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "mpx")
	writeScript(t, exe, oldScript)

	if err := Install(exe, []byte(newScript), "v0.6.1"); err != nil {
		t.Fatalf("Install 失败: %v", err)
	}
	// 初始为「已安装」态: exe=新, .prev=旧。

	out, err := Rollback(exe)
	if err != nil {
		t.Fatalf("第一次 Rollback 失败: %v", err)
	}
	if out != "mpx v0.6.0" {
		t.Errorf("回退输出 = %q, 期望 mpx v0.6.0", out)
	}
	if got := readFile(t, exe); got != oldScript {
		t.Errorf("回退后 exe 内容 = %q, 期望旧脚本", got)
	}
	if got := readFile(t, exe+".prev"); got != newScript {
		t.Errorf("回退后 .prev 内容 = %q, 期望新脚本", got)
	}

	// 再回退一次应回到最初的已安装态。
	out, err = Rollback(exe)
	if err != nil {
		t.Fatalf("第二次 Rollback 失败: %v", err)
	}
	if out != "mpx v0.6.1" {
		t.Errorf("二次回退输出 = %q, 期望 mpx v0.6.1", out)
	}
	if got := readFile(t, exe); got != newScript {
		t.Errorf("二次回退后 exe 内容 = %q, 期望新脚本", got)
	}
	if got := readFile(t, exe+".prev"); got != oldScript {
		t.Errorf("二次回退后 .prev 内容 = %q, 期望旧脚本", got)
	}
	mustNotExist(t, exe+".rollback")
}

func TestRollbackNoPrev(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "mpx")
	writeScript(t, exe, oldScript)

	if _, err := Rollback(exe); err == nil {
		t.Fatal("无 .prev 时期望 Rollback 报错")
	}
}

func TestSaveLoadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// 多套一层目录，验证自动建目录。
	path := filepath.Join(dir, "nested", "state.json")

	if _, ok, err := LoadState(path); err != nil || ok {
		t.Fatalf("不存在时应返回 ok=false, err=nil, 实际 ok=%v err=%v", ok, err)
	}

	started := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	finished := time.Now().UTC().Truncate(time.Second)
	want := State{
		Component:  "cli",
		State:      "done",
		From:       "v0.6.0",
		To:         "v0.6.1",
		StartedAt:  started,
		FinishedAt: finished,
		Result:     "ok",
		Trigger:    "manual",
		Error:      "",
	}
	if err := SaveState(path, want); err != nil {
		t.Fatalf("SaveState 失败: %v", err)
	}

	got, ok, err := LoadState(path)
	if err != nil || !ok {
		t.Fatalf("LoadState 失败: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Errorf("LoadState = %+v, 期望 %+v", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 状态文件失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("状态文件权限 = %o, 期望 600", perm)
	}
}

func TestVersionMatchesExact(t *testing.T) {
	if !versionMatches("mpx v0.6.1\n", "v0.6.1") || versionMatches("mpx v0.6.10", "v0.6.1") || versionMatches("", "v0.6.1") {
		t.Fatal("版本比对应当按整个字段精确匹配")
	}
}
