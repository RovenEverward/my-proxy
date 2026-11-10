package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// source_test 钉住 CLI 对 /api/sources 这组接口的请求形状。
// 控制面还没实现这些接口时,这层测试先用假控制面证明 CLI 说的每句话都对。

// recordedReq 记下假控制面收到的一次请求。
type recordedReq struct {
	method string
	path   string
	query  string
	body   string
}

// scriptedPlane 起一个按脚本回答的假控制面。
//
// respond 收到请求原文,决定要写的状态码与正文;返回 false 交给下一组匹配,
// 都没有就给 404。测试用开关量(如 hit)表达"命中过的请求类型"。
type scriptedPlane struct {
	t    *testing.T
	mu   sync.Mutex
	reqs []recordedReq
	srv  *httptest.Server
}

func startPlane(t *testing.T, respond func(w http.ResponseWriter, r recordedReq) bool) *scriptedPlane {
	t.Helper()
	p := &scriptedPlane{t: t}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := recordedReq{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(body)}
		p.mu.Lock()
		p.reqs = append(p.reqs, rec)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if respond(w, rec) {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"test: unmatched ` + rec.method + " " + rec.path + `"}`))
	}))
	t.Cleanup(p.srv.Close)

	t.Setenv("MPX_SERVER", p.srv.URL)
	t.Setenv("MPX_TOKEN", "secret-token")
	opts.server, opts.token = "", ""
	return p
}

func (p *scriptedPlane) requests() []recordedReq {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedReq(nil), p.reqs...)
}

// resetFlagTree 把一棵命令树的 flag 值与 Changed 位都归回默认值。
//
// 命令对象是 init() 建的包级共享件,pflag 执行完不会自己清 Changed ——
// 上一个用例给的 --region 不擦掉,下一个用例的 PATCH 里就会多出一个幽灵字段。
// StringSlice 不能走 Set(DefValue):它的默认值字符串是 "[]",再喂回去会变成
// ["[]"] 逐次累加;直接把背后的切片清空才是归零。
func resetFlagTree(cmd *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace(nil)
			f.Changed = false
			return
		}
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	}
	cmd.Flags().VisitAll(reset)
	cmd.PersistentFlags().VisitAll(reset)
	for _, sub := range cmd.Commands() {
		resetFlagTree(sub)
	}
}

// runSource 跑一次完整 root 命令(含全局 flag 解析),结束后还原 flag 状态与
// opts 输出档。要错的用 runSourceErr。
func runSource(t *testing.T, args ...string) {
	t.Helper()
	if err := runSourceErr(t, args...); err != nil {
		t.Fatalf("mpx %v: %v", strings.Join(args, " "), err)
	}
}

func runSourceErr(t *testing.T, args ...string) error {
	t.Helper()
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		resetFlagTree(rootCmd)
		opts.output = outputTable
		opts.server, opts.token, opts.fingerprint, opts.profile = "", "", "", ""
	})
	return rootCmd.Execute()
}

// feedStdin 把一行内容塞进 os.Stdin,测 --url-stdin 与管道输入。
func feedStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("建 stdin 管道: %v", err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatalf("写 stdin: %v", err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
}

// sourceViewJSON 是一份够看的 sourceView 应答。
func sourceViewJSON(id int64, name string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"url_masked":"https://***","enabled":true,"interval_seconds":21600,"region":"","revision":1,"last_error":"","refreshing":false,"node_count":2,"input_format":"clash","issues":[],"formats":{"clash-meta":{"count":2,"issues":[]}}}`, id, name)
}

func wantRequest(t *testing.T, reqs []recordedReq, i int, method, path string) recordedReq {
	t.Helper()
	if len(reqs) <= i {
		t.Fatalf("只收到 %d 个请求,想要第 %d 个是 %s %s", len(reqs), i+1, method, path)
	}
	got := reqs[i]
	if got.method != method || got.path != path {
		t.Fatalf("第 %d 个请求 = %s %s,想要 %s %s", i+1, got.method, got.path, method, path)
	}
	return got
}

