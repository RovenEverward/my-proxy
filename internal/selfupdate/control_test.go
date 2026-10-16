package selfupdate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// controlHarness 是一套完整的控制面执行器测试夹具:全部路径都在 t.TempDir 下,
// 会改系统的动作全部用假实现记下来。
type controlHarness struct {
	t     *testing.T
	dir   string
	paths ControlPaths
	run   *ControlRunner

	// 假实现记录
	snapCalls int
	snapDB    string
	snapDst   string
	systemctl [][]string

	// 可调行为
	fetchErr   error
	healthFn   func(ctx context.Context) (string, error)
	healthVers string
}

// scriptFor 造一个输出 "mpx <v>" 的假二进制脚本。
func scriptFor(v string) string {
	return "#!/bin/sh\necho \"mpx " + v + "\"\n"
}

func newControlHarness(t *testing.T) *controlHarness {
	t.Helper()
	skipWindows(t)

	dir := t.TempDir()
	h := &controlHarness{t: t, dir: dir}
	h.paths = ControlPaths{
		Request:  filepath.Join(dir, "update", "request"),
		State:    filepath.Join(dir, "update", "state.json"),
		Snapshot: filepath.Join(dir, "update", "pre.db"),
		DB:       filepath.Join(dir, "mpx.db"),
		Releases: filepath.Join(dir, "releases"),
		Link:     filepath.Join(dir, "bin", "mpx"),
	}
	for _, d := range []string{h.paths.Releases, filepath.Dir(h.paths.Link)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("建目录 %s 失败: %v", d, err)
		}
	}
	h.healthVers = "v0.6.0"

	h.run = &ControlRunner{
		Paths:  h.paths,
		Latest: func(ctx context.Context) (string, error) { return "v0.6.1", nil },
		Fetch: func(ctx context.Context, v string) ([]byte, bool, error) {
			if h.fetchErr != nil {
				return nil, false, h.fetchErr
			}
			return []byte(scriptFor(v)), true, nil
		},
		Version: func(bin string) (string, error) {
			// 与生产一致:真的跑一遍 <bin> --version。
			return runVersion(bin)
		},
		Systemctl: func(ctx context.Context, args ...string) error {
			h.systemctl = append(h.systemctl, append([]string(nil), args...))
			return nil
		},
		Health: func(ctx context.Context) (string, error) {
			if h.healthFn != nil {
				return h.healthFn(ctx)
			}
			return h.healthVers, nil
		},
		SnapshotDB: func(db, dst string) error {
			h.snapCalls++
			h.snapDB = db
			h.snapDst = dst
			return copyFileInto(db, dst)
		},
		HealthTimeout:  25 * time.Millisecond,
		HealthInterval: 2 * time.Millisecond,
	}

	h.installVersion(t, "v0.6.0")
	h.writeDB("快照时内容")
	return h
}

// installVersion 在 Releases 下放一个版本,并把 Link 指向它。
func (h *controlHarness) installVersion(t *testing.T, v string) {
	t.Helper()
	bin := filepath.Join(h.paths.Releases, v, "mpx")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatalf("建版本目录失败: %v", err)
	}
	writeScript(t, bin, scriptFor(v))
	_ = os.Remove(h.paths.Link)
	if err := os.Symlink(bin, h.paths.Link); err != nil {
		t.Fatalf("建立初始链接失败: %v", err)
	}
}

func (h *controlHarness) writeDB(content string) {
	h.t.Helper()
	if err := os.WriteFile(h.paths.DB, []byte(content), 0o600); err != nil {
		h.t.Fatalf("写数据库失败: %v", err)
	}
}

func (h *controlHarness) readDB() string {
	h.t.Helper()
	data, err := os.ReadFile(h.paths.DB)
	if err != nil {
		h.t.Fatalf("读数据库失败: %v", err)
	}
	return string(data)
}

// linkVersion 返回 Link 当前指向的版本目录名。
func (h *controlHarness) linkVersion() string {
	h.t.Helper()
	dest, err := os.Readlink(h.paths.Link)
	if err != nil {
		h.t.Fatalf("读链接 %s 失败: %v", h.paths.Link, err)
	}
	return filepath.Base(filepath.Dir(dest))
}

