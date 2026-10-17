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

// addrBlock 是 install.sh 里「决定监听地址」那一段,unitBlock 是装单元的那一段,
// 都原样抽出来对着 MPX_PREFIX 下的临时目录跑。
var (
	addrBlock = regexp.MustCompile(`(?s)\n# >>> resolve_addr.*?# <<< resolve_addr\n`)
	unitBlock = regexp.MustCompile(`(?s)\n# >>> install_unit.*?# <<< install_unit\n`)
)

// writeExistingUnit 在 PREFIX 下放一份旧的 mpx.service;addr 是 ExecStart 里 --addr
// 那段原文(`--addr 0.0.0.0:8080`、`--addr=0.0.0.0:8080` 都行),空串表示不带 --addr。
func writeExistingUnit(t *testing.T, prefix, addr string) {
	t.Helper()
	dir := filepath.Join(prefix, "etc/systemd/system")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	execStart := "/usr/local/bin/mpx serve"
	if addr != "" {
		execStart += " " + addr
	}
	execStart += " --db /var/lib/mpx/mpx.db"
	body := "[Unit]\nDescription=mpx\n\n[Service]\nExecStart=" + execStart + "\n\n[Install]\nWantedBy=multi-user.target\n"
	if err := os.WriteFile(filepath.Join(dir, "mpx.service"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runInstallUnit 模拟一次升级里的装单元:先按参数(可能为空)走 resolve_addr,再装
// mpx.service,返回最终写到目标位置的单元内容和这两步的输出。
func runInstallUnit(t *testing.T, prefix, given string) (string, string) {
	t.Helper()
	deploy, err := filepath.Abs(filepath.Join("..", "..", "deploy"))
	if err != nil {
		t.Fatal(err)
	}
	sh := `set -euo pipefail
die() { printf '错误:%s\n' "$1" >&2; exit 1; }
note() { printf '    %s\n' "$1"; }
PREFIX="${MPX_PREFIX:-}"
SCRIPT_DIR="` + deploy + `"
install -d -m 0755 "$PREFIX/etc/systemd/system"
FETCH=
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
` + addrBlock.FindString(readScript(t, "install.sh")) + `
` + unitBlock.FindString(readScript(t, "install.sh")) + `
resolve_addr "$1" "$PREFIX/etc/systemd/system/mpx.service"
install_unit mpx.service "${RESOLVED_ADDR:-}"
`
	cmd := exec.Command("bash", "-c", sh, "install-addr-test", given)
	cmd.Env = append(os.Environ(), "MPX_PREFIX="+prefix)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("装单元失败: %v\n%s", err, out)
	}
	unit, err := os.ReadFile(filepath.Join(prefix, "etc/systemd/system/mpx.service"))
	if err != nil {
		t.Fatal(err)
	}
	return string(unit), string(out)
}

// addrInUnit 从单元文本里取 --addr 的值(`--addr 值`、`--addr=值` 都认)。
func addrInUnit(t *testing.T, unit string) string {
	t.Helper()
	m := regexp.MustCompile(`--addr[= ](\S+)`).FindStringSubmatch(unit)
	if m == nil {
		t.Fatalf("单元里没有 --addr:\n%s", unit)
	}
	return m[1]
}

// TestInstallUnitResolveAddr:升级补跑 install.sh 时,mpx.service 的监听地址要么按
// 用户给的,要么沿用现有单元的,不能默默盖回仓库默认的回环。
func TestInstallUnitResolveAddr(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("install.sh 只支持 Linux")
	}
	cases := []struct {
		name string
		// hasOld:目标位置有没有旧单元;oldAddr:旧单元 ExecStart 里 --addr 那段原文
		//(空串 = 旧单元不带 --addr);given:这次传的 --addr(空 = 没传)。
		hasOld               bool
		oldAddr, given       string
		want                 string
		wantNote, forbidNote string
	}{
		{
			name:   "已有单元 0.0.0.0、没传 --addr:沿用",
			hasOld: true, oldAddr: "--addr 0.0.0.0:8080", given: "",
			want:     "0.0.0.0:8080",
			wantNote: "沿用现有 mpx.service 的监听地址 0.0.0.0:8080",
		},
		{
			name:   "已有单元、传了 --addr:以传的为准",
			hasOld: true, oldAddr: "--addr 0.0.0.0:8080", given: "127.0.0.1:9000",
			want:       "127.0.0.1:9000",
			forbidNote: "沿用",
		},
		{
			name:   "没有旧单元、没传 --addr:仓库默认",
			hasOld: false, oldAddr: "", given: "",
			want:       "127.0.0.1:8080",
			forbidNote: "沿用",
		},
		{
			name:   "旧单元写的是 --addr=值:也认",
			hasOld: true, oldAddr: "--addr=0.0.0.0:8080", given: "",
			want:     "0.0.0.0:8080",
			wantNote: "沿用现有 mpx.service 的监听地址 0.0.0.0:8080",
		},
		{
			name:   "旧单元读不出 --addr:按默认装并说明",
			hasOld: true, oldAddr: "", given: "",
			want:     "127.0.0.1:8080",
			wantNote: "读不出现有 mpx.service 的监听地址",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefix := t.TempDir()
			if c.hasOld {
				writeExistingUnit(t, prefix, c.oldAddr)
			}
			unit, out := runInstallUnit(t, prefix, c.given)
			if got := addrInUnit(t, unit); got != c.want {
				t.Fatalf("新单元的监听地址 = %s,想要 %s\n%s", got, c.want, unit)
			}
			if c.wantNote != "" && !strings.Contains(out, c.wantNote) {
				t.Fatalf("输出里没有「%s」:\n%s", c.wantNote, out)
			}
			if c.forbidNote != "" && strings.Contains(out, c.forbidNote) {
				t.Fatalf("不该出现「%s」的输出却出现了:\n%s", c.forbidNote, out)
			}
		})
	}
}

