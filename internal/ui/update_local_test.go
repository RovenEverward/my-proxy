package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
