package release

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestCheck(t *testing.T) {
	archive := []byte("tarball")
	sums := []byte(sum(archive) + "  mpx_linux_amd64.tar.gz\n" + sum([]byte("x")) + " *mpx_windows_amd64.zip\n")
	if err := Check(sums, "mpx_linux_amd64.tar.gz", archive); err != nil {
		t.Fatalf("正常发布件应通过: %v", err)
	}
	if err := Check(sums, "mpx_windows_amd64.zip", []byte("x")); err != nil {
		t.Fatalf("二进制模式(*name)也要认: %v", err)
	}
	if Check(sums, "mpx_linux_amd64.tar.gz", []byte("tarbalL")) == nil {
		t.Error("发布件被改:应当拒绝")
	}
	if Check(sums, "mpx_darwin_arm64.tar.gz", archive) == nil {
		t.Error("清单里没有:应当拒绝")
	}
	if Check([]byte("zz  mpx_linux_amd64.tar.gz\n"), "mpx_linux_amd64.tar.gz", archive) == nil {
		t.Error("哈希格式不对:应当拒绝")
	}
}

var block = regexp.MustCompile(`(?s)\n# >>> verify_release.*?# <<< verify_release\n`)

func readScript(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestScriptsShareVerify:两个安装脚本的校验函数逐字相同。
func TestScriptsShareVerify(t *testing.T) {
	a, b := block.FindString(readScript(t, "install.sh")), block.FindString(readScript(t, "install-cli.sh"))
	if a == "" || a != b {
		t.Fatal("install.sh 与 install-cli.sh 的 verify_release 段不一致(或找不到)")
	}
}

// TestScriptVerify 把脚本里那段 verify_release 原样拿出来,对着 file:// 的模拟发布目录跑。
func TestScriptVerify(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("没有 curl")
	}
	dir := t.TempDir()
	rel := filepath.Join(dir, "releases")
	mk := func(ver string, withSums bool) string {
		d := filepath.Join(rel, "download", ver)
		os.MkdirAll(d, 0o755)
		body := []byte("archive-" + ver)
		os.WriteFile(filepath.Join(d, "a.tar.gz"), body, 0o644)
		if withSums {
			os.WriteFile(filepath.Join(d, "SHA256SUMS"), []byte(sum(body)+"  a.tar.gz\n"), 0o644)
		}
		return filepath.Join(d, "a.tar.gz")
	}
	old, good := mk("v0", false), mk("v1", true)
	bad := filepath.Join(dir, "bad.tar.gz")
	os.WriteFile(bad, []byte("archive-v2"), 0o644)
	fn := block.FindString(readScript(t, "install-cli.sh"))

	cases := []struct {
		name, ver, file, want string
		ok                    bool
	}{
		{"哈希对", "v1", good, "sha256 ✓", true},
		{"发布件被换", "v1", bad, "不符", false},
		{"旧版本没有清单:提示后照常安装", "v0", old, "还没有 SHA256SUMS", true},
	}
	for _, c := range cases {
		sh := `set -euo pipefail
die() { printf '错误:%s\n' "$1" >&2; exit 1; }
note() { printf '  %s\n' "$1"; }
FETCH="curl -fsSL"; DOWNLOAD_BASE="file://` + rel + `"; VERSION="` + c.ver + `"; TMP="$(mktemp -d)"
` + fn + `
verify_release "` + c.file + `" a.tar.gz && echo PASSED`
		out, err := exec.Command("bash", "-c", sh).CombinedOutput()
		if (err == nil) != c.ok || !strings.Contains(string(out), c.want) {
			t.Errorf("%s:err=%v 输出=%s", c.name, err, out)
		}
	}
}
