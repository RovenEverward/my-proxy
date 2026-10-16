package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// withLocalVersion 把本机 CLI 版本钉成 v,测试结束复原。
//
// go test 编出来的二进制没带版本号,不注入的话所有涉及版本比较的分支都落进
// "空版本"那一支,真正要测的路径一条也走不到。
func withLocalVersion(t *testing.T, v string) {
	t.Helper()
	old := localVersion
	localVersion = func() string { return v }
	t.Cleanup(func() { localVersion = old })
}

// captureStderr 抓住直接写 os.Stderr 的警告。它必须与 stdout 分开抓:警告混进
// stdout 正是 json 模式下要避免的事,合在一起断言就看不出有没有混。
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("建管道: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

// updatePayload 拼一份控制面回 `/api/update` 的样例响应。
func updatePayload(t *testing.T, current, latest string, nodes []model.UpdateNodeCore) []byte {
	t.Helper()
	b, err := json.Marshal(model.UpdateSnapshot{
		Control: model.UpdatePart{
			Current:     current,
			Latest:      latest,
			Behind:      2,
			Available:   current != "" && latest != "",
			ReleaseURL:  "https://example.test/releases/" + latest,
			PublishedAt: nil,
			CheckedAt:   nil,
		},
		Nodes: nodes,
	})
	if err != nil {
		t.Fatalf("拼响应: %v", err)
	}
	return b
}

func TestUpdateCommandIsRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"update"})
	if err != nil {
		t.Fatalf("mpx update 没有注册: %v", err)
	}
	if cmd != updateCmd {
		t.Fatalf("update 落到了别的命令上: %s", cmd.CommandPath())
	}
	show, _, err := rootCmd.Find([]string{"update", "show"})
	if err != nil {
		t.Fatalf("mpx update show 没有注册: %v", err)
	}
	if show != updateShowCmd {
		t.Fatalf("update show 落到了别的命令上: %s", show.CommandPath())
	}
}

// 走完整命令:请求路径、鉴权头、四列表头,以及三部分各一行。
func TestUpdateCommandPrintsTable(t *testing.T) {
	withLocalVersion(t, "v0.2.0")
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/update" {
			t.Errorf("意外的请求路径: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write(updatePayload(t, "v0.2.0", "v0.3.0", []model.UpdateNodeCore{
			{NodeID: 1, Name: "hk-1", Core: model.CoreXray, Current: "26.3.27"},
			{NodeID: 2, Name: "jp-2", Core: model.CoreSingBox, Current: "1.12.0", Target: "v1.14.3"},
		}))
	})

	out := captureStdout(t, func() { runRoot(t, "update") })

	for _, want := range []string{
		"部分", "当前", "目标/最新", "状态",
		"本机 CLI", "控制面",
		"节点 hk-1", "xray 26.3.27",
		"节点 jp-2", "sing-box 1.12.0", "v1.14.3",
		"可升级",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里缺少 %q:\n%s", want, out)
		}
	}
	// 升级提示只能说去见发布页,不能提到还没实现的 apply 命令 —— 说了会让人
	// 以为敲一下就能升。
	if strings.Contains(out, "update apply") {
		t.Errorf("不该提到还没实现的 apply 命令:\n%s", out)
	}
}

// 节点核心的空版本是"还没观察到",不能套用二进制那套「源码编译」的说法 ——
// 那会把"巡检还没轮到"说成"编译方式",指向完全不同的两件事。
func TestUpdateNodeCoreUnknownVersionWording(t *testing.T) {
	withLocalVersion(t, "v0.2.0")
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(updatePayload(t, "v0.2.0", "v0.2.0", []model.UpdateNodeCore{
			{NodeID: 1, Name: "hk-1", Core: model.CoreXray},
		}))
	})

	// 本机与控制面版本都已知,输出里唯一的空版本属于节点核心。
	out := captureStdout(t, func() { runRoot(t, "update") })
	if !strings.Contains(out, "xray 未知（还没查到）") {
		t.Errorf("节点核心的空版本该说「还没查到」:\n%s", out)
	}
	if strings.Contains(out, "xray 未知（源码编译）") {
		t.Errorf("把节点核心的空版本说成了源码编译:\n%s", out)
	}
}

