package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var portBlock = regexp.MustCompile(`(?s)\n# >>> port_preflight.*?# <<< port_preflight\n`)

// runPreflight 把 install.sh 的端口预检那一段抽出来跑;ss 换成一个假脚本,输出 ssOut。
func runPreflight(t *testing.T, ssOut, addr, unitBody string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh 只在 Linux 上跑")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if ssOut != "-" { // "-" = 机器上没有 ss
		if err := os.WriteFile(filepath.Join(dir, "ss.out"), []byte(ssOut), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "ss"), []byte("#!/bin/sh\ncat "+filepath.Join(dir, "ss.out")+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	unit := filepath.Join(dir, "mpx.service")
	if unitBody != "" {
		if err := os.WriteFile(unit, []byte(unitBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	block := portBlock.FindString(readScript(t, "install.sh"))
	if block == "" {
		t.Fatal("install.sh 里找不到 port_preflight 段")
	}
	sh := "set -euo pipefail\ndie() { printf '错误:%s\\n' \"$1\" >&2; exit 1; }\n" + block + "\npreflight_port \"$1\" \"$2\"\necho PREFLIGHT_OK\n"
	cmd := exec.Command("bash", "-c", sh, "port-test", addr, unit)
	// PATH 只留假 ss 和基本工具所在目录(awk/sed/head),不让真的 ss 混进来。
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+toolsPath(t))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// toolsPath 返回 awk、sed、head 所在目录,但去掉真 ss 所在的目录(若在同一目录则建软链目录)。
func toolsPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"awk", "sed", "head", "cat", "bash"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("缺 %s", tool)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestInstallPreflightPortBusy(t *testing.T) {
	ss := `LISTEN 0 511 0.0.0.0:8080 0.0.0.0:* users:(("nginx",pid=1234,fd=6))` + "\n"
	out, err := runPreflight(t, ss, "0.0.0.0:8080", "")
	if err == nil {
		t.Fatalf("8080 被占时应退出: %s", out)
	}
	for _, want := range []string{"端口 8080 已被 nginx(进程 1234) 占用", "还没有装任何东西", "--addr"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺 %q: %s", want, out)
		}
	}
}

func TestInstallPreflightPortFreeOrOurs(t *testing.T) {
	cases := map[string]struct{ ss, addr, unit string }{
		"空着":           {`LISTEN 0 511 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=1,fd=3))` + "\n", "0.0.0.0:8080", ""},
		"mpx 自己(重装)":   {`LISTEN 0 511 0.0.0.0:8080 0.0.0.0:* users:(("mpx",pid=42,fd=6))` + "\n", "0.0.0.0:8080", ""},
		"没有 ss":        {"-", "0.0.0.0:8080", ""},
		"换了端口":         {`LISTEN 0 511 0.0.0.0:8080 0.0.0.0:* users:(("nginx",pid=1234,fd=6))` + "\n", "0.0.0.0:18080", ""},
		"18080 占用但不相关": {`LISTEN 0 511 0.0.0.0:18080 0.0.0.0:* users:(("x",pid=9,fd=6))` + "\n", "", ""},
	}
	for name, c := range cases {
		out, err := runPreflight(t, c.ss, c.addr, c.unit)
		if err != nil || !strings.Contains(out, "PREFLIGHT_OK") {
			t.Errorf("%s: 不该拦: %v %s", name, err, out)
		}
	}
}

func TestInstallPreflightUsesExistingUnitPort(t *testing.T) {
	// 没给 --addr 时按已有单元里的端口检查(升级重装的情况)。
	ss := `LISTEN 0 511 [::]:9090 [::]:* users:(("caddy",pid=77,fd=6))` + "\n"
	unit := "[Service]\nExecStart=/usr/local/bin/mpx serve --addr 0.0.0.0:9090 --db /var/lib/mpx/mpx.db\n"
	out, err := runPreflight(t, ss, "", unit)
	if err == nil || !strings.Contains(out, "端口 9090 已被 caddy(进程 77) 占用") {
		t.Fatalf("应按单元里的 9090 检查: %v %s", err, out)
	}
}