func bodyJSON(t *testing.T, r recordedReq) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("请求体不是 JSON: %q", r.body)
	}
	return m
}

// TestSourceAddPostsContractBody 钉住 POST /api/sources 的请求体契约:
// name/url/interval_seconds/enabled;URL 只从 stdin 进,不落在命令行上。
func TestSourceAddPostsContractBody(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "POST" && r.path == "/api/sources" {
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用订阅")))
			return true
		}
		return false
	})
	feedStdin(t, "https://upstream.example/sub?token=secret123\n")

	runSource(t, "source", "add", "常用订阅", "--url-stdin", "--wait=false")

	reqs := plane.requests()
	if len(reqs) != 1 {
		t.Fatalf("--wait=false 不该再发别的请求,收到 %d 个", len(reqs))
	}
	body := bodyJSON(t, reqs[0])
	for k, want := range map[string]any{
		"name":             "常用订阅",
		"url":              "https://upstream.example/sub?token=secret123",
		"interval_seconds": float64(21600),
		"enabled":          true,
	} {
		if body[k] != want {
			t.Errorf("body[%q] = %v, 想要 %v", k, body[k], want)
		}
	}
	if _, ok := body["region"]; ok {
		t.Errorf("没给 --region 时 body 不该带 region: %v", body)
	}
}

// TestSourceAddDefaults 钉住 --interval 0(只手动)与 --region 的写法。
func TestSourceAddIntervalZeroAndRegion(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "POST" && r.path == "/api/sources" {
			_, _ = w.Write([]byte(sourceViewJSON(3, "手动源")))
			return true
		}
		return false
	})
	feedStdin(t, "https://u.example/x\n")
	runSource(t, "source", "add", "手动源", "--url-stdin", "--interval", "0", "--region", "JP")

	reqs := plane.requests()
	body := bodyJSON(t, reqs[0])
	if body["interval_seconds"] != float64(0) {
		t.Errorf("--interval 0 应发送 interval_seconds=0,得到 %v", body["interval_seconds"])
	}
	if body["region"] != "JP" {
		t.Errorf("--region JP 应发送 region=JP,得到 %v", body["region"])
	}
}

// TestSourceAddRejectsEmptyStdin 空 URL 不能发出请求 —— 服务端建出
// 一个永远拉不成功的来源比当场报错更难发现。
func TestSourceAddRejectsEmptyStdin(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool { return false })
	feedStdin(t, "\n")
	if err := runSourceErr(t, "source", "add", "空", "--url-stdin", "--wait=false"); err == nil {
		t.Fatal("空 URL 没有报错")
	}
	if len(plane.requests()) != 0 {
		t.Fatalf("空 URL 不该发任何请求,发了 %d 个", len(plane.requests()))
	}
}

// TestSourceListHitsGet 只验证请求路径与表格可解。
func TestSourceListHitsGet(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "GET" && r.path == "/api/sources" {
			_, _ = w.Write([]byte("[" + sourceViewJSON(7, "常用") + "]"))
			return true
		}
		return false
	})
	out := captureStdout(t, func() { runSource(t, "source", "list") })
	wantRequest(t, plane.requests(), 0, "GET", "/api/sources")
	if !strings.Contains(out, "常用") {
		t.Fatalf("列表输出里应有来源名: %q", out)
	}
}

// TestSourceShowReadsViewAndGrants 钉住 show 的两段读取:详情 + 授权名单。
func TestSourceShowReadsViewAndGrants(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		case r.method == "GET" && r.path == "/api/sources/7/grants":
			_, _ = w.Write([]byte(`{"users":["alice"]}`))
		default:
			return false
		}
		return true
	})
	out := captureStdout(t, func() { runSource(t, "source", "show", "7") })
	reqs := plane.requests()
	wantRequest(t, reqs, 0, "GET", "/api/sources/7")
	wantRequest(t, reqs, 1, "GET", "/api/sources/7/grants")
	if !strings.Contains(out, "alice") {
		t.Fatalf("show 应列出授权用户: %q", out)
	}
}

