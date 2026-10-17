package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/selfupdate"
)

// updateRig 起一个假控制面(按 /api/update、/api/status 给不同响应)与一个正常的
// ui.Server,返回已登录的 cookie。用 cpH 允许测试改控制面行为。
func updateRig(t *testing.T, cpH http.HandlerFunc) (*Server, *http.Cookie) {
	t.Helper()
	cp := httptest.NewServer(cpH)
	t.Cleanup(cp.Close)
	up, _ := url.Parse(cp.URL)
	s, err := New("127.0.0.1:7480", Options{
		Upstream: up, Token: "secret-token", Allow: []string{"GET /api", "POST /api/update/control"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/?k="+s.Key(), nil)
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	if c == nil {
		t.Fatal("没拿到会话 cookie")
	}
	return s, c
}

// withLocalVersion 把本机版本替换成一个已知值,测试结束自动还原。
func withLocalVersion(t *testing.T, v string) {
	t.Helper()
	old := updateLocalVersion
	updateLocalVersion = func() string { return v }
	t.Cleanup(func() { updateLocalVersion = old })
}

func TestLocalUpdateNeedsSession(t *testing.T) {
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer cp.Close()
	up, _ := url.Parse(cp.URL)
	s, err := New("127.0.0.1:7480", Options{Upstream: up, Allow: []string{"GET /api"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/local/update", "/local/update/cli", "/local/update/control"} {
		method := http.MethodGet
		if strings.HasSuffix(p, "/cli") || strings.HasSuffix(p, "/control") {
			method = http.MethodPost
		}
		if w := doJSON(s, method, p, nil, "{}"); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 无会话应 401,得到 %d %s", method, p, w.Code, w.Body)
		}
	}
}

func TestLocalUpdateView(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	cpH := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/update":
			if r.URL.Query().Get("refresh") != "1" {
				t.Errorf("应带 refresh=1,得到 %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"control":{"current":"v0.6.4","latest":"v0.6.5","behind":1,"available":true,"release_url":"https://example.test/rel/v0.6.5","error":""},"nodes":[]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"update":{"notes":"修了几个问题"}}`))
		default:
			http.NotFound(w, r)
		}
	}
	s, c := updateRig(t, cpH)
	w := doJSON(s, http.MethodGet, "/local/update", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /local/update 应 200,得到 %d %s", w.Code, w.Body)
	}
	var view updateViewBody
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.CLI.Current != "v0.6.4" || view.CLI.Latest != "v0.6.5" || !view.CLI.Available {
		t.Fatalf("cli 部分不对: %+v", view.CLI)
	}
	if view.CLI.ReleaseURL != "https://example.test/rel/v0.6.5" || view.CLI.Error != "" {
		t.Fatalf("cli 的发布地址/错误不对: %+v", view.CLI)
	}
	if view.Control.Current != "v0.6.4" || view.Control.Latest != "v0.6.5" {
		t.Fatalf("control 部分不对: %+v", view.Control)
	}
	if view.Notes != "修了几个问题" {
		t.Fatalf("notes 不对: %q", view.Notes)
	}
}

func TestLocalUpdateViewControlDown(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	s, c := updateRig(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	w := doJSON(s, http.MethodGet, "/local/update", c, "")
	// 控制面不可用也要 200:页面靠 cli.error 说明原因,而不是整页报错。
	if w.Code != http.StatusOK {
		t.Fatalf("控制面不可用时应 200,得到 %d %s", w.Code, w.Body)
	}
	var view updateViewBody
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.CLI.Latest != "" || view.CLI.Available || view.CLI.Error == "" {
		t.Fatalf("控制面不可用时 cli 应空且带 error: %+v", view.CLI)
	}
	if view.Control.Latest != "" {
		t.Fatalf("控制面不可用时 control 应为空: %+v", view.Control)
	}
}

func TestLocalUpdateCLIMethodAndBody(t *testing.T) {
	s, c := updateRig(t, func(w http.ResponseWriter, r *http.Request) {})
	if w := doJSON(s, http.MethodGet, "/local/update/cli", c, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /local/update/cli 应 405,得到 %d", w.Code)
	}
	if w := doJSON(s, http.MethodPost, "/local/update/cli", c, "not json"); w.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应 400,得到 %d %s", w.Code, w.Body)
	}
}

func TestLocalUpdateCLIAlreadyLatest(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	s, c := updateRig(t, func(w http.ResponseWriter, r *http.Request) {})
	// 显式指定与当前相同的版本:Decide 直接命中 ErrUpToDate,不发网络请求。
	w := doJSON(s, http.MethodPost, "/local/update/cli", c, `{"version":"v0.6.4"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("已是最新应 200,得到 %d %s", w.Code, w.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["ok"] != true || resp["message"] != "本机 CLI 已是最新" {
		t.Fatalf("已是最新响应不对: %s", w.Body)
	}
}

func TestLocalUpdateControlForwards(t *testing.T) {
	var gotAuth, gotBody string
	s, c := updateRig(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/update/control" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, 256)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"from":"v0.6.4","to":"v0.6.5","action":"upgrade"}`))
	})
	w := doJSON(s, http.MethodPost, "/local/update/control", c, `{"version":"v0.6.5"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("转发应透传 202,得到 %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"to":"v0.6.5"`) {
		t.Fatalf("响应正文应透传: %s", w.Body)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBody, "v0.6.5") {
		t.Fatalf("请求体应原样转发: %q", gotBody)
	}
}

func TestLocalUpdateControlNoUpstream(t *testing.T) {
	s, err := New("127.0.0.1:7480", Options{Setup: &fakeSetup{}})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/?k="+s.Key(), nil)
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	if w := doJSON(s, http.MethodPost, "/local/update/control", c, "{}"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("没有控制面应 503,得到 %d %s", w.Code, w.Body)
	}
}

// cliUpdateCall 记下一次 CLIUpdated 回调收到的参数。
type cliUpdateCall struct {
	from, to string
	started  time.Time
	err      error
}

// withCLIUpdatedRig 起一个带 CLIUpdated 回调的 ui.Server,回调把每次调用送进
// 返回的通道。控制面只用来完成会话登录,不参与本机升级。
func withCLIUpdatedRig(t *testing.T) (*Server, *http.Cookie, <-chan cliUpdateCall) {
	t.Helper()
	called := make(chan cliUpdateCall, 4)
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(cp.Close)
	up, _ := url.Parse(cp.URL)
	s, err := New("127.0.0.1:7480", Options{
		Upstream: up, Token: "secret-token", Allow: []string{"GET /api"},
		CLIUpdated: func(from, to string, started time.Time, err error) {
			called <- cliUpdateCall{from: from, to: to, started: started, err: err}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/?k="+s.Key(), nil)
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	if c == nil {
		t.Fatal("没拿到会话 cookie")
	}
	return s, c, called
}

// withFakeCLIRelease 把发布件来源指向假服务器、可执行文件指向临时目录里的一份,
// 让 /local/update/cli 真的走完下载与替换。返回假服务器地址。
func withFakeCLIRelease(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("本机自更新测试用的是 shell 脚本,只在类 unix 上跑")
	}
	archive := tarGzWithMPX(t, "#!/bin/sh\necho mpx v0.6.5\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_, _ = w.Write([]byte(`{"tag_name":"v0.6.5"}`))
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			// 旧发布没有清单:走"未校验也继续"的分支。
			http.NotFound(w, r)
		default:
			_, _ = w.Write(archive)
		}
	}))
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "mpx")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho mpx v0.6.4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSrc, oldExe := updateSource, selfExecutable
	updateSource = func() selfupdate.Source {
		return selfupdate.Source{Repo: "test/mpx", APIBase: srv.URL, DownloadBase: srv.URL}
	}
	selfExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { updateSource, selfExecutable = oldSrc, oldExe })
	return srv.URL
}

