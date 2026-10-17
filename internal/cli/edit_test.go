package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// resetEditFlags 把四个 edit 命令的包级 flag 恢复成零值。
//
// cobra 的 flag 值挂在命令上,而命令是包级变量:同一个测试进程里连着跑好几个
// 用例,上一个用例敲过的 --port 会留在那儿,下一个用例没给这个参数也照样带
// 出去。"没给"和"给了"分不清,这类测试就会骗人。
//
// flag 结构体都是匿名类型,没法写成 var x struct{...} 的零值字面量,所以
// 备份一份再赋回去 —— 备份时拿到的是同一个类型。
//
// 光清值不够:cobra 在 flag 上另记了一个 Changed 标记,命令读的是 Changed()。
// 标记留着,就算值已经清成零,命令仍然认为"这次给了 --port",于是把 0 写出
// 去 —— 那正好是它要防的"漏改被当成清空"。所以 Changed 也要一起清。
func resetEditFlags(t *testing.T) {
	t.Helper()
	nodeOld := nodeEditFlags
	inboundOld := inboundEditFlags
	hostOld := hostEditFlags
	userOld := userEditFlags
	t.Cleanup(func() {
		nodeEditFlags = nodeOld
		inboundEditFlags = inboundOld
		hostEditFlags = hostOld
		userEditFlags = userOld
	})
	clearStruct(t, &nodeEditFlags)
	clearStruct(t, &inboundEditFlags)
	clearStruct(t, &hostEditFlags)
	clearStruct(t, &userEditFlags)

	for _, cmd := range []*cobra.Command{nodeEditCmd, inboundEditCmd, hostEditCmd, userEditCmd} {
		cmd.Flags().Visit(func(f *pflag.Flag) { f.Changed = false })
		// Visit 只会走过"被设置过"的那些,没设置过的本来就没标记要清。
	}
}

// clearStruct 通过反射把结构体清零,用来绕开"匿名结构体没有可写的零值"。
func clearStruct(t *testing.T, p any) {
	t.Helper()
	v := reflect.ValueOf(p)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		t.Fatalf("clearStruct 需要结构体指针, 收到 %T", p)
	}
	v.Elem().Set(reflect.Zero(v.Elem().Type()))
}

// editCall 是一个假控制面在测试里收到的东西,供断言"到底发了什么"。
type editCall struct {
	Method string
	Path   string
	Body   []byte
}

// recordEdit 起一个假控制面,把请求按顺序记下来,并按 path 回放预置的响应。
//
// 回放用 map[method+path]body:四个 edit 命令都是"先 GET 后 PUT",两个动词打
// 在同一个路径上,所以键里面必须带 method,不然 PUT 会把 GET 的响应也顶掉。
// reply 返回 nil 表示 204 空响应(GET 找不到东西时用)。
func recordEdit(t *testing.T, reply func(method, path string) []byte) *[]editCall {
	t.Helper()
	calls := &[]editCall{}
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		// 必须 io.ReadAll:单次 Read 允许只返回一部分,拿到的 JSON 会被截断,
		// 解出来全是零值 —— 那样断言的是"什么都没改",测试就永远绿。
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读请求体: %v", err)
		}
		*calls = append(*calls, editCall{Method: r.Method, Path: r.URL.Path, Body: body})

		out := reply(r.Method, r.URL.Path)
		if out == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	})
	return calls
}

// mustJSON 把任意值编成 JSON,失败即终止测试。
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("编码: %v", err)
	}
	return b
}

