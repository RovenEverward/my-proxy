package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/selfupdate"
)

// makeTarGz 现场打一个只含 mpx 可执行文件的 tar.gz。
func makeTarGz(t *testing.T, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(script)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "mpx",
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("写 tar 头: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("写 tar 正文: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("关闭 tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("关闭 gzip: %v", err)
	}
	return buf.Bytes()
}

// releaseServer 起一个假发布站,同时充当 GitHub 接口与下载站。
//
// tag 是最新发布版本;archive 是发布件;sums 为 nil 时给 404(模拟没有清单的
// 旧发布)。handler 之外还接受 /api/status 之类与发布无关的请求,一律 404。
func releaseServer(t *testing.T, tag string, archive, sums []byte) *httptest.Server {
	t.Helper()
	name := selfupdate.ArchiveName(runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, tag)
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			if sums == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_, _ = w.Write(sums)
		case strings.HasSuffix(r.URL.Path, "/"+name):
			_, _ = w.Write(archive)
		default:
			http.Error(w, `{"error":"没有这个接口"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// selfUpdateFixture 把与本机自更新有关的外部世界全部换成假的,返回假可执行
// 文件的路径与状态文件路径。
func selfUpdateFixture(t *testing.T, local, target string, archive, sums []byte) (exe, statePath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("本机自更新测试用的是 shell 脚本,只在类 unix 上跑")
	}

	dir := t.TempDir()
	exe = filepath.Join(dir, "mpx")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho \"mpx "+local+"\"\n"), 0o755); err != nil {
		t.Fatalf("写假 mpx: %v", err)
	}
	statePath = filepath.Join(t.TempDir(), "update.json")

	tag := target
	srv := releaseServer(t, tag, archive, sums)

	oldSource, oldExe := selfupdateSource, selfExecutable
	oldState, oldTTY := cliStatePath, stdinIsTerminal
	t.Cleanup(func() {
		selfupdateSource, selfExecutable = oldSource, oldExe
		cliStatePath, stdinIsTerminal = oldState, oldTTY
		resetUpdateApplyFlags()
	})

	selfupdateSource = func() selfupdate.Source {
		return selfupdate.Source{Repo: "test/repo", APIBase: srv.URL, DownloadBase: srv.URL}
	}
	selfExecutable = func() (string, error) { return exe, nil }
	cliStatePath = func() (string, error) { return statePath, nil }
	stdinIsTerminal = func() bool { return false }

	// 把控制面清干净:warnControlAfterUpdate 该静默跳过,不能去碰发布站。
	t.Setenv("MPX_SERVER", "")
	t.Setenv("MPX_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	opts.server, opts.token = "", ""
	withLocalVersion(t, local)
	return exe, statePath
}

// resetUpdateApplyFlags 清掉 apply 子命令 flag 的 Changed 位,免得一个用例的
// --version 泄漏到下一个。
func resetUpdateApplyFlags() {
	for _, name := range []string{"version", "allow-downgrade", "yes"} {
		if f := updateApplyCmd.Flags().Lookup(name); f != nil {
			f.Changed = false
			_ = f.Value.Set(f.DefValue)
		}
	}
}

func wrongSums(t *testing.T, name string) []byte {
	t.Helper()
	return []byte(strings.Repeat("0", 64) + "  " + name + "\n")
}

func TestUpdateApplyCLIUpgrades(t *testing.T) {
	target := "v0.6.1"
	archive := makeTarGz(t, "#!/bin/sh\necho \"mpx "+target+"\"\n")
	sums := []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), selfupdate.ArchiveName(runtime.GOOS, runtime.GOARCH)))
	exe, statePath := selfUpdateFixture(t, "v0.6.0", target, archive, sums)

	out := captureStdout(t, func() {
		runRoot(t, "update", "apply", "cli", "--version", target, "-y")
	})

	if !strings.Contains(out, "v0.6.0 → v0.6.1") {
		t.Errorf("计划那一行不对:\n%s", out)
	}
	if !strings.Contains(out, "已升级到 v0.6.1") {
		t.Errorf("成功提示缺失:\n%s", out)
	}

	// 文件被换来;旧文件退到 .prev。
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("读升级后的 mpx: %v", err)
	}
	if !strings.Contains(string(got), target) {
		t.Errorf("exe 内容没换:\n%s", got)
	}
	if _, err := os.Stat(exe + ".prev"); err != nil {
		t.Errorf(".prev 不存在: %v", err)
	}

	st, ok, err := selfupdate.LoadState(statePath)
	if err != nil || !ok {
		t.Fatalf("读状态: ok=%v err=%v", ok, err)
	}
	if st.Result != "ok" || st.From != "v0.6.0" || st.To != target || st.Component != "cli" {
		t.Errorf("状态不对:%+v", st)
	}
}

func TestUpdateApplyCLIRejectsNonInteractiveWithoutYes(t *testing.T) {
	target := "v0.6.1"
	archive := makeTarGz(t, "#!/bin/sh\necho \"mpx "+target+"\"\n")
	exe, _ := selfUpdateFixture(t, "v0.6.0", target, archive, nil)

	err := runRootErr(t, "update", "apply", "cli", "--version", target)
	if err == nil || !strings.Contains(err.Error(), "非交互环境请加 -y") {
		t.Fatalf("err = %v, 想要「非交互环境请加 -y」", err)
	}
	// 没确认过就不该动文件。
	got, _ := os.ReadFile(exe)
	if !strings.Contains(string(got), "v0.6.0") {
		t.Errorf("被拒绝了却改了文件:\n%s", got)
	}
}

func TestUpdateApplyCLIAlreadyLatest(t *testing.T) {
	exe, _ := selfUpdateFixture(t, "v0.6.1", "v0.6.1", nil, nil)

	out := captureStdout(t, func() {
		runRoot(t, "update", "apply", "cli", "--version", "v0.6.1", "-y")
	})
	if !strings.Contains(out, "本机 CLI 已是最新") {
		t.Errorf("已最新时该直说:\n%s", out)
	}
	got, _ := os.ReadFile(exe)
	if !strings.Contains(string(got), "v0.6.1") {
		t.Errorf("文件被无谓地改了:\n%s", got)
	}
}

func TestUpdateApplyCLIHashMismatchLeavesFile(t *testing.T) {
	target := "v0.6.1"
	archive := makeTarGz(t, "#!/bin/sh\necho \"mpx "+target+"\"\n")
	exe, statePath := selfUpdateFixture(t, "v0.6.0", target, archive,
		wrongSums(t, selfupdate.ArchiveName(runtime.GOOS, runtime.GOARCH)))

	err := runRootErr(t, "update", "apply", "cli", "--version", target, "-y")
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err = %v, 想要哈希不符", err)
	}
	got, _ := os.ReadFile(exe)
	if !strings.Contains(string(got), "v0.6.0") {
		t.Errorf("校验失败却动了文件:\n%s", got)
	}
	if _, err := os.Stat(exe + ".prev"); !os.IsNotExist(err) {
		t.Errorf("校验失败不该留下 .prev: %v", err)
	}
	st, ok, err := selfupdate.LoadState(statePath)
	if err != nil || !ok {
		t.Fatalf("读状态: ok=%v err=%v", ok, err)
	}
	if st.Result != "failed" || st.Error == "" {
		t.Errorf("失败状态没记下:%+v", st)
	}
}

func TestUpdateRollbackCLIRoundTrip(t *testing.T) {
	target := "v0.6.1"
	archive := makeTarGz(t, "#!/bin/sh\necho \"mpx "+target+"\"\n")
	sums := []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), selfupdate.ArchiveName(runtime.GOOS, runtime.GOARCH)))
	exe, statePath := selfUpdateFixture(t, "v0.6.0", target, archive, sums)

	runRoot(t, "update", "apply", "cli", "--version", target, "-y")
	out := captureStdout(t, func() { runRoot(t, "update", "rollback", "cli") })
	if !strings.Contains(out, "已回退") || !strings.Contains(out, "mpx v0.6.0") {
		t.Errorf("回退输出不对:\n%s", out)
	}
	got, _ := os.ReadFile(exe)
	if !strings.Contains(string(got), "v0.6.0") {
		t.Errorf("回退后 exe 内容不对:\n%s", got)
	}
	st, ok, err := selfupdate.LoadState(statePath)
	if err != nil || !ok {
		t.Fatalf("读状态: ok=%v err=%v", ok, err)
	}
	if st.Result != "rolled-back" {
		t.Errorf("回退状态没记下:%+v", st)
	}
}

func TestUpdateApplyUnsupportedComponents(t *testing.T) {
	cases := []struct {
		part string
		want string
	}{
		{"control", "控制面远程升级尚未支持（M3）"},
		{"nodes", "节点核心升级尚未支持（M4）"},
	}
	for _, tc := range cases {
		t.Run(tc.part, func(t *testing.T) {
			err := runRootErr(t, "update", "apply", tc.part)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, 想要 %q", err, tc.want)
			}
		})
	}
}

func TestUpdateApplyUnknownComponent(t *testing.T) {
	err := runRootErr(t, "update", "apply", "database")
	if err == nil {
		t.Fatal("未知部件该报错")
	}
	if !strings.Contains(err.Error(), "database") || !strings.Contains(err.Error(), "cli、control、nodes") {
		t.Errorf("错误没有列出可选值: %v", err)
	}
}

func TestUpdateApplyAllowDowngradeNeedsVersion(t *testing.T) {
	// 起一个空发布站,确认在问网络之前就被拦住。
	t.Setenv("MPX_SERVER", "")
	err := runRootErr(t, "update", "apply", "cli", "--allow-downgrade", "-y")
	if err == nil || !strings.Contains(err.Error(), "--allow-downgrade") {
		t.Fatalf("err = %v, 想要要求 --version", err)
	}
}

// mpx update 的本机 CLI 那行要带上最近一次自更新的结果。
func TestUpdateShowDisplaysLastResult(t *testing.T) {
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })
	withLocalVersion(t, "v0.2.0")

	statePath := filepath.Join(t.TempDir(), "update.json")
	oldState := cliStatePath
	cliStatePath = func() (string, error) { return statePath, nil }
	t.Cleanup(func() { cliStatePath = oldState })

	if err := selfupdate.SaveState(statePath, selfupdate.State{
		Component:  "cli",
		Result:     "ok",
		From:       "v0.1.0",
		To:         "v0.2.0",
		FinishedAt: time.Date(2026, 5, 1, 3, 2, 0, 0, time.Local),
	}); err != nil {
		t.Fatalf("写状态: %v", err)
	}

	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(updatePayload(t, "v0.2.0", "v0.2.0", nil))
	})

	out := captureStdout(t, func() { runRoot(t, "update", "show") })
	if !strings.Contains(out, "上次") || !strings.Contains(out, "v0.1.0→v0.2.0") || !strings.Contains(out, "成功") {
		t.Errorf("本机 CLI 那行没带上上次结果:\n%s", out)
	}

	// json 输出要有 cli_last 字段。
	opts.output = outputJSON
	jsonOut := captureStdout(t, func() { runRoot(t, "update", "-o", "json") })
	var report struct {
		CLI     model.UpdatePart  `json:"cli"`
		CLILast *selfupdate.State `json:"cli_last"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("json 解析失败: %v\n%s", err, jsonOut)
	}
	if report.CLILast == nil || report.CLILast.Result != "ok" {
		t.Errorf("cli_last 没进 json: %s", jsonOut)
	}
}