// 页面发起本机升级成功后,CLIUpdated 要拿到 from/to/开始时间,且 err 为 nil ——
// cli 侧靠它写 update.json,mpx update show 才看得到这次升级。
func TestLocalUpdateCLIReportsSuccess(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	withFakeCLIRelease(t)
	s, c, called := withCLIUpdatedRig(t)

	w := doJSON(s, http.MethodPost, "/local/update/cli", c, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("升级成功应 200,得到 %d %s", w.Code, w.Body)
	}
	select {
	case call := <-called:
		if call.from != "v0.6.4" || call.to != "v0.6.5" {
			t.Errorf("回调的版本 = %s → %s, 想要 v0.6.4 → v0.6.5", call.from, call.to)
		}
		if call.err != nil {
			t.Errorf("成功时 err 应为 nil,得到 %v", call.err)
		}
		if call.started.IsZero() {
			t.Error("回调没带上开始时间")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("升级成功了却没调用 CLIUpdated")
	}
}

// 下载失败时同样要回调,把失败原因带过去:update.json 里记 failed 而不是悄悄不记。
func TestLocalUpdateCLIReportsFailure(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 问最新版就失败。
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	oldSrc := updateSource
	updateSource = func() selfupdate.Source {
		return selfupdate.Source{Repo: "test/mpx", APIBase: srv.URL, DownloadBase: srv.URL}
	}
	t.Cleanup(func() { updateSource = oldSrc })
	s, c, called := withCLIUpdatedRig(t)

	// 显式版本 + 假可执行文件:让流程走到下载那一步再失败。
	exe := filepath.Join(t.TempDir(), "mpx")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldExe := selfExecutable
	selfExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { selfExecutable = oldExe })

	w := doJSON(s, http.MethodPost, "/local/update/cli", c, `{"version":"v0.6.5"}`)
	if w.Code == http.StatusOK {
		t.Fatalf("下载失败不该回 200: %s", w.Body)
	}
	select {
	case call := <-called:
		if call.from != "v0.6.4" || call.to != "v0.6.5" {
			t.Errorf("回调的版本 = %s → %s, 想要 v0.6.4 → v0.6.5", call.from, call.to)
		}
		if call.err == nil {
			t.Error("失败时应把错误带给回调")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("升级失败了却没调用 CLIUpdated")
	}
}

// 已是最新时没换二进制,不算一次升级尝试,不该写状态。
func TestLocalUpdateCLIAlreadyLatestDoesNotReport(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	s, c, called := withCLIUpdatedRig(t)

	w := doJSON(s, http.MethodPost, "/local/update/cli", c, `{"version":"v0.6.4"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("已是最新应 200,得到 %d %s", w.Code, w.Body)
	}
	select {
	case call := <-called:
		t.Fatalf("没升级却回调了: %+v", call)
	default:
	}
}

// applyCLIUpdate 的早退路径:目标版本比当前旧、又不允许降级时,在下载之前就失败,
// 不回调 —— 那不是一次升级尝试,不该覆盖掉 update.json 里上一次的结果。
func TestApplyCLIUpdateRejectedBeforeDownloadNoCallback(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	s, _, called := withCLIUpdatedRig(t)
	_, _, changed, _, code, err := s.applyCLIUpdate(context.Background(), "v0.6.3")
	if err == nil || changed || code != http.StatusBadRequest {
		t.Fatalf("降级应被拒: changed=%v code=%d err=%v", changed, code, err)
	}
	select {
	case call := <-called:
		t.Fatalf("还没开始下载就失败,不该回调: %+v", call)
	default:
	}
}

func TestUIMetaHasCLIVersion(t *testing.T) {
	s, _, _, c := rig(t)
	w := do(s, "GET", "/ui/meta", c)
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["cli_version"]; !ok {
		t.Fatalf("/ui/meta 缺 cli_version: %s", w.Body)
	}
}