// TestSourceShowResolvesName 名字参数先取列表匹配,再按 ID 读详情。
func TestSourceShowResolvesName(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources":
			_, _ = w.Write([]byte("[" + sourceViewJSON(7, "常用") + "]"))
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		case r.method == "GET" && r.path == "/api/sources/7/grants":
			_, _ = w.Write([]byte(`{"users":[]}`))
		default:
			return false
		}
		return true
	})
	captureStdout(t, func() { runSource(t, "source", "show", "常用") })
	reqs := plane.requests()
	// 按名字查:先取列表匹配出 ID,详情就用列表里那条;最后读授权名单。
	wantRequest(t, reqs, 0, "GET", "/api/sources")
	wantRequest(t, reqs, 1, "GET", "/api/sources/7/grants")
	if len(reqs) != 2 {
		t.Fatalf("按名字 show 应只需两次请求,收到 %d 个", len(reqs))
	}
}

// TestSourceEditSendsOnlyChangedFields 钉住 PATCH 只发改了的键。
func TestSourceEditSendsOnlyChangedFields(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		case r.method == "PATCH" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		default:
			return false
		}
		return true
	})
	runSource(t, "source", "edit", "7", "--region", "JP", "--interval", "6h")

	reqs := plane.requests()
	wantRequest(t, reqs, 0, "GET", "/api/sources/7")
	patch := bodyJSON(t, wantRequest(t, reqs, 1, "PATCH", "/api/sources/7"))
	if patch["region"] != "JP" || patch["interval_seconds"] != float64(21600) {
		t.Errorf("PATCH 内容不对: %v", patch)
	}
	if len(patch) != 2 {
		t.Errorf("PATCH 应只含改了的两个字段,得到 %v", patch)
	}
}

// TestSourceEditURLViaStdin 换地址只能经 stdin,不给明文 flag。
func TestSourceEditURLViaStdin(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		case r.method == "PATCH" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		default:
			return false
		}
		return true
	})
	feedStdin(t, "https://new.example/sub\n")
	runSource(t, "source", "edit", "7", "--url-stdin")

	reqs := plane.requests()
	patch := bodyJSON(t, wantRequest(t, reqs, 1, "PATCH", "/api/sources/7"))
	if patch["url"] != "https://new.example/sub" {
		t.Errorf("PATCH 应带新 url,得到 %v", patch)
	}
}

// TestSourceEditNothingChanged 什么都没给时不该发 PATCH。
func TestSourceEditNothingChanged(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "GET" && r.path == "/api/sources/7" {
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
			return true
		}
		return false
	})
	err := runSourceErr(t, "source", "edit", "7")
	if err == nil {
		t.Fatal("不给字段的 edit 没有报错")
	}
	for _, r := range plane.requests() {
		if r.method == "PATCH" {
			t.Fatal("没给字段却发了 PATCH")
		}
	}
}

// TestSourceRefreshPostsAndReports 钉住 refresh 的 202 应答路径。
func TestSourceRefreshPosts(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		case r.method == "POST" && r.path == "/api/sources/7/refresh":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		default:
			return false
		}
		return true
	})
	out := captureStdout(t, func() { runSource(t, "source", "refresh", "7") })
	reqs := plane.requests()
	wantRequest(t, reqs, 0, "GET", "/api/sources/7")
	wantRequest(t, reqs, 1, "POST", "/api/sources/7/refresh")
	if !strings.Contains(out, "已刷新") {
		t.Fatalf("完成时应有回执: %q", out)
	}
}