// TestInstallUpdateAddr:mpx-update.service 健康检查的地址跟着 mpx.service 最终
// 的监听地址走 —— 包括补跑时没传 --addr、沿用旧单元的情况(以前这时固定连
// 127.0.0.1:8080,端口不是 8080 就检查失败)。
func TestInstallUpdateAddr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh 只在 Linux 上跑")
	}
	deploy, err := filepath.Abs(filepath.Join("..", "..", "deploy"))
	if err != nil {
		t.Fatal(err)
	}
	script := readScript(t, "install.sh")
	cases := []struct{ name, given, existing, want string }{
		{"默认", "", "", "127.0.0.1:8080"},
		{"传了通配地址", "0.0.0.0:9090", "", "127.0.0.1:9090"},
		{"沿用旧单元的非 8080 端口", "", "--addr 0.0.0.0:9443", "127.0.0.1:9443"},
		{"沿用旧单元的 = 写法", "", "--addr=0.0.0.0:7000", "127.0.0.1:7000"},
		{"IPv6 通配", "[::]:8443", "", "127.0.0.1:8443"},
		{"绑具体地址", "10.0.0.5:8080", "", "10.0.0.5:8080"},
		{"绑 IPv6 回环", "[::1]:8081", "", "[::1]:8081"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefix := t.TempDir()
			if c.existing != "" {
				writeExistingUnit(t, prefix, c.existing)
			}
			sh := `set -euo pipefail
die() { printf '错误:%s\n' "$1" >&2; exit 1; }
note() { printf '    %s\n' "$1"; }
PREFIX="${MPX_PREFIX:-}"
SCRIPT_DIR="` + deploy + `"
install -d -m 0755 "$PREFIX/etc/systemd/system"
FETCH=
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
` + addrBlock.FindString(script) + `
` + unitBlock.FindString(script) + `
resolve_addr "$1" "$PREFIX/etc/systemd/system/mpx.service"
update_addr "${RESOLVED_ADDR:-}"
install_unit mpx-update.service "$UPDATE_ADDR"
`
			cmd := exec.Command("bash", "-c", sh, "update-addr-test", c.given)
			cmd.Env = append(os.Environ(), "MPX_PREFIX="+prefix)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("装单元失败: %v\n%s", err, out)
			}
			unit, err := os.ReadFile(filepath.Join(prefix, "etc/systemd/system/mpx-update.service"))
			if err != nil {
				t.Fatal(err)
			}
			if got := addrInUnit(t, string(unit)); got != c.want {
				t.Fatalf("健康检查地址 = %s,想要 %s\n%s", got, c.want, out)
			}
		})
	}
}

// TestInstallArgs:带值参数缺值要明说(以前 `shift 2` 在 set -e 下静默退出),
// 并认 --addr=值 / --version=值 写法。只跑到参数解析为止(非 root 会停在 root 检查)。
func TestInstallArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh 只在 Linux 上跑")
	}
	if os.Geteuid() == 0 {
		t.Skip("以 root 跑会真的往下装")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "deploy", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--addr"}, "--addr 后面要跟一个值"},
		{[]string{"--version"}, "--version 后面要跟一个值"},
		{[]string{"--addr", "--version", "v0.9.1"}, "--addr 后面要跟一个值"},
		{[]string{"--addr="}, "--addr 后面要跟一个值"},
		{[]string{"--addr=0.0.0.0:9090"}, "要 root"},
		{[]string{"--version=v0.9.1", "--addr", "0.0.0.0:8080"}, "要 root"},
	}
	for _, c := range cases {
		out, err := exec.Command("bash", append([]string{script}, c.args...)...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), c.want) {
			t.Errorf("%v:err=%v 输出=%s", c.args, err, out)
		}
	}
}

// TestInstallCLIArgs:install-cli.sh 的带值参数缺值同样要明说。
func TestInstallCLIArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install-cli.sh 不在 Windows 上跑")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "deploy", "install-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--version"}, {"--prefix="}, {"--from", "--version", "v1"}} {
		out, err := exec.Command("bash", append([]string{script}, args...)...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "后面要跟一个值") {
			t.Errorf("%v:err=%v 输出=%s", args, err, out)
		}
	}
}
