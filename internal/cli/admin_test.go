package cli

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/server"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// adminEnv 起一个真控制面(httptest),把 MPX_SERVER/MPX_TOKEN 指过去。
// 用真 server 而不是手写应答:命令要验的是「调对了接口+打印对了」,服务端
// 行为本身由 server 包自己的测试兜着。
type adminEnv struct {
	srv *httptest.Server
	st  *store.Store
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/mpx.db")
	if err != nil {
		t.Fatalf("开测试库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := server.New(st, "master-token", nil)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "master-token")
	return &adminEnv{srv: srv, st: st}
}

func resetAdminFlags() {
	adminCreateFlags.username, adminCreateFlags.password, adminCreateFlags.passwordStdin = "", "", false
	adminPasswordFlags.oldPassword, adminPasswordFlags.passwordStdin = "", false
	for _, f := range []*cobra.Command{adminCreateCmd, adminPasswordCmd} {
		for _, name := range []string{"username", "password", "password-stdin", "old-password"} {
			if fl := f.Flags().Lookup(name); fl != nil {
				fl.Changed = false
			}
		}
	}
}

func TestAdminCreateAndPassword(t *testing.T) {
	newAdminEnv(t)
	t.Cleanup(resetAdminFlags)

	// 建号:--password 走 flag,不碰 stdin。
	out := captureStdout(t, func() {
		runRoot(t, "admin", "create", "--username", "ops", "--password", "correct horse battery")
	})
	if !strings.Contains(out, "管理员账号已建立") {
		t.Fatalf("建号输出不对: %s", out)
	}

	// 再跑一遍:已有账号,报「改名」而不是「建号」。
	out = captureStdout(t, func() {
		runRoot(t, "admin", "create", "--username", "admin", "--password", "whatever123456")
	})
	if !strings.Contains(out, "用户名已改为") {
		t.Fatalf("改名输出不对: %s", out)
	}

	// 改密码:stdin 喂新密码,--old-password 给旧密码。
	feedStdin(t, "new horse battery\n")
	out = captureStdout(t, func() {
		runRoot(t, "admin", "password", "--old-password", "correct horse battery", "--password-stdin")
	})
	if !strings.Contains(out, "管理员密码已更新") || !strings.Contains(out, "重新登录") {
		t.Fatalf("改密码输出不对: %s", out)
	}
}