// TestSourceRefreshWaitsForRefreshing 202 回来还在拉时,refresh 轮询详情直到落停。
func TestSourceRefreshWaitsForRefreshing(t *testing.T) {
	var polls int
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			polls++
			if polls >= 3 {
				_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
			} else {
				_, _ = w.Write([]byte(`{"id":7,"name":"常用","refreshing":true,"enabled":true,"interval_seconds":21600}`))
			}
		case r.method == "POST" && r.path == "/api/sources/7/refresh":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":7,"name":"常用","refreshing":true,"enabled":true,"interval_seconds":21600}`))
		default:
			return false
		}
		return true
	})
	captureStdout(t, func() { runSource(t, "source", "refresh", "7") })
	reqs := plane.requests()
	// GET(定位) + POST refresh + GET(还在拉) + GET(完成)。
	if len(reqs) != 4 {
		t.Fatalf("等待刷新应轮询到落停,共收到 %d 个请求", len(reqs))
	}
	wantRequest(t, reqs, 3, "GET", "/api/sources/7")
}

// TestSourceRefreshFailureExitsNonZero 刷新留下 last_error 时命令必须失败 ——
// 脚本靠退出码区分成败,不是靠 grep 输出。
func TestSourceRefreshFailureExitsNonZero(t *testing.T) {
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(`{"id":7,"name":"常用","enabled":true,"refreshing":false,"last_error":"拉取超时","last_good_at":"2026-11-09T01:00:00Z","node_count":5}`))
		case r.method == "POST" && r.path == "/api/sources/7/refresh":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":7,"name":"常用","enabled":true,"refreshing":false,"last_error":"拉取超时","last_good_at":"2026-11-09T01:00:00Z","node_count":5}`))
		default:
			return false
		}
		return true
	})
	err := runSourceErr(t, "source", "refresh", "7")
	if err == nil {
		t.Fatal("刷新失败应返回错误")
	}
	if !strings.Contains(err.Error(), "拉取超时") {
		t.Fatalf("错误里应带服务端那句: %v", err)
	}
}

// TestSourceGrantRevokePerUser 钉住按人授权/撤权的幂等接口形状。
func TestSourceGrantRevokePerUser(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		case r.method == "PUT" && strings.HasPrefix(r.path, "/api/sources/7/grants/"):
			_, _ = w.Write([]byte(`{"users":["alice","bob"]}`))
		case r.method == "DELETE" && strings.HasPrefix(r.path, "/api/sources/7/grants/"):
			_, _ = w.Write([]byte(`{"users":[]}`))
		default:
			return false
		}
		return true
	})
	captureStdout(t, func() { runSource(t, "source", "grant", "7", "--user", "alice,bob") })
	captureStdout(t, func() { runSource(t, "source", "revoke", "7", "--user", "alice") })

	reqs := plane.requests()
	wantRequest(t, reqs, 1, "PUT", "/api/sources/7/grants/alice")
	wantRequest(t, reqs, 2, "PUT", "/api/sources/7/grants/bob")
	wantRequest(t, reqs, 4, "DELETE", "/api/sources/7/grants/alice")
}

// TestSourceEnableDisablePatchEnabled 启停用只改 enabled 一个键。
func TestSourceEnableDisable(t *testing.T) {
	var patches []map[string]any
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		case r.method == "PATCH" && r.path == "/api/sources/7":
			var m map[string]any
			_ = json.Unmarshal([]byte(r.body), &m)
			patches = append(patches, m)
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		default:
			return false
		}
		return true
	})
	captureStdout(t, func() { runSource(t, "source", "disable", "7") })
	captureStdout(t, func() { runSource(t, "source", "enable", "7") })
	if len(patches) != 2 {
		t.Fatalf("应发两次 PATCH,收到 %d 个请求", len(plane.requests()))
	}
	if patches[0]["enabled"] != false || len(patches[0]) != 1 {
		t.Errorf("disable 应只发 {\"enabled\":false},得到 %v", patches[0])
	}
	if patches[1]["enabled"] != true || len(patches[1]) != 1 {
		t.Errorf("enable 应只发 {\"enabled\":true},得到 %v", patches[1])
	}
}

