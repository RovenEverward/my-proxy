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
