package cli

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/server"
	"github.com/RovenEverward/my-proxy/internal/store"
)

func TestRuleAndGroupCLI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateUser(&model.User{Username: "alice", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	srv := server.New(st, "test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := httptest.NewServer(srv.Handler())
	t.Cleanup(h.Close)
	t.Setenv("MPX_SERVER", h.URL)
	t.Setenv("MPX_TOKEN", "test-token")
	opts.server, opts.token = "", ""

	run := func(c func() any, args ...string) string {
		t.Helper()
		var out string
		var runErr error
		out = captureStdout(t, func() {
			cmd := c().(interface {
				SetArgs([]string)
				Execute() error
			})
			cmd.SetArgs(args)
			runErr = cmd.Execute()
		})
		if runErr != nil {
			t.Fatalf("%v: %v", args, runErr)
		}
		return out
	}
	rule := func() any { return newRuleCmd() }
	group := func() any { return newGroupCmd() }

	run(group, "add", "日本", "--region", "JP", "--mode", "manual")
	if out := run(group, "list"); !strings.Contains(out, "地区 JP") || !strings.Contains(out, "manual") {
		t.Fatal(out)
	}
	run(group, "edit", "日本", "--mode", "auto", "--protocol", "Reality")
	run(rule, "add", "suffix", "example.jp", "日本")
	run(rule, "add", "keyword", "ads", "拦截", "--at", "99")
	run(rule, "add", "set", "ai", "DIRECT", "--at", "2")
	run(rule, "move", "3", "1")
	run(rule, "toggle", "3")
	run(rule, "final", "日本")
	run(rule, "--user", "alice", "add", "keyword", "bili", "直连")
	out := run(rule, "list", "--user", "alice")
	for _, want := range []string{"alice", "bili", "ads", "AI 服务 (ai)", "停用", "其他全部 → 日本"} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺 %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "ads") > strings.Index(out, "example.jp") {
		t.Fatalf("move 没生效:\n%s", out)
	}
	if out := run(rule, "test", "www.example.jp"); !strings.Contains(out, "去向:日本") {
		t.Fatal(out)
	}
	run(rule, "rm", "1")
	if out := run(rule, "list"); strings.Contains(out, "ads") {
		t.Fatal(out)
	}
	if out := run(rule, "catalog"); !strings.Contains(out, "telegram") {
		t.Fatal(out)
	}
	cmd := newGroupCmd()
	cmd.SetArgs([]string{"rm", "日本"})
	captureStdout(t, func() {
		if cmd.Execute() == nil {
			t.Error("被规则引用的策略组应删不掉")
		}
	})
	cmd = newRuleCmd()
	cmd.SetArgs([]string{"add", "suffix", "x.com", "不存在"})
	captureStdout(t, func() {
		if cmd.Execute() == nil {
			t.Error("未知去向应报错")
		}
	})
}
