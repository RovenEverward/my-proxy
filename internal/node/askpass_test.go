package node

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteAskpassKeepsPasswordOutOfScript(t *testing.T) {
	dir := t.TempDir()
	pw := "s3cret'\"and`$meta\nline"
	if err := writeAskpass(dir, pw); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(dir, "askpass.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), pw) {
		t.Fatal("密码被写进了 askpass 脚本正文")
	}
	if strings.Contains(string(script), "MPX_ASKPASS_PASSWORD") {
		t.Fatal("脚本仍在读环境变量口令")
	}
	got, err := os.ReadFile(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != pw {
		t.Fatalf("密码文件内容不对: %q", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "password"))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("密码文件权限过宽: %o", perm)
		}
	}
}