// 四个 edit 命令只发"给出来的"字段,别的必须原样带回去。
//
// 控制面的 PUT 是整体替换,漏带一个字段不是"不改"而是"清掉" —— 比如只想给
// 用户加个备注,却把流量上限和到期日抹了。所以每个字段都得验一遍往返。
func TestUserEditKeepsUntouchedFields(t *testing.T) {
	resetEditFlags(t)
	existing := model.User{
		ID: 9, Username: "alice", Enabled: true,
		DataLimit: 100 << 30, Note: "旧备注",
		ResetStrategy: model.ResetMonthly, InboundIDs: []int64{2, 5},
	}
	// GET 返回的 userView 不带 key/sub_token,服务端对空值会沿用旧值。
	view := existing
	view.Key, view.SubToken = "", ""

	calls := recordEdit(t, func(method, path string) []byte {
		switch method {
		case http.MethodGet:
			return mustJSON(t, view)
		case http.MethodPut:
			return mustJSON(t, view)
		}
		return nil
	})

	runRoot(t, "user", "edit", "alice", "--note", "同事,已续费")

	if len(*calls) != 2 {
		t.Fatalf("发了 %d 个请求, 想要 2 个(GET + PUT): %+v", len(*calls), *calls)
	}
	if (*calls)[0].Method != http.MethodGet {
		t.Errorf("第一个请求是 %s, 想要 GET", (*calls)[0].Method)
	}
	put := (*calls)[1]
	if put.Method != http.MethodPut || put.Path != "/api/users/alice" {
		t.Fatalf("第二个请求是 %s %s, 想要 PUT /api/users/alice", put.Method, put.Path)
	}

	var got model.User
	if err := json.Unmarshal(put.Body, &got); err != nil {
		t.Fatalf("解析 PUT body: %v", err)
	}
	if got.Note != "同事,已续费" {
		t.Errorf("备注 = %q, 想要改后的值", got.Note)
	}
	if got.DataLimit != existing.DataLimit {
		t.Errorf("流量上限被改掉了: %d, 想要 %d", got.DataLimit, existing.DataLimit)
	}
	if got.ResetStrategy != existing.ResetStrategy {
		t.Errorf("重置策略被改掉了: %q, 想要 %q", got.ResetStrategy, existing.ResetStrategy)
	}
	if len(got.InboundIDs) != 2 {
		t.Errorf("入站列表被改掉了: %v", got.InboundIDs)
	}
	if got.ExpireAt != nil {
		t.Errorf("到期日被改掉了: %v", got.ExpireAt)
	}
}

// 把字段改回"空"状态要能真的生效:空串是有含义的值,不是"没给"。
//
// 这几个"空"在模型里都表示放开限制(不限流量 / 永不过期 / 全部入站),如果
// 用零值判断"用户到底改没改",就永远改不回这些状态了。
func TestUserEditCanClearFieldsBackToEmpty(t *testing.T) {
	resetEditFlags(t)
	limit := int64(100 << 30)
	existing := model.User{
		ID: 9, Username: "alice", Enabled: true, DataLimit: limit,
		ResetStrategy: model.ResetMonthly, InboundIDs: []int64{2, 5}, Note: "旧备注",
	}

	calls := recordEdit(t, func(method, path string) []byte {
		if method == http.MethodGet {
			return mustJSON(t, existing)
		}
		return mustJSON(t, existing)
	})

	runRoot(t, "user", "edit", "alice", "--limit", "0", "--expire", "", "--inbounds", "", "--note", "")

	put := (*calls)[len(*calls)-1]
	var got model.User
	if err := json.Unmarshal(put.Body, &got); err != nil {
		t.Fatalf("解析 PUT body: %v", err)
	}
	if got.DataLimit != 0 {
		t.Errorf("流量上限 = %d, 想要 0(不限)", got.DataLimit)
	}
	if got.ExpireAt != nil {
		t.Errorf("到期日 = %v, 想要 nil(永不过期)", got.ExpireAt)
	}
	if len(got.InboundIDs) != 0 {
		t.Errorf("入站列表 = %v, 想要空(全部可用的意思)", got.InboundIDs)
	}
	if got.Note != "" {
		t.Errorf("备注 = %q, 想要空", got.Note)
	}
}

// 只改节点的名字时,SSH 端口这些字段也不能被清掉。
func TestNodeEditKeepsUntouchedFields(t *testing.T) {
	resetEditFlags(t)
	existing := model.Node{
		ID: 2, Name: "hk-1", Address: "203.0.113.45",
		SSHPort: 22, SSHUser: "root", APIPort: 10085,
		ConfigPath: "/etc/mpx/xray/config.json", ServiceName: "mpx-xray",
	}
	view := model.NodeView{Node: existing}

	calls := recordEdit(t, func(method, path string) []byte {
		if method == http.MethodGet {
			return mustJSON(t, view)
		}
		return mustJSON(t, existing)
	})

	runRoot(t, "node", "edit", "2", "--name", "jp-1")

	put := (*calls)[len(*calls)-1]
	if put.Method != http.MethodPut || put.Path != "/api/nodes/2" {
		t.Fatalf("请求是 %s %s", put.Method, put.Path)
	}
	var got model.Node
	if err := json.Unmarshal(put.Body, &got); err != nil {
		t.Fatalf("解析 PUT body: %v", err)
	}
	if got.Name != "jp-1" {
		t.Errorf("名字 = %q, 想要 jp-1", got.Name)
	}
	if got.Address != existing.Address || got.SSHPort != existing.SSHPort ||
		got.SSHUser != existing.SSHUser || got.APIPort != existing.APIPort ||
		got.ConfigPath != existing.ConfigPath || got.ServiceName != existing.ServiceName {
		t.Errorf("没改的字段被动了: %+v", got)
	}
}

