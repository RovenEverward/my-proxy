package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeNamedConnect 在 fakeConnect 上补一个 ConnectProfile:模拟实现了
// ConnectNamedRunner 的接入实现(真正的 cli.connectRunner 就是这么实现的)。
type fakeNamedConnect struct {
	fakeConnect
	profileFn func(ctx context.Context, server, fp, password, name, profile string) (*SetupResult, error)
	profiled  []profileCall
}

// profileCall 记下 ConnectProfile 收到的参数,password 由用例自己核对后不外传。
type profileCall struct {
	server, fp, password, name, profile string
}

func (f *fakeNamedConnect) ConnectProfile(ctx context.Context, server, fp, password, name, profile string) (*SetupResult, error) {
	f.mu.Lock()
	f.profiled = append(f.profiled, profileCall{server, fp, password, name, profile})
	fn := f.profileFn
	f.mu.Unlock()
	if fn == nil {
		return &SetupResult{}, nil
	}
	return fn(ctx, server, fp, password, name, profile)
}

// namedRig 起正常模式的 Server:当前档 alpha(带指纹),Connect 用 fakeNamedConnect,
// ConnectProfile 默认成功并把 upstream 指到新起的假控制面 newCP。返回 newCP 便于
// 断言接入成功后转发真的过去了。
func namedRig(t *testing.T) (*Server, *http.Cookie, *fakeNamedConnect, *httptest.Server) {
	t.Helper()
	mk := func(tag string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authMu.Lock()
			authLast = r.Header.Get("Authorization")
			authMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"who":"`+tag+`"}`)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	cpA, newCP := mk("a"), mk("new")
	upA, _ := url.Parse(cpA.URL)
	upNew, _ := url.Parse(newCP.URL)
	fc := &fakeNamedConnect{}
	fc.profileFn = func(ctx context.Context, server, fp, password, name, profile string) (*SetupResult, error) {
		return &SetupResult{Upstream: upNew, Token: "token-new", Transport: http.DefaultTransport, Profile: profile}, nil
	}
	fp := &fakeProfiles{
		infos: []ProfileInfo{
			{Name: "alpha", Server: cpA.URL, Current: true, Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
			{Name: "beta", Server: "http://127.0.0.1:1"},
		},
		ups:    map[string]*url.URL{"alpha": upA},
		tokens: map[string]string{"alpha": "token-a"},
	}
	s, err := New("127.0.0.1:7480", Options{Upstream: upA, Token: "token-a", Profile: "alpha",
		Allow: []string{"GET /api"}, Profiles: fp, Connect: fc})
	if err != nil {
		t.Fatal(err)
	}
	return s, loginCookie(t, s), fc, newCP
}

// authLast / authMu 帮 namedRig 的假控制面记下最近一次请求的 Authorization。
var (
	authMu   sync.Mutex
	authLast string
)

func TestProfilesConnectProbe(t *testing.T) {
	s, c, fc, _ := namedRig(t)

	w := doJSON(s, "POST", "/local/profiles/connect/probe", c, `{"addr":"203.0.113.10"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("probe 应 200,得到 %d %s", w.Code, w.Body)
	}
	var resp struct {
		Server      string `json:"server"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Server == "" || !strings.HasPrefix(resp.Fingerprint, "SHA256:") {
		t.Fatalf("probe 返回不对: %s", w.Body)
	}
	fc.mu.Lock()
	got := fc.probed
	fc.mu.Unlock()
	if len(got) != 1 || got[0] != "203.0.113.10" {
		t.Fatalf("runner 收到的地址不对: %v", got)
	}

	// 与 /local/profiles 同一套规矩:缺地址 400,GET 405,无会话 401。
	if w := doJSON(s, "POST", "/local/profiles/connect/probe", c, `{"addr":"  "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("空地址应 400,得到 %d %s", w.Code, w.Body)
	}
	if w := do(s, "GET", "/local/profiles/connect/probe", c); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405,得到 %d", w.Code)
	}
	if w := doJSON(s, "POST", "/local/profiles/connect/probe", nil, `{"addr":"x"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("无会话应 401,得到 %d", w.Code)
	}
}

func TestProfilesConnectSwitchesToNew(t *testing.T) {
	s, c, fc, _ := namedRig(t)

	const pw = "device-password-123"
	body := `{"server":"https://203.0.113.10:8080","fingerprint":"SHA256:abc","password":"` + pw + `","name":"office","profile":"gamma"}`
	w := doJSON(s, "POST", "/local/profiles/connect", c, body)
	if w.Code != http.StatusOK {
		t.Fatalf("connect 应 200,得到 %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), pw) {
		t.Fatal("connect 响应泄露了密码")
	}
	var resp struct {
		OK      bool   `json:"ok"`
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.OK || resp.Profile != "gamma" {
		t.Fatalf("connect 返回不对: %s", w.Body)
	}

	// 本进程随即切到新控制面:转发带新 token,meta 的 profile 也换。
	if r := do(s, "GET", "/api/x", c); r.Code != 200 || !strings.Contains(r.Body.String(), `"who":"new"`) {
		t.Fatalf("接入后应转发到新控制面,得到 %d %s", r.Code, r.Body)
	}
	wm := do(s, "GET", "/ui/meta", c)
	var meta map[string]any
	_ = json.Unmarshal(wm.Body.Bytes(), &meta)
	if meta["profile"] != "gamma" {
		t.Fatalf("meta 的 profile 应为 gamma: %s", wm.Body)
	}
	authMu.Lock()
	gotAuth := authLast
	authMu.Unlock()
	if gotAuth != "Bearer token-new" {
		t.Fatalf("转发 Authorization = %q", gotAuth)
	}

	// runner 收到的是页面交上来的那一份,profile 带上。
	fc.mu.Lock()
	calls := fc.profiled
	fc.mu.Unlock()
	if len(calls) != 1 || calls[0].server != "https://203.0.113.10:8080" || calls[0].fp != "SHA256:abc" ||
		calls[0].password != pw || calls[0].name != "office" || calls[0].profile != "gamma" {
		t.Fatalf("runner 收到的参数不对: %+v", calls)
	}
}

func TestProfilesConnectValidation(t *testing.T) {
	s, c, fc, _ := namedRig(t)

	cases := []struct {
		body string
		want string
	}{
		{`{"fingerprint":"SHA256:abc","password":"x","profile":"gamma"}`, "缺少控制面地址"},
		{`{"server":"https://x:8080","password":"x","profile":"gamma"}`, "缺少证书指纹"},
		{`{"server":"https://x:8080","fingerprint":"SHA256:abc","profile":"gamma"}`, "请输入设备密码"},
		{`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x"}`, "起个名字"},
		{`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x","profile":"bad name"}`, "只能用字母"},
		{`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x","profile":"a.b"}`, "只能用字母"},
		{`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x","profile":"` + strings.Repeat("n", 33) + `"}`, "只能用字母"},
		{`not json`, "请求体不是合法的 JSON"},
	}
	for _, tc := range cases {
		if w := doJSON(s, "POST", "/local/profiles/connect", c, tc.body); w.Code != http.StatusBadRequest ||
			!strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("校验 %q 应 400 含 %q,得到 %d %s", tc.body, tc.want, w.Code, w.Body)
		}
	}

	// 名字被占用:409 提示换名,alpha 是已存在的档。
	if w := doJSON(s, "POST", "/local/profiles/connect", c,
		`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x","profile":"alpha"}`); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "已存在") {
		t.Fatalf("重名应 409,得到 %d %s", w.Code, w.Body)
	}

	// 实现方失败:502 原话,本进程仍停在原来的控制面上。
	fc.mu.Lock()
	fc.profileFn = func(ctx context.Context, server, fp, password, name, profile string) (*SetupResult, error) {
		return nil, errors.New("设备密码不对")
	}
	fc.mu.Unlock()
	if w := doJSON(s, "POST", "/local/profiles/connect", c,
		`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x","profile":"gamma"}`); w.Code != http.StatusBadGateway ||
		!strings.Contains(w.Body.String(), "设备密码不对") {
		t.Fatalf("失败应 502 原话,得到 %d %s", w.Code, w.Body)
	}
	if r := do(s, "GET", "/api/x", c); !strings.Contains(r.Body.String(), `"who":"a"`) {
		t.Fatalf("失败后不该切换: %s", r.Body)
	}
}

