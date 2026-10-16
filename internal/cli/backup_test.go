package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runRoot 跑一次完整命令(含 flag 解析),并在测试结束复原可变状态。
// 别的测试直接调 Client,这里要覆盖的是命令注册与 flag 接线。
func runRoot(t *testing.T, args ...string) {
	t.Helper()
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		backupOut = ""
		restoreForce = false
		resetRulesFlags()
		resetUpdateApplyFlags()
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("mpx %v: %v", strings.Join(args, " "), err)
	}
}

// runRootErr 同 runRoot,但预期失败并把错误交回给调用方。
//
// 命令对象是包级共享的,pflag 的 Changed 位在 Execute 之后不会自己清掉 ——
// 一个用例给了 --index,下一个用例即便不给也会被当成"给过了"。这里的清理由
// runRoot/resetRulesFlags 统一负责。
func runRootErr(t *testing.T, args ...string) error {
	t.Helper()
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		resetRulesFlags()
		resetUpdateApplyFlags()
	})
	return rootCmd.Execute()
}

// resetRulesFlags 把 rules 子命令的 flag 值与 Changed 位都归零。pflag 只暴露
// Changed 字段本身,没有"取消设置"的 API,所以直接改字段。
func resetRulesFlags() {
	settingsRulesResult, settingsRulesIndex = "", ""
	for _, name := range []string{"result", "index"} {
		if f := settingsRulesAddCmd.Flags().Lookup(name); f != nil {
			f.Changed = false
			_ = f.Value.Set(f.DefValue)
		}
	}
}

// fakeControlPlane 起一个假控制面,把请求记下来。returns 前先 t.Cleanup 关掉。
func fakeControlPlane(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "secret-token")
	// 全局 flag 的值跨测试留着,先清掉,免得某个测试的 --server 泄漏进来。
	opts.server, opts.token = "", ""
}

func TestBackupCommandWritesArchiveToFlagPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 Unix 文件权限位")
	}
	payload := []byte("fake-archive-bytes")
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/backup" {
			t.Errorf("意外的请求: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write(payload)
	})

	out := filepath.Join(t.TempDir(), "backup.tar.gz")
	runRoot(t, "backup", "-o", out)

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读备份文件: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("落盘内容 = %q, 想要 %q", got, payload)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("查看备份文件: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("备份文件权限 = %o, 想要 600", perm)
	}
}

// TestBackupCommandDefaultFileName:不给 -o 时用 mpx-backup-<时间戳>.tar.gz。
func TestBackupCommandDefaultFileName(t *testing.T) {
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("archive"))
	})

	dir := t.TempDir()
	t.Chdir(dir)
	runRoot(t, "backup")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读目录: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("目录里应当只有一个备份文件,得到 %d 个", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "mpx-backup-") || !strings.HasSuffix(name, ".tar.gz") {
		t.Errorf("默认文件名 = %q, 想要 mpx-backup-<时间戳>.tar.gz", name)
	}
}

func TestRestoreCommandPostsArchiveBytes(t *testing.T) {
	archive := []byte("mpx-archive\x00\x01\x02-bytes")
	var (
		gotBody  []byte
		gotQuery string
		calls    int
	)
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/restore" {
			t.Errorf("意外的请求: %s %s", r.Method, r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读请求体: %v", err)
		}
		gotBody = body
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"message":"已就绪,重启 mpx 后生效"}`))
	})

	path := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatalf("写归档: %v", err)
	}

	runRoot(t, "restore", path)
	if calls != 1 {
		t.Fatalf("请求次数 = %d, 想要 1", calls)
	}
	if !bytes.Equal(gotBody, archive) {
		t.Errorf("正文 = %q, 想要归档原样 %q", gotBody, archive)
	}
	if gotQuery != "" {
		t.Errorf("不带 --force 时查询串 = %q, 想要空", gotQuery)
	}

	runRoot(t, "restore", "--force", path)
	if calls != 2 {
		t.Fatalf("请求次数 = %d, 想要 2", calls)
	}
	if gotQuery != "force=1" {
		t.Errorf("带 --force 时查询串 = %q, 想要 force=1", gotQuery)
	}
	if !bytes.Equal(gotBody, archive) {
		t.Errorf("第二次正文 = %q, 想要归档原样", gotBody)
	}
}

// TestRestoreCommandReportsAPIError:控制面拒绝时把服务端那句原样报出来。
func TestRestoreCommandReportsAPIError(t *testing.T) {
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"已有一份待生效的恢复,重启后生效;要覆盖它请加 ?force=1"}`))
	})

	path := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
		t.Fatalf("写归档: %v", err)
	}

	rootCmd.SetArgs([]string{"restore", path})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("期望报错,实际成功")
	}
	if !strings.Contains(err.Error(), "已有一份待生效的恢复") {
		t.Errorf("错误 = %q, 想要服务端那句", err.Error())
	}
}
