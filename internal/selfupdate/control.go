// 本文件是控制面升级在 root 侧的执行器。
//
// 控制面以普通用户运行,换不了 /usr/local/bin/mpx;root 的 oneshot 单元执行
// `mpx update run control`。控制面接口只落一个请求文件,这里负责读请求、执行
// 升级或回退、并在每个阶段写状态文件。所有会改动系统的动作都通过字段注入,
// 便于测试,也避免这一层自己去猜环境。

package selfupdate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 驱动名 "sqlite",与 store 包一致
)

// 控制面升级各阶段的固定取值。
const (
	controlComponent = "control"
	controlTrigger   = "manual"

	stateIdle        = "idle"
	stateDownloading = "downloading"
	stateVerifying   = "verifying"
	stateSwitching   = "switching"
	stateHealthCheck = "health-check"

	resultOK         = "ok"
	resultRolledBack = "rolled-back"
	resultFailed     = "failed"
)

// ControlRequest 是控制面接口写下的一个更新请求。
type ControlRequest struct {
	Action         string    `json:"action"` // "upgrade"|"rollback"
	To             string    `json:"to"`     // 空 = latest
	AllowDowngrade bool      `json:"allow_downgrade"`
	RequestedAt    time.Time `json:"requested_at"`
}

// ControlPaths 汇总 root 侧执行器用到的路径。
type ControlPaths struct {
	Request  string // 请求文件
	State    string // 状态文件
	Snapshot string // 升级前的库快照(pre.db,只留一份)
	DB       string // 控制面数据库
	Releases string // 各版本可执行文件目录
	Link     string // 当前生效的符号链接
}

// DefaultControlPaths 给出部署时的默认路径。
func DefaultControlPaths() ControlPaths {
	return ControlPaths{
		Request:  "/var/lib/mpx/update/request",
		State:    "/var/lib/mpx/update/state.json",
		Snapshot: "/var/lib/mpx/update/pre.db",
		DB:       "/var/lib/mpx/mpx.db",
		Releases: "/usr/local/lib/mpx/releases",
		Link:     "/usr/local/bin/mpx",
	}
}

// ErrRequestPending 表示已经有一个请求等着处理。
var ErrRequestPending = errors.New("已有一个更新请求在处理中")

// ControlRunner 执行一次控制面升级或回退。
//
// 除 Paths 外的字段都可以注入:生产由命令接线填真实实现,测试填假实现。
// Version/Systemctl/SnapshotDB/Now 为空时用默认实现。
type ControlRunner struct {
	Paths ControlPaths

	// Fetch 取回某一版的 mpx 可执行文件,返回是否经过哈希校验。
	Fetch func(ctx context.Context, version string) (bin []byte, verified bool, err error)
	// Latest 问最新发布是哪一版。
	Latest func(ctx context.Context) (string, error)
	// Version 运行 <bin> --version,返回输出。
	Version func(bin string) (string, error)
	// Systemctl 执行一条 systemctl 命令。
	Systemctl func(ctx context.Context, args ...string) error
	// Health 本机 GET /api/status 拿控制面版本;为空时报错。
	Health func(ctx context.Context) (version string, err error)
	// SnapshotDB 把控制面库快照到 dst(dst 先删除)。
	SnapshotDB func(db, dst string) error

	// HealthTimeout 是健康检查的总时长,默认 60s。
	HealthTimeout time.Duration
	// HealthInterval 是两次健康检查之间的间隔,默认 2s。
	HealthInterval time.Duration
	// Now 返回当前时间,默认 time.Now。
	Now func() time.Time
}

// WriteControlRequest 原子地写下请求文件(临时文件 + rename,0644,自动建目录)。
// 请求文件已存在时返回 ErrRequestPending。
func WriteControlRequest(path string, req ControlRequest) error {
	if _, err := os.Stat(path); err == nil {
		return ErrRequestPending
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查请求文件 %s 失败: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建请求目录 %s 失败: %w", dir, err)
	}
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化更新请求失败: %w", err)
	}
	data = append(data, '\n')

	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建请求临时文件失败: %w", err)
	}
	tmpName := f.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return fmt.Errorf("设置请求文件权限失败: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入请求文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭请求文件失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换请求文件失败: %w", err)
	}
	tmpName = ""
	return nil
}

// ReadControlState 读取状态文件,供控制面接口查询。就是 LoadState 的包装。
func ReadControlState(path string) (State, bool, error) {
	return LoadState(path)
}