// TestSourceDeleteHitsDelete 删除走 DELETE。
func TestSourceDelete(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(sourceViewJSON(7, "常用")))
		case r.method == "DELETE" && r.path == "/api/sources/7":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("null"))
		default:
			return false
		}
		return true
	})
	captureStdout(t, func() { runSource(t, "source", "delete", "7") })
	wantRequest(t, plane.requests(), 1, "DELETE", "/api/sources/7")
}

// TestSourcePreviewPathAndFormat 钉住按用户预览的接口形状与 format 查询参数。
func TestSourcePreviewPathAndFormat(t *testing.T) {
	var got recordedReq
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "GET" && r.path == "/api/users/alice/subscription-preview" {
			got = r
			_, _ = w.Write([]byte(`{"nodes":[{"name":"自建 · hk-1","protocol":"vless","groups":["自动"],"reasons":[]},{"name":"常用 · jp-1","protocol":"ss","source_id":7,"source_name":"常用","groups":[],"reasons":["未进入任何策略组"]}],"issues":[]}`))
			return true
		}
		return false
	})
	captureStdout(t, func() {
		runSource(t, "source", "preview", "--user", "alice", "--format", "sing-box")
	})
	if got.method == "" {
		t.Fatal("没有收到 preview 请求")
	}
	if got.query != "format=sing-box" {
		t.Fatalf("format 查询串 = %q, 想要 format=sing-box", got.query)
	}
}

// TestSourceAddFailedFetchExitsNonZero POST 应答里刷新已完成但带 last_error
// 时(拉得快,应答就是终态),--wait 也要非零退出。
func TestSourceAddFailedFetchExitsNonZero(t *testing.T) {
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "POST" && r.path == "/api/sources" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7,"name":"坏源","enabled":true,"refreshing":false,"last_error":"连接超时","node_count":0}`))
			return true
		}
		return false
	})
	feedStdin(t, "https://dead.example/sub\n")
	if err := runSourceErr(t, "source", "add", "坏源", "--url-stdin", "--wait=true"); err == nil {
		t.Fatal("首拉失败的 add 应返回错误")
	}
}

// TestSourceAddFailedFetchAfterWait 等轮询落停后发现 last_error 同样非零退出。
func TestSourceAddFailedFetchAfterWait(t *testing.T) {
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "POST" && r.path == "/api/sources":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7,"name":"坏源","enabled":true,"refreshing":true}`))
		case r.method == "GET" && r.path == "/api/sources/7":
			_, _ = w.Write([]byte(`{"id":7,"name":"坏源","enabled":true,"refreshing":false,"last_error":"上游 500","node_count":0}`))
		default:
			return false
		}
		return true
	})
	feedStdin(t, "https://dead.example/sub\n")
	if err := runSourceErr(t, "source", "add", "坏源", "--url-stdin", "--wait=true"); err == nil {
		t.Fatal("等待后首拉失败的 add 应返回错误")
	}
}