func (h *controlHarness) releases() []string {
	h.t.Helper()
	entries, err := os.ReadDir(h.paths.Releases)
	if err != nil {
		h.t.Fatalf("读 Releases 失败: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func (h *controlHarness) state() State {
	h.t.Helper()
	st, ok, err := LoadState(h.paths.State)
	if err != nil || !ok {
		h.t.Fatalf("读状态失败: ok=%v err=%v", ok, err)
	}
	return st
}

func (h *controlHarness) request(req ControlRequest) {
	h.t.Helper()
	if err := WriteControlRequest(h.paths.Request, req); err != nil {
		h.t.Fatalf("写请求失败: %v", err)
	}
}

func TestControlUpgradeSuccess(t *testing.T) {
	h := newControlHarness(t)
	h.healthVers = "v0.6.1" // 切换后健康检查应看到新版
	h.request(ControlRequest{Action: "upgrade"})

	if err := h.run.Run(context.Background()); err != nil {
		t.Fatalf("升级应成功, 实际: %v", err)
	}
	if got := h.linkVersion(); got != "v0.6.1" {
		t.Errorf("链接版本 = %q, 期望 v0.6.1", got)
	}
	if got := h.releases(); len(got) != 2 || got[0] != "v0.6.0" || got[1] != "v0.6.1" {
		t.Errorf("Releases = %v, 期望只剩 v0.6.0 与 v0.6.1", got)
	}
	st := h.state()
	if st.State != stateIdle || st.Result != resultOK || st.Component != controlComponent || st.Trigger != controlTrigger {
		t.Errorf("状态 = %+v, 期望 idle/ok/control/manual", st)
	}
	if _, err := os.Stat(h.paths.Request); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("请求文件应已删除, err=%v", err)
	}
	if h.snapCalls != 1 || h.snapDB != h.paths.DB || h.snapDst != h.paths.Snapshot {
		t.Errorf("快照调用 = %d (%s -> %s), 期望一次且路径正确", h.snapCalls, h.snapDB, h.snapDst)
	}
}

func TestControlUpgradeHealthFailsThenRollback(t *testing.T) {
	h := newControlHarness(t)
	h.request(ControlRequest{Action: "upgrade"})

	wrote := false
	h.healthFn = func(ctx context.Context) (string, error) {
		// 模拟新版起来后往库里写了自己的数据;它从未健康过。
		if !wrote {
			wrote = true
			_ = os.WriteFile(h.paths.DB, []byte("新版本写入"), 0o600)
		}
		return "v0.6.0", nil
	}

	err := h.run.Run(context.Background())
	if err == nil {
		t.Fatal("新版健康检查失败时 Run 应返回错误(原因写在状态里)")
	}
	if got := h.linkVersion(); got != "v0.6.0" {
		t.Errorf("回退后链接版本 = %q, 期望 v0.6.0", got)
	}
	if got := h.readDB(); got != "快照时内容" {
		t.Errorf("回退后数据库 = %q, 期望恢复成快照内容", got)
	}
	st := h.state()
	if st.Result != resultRolledBack {
		t.Errorf("结果 = %q, 期望 rolled-back", st.Result)
	}
}

func TestControlRollbackStillUnhealthy(t *testing.T) {
	h := newControlHarness(t)
	h.request(ControlRequest{Action: "upgrade"})
	h.healthFn = func(ctx context.Context) (string, error) { return "v0.5.0", nil }

	err := h.run.Run(context.Background())
	if err == nil {
		t.Fatal("回退后仍不健康时 Run 应返回错误")
	}
	st := h.state()
	if st.Result != resultFailed {
		t.Errorf("结果 = %q, 期望 failed", st.Result)
	}
	for _, want := range []string{"journalctl -u mpx", "ln -sfn"} {
		if !strings.Contains(st.Error, want) {
			t.Errorf("错误信息 %q 应包含 %q", st.Error, want)
		}
	}
}

func TestControlRecoversInterruptedState(t *testing.T) {
	h := newControlHarness(t)
	if err := SaveState(h.paths.State, State{
		Component: controlComponent,
		State:     stateSwitching,
		From:      "v0.6.0",
		To:        "v0.6.1",
		StartedAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("写中断状态失败: %v", err)
	}

	// 没有新请求;健康检查报告的就是当前链接版本。
	if err := h.run.Run(context.Background()); err != nil {
		t.Fatalf("自愈不应报错, 实际: %v", err)
	}
	st := h.state()
	if st.State != stateIdle || st.Result != resultFailed {
		t.Errorf("自愈后状态 = %+v, 期望 idle/failed", st)
	}
	if !strings.Contains(st.Error, "上次升级中途中断") {
		t.Errorf("错误信息 = %q, 应说明上次中断", st.Error)
	}
	if len(h.systemctl) != 0 {
		t.Errorf("健康时不应重启, systemctl 调用 = %v", h.systemctl)
	}
}

func TestControlMigratesOldLayout(t *testing.T) {
	h := newControlHarness(t)
	// 还原成老布局:Link 是普通文件,Releases 为空。
	_ = os.Remove(h.paths.Link)
	if err := os.RemoveAll(h.paths.Releases); err != nil {
		t.Fatalf("清空 Releases 失败: %v", err)
	}
	writeScript(t, h.paths.Link, scriptFor("v0.6.0"))

	if err := h.run.migrateLayout(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	dest, err := os.Readlink(h.paths.Link)
	if err != nil {
		t.Fatalf("迁移后 Link 应是符号链接: %v", err)
	}
	if want := filepath.Join(h.paths.Releases, "v0.6.0", "mpx"); dest != want {
		t.Errorf("链接指向 = %q, 期望 %q", dest, want)
	}
	if got := h.linkVersion(); got != "v0.6.0" {
		t.Errorf("链接版本 = %q, 期望 v0.6.0", got)
	}

	// 再跑一次是空操作。
	if err := h.run.migrateLayout(); err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}
	if dest2, err := os.Readlink(h.paths.Link); err != nil || dest2 != dest {
		t.Errorf("二次迁移改变了链接: dest=%q err=%v", dest2, err)
	}
}

func TestControlFetchErrorLeavesLink(t *testing.T) {
	h := newControlHarness(t)
	h.fetchErr = errors.New("网络断了")
	h.request(ControlRequest{Action: "upgrade"})

	if err := h.run.Run(context.Background()); err == nil {
		t.Fatal("取件失败时 Run 应返回错误")
	}
	if got := h.linkVersion(); got != "v0.6.0" {
		t.Errorf("取件失败后链接 = %q, 期望不变", got)
	}
	if h.state().Result != resultFailed {
		t.Errorf("结果 = %q, 期望 failed", h.state().Result)
	}
	if _, err := os.Stat(filepath.Join(h.paths.Releases, "v0.6.1", "mpx.new")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("失败后不应留下 .new, err=%v", err)
	}
}

func TestControlNoRequestDoesNothing(t *testing.T) {
	h := newControlHarness(t)
	if err := h.run.Run(context.Background()); err != nil {
		t.Fatalf("无请求时 Run 应返回 nil, 实际: %v", err)
	}
	if len(h.systemctl) != 0 {
		t.Errorf("无请求时不应调用 systemctl, 实际: %v", h.systemctl)
	}
	if _, err := os.Stat(h.paths.State); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("无请求时不应写状态, err=%v", err)
	}
}

func TestControlRollbackAction(t *testing.T) {
	h := newControlHarness(t)
	// 当前跑 v0.6.1,旧版 v0.6.0 留着。
	h.installVersion(t, "v0.6.1")
	before := h.readDB()
	h.healthVers = "v0.6.0"
	h.request(ControlRequest{Action: "rollback"})

	if err := h.run.Run(context.Background()); err != nil {
		t.Fatalf("回退动作应成功, 实际: %v", err)
	}
	if got := h.linkVersion(); got != "v0.6.0" {
		t.Errorf("回退后链接 = %q, 期望 v0.6.0", got)
	}
	if h.snapCalls != 0 {
		t.Errorf("回退动作不应做快照, 实际调了 %d 次", h.snapCalls)
	}
	if got := h.readDB(); got != before {
		t.Errorf("回退动作不应恢复数据库, DB = %q, 期望 %q", got, before)
	}
	if h.state().Result != resultOK {
		t.Errorf("结果 = %q, 期望 ok", h.state().Result)
	}
}

func TestControlUpToDateNoRestart(t *testing.T) {
	h := newControlHarness(t)
	h.request(ControlRequest{Action: "upgrade", To: "v0.6.0"})

	if err := h.run.Run(context.Background()); err != nil {
		t.Fatalf("已是最新时 Run 应返回 nil, 实际: %v", err)
	}
	if len(h.systemctl) != 0 {
		t.Errorf("已是最新时不应重启, systemctl 调用 = %v", h.systemctl)
	}
	st := h.state()
	if st.Result != resultOK || st.From != "v0.6.0" || st.To != "v0.6.0" {
		t.Errorf("状态 = %+v, 期望 ok 且 from==to==v0.6.0", st)
	}
}

func TestWriteControlRequestPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "req", "request")
	if err := WriteControlRequest(path, ControlRequest{Action: "upgrade"}); err != nil {
		t.Fatalf("首次写请求失败: %v", err)
	}
	if err := WriteControlRequest(path, ControlRequest{Action: "upgrade"}); !errors.Is(err, ErrRequestPending) {
		t.Fatalf("重复写应返回 ErrRequestPending, 实际: %v", err)
	}
}