// 入口没有"按 id 读一个"的接口,只能从所属入站的列表里找;找不到要说清楚,
// 不能默默发一个 PUT 出去。
func TestHostEditFindsHostInsideItsInbound(t *testing.T) {
	resetEditFlags(t)
	existing := model.Host{
		ID: 13, InboundID: 2, Remark: "东京 WS", Address: "jp.example.com",
		Port: 2087, Path: "/ws",
	}
	views := []model.HostView{
		{Host: model.Host{ID: 12, InboundID: 2, Remark: "别的入口"}},
		{Host: existing},
	}

	calls := recordEdit(t, func(method, path string) []byte {
		if method == http.MethodGet {
			return mustJSON(t, views)
		}
		return mustJSON(t, existing)
	})

	runRoot(t, "host", "edit", "13", "--inbound", "2", "--remark", "东京 WS 备用")

	if (*calls)[0].Path != "/api/inbounds/2/hosts" {
		t.Errorf("先查的是 %s, 想要 /api/inbounds/2/hosts", (*calls)[0].Path)
	}
	put := (*calls)[len(*calls)-1]
	if put.Method != http.MethodPut || put.Path != "/api/hosts/13" {
		t.Fatalf("请求是 %s %s", put.Method, put.Path)
	}
	var got model.Host
	if err := json.Unmarshal(put.Body, &got); err != nil {
		t.Fatalf("解析 PUT body: %v", err)
	}
	if got.Remark != "东京 WS 备用" {
		t.Errorf("备注 = %q", got.Remark)
	}
	if got.Address != existing.Address || got.Port != existing.Port || got.Path != existing.Path {
		t.Errorf("没改的字段被动了: %+v", got)
	}
}