// TestSourceAddNoWaitKeepsZeroExit --wait=false 是「触发即走」:就算应答里
// 已经带了失败,退出码也保持 0(结果由 source show 看)。
func TestSourceAddNoWaitKeepsZeroExit(t *testing.T) {
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "POST" && r.path == "/api/sources" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7,"name":"源","enabled":true,"refreshing":false,"last_error":"连接超时"}`))
			return true
		}
		return false
	})
	feedStdin(t, "https://dead.example/sub\n")
	runSource(t, "source", "add", "源", "--url-stdin", "--wait=false")
}

// TestSourcePreviewNonEmptyIssues 兼容报告是真对象数组,不是字符串数组 ——
// 空 issues 的用例盖不住这个形状错误。
func TestSourcePreviewNonEmptyIssues(t *testing.T) {
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "GET" && r.path == "/api/users/alice/subscription-preview" {
			_, _ = w.Write([]byte(`{"nodes":[{"name":"hk-1","protocol":"vless","groups":["自动"],"reasons":[]}],"issues":[{"index":2,"name":"ext-sg","reason":"trojan 不带 TLS 无法忠实输出"}]}`))
			return true
		}
		return false
	})
	out := captureStdout(t, func() { runSource(t, "source", "preview", "--user", "alice") })
	if !strings.Contains(out, "ext-sg") || !strings.Contains(out, "trojan") {
		t.Fatalf("兼容报告应逐条列出节点与原因: %q", out)
	}
}

// ---- group 的来源条件与按用户预览 ----

// TestGroupAddThenPutSources 建组时给了 --source,先 POST 建组再 PUT 独立来源接口。
func TestGroupAddThenPutSources(t *testing.T) {
	var sourcesBody map[string]any
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "POST" && r.path == "/api/proxy-groups":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":9,"name":"混合","mode":"manual"}`))
		case r.method == "PUT" && r.path == "/api/proxy-groups/9/sources":
			var m map[string]any
			_ = json.Unmarshal([]byte(r.body), &m)
			sourcesBody = m
			_, _ = w.Write([]byte(`{"sources":["managed","source:7"]}`))
		default:
			return false
		}
		return true
	})
	captureStdout(t, func() {
		runSource(t, "group", "add", "混合", "--mode", "manual", "--source", "managed,source:7")
	})
	reqs := plane.requests()
	wantRequest(t, reqs, 0, "POST", "/api/proxy-groups")
	wantRequest(t, reqs, 1, "PUT", "/api/proxy-groups/9/sources")
	if sourcesBody == nil {
		t.Fatal("没发 sources PUT")
	}
	got := sourcesBody["sources"]
	want := []any{"managed", "source:7"}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("sources = %v, 想要 %v", got, want)
	}
}

// TestGroupAddWithoutSourceStaysPutOnly 不给 --source 时不发第二条请求 ——
// 旧行为一字不变。
func TestGroupAddWithoutSourceStaysPutOnly(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "POST" && r.path == "/api/proxy-groups" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":9,"name":"g"}`))
			return true
		}
		return false
	})
	captureStdout(t, func() { runSource(t, "group", "add", "g", "--mode", "auto") })
	if len(plane.requests()) != 1 {
		t.Fatalf("不带 --source 建组应只发一次 POST,收到 %d 个请求", len(plane.requests()))
	}
}

// TestGroupEditSourceUsesDedicatedAPI 改来源条件走 /sources 接口,
// 绝不塞进整组 PUT 的字段里(旧客户端语义保护)。
func TestGroupEditSourceUsesDedicatedAPI(t *testing.T) {
	var putSources map[string]any
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/proxy-groups":
			_, _ = w.Write([]byte(`[{"id":9,"name":"混合","mode":"manual"}]`))
		case r.method == "PUT" && r.path == "/api/proxy-groups/9":
			var m map[string]any
			_ = json.Unmarshal([]byte(r.body), &m)
			if _, leaked := m["sources"]; leaked {
				t.Errorf("整组 PUT 里不许带 sources 字段: %v", m)
			}
			_, _ = w.Write([]byte(`{"id":9,"name":"混合","mode":"manual"}`))
		case r.method == "PUT" && r.path == "/api/proxy-groups/9/sources":
			_ = json.Unmarshal([]byte(r.body), &putSources)
			_, _ = w.Write([]byte(`{"sources":["source:7"]}`))
		default:
			return false
		}
		return true
	})
	captureStdout(t, func() {
		runSource(t, "group", "edit", "混合", "--source", "source:7")
	})
	wantRequest(t, plane.requests(), 2, "PUT", "/api/proxy-groups/9/sources")
	if putSources["sources"] == nil {
		t.Fatal("sources PUT 没带 sources 键")
	}
}

