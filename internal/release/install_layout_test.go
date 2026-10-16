package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// layoutBlock 是 install.sh 里「版本目录 + 符号链接」那一段。
var layoutBlock = regexp.MustCompile(`(?s)\n# >>> install_release_layout.*?# <<< install_release_layout\n`)

// writeFake 造一个只回答 --version / --help 的假 mpx。
func writeFake(t *testing.T, path, version string) {
	t.Helper()
	body := "#!/usr/bin/env bash\n" +
		"if [[ \"${1:-}\" == --version ]]; then echo \"mpx version " + version + "\"; else echo usage; fi\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runLayout 把脚本里那段布局函数原样拿出来,在 MPX_PREFIX 前缀下装一个假二进制。
func runLayout(t *testing.T, prefix, bin string) {
	t.Helper()
	sh := `set -euo pipefail
die() { printf '错误:%s\n' "$1" >&2; exit 1; }
VERSION=latest
BIN_ARG=local
` + layoutBlock.FindString(readScript(t, "install.sh")) + `
install_release_link "$1"
`
	cmd := exec.Command("bash", "-c", sh, "install-layout-test", bin)
	cmd.Env = append(os.Environ(), "MPX_PREFIX="+prefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("安装布局失败: %v\n%s", err, out)
	}
}

func releases(t *testing.T, prefix string) []string {
	t.Helper()
	names, err := os.ReadDir(filepath.Join(prefix, "usr/local/lib/mpx/releases"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range names {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func linkTarget(t *testing.T, prefix string) string {
	t.Helper()
	link := filepath.Join(prefix, "usr/local/bin/mpx")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("%s 不是符号链接: %v", link, err)
	}
	return target
}

// TestInstallReleaseLayout 验证版本目录 + 链接切换 + 只留两版(含老布局迁移与重复安装)。
func TestInstallReleaseLayout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("install.sh 只支持 Linux")
	}
	prefix := t.TempDir()
	binDir := t.TempDir()

	// 老布局:安装前 /usr/local/bin/mpx 是个普通文件(v0.5.0),应被收进 releases/v0.5.0。
	old := filepath.Join(prefix, "usr/local/bin/mpx")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFake(t, old, "v0.5.0")

	// 第一次:普通文件迁移 + 装 v0.6.0。
	v6 := filepath.Join(binDir, "mpx-v6")
	writeFake(t, v6, "v0.6.0")
	runLayout(t, prefix, v6)
	if got := releases(t, prefix); strings.Join(got, ",") != "v0.5.0,v0.6.0" {
		t.Fatalf("装了 v0.6.0 之后版本目录 = %v,想要 [v0.5.0 v0.6.0]", got)
	}
	want := filepath.Join(prefix, "usr/local/lib/mpx/releases/v0.6.0/mpx")
	if got := linkTarget(t, prefix); got != want {
		t.Fatalf("链接指向 %s,想要 %s", got, want)
	}
	if fi, err := os.Lstat(old); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s 不是符号链接: %v(普通文件没有迁进 releases)", old, err)
	}
	if _, err := os.Stat(filepath.Join(prefix, "usr/local/lib/mpx/releases/v0.5.0/mpx")); err != nil {
		t.Fatalf("老布局的普通文件没有按旧版本收进版本目录: %v", err)
	}

	// 第二次:装 v0.7.0,上一版 v0.6.0 留下,v0.5.0 该被清掉。
	v7 := filepath.Join(binDir, "mpx-v7")
	writeFake(t, v7, "v0.7.0")
	runLayout(t, prefix, v7)
	if got := releases(t, prefix); strings.Join(got, ",") != "v0.6.0,v0.7.0" {
		t.Fatalf("装了 v0.7.0 之后版本目录 = %v,想要 [v0.6.0 v0.7.0]", got)
	}

	// 第三次:装 v0.8.0,只剩当前版与上一版。
	v8 := filepath.Join(binDir, "mpx-v8")
	writeFake(t, v8, "v0.8.0")
	runLayout(t, prefix, v8)
	if got := releases(t, prefix); strings.Join(got, ",") != "v0.7.0,v0.8.0" {
		t.Fatalf("装了 v0.8.0 之后版本目录 = %v,想要 [v0.7.0 v0.8.0]", got)
	}
	if got := linkTarget(t, prefix); got != filepath.Join(prefix, "usr/local/lib/mpx/releases/v0.8.0/mpx") {
		t.Fatalf("链接指向 %s,想要 v0.8.0", got)
	}

	// 第四次:重复装同一版,不新增目录,链接仍对。
	runLayout(t, prefix, v8)
	if got := releases(t, prefix); strings.Join(got, ",") != "v0.7.0,v0.8.0" {
		t.Fatalf("重复装 v0.8.0 之后版本目录 = %v,想要 [v0.7.0 v0.8.0]", got)
	}
	if got := linkTarget(t, prefix); got != filepath.Join(prefix, "usr/local/lib/mpx/releases/v0.8.0/mpx") {
		t.Fatalf("重复装之后链接指向 %s,想要 v0.8.0", got)
	}
}

// TestInstallReleaseLayoutLocalVersion 验证源码编出来的二进制(没有 v 开头版本)
// 会落进 local-<时间戳> 目录,而不是拿一个奇怪的目录名。
func TestInstallReleaseLayoutLocalVersion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("install.sh 只支持 Linux")
	}
	prefix := t.TempDir()
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "mpx-local")
	writeFake(t, bin, "未知(从源码直接编译)")
	runLayout(t, prefix, bin)

	got := releases(t, prefix)
	if len(got) != 1 || !strings.HasPrefix(got[0], "local-") {
		t.Fatalf("版本目录 = %v,想要一个 local-<时间戳> 目录", got)
	}
	if target := linkTarget(t, prefix); filepath.Base(target) != "mpx" {
		t.Fatalf("链接指向 %s,想要某个 releases/<版本>/mpx", target)
	}
}