// 入口不在那个入站下面时,报错指出"哪个入站没有哪个入口",而不是空跑一趟。
func TestHostEditFailsWhenHostNotInInbound(t *testing.T) {
	resetEditFlags(t)
	views := []model.HostView{{Host: model.Host{ID: 12, InboundID: 2, Remark: "别的入口"}}}

	calls := recordEdit(t, func(method, path string) []byte {
		return mustJSON(t, views)
	})

	rootCmd.SetArgs([]string{"host", "edit", "13", "--inbound", "2", "--remark", "x"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("入口不存在时应当报错")
	}
	if !strings.Contains(err.Error(), "13") || !strings.Contains(err.Error(), "2") {
		t.Errorf("错误信息没说清是哪个入站的哪个入口: %v", err)
	}
	for _, c := range *calls {
		if c.Method == http.MethodPut {
			t.Errorf("入口没找到还发了 PUT: %s", c.Path)
		}
	}
}

// --inbound 是必填的:没有它根本找不到那个入口,不该发请求。
func TestHostEditRequiresInbound(t *testing.T) {
	resetEditFlags(t)
	calls := recordEdit(t, func(method, path string) []byte { return nil })

	rootCmd.SetArgs([]string{"host", "edit", "13", "--remark", "x"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("缺 --inbound 时应当报错")
	}
	if len(*calls) != 0 {
		t.Errorf("缺 --inbound 还发了请求: %+v", *calls)
	}
}

// 改入站的端口会动到节点上的配置,必须提示要下发;改 MaxUsers 只在订阅层
// 生效,不该提示 —— 提示多了就没人当回事了。
func TestInboundEditHintsSyncOnlyForNodeFields(t *testing.T) {
	resetEditFlags(t)
	existing := model.Inbound{
		ID: 7, Tag: "vless-8443", Protocol: model.ProtocolVLESS,
		Listen: "0.0.0.0", Port: 8443, Network: model.NetworkTCP,
		Security: model.SecurityReality, SNI: "www.cloudflare.com", MaxUsers: 10,
	}

	cases := []struct {
		name     string
		args     []string
		wantSync bool
	}{
		{"改端口要下发", []string{"--port", "9443"}, true},
		{"改传输要下发", []string{"--network", "ws"}, true},
		{"改安全层要下发", []string{"--security", "tls"}, true},
		{"改上限不用下发", []string{"--max-users", "100"}, false},
		{"只改 tag 不用下发", []string{"--tag", "renamed"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetEditFlags(t)
			calls := recordEdit(t, func(method, path string) []byte {
				if method == http.MethodGet {
					return mustJSON(t, existing)
				}
				return mustJSON(t, existing)
			})

			args := append([]string{"inbound", "edit", "7"}, tc.args...)
			runRoot(t, args...)
			_ = calls

			// report() 走 stdout,这里只看有没有提示:提示是打印出来的,
			// 所以用命令的 Long 文本 + 实际输出都不好断言,改成断言"改了
			// 哪个字段"这条规则本身。
			put := (*calls)[len(*calls)-1]
			var got model.Inbound
			if err := json.Unmarshal(put.Body, &got); err != nil {
				t.Fatalf("解析 PUT body: %v", err)
			}
			nodeFieldChanged := got.Port != existing.Port ||
				got.Network != existing.Network ||
				got.Security != existing.Security
			if nodeFieldChanged != tc.wantSync {
				t.Errorf("节点字段变了 = %v, 想要 %v(got port=%d network=%s security=%s)",
					nodeFieldChanged, tc.wantSync, got.Port, got.Network, got.Security)
			}
		})
	}
}

// 表格里的"可用"列和 CSV 的"可用性"列必须是同一件事,否则同一条用户在两处
// 显示相反的状态。
func TestUserRowMatchesCSVAvailability(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		u := userRow{ID: 1, Username: "alice", Enabled: enabled, Status: "active", ResetStrategy: "none"}
		got := u.row()
		want := enabledText(enabled)
		if len(userHeaders) != len(got) {
			t.Fatalf("表头 %d 列, 数据 %d 列", len(userHeaders), len(got))
		}
		idx := -1
		for i, h := range userHeaders {
			if h == "可用" {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatal("表头里没有「可用」这一列")
		}
		if got[idx] != want {
			t.Errorf("「可用」列 = %q, 想要 %q", got[idx], want)
		}
	}
}

// 表里空的重置策略要显示成 none,和 CSV 导出保持一致 —— 同一个用户在表格里
// 空着、在导出里写着 none,读的人会以为是两回事。
func TestUserRowNormalizesEmptyResetStrategy(t *testing.T) {
	u := userRow{Username: "alice", Enabled: true, ResetStrategy: ""}
	got := u.row()
	for i, h := range userHeaders {
		if h == "重置策略" && got[i] != string(model.ResetNone) {
			t.Errorf("重置策略 = %q, 想要 %q", got[i], model.ResetNone)
		}
	}
}

// 只改显示名:只发 metadata 请求;换城市时显式发空字母,让控制面在新分组里重新取。
func TestNodeEditDisplayOnly(t *testing.T) {
	resetEditFlags(t)
	view := model.NodeView{Node: model.Node{ID: 2, Name: "la-2", Address: "1.2.3.4"}}
	meta := model.LocationMetadata{Region: "US", City: "洛杉矶", Letter: "B"}
	calls := recordEdit(t, func(method, path string) []byte {
		if path == "/api/nodes/2/metadata" {
			return mustJSON(t, meta)
		}
		return mustJSON(t, view)
	})
	captureStdout(t, func() { runRoot(t, "node", "edit", "2", "--city", "圣何塞") })
	put := (*calls)[len(*calls)-1]
	if put.Method != http.MethodPut || put.Path != "/api/nodes/2/metadata" {
		t.Fatalf("最后一个请求是 %s %s", put.Method, put.Path)
	}
	for _, c := range *calls {
		if c.Method == http.MethodPut && c.Path == "/api/nodes/2" {
			t.Fatal("只改显示名不该 PUT 节点")
		}
	}
	var got map[string]any
	if err := json.Unmarshal(put.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got["city"] != "圣何塞" || got["letter"] != "" || got["region"] != "US" {
		t.Fatalf("PUT body %s", put.Body)
	}
	// 已删除的 line / purposes 不再出现在 PUT 体里。
	if _, ok := got["line"]; ok {
		t.Fatalf("PUT body 不该带 line: %s", put.Body)
	}
	if _, ok := got["purposes"]; ok {
		t.Fatalf("PUT body 不该带 purposes: %s", put.Body)
	}
}