// Run 处理一次更新请求。没有请求时什么都不做。
func (r *ControlRunner) Run(ctx context.Context) error {
	r.applyDefaults()

	if err := r.recoverInterrupted(ctx); err != nil {
		return err
	}

	req, ok, err := readControlRequest(r.Paths.Request)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// 请求已经拿到手,先删掉它:处理过程中再写进来的是一个新请求,不能被这一轮
	// 顺手覆盖掉。
	if err := os.Remove(r.Paths.Request); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("删除请求文件失败: %w", err)
	}

	// 老布局迁移是幂等的:只在 Link 还是普通文件时动一次。
	if err := r.migrateLayout(); err != nil {
		return r.finish("", "", r.now(), resultFailed, "迁移旧布局失败: "+err.Error())
	}

	if req.Action == "" {
		req.Action = "upgrade"
	}
	switch req.Action {
	case "upgrade":
		return r.runUpgrade(ctx, req)
	case "rollback":
		return r.runRollback(ctx, req)
	default:
		return r.finish("", "", r.now(), resultFailed, fmt.Sprintf("未知的更新动作 %q", req.Action))
	}
}

// recoverInterrupted 处理上一次中途被打断的状态:记 failed,确认当前链接版本健康;
// 不健康则按回退流程处理,最后把状态置回 idle。
func (r *ControlRunner) recoverInterrupted(ctx context.Context) error {
	st, ok, err := LoadState(r.Paths.State)
	if err != nil {
		return err
	}
	if !ok || st.State == stateIdle {
		return nil
	}

	current := r.linkVersion()
	// 还没走到 switching 就中断:链接没动、库也没动,只记一笔失败。这时库里的
	// 快照可能是更早某一轮留下的,绝不能拿来恢复。
	early := st.State == stateDownloading || st.State == stateVerifying
	if v, herr := r.health(ctx); early || (herr == nil && v != "" && v == current) {
		st.Component = controlComponent
		st.Trigger = controlTrigger
		st.State = stateIdle
		st.Result = resultFailed
		st.Error = "上次升级中途中断"
		st.FinishedAt = r.now()
		return r.saveState(st)
	}

	previous := r.otherRelease(current)
	if previous == "" {
		st.Component = controlComponent
		st.Trigger = controlTrigger
		st.State = stateIdle
		st.Result = resultFailed
		st.Error = "上次升级中途中断,且没有可回退的版本"
		st.FinishedAt = r.now()
		return r.saveState(st)
	}

	if rerr := r.rollbackTo(ctx, previous); rerr == nil {
		st.State = stateIdle
		st.Result = resultRolledBack
		st.Error = "上次升级中途中断,已回退到 " + previous
		st.FinishedAt = r.now()
		return r.saveState(st)
	} else {
		msg := r.manualRecovery(previous, fmt.Errorf("上次升级中途中断且回退失败: %w", rerr))
		st.State = stateIdle
		st.Result = resultFailed
		st.Error = msg
		st.FinishedAt = r.now()
		if saveErr := r.saveState(st); saveErr != nil {
			return saveErr
		}
		return errors.New(msg)
	}
}