func TestControlDefaultSnapshotDB(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mpx.db")

	conn, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if _, err := conn.Exec("CREATE TABLE t (v TEXT)"); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := conn.Exec("INSERT INTO t VALUES ('快照行')"); err != nil {
		t.Fatalf("插行失败: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}

	dst := filepath.Join(dir, "pre.db")
	if err := snapshotViaSQLite(dbPath, dst); err != nil {
		t.Fatalf("默认快照失败: %v", err)
	}

	snap, err := sql.Open("sqlite", sqliteDSN(dst))
	if err != nil {
		t.Fatalf("打开快照失败: %v", err)
	}
	defer snap.Close()
	var got string
	if err := snap.QueryRow("SELECT v FROM t").Scan(&got); err != nil {
		t.Fatalf("读快照失败: %v", err)
	}
	if got != "快照行" {
		t.Errorf("快照内容 = %q, 期望 快照行", got)
	}
}

// 在下载阶段中断时链接和库都没动过,自愈不能拿可能是更早一轮留下的旧快照去
// 恢复库,即使此刻健康检查不通过。
func TestControlInterruptedBeforeSwitchKeepsDB(t *testing.T) {
	h := newControlHarness(t)
	h.installVersion(t, "v0.5.9") // 另一个版本目录,给"回退"一个可选目标
	h.installVersion(t, "v0.6.0")
	_ = os.MkdirAll(filepath.Dir(h.paths.Snapshot), 0o755)
	if err := os.WriteFile(h.paths.Snapshot, []byte("更早一轮的旧快照"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.writeDB("最新内容")
	h.healthFn = func(ctx context.Context) (string, error) { return "", errors.New("暂时连不上") }
	if err := SaveState(h.paths.State, State{Component: controlComponent, State: stateDownloading, From: "v0.6.0", To: "v0.6.1"}); err != nil {
		t.Fatal(err)
	}
	if err := h.run.Run(context.Background()); err != nil {
		t.Fatalf("自愈不应报错: %v", err)
	}
	if got := h.readDB(); got != "最新内容" {
		t.Fatalf("库被旧快照覆盖了: %q", got)
	}
	if len(h.systemctl) != 0 || h.linkVersion() != "v0.6.0" {
		t.Fatalf("不该动服务或链接: %v %s", h.systemctl, h.linkVersion())
	}
	if st := h.state(); st.State != stateIdle || st.Result != resultFailed {
		t.Fatalf("状态 = %+v", st)
	}
}