// TestGroupEditSourceEmptyClears "--source \"\"" 传空列表 = 回到全部来源。
func TestGroupEditSourceEmptyClears(t *testing.T) {
	var putSources map[string]any
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/proxy-groups":
			_, _ = w.Write([]byte(`[{"id":9,"name":"混合","mode":"manual"}]`))
		case r.method == "PUT" && r.path == "/api/proxy-groups/9":
			_, _ = w.Write([]byte(`{"id":9,"name":"混合","mode":"manual"}`))
		case r.method == "PUT" && r.path == "/api/proxy-groups/9/sources":
			_ = json.Unmarshal([]byte(r.body), &putSources)
			_, _ = w.Write([]byte(`{"sources":[]}`))
		default:
			return false
		}
		return true
	})
	captureStdout(t, func() {
		runSource(t, "group", "edit", "混合", "--source", "")
	})
	got, ok := putSources["sources"].([]any)
	if !ok || len(got) != 0 {
		t.Fatalf("--source \"\" 应发空数组,得到 %v", putSources)
	}
}

// TestGroupSourcesReadsList group sources 读回来源条件。
func TestGroupSourcesReadsList(t *testing.T) {
	plane := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		switch {
		case r.method == "GET" && r.path == "/api/proxy-groups":
			_, _ = w.Write([]byte(`[{"id":9,"name":"混合","mode":"manual"}]`))
		case r.method == "GET" && r.path == "/api/proxy-groups/9/sources":
			_, _ = w.Write([]byte(`{"sources":["managed","source:7"]}`))
		default:
			return false
		}
		return true
	})
	out := captureStdout(t, func() { runSource(t, "group", "sources", "混合") })
	wantRequest(t, plane.requests(), 1, "GET", "/api/proxy-groups/9/sources")
	if !strings.Contains(out, "source:7") {
		t.Fatalf("应列出来源条件: %q", out)
	}
}

// TestGroupPreviewUserAndFormat 钉住按用户、按格式预览的查询参数。
func TestGroupPreviewUserAndFormat(t *testing.T) {
	var got recordedReq
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "POST" && r.path == "/api/proxy-groups/preview" {
			got = r
			_, _ = w.Write([]byte(`[{"name":"hk-1","protocol":"vless","region":"HK","admitted":true,"reasons":[]}]`))
			return true
		}
		return false
	})
	captureStdout(t, func() {
		runSource(t, "group", "preview", "--user", "alice", "--format", "sing-box")
	})
	q := got.query
	for _, want := range []string{"user=alice", "format=sing-box"} {
		if !strings.Contains(q, want) {
			t.Fatalf("预览查询串 %q 少了 %q", q, want)
		}
	}
}

// TestGroupPreviewCarriesSources 预览把 --source 放进 sources 查询参数,
// 不进请求体 —— 预览只为演算,不该为看一眼写关系。
func TestGroupPreviewCarriesSources(t *testing.T) {
	var got recordedReq
	_ = startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method == "POST" && r.path == "/api/proxy-groups/preview" {
			got = r
			_, _ = w.Write([]byte(`[]`))
			return true
		}
		return false
	})
	captureStdout(t, func() {
		runSource(t, "group", "preview", "--source", "managed,source:7")
	})
	if !strings.Contains(got.query, "sources=managed%2Csource%3A7") && !strings.Contains(got.query, "sources=managed,source:7") {
		t.Fatalf("预览查询串应带 sources=managed,source:7,得到 %q", got.query)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got.body), &m); err != nil {
		t.Fatalf("预览请求体不是 JSON: %q", got.body)
	}
	if _, leaked := m["sources"]; leaked {
		t.Fatalf("sources 不许塞进 ProxyGroup 请求体: %v", m)
	}
}