// runUpgrade 执行一次升级。
func (r *ControlRunner) runUpgrade(ctx context.Context, req ControlRequest) error {
	started := r.now()
	current := r.linkVersion()

	target := strings.TrimSpace(req.To)
	explicit := target != ""
	if target == "" {
		latest, err := r.latest(ctx)
		if err != nil {
			return r.finish(current, "", started, resultFailed, "查询最新版本失败: "+err.Error())
		}
		target = latest
	}

	if err := Decide(current, target, explicit, req.AllowDowngrade); err != nil {
		if errors.Is(err, ErrUpToDate) {
			return r.finish(current, target, started, resultOK, "")
		}
		return r.finish(current, target, started, resultFailed, err.Error())
	}

	st := r.newState(current, target, started)
	newPath := filepath.Join(r.Paths.Releases, target, "mpx.new")
	finalPath := filepath.Join(r.Paths.Releases, target, "mpx")
	// 任何失败路径下都不该留下半截 .new。
	cleanupNew := func() { _ = os.Remove(newPath) }

	// downloading
	if err := r.savePhase(st, stateDownloading); err != nil {
		return err
	}
	bin, _, err := r.fetch(ctx, target)
	if err != nil {
		cleanupNew()
		return r.finish(current, target, started, resultFailed, "下载新版本失败: "+err.Error())
	}

	// verifying
	if err := r.savePhase(st, stateVerifying); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		cleanupNew()
		return r.finish(current, target, started, resultFailed, "创建版本目录失败: "+err.Error())
	}
	if err := os.WriteFile(newPath, bin, 0o755); err != nil {
		cleanupNew()
		return r.finish(current, target, started, resultFailed, "写入新二进制失败: "+err.Error())
	}
	out, err := r.versionOf(newPath)
	if err != nil {
		cleanupNew()
		return r.finish(current, target, started, resultFailed, "新二进制自检失败: "+err.Error())
	}
	if !versionMatches(out, target) {
		cleanupNew()
		return r.finish(current, target, started, resultFailed,
			fmt.Sprintf("新二进制版本输出中未找到 %q: %s", target, strings.TrimSpace(out)))
	}
	if err := os.Rename(newPath, finalPath); err != nil {
		cleanupNew()
		return r.finish(current, target, started, resultFailed, "放置新二进制失败: "+err.Error())
	}

	// 快照放在进入 switching 之前:状态一旦是 switching/health-check,就说明这一轮
	// 的快照已经落盘,中断后的自愈可以放心用它恢复库。
	if err := r.SnapshotDB(r.Paths.DB, r.Paths.Snapshot); err != nil {
		return r.finish(current, target, started, resultFailed, "快照数据库失败: "+err.Error())
	}

	// switching
	if err := r.savePhase(st, stateSwitching); err != nil {
		return err
	}
	if err := r.switchLink(target); err != nil {
		return r.finish(current, target, started, resultFailed, "切换符号链接失败: "+err.Error())
	}
	if err := r.Systemctl(ctx, "restart", "mpx"); err != nil {
		// 已经切到新版但没能重启,按健康检查失败的回退流程走。
		return r.rollbackAfterFailure(ctx, current, target, started, err)
	}

	// health-check
	if err := r.savePhase(st, stateHealthCheck); err != nil {
		return err
	}
	if err := r.waitHealthy(ctx, target); err != nil {
		return r.rollbackAfterFailure(ctx, current, target, started, err)
	}

	r.pruneReleases(target, current)
	return r.finish(current, target, started, resultOK, "")
}

// runRollback 把链接切到除当前版本外的另一个版本。它不恢复数据库:回退动作是
// 人显式要求的,库保持现状。
func (r *ControlRunner) runRollback(ctx context.Context, req ControlRequest) error {
	started := r.now()
	current := r.linkVersion()
	previous := r.otherRelease(current)
	if previous == "" {
		return r.finish(current, "", started, resultFailed, "没有可回退的版本")
	}

	st := r.newState(current, previous, started)
	if err := r.savePhase(st, stateSwitching); err != nil {
		return err
	}
	if err := r.switchLink(previous); err != nil {
		return r.finish(current, previous, started, resultFailed, "切换符号链接失败: "+err.Error())
	}
	if err := r.Systemctl(ctx, "restart", "mpx"); err != nil {
		return r.finish(current, previous, started, resultFailed, "重启 mpx 服务失败: "+err.Error())
	}
	if err := r.savePhase(st, stateHealthCheck); err != nil {
		return err
	}
	if err := r.waitHealthy(ctx, previous); err != nil {
		return r.finish(current, previous, started, resultFailed, "回退后健康检查失败: "+err.Error())
	}

	r.pruneReleases(previous, current)
	return r.finish(current, previous, started, resultOK, "")
}

// rollbackAfterFailure 是升级健康检查失败后的回退:停服、恢复库快照、切回旧版、
// 启服再看健康。它把最终状态写成 rolled-back 或 failed。
func (r *ControlRunner) rollbackAfterFailure(ctx context.Context, previous, target string, started time.Time, cause error) error {
	if previous == "" {
		return r.finish(previous, target, started, resultFailed, r.manualRecovery(previous, cause))
	}
	if rerr := r.rollbackTo(ctx, previous); rerr == nil {
		return r.finish(previous, target, started, resultRolledBack, "新版本健康检查失败: "+cause.Error())
	} else {
		return r.finish(previous, target, started, resultFailed, r.manualRecovery(previous, rerr))
	}
}

// rollbackTo 停服、恢复快照、切回 previous、启服并确认健康。
func (r *ControlRunner) rollbackTo(ctx context.Context, previous string) error {
	if err := r.Systemctl(ctx, "stop", "mpx"); err != nil {
		return fmt.Errorf("停止 mpx 服务失败: %w", err)
	}
	if err := r.restoreDB(); err != nil {
		return err
	}
	if err := r.switchLink(previous); err != nil {
		return fmt.Errorf("切回旧版本链接失败: %w", err)
	}
	if err := r.Systemctl(ctx, "start", "mpx"); err != nil {
		return fmt.Errorf("启动 mpx 服务失败: %w", err)
	}
	if err := r.waitHealthy(ctx, previous); err != nil {
		return fmt.Errorf("回退后健康检查失败: %w", err)
	}
	return nil
}