func TestProfilesConnectUnsupported(t *testing.T) {
	// 只实现了 ConnectRunner(旧实现):404,旧页面照常工作。
	s1, c1 := profileRigWithConnect(t, &fakeConnect{})
	if w := doJSON(s1, "POST", "/local/profiles/connect", c1,
		`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x","profile":"gamma"}`); w.Code != http.StatusNotFound {
		t.Fatalf("无 ConnectProfile 应 404,得到 %d %s", w.Code, w.Body)
	}
	// Connect 整个没给:probe 与 connect 都 404。
	s2, c2 := profileRigWithConnect(t, nil)
	for _, p := range []string{"/local/profiles/connect/probe", "/local/profiles/connect"} {
		if w := doJSON(s2, "POST", p, c2, `{"addr":"x"}`); w.Code != http.StatusNotFound {
			t.Fatalf("无 Connect 时 POST %s 应 404,得到 %d %s", p, w.Code, w.Body)
		}
	}
}

// TestProfilesConnectInSetupMode:首次安装模式(还没连控制面)下也能用 —— 接入
// 第一个控制面本来就是这个流程,与 /local/setup/connect 不冲突:那个存进实现方
// 自己挑的档,这个存进指名的档。
func TestProfilesConnectInSetupMode(t *testing.T) {
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer cp.Close()
	up, _ := url.Parse(cp.URL)
	fc := &fakeNamedConnect{}
	fc.profileFn = func(ctx context.Context, server, fp, password, name, profile string) (*SetupResult, error) {
		return &SetupResult{Upstream: up, Token: "t-new", Transport: http.DefaultTransport, Profile: profile}, nil
	}
	fp := &fakeProfiles{}
	s, err := New("127.0.0.1:7480", Options{Profiles: fp, Connect: fc, Allow: []string{"GET /api"}})
	if err != nil {
		t.Fatal(err)
	}
	c := loginCookie(t, s)
	w := doJSON(s, "POST", "/local/profiles/connect", c,
		`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x","profile":"first"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("setup 模式接入应 200,得到 %d %s", w.Code, w.Body)
	}
	if r := do(s, "GET", "/api/x", c); r.Code != 200 {
		t.Fatalf("接入后转发应 200,得到 %d %s", r.Code, r.Body)
	}
}

// profileRigWithConnect:profileRig 的变体,Connect 用传入的实现(可为 nil)。
func profileRigWithConnect(t *testing.T, connect ConnectRunner) (*Server, *http.Cookie) {
	t.Helper()
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"who":"a"}`)
	}))
	t.Cleanup(cp.Close)
	up, _ := url.Parse(cp.URL)
	fp := &fakeProfiles{
		infos: []ProfileInfo{{Name: "alpha", Server: cp.URL, Current: true}},
		ups:   map[string]*url.URL{"alpha": up}, tokens: map[string]string{"alpha": "token-a"},
	}
	s, err := New("127.0.0.1:7480", Options{Upstream: up, Token: "token-a", Profile: "alpha",
		Allow: []string{"GET /api"}, Profiles: fp, Connect: connect})
	if err != nil {
		t.Fatal(err)
	}
	return s, loginCookie(t, s)
}