// mpx update 与 mpx update show 必须完全等价:两种敲法走同一个入口。
func TestUpdateShowIsEquivalent(t *testing.T) {
	withLocalVersion(t, "v0.2.0")
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/update" {
			t.Errorf("意外的请求路径: %s", r.URL.Path)
		}
		_, _ = w.Write(updatePayload(t, "v0.2.0", "v0.3.0", nil))
	})

	first := captureStdout(t, func() { runRoot(t, "update") })
	second := captureStdout(t, func() { runRoot(t, "update", "show") })
	if first != second {
		t.Errorf("update 与 update show 的输出不一样:\n--- update ---\n%s\n--- update show ---\n%s", first, second)
	}
}

// -o json 给的是客户端拼的报告,形状要含三部分,且不能混进警告文字。
func TestUpdateCommandJSON(t *testing.T) {
	withLocalVersion(t, "v0.2.0")
	opts.output = outputJSON
	t.Cleanup(func() { opts.output = outputTable })
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(updatePayload(t, "v0.2.0", "v0.3.0", []model.UpdateNodeCore{
			{NodeID: 1, Name: "hk-1", Core: model.CoreXray, Current: "26.3.27"},
		}))
	})

	out := captureStdout(t, func() { runRoot(t, "update", "-o", "json") })

	// 输出必须是能直接解析的 JSON:一个字都不能多。
	var report struct {
		CLI     model.UpdatePart       `json:"cli"`
		Control model.UpdatePart       `json:"control"`
		Nodes   []model.UpdateNodeCore `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("json 输出解析失败: %v\n%s", err, out)
	}
	if report.CLI.Current != "v0.2.0" || report.Control.Current != "v0.2.0" {
		t.Errorf("版本没对上:cli=%+v control=%+v", report.CLI, report.Control)
	}
	if len(report.Nodes) != 1 || report.Nodes[0].Name != "hk-1" {
		t.Errorf("节点行不对:%+v", report.Nodes)
	}
}

// 老控制面没有 /api/update:回退到 /api/status 里的版本信息,节点那几行说清
// 拿不到,而不是静静地少打几行。
func TestUpdateFallsBackOnOldControlPlane(t *testing.T) {
	withLocalVersion(t, "v0.2.0")
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })

	seenStatus := false
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/update":
			http.Error(w, `{"error":"没有这个接口"}`, http.StatusNotFound)
		case "/api/status":
			seenStatus = true
			published := time.Date(2026, 5, 1, 3, 2, 0, 0, time.UTC)
			_ = json.NewEncoder(w).Encode(model.ControlStatus{
				Version: "v0.2.0",
				Update: model.UpdateInfo{
					Current: "v0.2.0", Latest: "v0.3.0", Behind: 2,
					URL: "https://example.test/releases/v0.3.0", PublishedAt: &published,
				},
			})
		default:
			t.Errorf("意外的请求路径: %s", r.URL.Path)
		}
	})

	out := captureStdout(t, func() { runRoot(t, "update") })

	if !seenStatus {
		t.Fatal("没有回退去问 /api/status")
	}
	for _, want := range []string{"控制面", "v0.2.0", "v0.3.0", "可升级"} {
		if !strings.Contains(out, want) {
			t.Errorf("回退输出里缺少 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "节点 ") {
		t.Errorf("老控制面不该有节点行:\n%s", out)
	}
	if !strings.Contains(out, updateStaleControlNotice) {
		t.Errorf("该提示控制面过旧:\n%s", out)
	}
	// 老控制面在表格里回退到 /api/status 的 version,而不是显示"未知"。
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "控制面") && strings.Contains(line, "v0.2.0") {
			found = true
		}
	}
	if !found {
		t.Errorf("回退时控制面当前版本该取 /api/status 的 version:\n%s", out)
	}

	// json 模式下这句话不能进 stdout,否则那份 JSON 就解析不了了。
	opts.output = outputJSON
	var jsonOut, jsonErr string
	jsonErr = captureStderr(t, func() {
		jsonOut = captureStdout(t, func() { runRoot(t, "update", "-o", "json") })
	})
	var report struct {
		Control model.UpdatePart `json:"control"`
		Nodes   []model.UpdateNodeCore
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("json 输出被污染了: %v\n%s", err, jsonOut)
	}
	if report.Control.Latest != "v0.3.0" {
		t.Errorf("回退拿到的版本没进 json:%+v", report.Control)
	}
	if report.Nodes == nil {
		t.Errorf("没有节点时该给空数组,而不是 null:%s", jsonOut)
	}
	if !strings.Contains(jsonErr, updateStaleControlNotice) {
		t.Errorf("json 模式下提示该走 stderr,实际 %q", jsonErr)
	}
	opts.output = outputTable
}

// 从源码编出来的 CLI 不知道自己是谁:显示「未知（源码编译）」,不声称有新版。
func TestUpdateUnknownCLIVersion(t *testing.T) {
	withLocalVersion(t, "")
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		// 控制面那边甚至报了一份"有新版";本机版本未知时 CLI 这一行也不该跟着喊。
		_, _ = w.Write(updatePayload(t, "v0.2.0", "v0.3.0", nil))
	})

	out := captureStdout(t, func() { runRoot(t, "update") })

	if !strings.Contains(out, "未知（源码编译）") {
		t.Errorf("该写明版本未知且是源码编译:\n%s", out)
	}
	// 第一行(本机 CLI)不能说可升级:控制面的版本对不上本机,拿它当依据是替
	// 使用者许了一个不成立的承诺。
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "本机 CLI") && strings.Contains(line, "可升级") {
			t.Errorf("本机版本未知时不该说可升级:%q", line)
		}
	}
}

// 主/次版本不一致要警告;一致、或者任一边未知时都不警告。
func TestUpdateWarnsOnVersionMismatch(t *testing.T) {
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })

	cases := []struct {
		name    string
		cli     string
		control string
		want    bool
	}{
		{"次版本不同", "v0.2.0", "v0.3.0", true},
		{"主版本不同", "v0.9.0", "v1.0.0", true},
		{"补丁不同不算", "v0.2.0", "v0.2.9", false},
		{"同一个", "v0.2.0", "v0.2.0", false},
		{"控制面未知", "v0.2.0", "", false},
		{"本机未知", "", "v0.3.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withLocalVersion(t, tc.cli)
			fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(updatePayload(t, tc.control, "v0.9.9", nil))
			})

			var errOut string
			errOut = captureStderr(t, func() { captureStdout(t, func() { runRoot(t, "update") }) })

			has := strings.Contains(errOut, "主/次版本不一致")
			if has != tc.want {
				t.Errorf("警告出现=%v,想要 %v;stderr=%q", has, tc.want, errOut)
			}
		})
	}
}

// mpx status 也要在版本不一致时警告,而且必须不影响退出码、不混进 --json。
func TestStatusWarnsOnVersionMismatch(t *testing.T) {
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })
	withLocalVersion(t, "v0.2.0")
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.ControlStatus{Version: "v0.3.0"})
	})

	var out, errOut string
	errOut = captureStderr(t, func() {
		out = captureStdout(t, func() { runRoot(t, "status") })
	})
	if !strings.Contains(errOut, "主/次版本不一致") {
		t.Errorf("status 也该警告,stderr=%q", errOut)
	}
	if strings.Contains(out, "主/次版本不一致") {
		t.Errorf("警告不该混进表格:\n%s", out)
	}
	if !strings.Contains(out, "字段") {
		t.Errorf("表格仍该正常打出:\n%s", out)
	}

	// json 模式下 stdout 必须干净,警告只走 stderr。
	opts.output = outputJSON
	var jsonOut, jsonErr string
	jsonErr = captureStderr(t, func() {
		jsonOut = captureStdout(t, func() { runRoot(t, "status", "-o", "json") })
	})
	if !strings.Contains(jsonErr, "主/次版本不一致") {
		t.Errorf("json 模式下警告仍该出现,stderr=%q", jsonErr)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("json 输出被污染了: %v\n%s", err, jsonOut)
	}
	opts.output = outputTable
}
