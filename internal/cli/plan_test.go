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
	"github.com/spf13/cobra"
)

// setupPlanCLI 起一个真控制面(真库 + 真 HTTP),建好一个节点与一个入站,
// 让 mpx plan / mpx user 的名字解析有东西可用。
func setupPlanCLI(t *testing.T) (*store.Store, model.Node, model.Inbound) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	node := &model.Node{Name: "la-1", Address: "la.example.com"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	in := &model.Inbound{
		NodeID: node.ID, Tag: "reality", Protocol: model.ProtocolVLESS,
		Port: 443, Network: model.NetworkTCP, Security: model.SecurityNone,
	}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}

	srv := server.New(st, "test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := httptest.NewServer(srv.Handler())
	t.Cleanup(h.Close)
	t.Setenv("MPX_SERVER", h.URL)
	t.Setenv("MPX_TOKEN", "test-token")
	opts.server, opts.token = "", ""
	opts.output = outputTable
	return st, *node, *in
}

// runPlan 用一个新的 plan 命令实例跑一次,顺带抓标准输出。
// 每次新建是有意的:plan 的 flag 是包级变量,新实例注册时会把它们重置成默认值。
func runPlan(t *testing.T, args ...string) string {
	t.Helper()
	var out string
	var runErr error
	out = captureStdout(t, func() {
		cmd := newPlanCmd()
		cmd.SetArgs(args)
		runErr = cmd.Execute()
	})
	if runErr != nil {
		t.Fatalf("mpx plan %s: %v", strings.Join(args, " "), runErr)
	}
	return out
}

// runPlanErr 同 runPlan,但把错误交给调用方。
func runPlanErr(t *testing.T, args ...string) error {
	t.Helper()
	var runErr error
	_ = captureStdout(t, func() {
		cmd := newPlanCmd()
		cmd.SetArgs(args)
		runErr = cmd.Execute()
	})
	return runErr
}

