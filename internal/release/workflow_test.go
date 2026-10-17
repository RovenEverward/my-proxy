package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 这组测试直接跑 .github/workflows/release.yml 里 publish job 的「改名」和
// 「SHA256SUMS」两步脚本:用一份假的 dist/(各打包 job 的中间名)喂进去,核对
// 对外的文件名、固定名副本、自更新压缩包不改名,以及大小写查重。

type wfStep struct {
	Name string            `yaml:"name"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
}

func publishSteps(t *testing.T) []wfStep {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []wfStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	return wf.Jobs["publish"].Steps
}

func stepRun(t *testing.T, steps []wfStep, prefix string) string {
	t.Helper()
	for _, s := range steps {
		if strings.HasPrefix(s.Name, prefix) {
			return s.Run
		}
	}
	t.Fatalf("publish job 里没有「%s」这一步", prefix)
	return ""
}

const testVer = "0.9.4"

// intermediate 是各打包 job 产出的文件名(上传前)。
func intermediate() []string {
	v := testVer
	return []string{
		"mpx_darwin_amd64.tar.gz", "mpx_darwin_arm64.tar.gz", "mpx_linux_amd64.tar.gz", "mpx_linux_arm64.tar.gz", "mpx_windows_amd64.zip",
		"mpx_desktop_darwin_universal.tar.gz", "mpx_desktop_linux_amd64.tar.gz", "mpx_desktop_linux_arm64.tar.gz", "mpx_desktop_windows_amd64.zip",
		"mpx_" + v + "_darwin_universal.pkg", "mpx_" + v + "_windows_amd64_setup.exe",
		"mpx_" + v + "_linux_amd64.deb", "mpx_" + v + "_linux_arm64.deb", "mpx_" + v + "_linux_amd64.rpm", "mpx_" + v + "_linux_arm64.rpm",
		"mpx-desktop_" + v + "_darwin_universal.dmg", "mpx-desktop_" + v + "_windows_amd64_setup.exe",
		"mpx-desktop_" + v + "_amd64.deb", "mpx-desktop_" + v + "_arm64.deb",
		"mpx-desktop_" + v + "_linux_amd64.tar.gz", "mpx-desktop_" + v + "_linux_arm64.tar.gz",
	}
}

func runScript(t *testing.T, dir, script string) (string, error) {
	t.Helper()
	script = strings.ReplaceAll(script, "${{ steps.ver.outputs.ver }}", testVer)
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "VER="+testVer)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestReleaseAssetNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("需要 bash")
	}
	steps := publishSteps(t)
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	os.Mkdir(dist, 0o755)
	for _, n := range intermediate() {
		os.WriteFile(filepath.Join(dist, n), []byte("content of "+n), 0o644)
	}
	if out, err := runScript(t, root, stepRun(t, steps, "安装包改成对外名字")); err != nil {
		t.Fatalf("改名失败: %v\n%s", err, out)
	}
	if out, err := runScript(t, root, stepRun(t, steps, "检查附件名不分大小写也不重复")); err != nil {
		t.Fatalf("查重失败: %v\n%s", err, out)
	}
	if out, err := runScript(t, dist, strings.Replace(stepRun(t, steps, "SHA256SUMS"), "cd dist\n", "", 1)); err != nil {
		t.Fatalf("SHA256SUMS 失败: %v\n%s", err, out)
	}
	ents, _ := os.ReadDir(dist)
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	v := testVer
	want := []string{
		"MPX-" + v + "-Linux-amd64.deb", "MPX-" + v + "-Linux-amd64.tar.gz", "MPX-" + v + "-Linux-arm64.deb", "MPX-" + v + "-Linux-arm64.tar.gz",
		"MPX-" + v + "-Windows-x64-setup.exe", "MPX-" + v + "-macOS.dmg",
		"MPX-Linux-amd64.deb", "MPX-Linux-amd64.tar.gz", "MPX-Linux-arm64.deb", "MPX-Linux-arm64.tar.gz", "MPX-Windows-x64-setup.exe", "MPX-macOS.dmg",
		"SHA256SUMS",
		"mpx-cli_" + v + "_darwin_universal.pkg", "mpx-cli_" + v + "_linux_amd64.deb", "mpx-cli_" + v + "_linux_amd64.rpm",
		"mpx-cli_" + v + "_linux_arm64.deb", "mpx-cli_" + v + "_linux_arm64.rpm", "mpx-cli_" + v + "_windows_amd64_setup.exe",
		"mpx_darwin_amd64.tar.gz", "mpx_darwin_arm64.tar.gz",
		"mpx_desktop_darwin_universal.tar.gz", "mpx_desktop_linux_amd64.tar.gz", "mpx_desktop_linux_arm64.tar.gz", "mpx_desktop_windows_amd64.zip",
		"mpx_linux_amd64.tar.gz", "mpx_linux_arm64.tar.gz", "mpx_windows_amd64.zip",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("发布件名字不对\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	sums, _ := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	for _, n := range want {
		if n == "SHA256SUMS" {
			continue
		}
		if !strings.Contains(string(sums), "  "+n+"\n") {
			t.Errorf("SHA256SUMS 里没有 %s", n)
		}
	}
	// 固定名副本与带版本号的那份内容相同。
	for _, f := range []string{"macOS.dmg", "Windows-x64-setup.exe", "Linux-amd64.deb", "Linux-arm64.deb", "Linux-amd64.tar.gz", "Linux-arm64.tar.gz"} {
		a, _ := os.ReadFile(filepath.Join(dist, "MPX-"+f))
		b, _ := os.ReadFile(filepath.Join(dist, "MPX-"+v+"-"+f))
		if string(a) != string(b) || len(a) == 0 {
			t.Errorf("MPX-%s 与带版本号的不一致", f)
		}
	}
}

func TestReleaseDupCheckCaseInsensitive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("需要 bash")
	}
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	os.Mkdir(dist, 0o755)
	os.WriteFile(filepath.Join(dist, "MPX-macOS.dmg"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dist, "mpx-macos.dmg"), []byte("b"), 0o644)
	if ents, _ := os.ReadDir(dist); len(ents) < 2 {
		t.Skip("文件系统不分大小写(macOS 默认),造不出两个只差大小写的文件")
	}
	if out, err := runScript(t, root, stepRun(t, publishSteps(t), "检查附件名不分大小写也不重复")); err == nil {
		t.Fatalf("大小写不同的同名附件应当被拦下:\n%s", out)
	}
}

func TestReleaseRenameFailsOnMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("需要 bash")
	}
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	os.Mkdir(dist, 0o755)
	for _, n := range intermediate() {
		if strings.HasSuffix(n, ".dmg") {
			continue // 少了 dmg:必须失败,不能悄悄发一个没有 macOS 安装包的版本
		}
		os.WriteFile(filepath.Join(dist, n), []byte(n), 0o644)
	}
	if out, err := runScript(t, root, stepRun(t, publishSteps(t), "安装包改成对外名字")); err == nil {
		t.Fatalf("缺安装包时改名一步应当失败:\n%s", out)
	}
}

func TestReleaseNotesTable(t *testing.T) {
	run := stepRun(t, publishSteps(t), "正文开头加「我该下哪个」")
	i := strings.Index(run, "## 我该下哪个")
	j := strings.Index(run, "<details>")
	if i < 0 || j < i {
		t.Fatal("「我该下哪个」表要在折叠区之前")
	}
	table := run[i:j]
	for _, f := range []string{"MPX-${{ steps.ver.outputs.ver }}-Windows-x64-setup.exe", "MPX-${{ steps.ver.outputs.ver }}-macOS.dmg", "MPX-${{ steps.ver.outputs.ver }}-Linux-amd64.deb", "MPX-${{ steps.ver.outputs.ver }}-Linux-arm64.deb"} {
		if !strings.Contains(table, "/"+f+")") {
			t.Errorf("表里没有链接 %s", f)
		}
	}
	if strings.Contains(table, "mpx-cli_") || strings.Contains(table, "mpx_") {
		t.Error("表里只该链接桌面安装包")
	}
	for _, w := range []string{"仍要运行", "仍要打开", "隐私与安全性"} {
		if !strings.Contains(run, w) {
			t.Errorf("发布说明要写清未签名安装包怎么放行,缺「%s」", w)
		}
	}
	if !strings.Contains(run, "cat DL.md NOTES.md") {
		t.Error("下载表要放在正文最前面")
	}
}