// manualRecovery 拼出回退也失败时给人看的手动命令。
func (r *ControlRunner) manualRecovery(previous string, cause error) string {
	bin := filepath.Join(r.Paths.Releases, previous, "mpx")
	if previous == "" {
		bin = filepath.Join(r.Paths.Releases, "<旧版本>", "mpx")
	}
	return fmt.Sprintf("回退后仍不健康(%v);请手动处理:journalctl -u mpx 查看日志;ln -sfn %s %s", cause, bin, r.Paths.Link)
}

// restoreDB 把快照复制回数据库,并清掉 WAL/SHM 附属文件。
func (r *ControlRunner) restoreDB() error {
	if err := copyFileInto(r.Paths.Snapshot, r.Paths.DB); err != nil {
		return fmt.Errorf("恢复数据库失败: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(r.Paths.DB + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("删除数据库附属文件失败: %w", err)
		}
	}
	return nil
}

// migrateLayout 把老布局(Link 是普通文件)迁到 Releases/<版本>/mpx + 符号链接。
// 已经是链接或不存在时是空操作,因此可以反复跑。
func (r *ControlRunner) migrateLayout() error {
	info, err := os.Lstat(r.Paths.Link)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("检查 %s 失败: %w", r.Paths.Link, err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	out, err := r.versionOf(r.Paths.Link)
	if err != nil {
		return fmt.Errorf("读取旧版二进制版本失败: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return errors.New("旧版二进制没有输出版本号,无法迁移")
	}
	version := fields[len(fields)-1]

	dst := filepath.Join(r.Paths.Releases, version, "mpx")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("创建版本目录失败: %w", err)
	}
	if err := os.Rename(r.Paths.Link, dst); err != nil {
		return fmt.Errorf("移动旧版二进制失败: %w", err)
	}
	if err := r.switchLink(version); err != nil {
		return fmt.Errorf("建立符号链接失败: %w", err)
	}
	return nil
}

// switchLink 用临时链接加 rename 的方式,把 Link 指向 Releases/<version>/mpx。
func (r *ControlRunner) switchLink(version string) error {
	target := filepath.Join(r.Paths.Releases, version, "mpx")
	if err := os.MkdirAll(filepath.Dir(r.Paths.Link), 0o755); err != nil {
		return fmt.Errorf("创建链接目录失败: %w", err)
	}
	tmp := r.Paths.Link + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("清理临时链接失败: %w", err)
	}
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("创建临时链接失败: %w", err)
	}
	if err := os.Rename(tmp, r.Paths.Link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换链接失败: %w", err)
	}
	return nil
}

// linkVersion 返回 Link 指向的目录名,也就是当前生效的版本。
func (r *ControlRunner) linkVersion() string {
	target, err := os.Readlink(r.Paths.Link)
	if err != nil {
		return ""
	}
	return filepath.Base(filepath.Dir(target))
}

// otherRelease 返回 Releases 下除 current 外的一个版本目录(按名字排序取第一个)。
func (r *ControlRunner) otherRelease(current string) string {
	for _, name := range r.listReleases() {
		if name != current {
			return name
		}
	}
	return ""
}

// listReleases 按名字排序列出 Releases 下的版本目录。
func (r *ControlRunner) listReleases() []string {
	entries, err := os.ReadDir(r.Paths.Releases)
	if err != nil {
		return nil
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

// pruneReleases 只保留 keep 里的版本目录,其余删除(尽力而为)。
func (r *ControlRunner) pruneReleases(keep ...string) {
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		if k != "" {
			keepSet[k] = true
		}
	}
	for _, name := range r.listReleases() {
		if keepSet[name] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(r.Paths.Releases, name))
	}
}

// waitHealthy 在 HealthTimeout 内轮询:服务 active 且控制面自报版本等于 want。
func (r *ControlRunner) waitHealthy(ctx context.Context, want string) error {
	deadline := time.Now().Add(r.HealthTimeout)
	var last error
	for {
		if err := r.Systemctl(ctx, "is-active", "--quiet", "mpx"); err != nil {
			last = fmt.Errorf("mpx 服务未处于 active: %w", err)
		} else if got, herr := r.health(ctx); herr != nil {
			last = herr
		} else if got != want {
			last = fmt.Errorf("控制面版本 %q 与期望 %q 不一致", got, want)
		} else {
			return nil
		}

		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.HealthInterval):
		}
	}
	if last == nil {
		last = errors.New("健康检查超时")
	}
	return last
}