// TestMetaFingerprint:meta 里的证书指纹跟着当前档走 —— 设置页靠它显示「当前
// 控制面」的指纹,切换配置档后要跟着变;http 控制面没有指纹,是空。
func TestMetaFingerprint(t *testing.T) {
	const fpAlpha = "SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg"
	fp := &fakeProfiles{
		infos: []ProfileInfo{
			{Name: "alpha", Server: "https://a.example:8080", Current: true, Fingerprint: fpAlpha},
			{Name: "beta", Server: "http://127.0.0.1:1"},
		},
		ups:    map[string]*url.URL{"alpha": {Scheme: "https", Host: "a.example:8080"}, "beta": {Scheme: "http", Host: "127.0.0.1:1"}},
		tokens: map[string]string{"alpha": "token-a", "beta": "token-b"},
	}
	s, err := New("127.0.0.1:7480", Options{Upstream: fp.ups["alpha"], Token: "token-a", Profile: "alpha",
		Allow: []string{"GET /api"}, Profiles: fp})
	if err != nil {
		t.Fatal(err)
	}
	c := loginCookie(t, s)

	meta := func() map[string]any {
		w := do(s, "GET", "/ui/meta", c)
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := meta()["fingerprint"]; got != fpAlpha {
		t.Fatalf("meta 指纹应为当前档 alpha 的,得到 %v", got)
	}
	// 列表里也带上指纹(页面下拉显示前 16 位用),但 token 仍然绝不出现。
	wl := doJSON(s, "POST", "/local/profiles", c, "{}")
	if !strings.Contains(wl.Body.String(), fpAlpha) || strings.Contains(wl.Body.String(), "token-a") {
		t.Fatalf("列表该带指纹、不该带 token: %s", wl.Body)
	}
	// 切到没有指纹的 beta(http):meta 指纹为空。
	if w := doJSON(s, "POST", "/local/profiles/use", c, `{"name":"beta"}`); w.Code != http.StatusOK {
		t.Fatalf("切换应 200,得到 %d %s", w.Code, w.Body)
	}
	if got := meta()["fingerprint"]; got != "" {
		t.Fatalf("beta 无指纹,meta 应为空,得到 %v", got)
	}
	// 切回来又有了。
	if w := doJSON(s, "POST", "/local/profiles/use", c, `{"name":"alpha"}`); w.Code != http.StatusOK {
		t.Fatal("切回 alpha 失败")
	}
	if got := meta()["fingerprint"]; got != fpAlpha {
		t.Fatalf("切回后 meta 指纹应为 alpha 的,得到 %v", got)
	}
}