// resetUserCLIFlags 把 user 各子命令的 flag 值恢复到默认值并清掉 Changed 位。
// pflag 不提供"取消设置",只能直接改字段;user 命令是包级共享的,不还原会让
// 后一次不给 --plan 的调用仍然被当成给过。
func resetUserCLIFlags() {
	cmds := []*cobra.Command{userAddCmd, userEditCmd, userListCmd}
	for _, cmd := range cmds {
		for _, name := range []string{
			"limit", "expire", "reset-strategy", "inbounds", "plan", "note", "disabled",
			"page", "page-size", "status", "search", "inbound",
		} {
			f := cmd.Flags().Lookup(name)
			if f == nil {
				continue
			}
			f.Changed = false
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	}
}

// runUser 重置 user 的 flag 后跑一次完整命令,返回标准输出。
func runUser(t *testing.T, args ...string) string {
	t.Helper()
	resetUserCLIFlags()
	t.Cleanup(resetUserCLIFlags)
	var out string
	out = captureStdout(t, func() { runRoot(t, append([]string{"user"}, args...)...) })
	return out
}

func TestPlanCLI(t *testing.T) {
	st, _, in := setupPlanCLI(t)

	runPlan(t, "add", "基础", "--node", "la-1", "--inbound", "la-1/reality", "--note", "入门档")
	list := runPlan(t, "list")
	for _, want := range []string{"基础", "la-1", "la-1/reality"} {
		if !strings.Contains(list, want) {
			t.Fatalf("plan list 缺 %q:\n%s", want, list)
		}
	}

	show := runPlan(t, "show", "基础")
	for _, want := range []string{"套餐 #", "基础", "la-1", "reality", "用户:0 个", "入门档"} {
		if !strings.Contains(show, want) {
			t.Fatalf("plan show 缺 %q:\n%s", want, show)
		}
	}

	// edit:换成另一个节点、去掉单独勾选的入站、改名。去掉 la-1 这个节点本身是
	// 必要的 —— 只 rm-inbound 的话,la-1 还在 NodeIDs 里,它的入站仍会通过节点
	// 展开被授权。
	jp := &model.Node{Name: "jp-1", Address: "jp.example.com"}
	if err := st.CreateNode(jp); err != nil {
		t.Fatal(err)
	}
	runPlan(t, "edit", "基础", "--add-node", "jp-1", "--rm-node", "la-1", "--rm-inbound", "la-1/reality", "--name", "基础Pro")
	show = runPlan(t, "show", "基础Pro")
	if !strings.Contains(show, "jp-1") || strings.Contains(show, "reality") {
		t.Fatalf("plan edit 没生效:\n%s", show)
	}
	list = runPlan(t, "list")
	if !strings.Contains(list, "基础Pro") {
		t.Fatalf("改名没反映到列表:\n%s", list)
	}

	// suggest:一个没挂套餐、限定了入站的用户。
	if err := st.CreateUser(&model.User{Username: "alice", Enabled: true, InboundIDs: []int64{in.ID}}); err != nil {
		t.Fatal(err)
	}
	out := runPlan(t, "suggest")
	if !strings.Contains(out, "整理") || !strings.Contains(out, "默认只打印建议") {
		t.Fatalf("plan suggest 输出:\n%s", out)
	}
	// 默认只看不写:还没有「整理」套餐。
	if list := runPlan(t, "list"); strings.Contains(list, "整理") {
		t.Fatalf("suggest 默认不该写入:\n%s", list)
	}
	runPlan(t, "suggest", "--apply")
	list = runPlan(t, "list")
	if !strings.Contains(list, "整理") {
		t.Fatalf("suggest --apply 没建套餐:\n%s", list)
	}
	alice, err := st.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if alice.PlanID == nil {
		t.Fatal("suggest --apply 没把用户挂上套餐")
	}

	// rm:没有用户的套餐直接删。
	if err := runPlanErr(t, "rm", "基础Pro"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if list := runPlan(t, "list"); strings.Contains(list, "基础Pro") {
		t.Fatalf("rm 没删掉:\n%s", list)
	}

	// rm --reassign:有用户的套餐先改挂再删。
	runPlan(t, "add", "临时")
	runPlan(t, "add", "落点")
	temp := planIDByName(t, st, "临时")
	dest := planIDByName(t, st, "落点")
	if _, err := st.SetUserPlan("alice", &temp); err != nil {
		t.Fatal(err)
	}
	runPlan(t, "rm", "临时", "--reassign", "落点")
	alice, err = st.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if alice.PlanID == nil || *alice.PlanID != dest {
		t.Fatalf("rm --reassign 没改挂: %+v", alice.PlanID)
	}
}

// planIDByName 按名字找一个套餐 id。
func planIDByName(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	plans, err := st.ListPlans()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plans {
		if p.Name == name {
			return p.ID
		}
	}
	t.Fatalf("没有叫 %q 的套餐", name)
	return 0
}

func TestUserPlanCLI(t *testing.T) {
	st, _, _ := setupPlanCLI(t)
	runPlan(t, "add", "基础")
	runPlan(t, "add", "全球")

	// add --plan:按名字解析并挂上。
	runUser(t, "add", "bob", "--plan", "基础")
	bob, err := st.GetUser("bob")
	if err != nil {
		t.Fatal(err)
	}
	if bob.PlanID == nil {
		t.Fatal("user add --plan 没挂上套餐")
	}
	basic, err := st.GetPlan(*bob.PlanID)
	if err != nil || basic.Name != "基础" {
		t.Fatalf("bob 挂的套餐 = %+v, err=%v", basic, err)
	}

	// edit --plan:改挂。
	runUser(t, "edit", "bob", "--plan", "全球")
	bob, _ = st.GetUser("bob")
	global, err := st.GetPlan(*bob.PlanID)
	if err != nil || global.Name != "全球" {
		t.Fatalf("user edit --plan 没改挂: %+v, err=%v", global, err)
	}

	// edit --plan "":摘掉。
	runUser(t, "edit", "bob", "--plan", "")
	bob, _ = st.GetUser("bob")
	if bob.PlanID != nil {
		t.Fatalf("--plan \"\" 没摘掉: %+v", bob.PlanID)
	}

	// list --plan:只看挂该套餐的用户。
	runUser(t, "add", "carol", "--plan", "全球")
	runUser(t, "add", "dave") // 默认套餐
	out := runUser(t, "list", "--plan", "全球")
	if !strings.Contains(out, "carol") || strings.Contains(out, "dave") {
		t.Fatalf("user list --plan 筛选不对:\n%s", out)
	}
	if !strings.Contains(out, "全球") {
		t.Fatalf("user list 没显示套餐名:\n%s", out)
	}
}