// newState 造一份阶段状态的骨架。
func (r *ControlRunner) newState(from, to string, started time.Time) State {
	return State{
		Component: controlComponent,
		Trigger:   controlTrigger,
		From:      from,
		To:        to,
		StartedAt: started,
	}
}

// savePhase 写一个进行中的阶段状态。
func (r *ControlRunner) savePhase(st State, phase string) error {
	st.State = phase
	st.FinishedAt = time.Time{}
	return r.saveState(st)
}

// finish 写最终状态。带错误信息时同时把该错误返回给调用方。
func (r *ControlRunner) finish(from, to string, started time.Time, result, errMsg string) error {
	st := r.newState(from, to, started)
	st.State = stateIdle
	st.Result = result
	st.FinishedAt = r.now()
	st.Error = errMsg
	if err := r.saveState(st); err != nil {
		return err
	}
	if errMsg != "" {
		return errors.New(errMsg)
	}
	return nil
}

func (r *ControlRunner) saveState(st State) error {
	if err := SaveState(r.Paths.State, st); err != nil {
		return fmt.Errorf("写入状态失败: %w", err)
	}
	// 控制面(mpx 用户)要通过 GET /api/update 读这份状态:放开读权限,属主
	// 与库目录一致。另外顺手把库的附属文件属主拉回来(见 matchOwner)。
	_ = os.Chmod(r.Paths.State, 0o644)
	ref := filepath.Dir(r.Paths.DB)
	matchOwner(ref, r.Paths.State, r.Paths.DB, r.Paths.DB+"-wal", r.Paths.DB+"-shm")
	return nil
}

func (r *ControlRunner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *ControlRunner) health(ctx context.Context) (string, error) {
	if r.Health == nil {
		return "", errors.New("未注入健康检查实现,无法判断控制面状态")
	}
	return r.Health(ctx)
}

func (r *ControlRunner) fetch(ctx context.Context, version string) ([]byte, bool, error) {
	if r.Fetch == nil {
		return nil, false, errors.New("未注入取件实现")
	}
	return r.Fetch(ctx, version)
}

func (r *ControlRunner) latest(ctx context.Context) (string, error) {
	if r.Latest == nil {
		return "", errors.New("未注入查询最新版本的实现")
	}
	return r.Latest(ctx)
}

func (r *ControlRunner) versionOf(bin string) (string, error) {
	if r.Version == nil {
		return "", errors.New("未注入版本检查实现")
	}
	return r.Version(bin)
}

// applyDefaults 补上可注入字段的默认实现。
func (r *ControlRunner) applyDefaults() {
	if r.Version == nil {
		r.Version = runVersion
	}
	if r.Systemctl == nil {
		r.Systemctl = func(ctx context.Context, args ...string) error {
			cmd := exec.CommandContext(ctx, "systemctl", args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("systemctl %s 失败: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	if r.SnapshotDB == nil {
		r.SnapshotDB = snapshotViaSQLite
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.HealthTimeout <= 0 {
		r.HealthTimeout = 60 * time.Second
	}
	if r.HealthInterval <= 0 {
		r.HealthInterval = 2 * time.Second
	}
}

// readControlRequest 读请求文件,不存在时返回 ok=false。
func readControlRequest(path string) (ControlRequest, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ControlRequest{}, false, nil
		}
		return ControlRequest{}, false, fmt.Errorf("读取更新请求失败: %w", err)
	}
	var req ControlRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ControlRequest{}, false, fmt.Errorf("解析更新请求失败: %w", err)
	}
	return req, true, nil
}

// copyFileInto 把 src 的内容复制到 dst(新建或截断)。
func copyFileInto(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if dir := filepath.Dir(dst); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// snapshotViaSQLite 是 SnapshotDB 的默认实现:用 VACUUM INTO 做一致性快照。
func snapshotViaSQLite(dbPath, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("删除旧快照 %s 失败: %w", dst, err)
	}
	if dir := filepath.Dir(dst); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建快照目录失败: %w", err)
		}
	}

	// 控制面可能正在写库,busy_timeout 必须有,否则会立刻撞 database is locked。
	conn, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("快照数据库失败: %w", err)
	}
	return nil
}

// sqliteDSN 拼出与 store 包一致的 DSN(路径转义 + busy_timeout)。
func sqliteDSN(path string) string {
	u := url.URL{Path: path}
	return "file:" + u.EscapedPath() + "?_pragma=busy_timeout(5000)"
}
