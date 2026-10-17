package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeConnect 是测试用的 ConnectRunner。
type fakeConnect struct {
	mu        sync.Mutex
	probeErr  error
	connectFn func(ctx context.Context, server, fp, password, name string) (*SetupResult, error)
	probed    []string
	connected []connectCall
}

// connectCall 记下 Connect 收到的参数,password 由用例自己核对后不外传。
type connectCall struct {
	server, fp, password, name string
}

func (f *fakeConnect) Probe(ctx context.Context, addr string) (string, string, error) {
	f.mu.Lock()
	f.probed = append(f.probed, addr)
	err := f.probeErr
	f.mu.Unlock()
	if err != nil {
		return "", "", err
	}
	return "https://203.0.113.10:8080", "SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg", nil
}

func (f *fakeConnect) Connect(ctx context.Context, server, fp, password, name string) (*SetupResult, error) {
	f.mu.Lock()
	f.connected = append(f.connected, connectCall{server, fp, password, name})
	fn := f.connectFn
	f.mu.Unlock()
	if fn == nil {
		return &SetupResult{}, nil
	}
	return fn(ctx, server, fp, password, name)
}

// connectRig 起一个带 ConnectRunner 的 Server(setup 模式)并返回已登录的 cookie。
func connectRig(t *testing.T, fc *fakeConnect) (*Server, *http.Cookie) {
	t.Helper()
	s, err := New("127.0.0.1:7480", Options{Setup: &fakeSetup{}, Connect: fc, Allow: []string{"GET /api"}})
	if err != nil {
		t.Fatal(err)
	}
	return s, loginCookie(t, s)
}

// loginCookie 用一次性 key 换会话 cookie。
func loginCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	w := do(s, "GET", "/?k="+s.Key(), nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("登录应 303,得到 %d %s", w.Code, w.Body)
	}
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	return c
}

func TestSetupConnectProbe(t *testing.T) {
	fc := &fakeConnect{}
	s, c := connectRig(t, fc)

	w := doJSON(s, "POST", "/local/setup/connect/probe", c, `{"addr":"203.0.113.10"}`)
	if w.Code != 200 {
		t.Fatalf("probe 应 200,得到 %d %s", w.Code, w.Body)
	}
	var resp struct {
		Server      string `json:"server"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Server != "https://203.0.113.10:8080" || !strings.HasPrefix(resp.Fingerprint, "SHA256:") {
		t.Fatalf("probe 返回不对: %s", w.Body)
	}
	fc.mu.Lock()
	got := fc.probed
	fc.mu.Unlock()
	if len(got) != 1 || got[0] != "203.0.113.10" {
		t.Fatalf("runner 收到的地址不对: %v", got)
	}

	// 缺地址 400;连不上 502 且是原话;GET 不行。
	if w := doJSON(s, "POST", "/local/setup/connect/probe", c, `{"addr":"  "}`); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "请填写控制面地址") {
		t.Fatalf("空地址应 400,得到 %d %s", w.Code, w.Body)
	}
	fc.mu.Lock()
	fc.probeErr = errors.New("拨号超时")
	fc.mu.Unlock()
	if w := doJSON(s, "POST", "/local/setup/connect/probe", c, `{"addr":"x"}`); w.Code != 502 ||
		!strings.Contains(w.Body.String(), "拨号超时") {
		t.Fatalf("probe 失败应 502 原话,得到 %d %s", w.Code, w.Body)
	}
	if w := do(s, "GET", "/local/setup/connect/probe", c); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405,得到 %d", w.Code)
	}
}

func TestSetupConnectSwitchesToNormal(t *testing.T) {
	var authHdr string
	var authMu sync.Mutex
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.Lock()
		authHdr = r.Header.Get("Authorization")
		authMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer cp.Close()
	up, _ := url.Parse(cp.URL)

	const pw = "device-password-123"
	fc := &fakeConnect{}
	fc.connectFn = func(ctx context.Context, server, fp, password, name string) (*SetupResult, error) {
		return &SetupResult{Upstream: up, Token: "connect-token-xyz", Transport: http.DefaultTransport, Profile: "work"}, nil
	}
	s, c := connectRig(t, fc)

	body := `{"server":"https://203.0.113.10:8080","fingerprint":"SHA256:abc","password":"` + pw + `","name":""}`
	w := doJSON(s, "POST", "/local/setup/connect", c, body)
	if w.Code != 200 {
		t.Fatalf("connect 应 200,得到 %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), pw) {
		t.Fatal("connect 响应泄露了密码")
	}
	var resp struct {
		OK      bool   `json:"ok"`
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.OK || resp.Profile != "work" {
		t.Fatalf("connect 返回不对: %s", w.Body)
	}

	// 已切到正常模式:meta 与 /api 转发都按新 upstream 走。
	wm := do(s, "GET", "/ui/meta", c)
	var meta map[string]any
	if err := json.Unmarshal(wm.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["mode"] != "normal" || meta["server"] != cp.URL || meta["profile"] != "work" {
		t.Fatalf("接入后 meta 不对: %s", wm.Body)
	}
	if w := do(s, "GET", "/api/x", c); w.Code != 200 {
		t.Fatalf("转发应 200,得到 %d %s", w.Code, w.Body)
	}
	authMu.Lock()
	gotAuth := authHdr
	authMu.Unlock()
	if gotAuth != "Bearer connect-token-xyz" {
		t.Fatalf("转发 Authorization = %q", gotAuth)
	}

	// 接入成功后再调这两个接口都 409。
	if w := doJSON(s, "POST", "/local/setup/connect/probe", c, `{"addr":"x"}`); w.Code != http.StatusConflict {
		t.Fatalf("接入后 probe 应 409,得到 %d %s", w.Code, w.Body)
	}
	if w := doJSON(s, "POST", "/local/setup/connect", c, body); w.Code != http.StatusConflict {
		t.Fatalf("接入后 connect 应 409,得到 %d %s", w.Code, w.Body)
	}

	// runner 收到的是页面交上来的那一份(name 为空原样传,由实现方取默认)。
	fc.mu.Lock()
	calls := fc.connected
	fc.mu.Unlock()
	if len(calls) != 1 || calls[0].server != "https://203.0.113.10:8080" ||
		calls[0].fp != "SHA256:abc" || calls[0].password != pw || calls[0].name != "" {
		t.Fatalf("runner 收到的参数不对: %+v", calls)
	}
}

func TestSetupConnectValidation(t *testing.T) {
	fc := &fakeConnect{}
	s, c := connectRig(t, fc)

	cases := []struct {
		body string
		want string
	}{
		{`{"fingerprint":"SHA256:abc","password":"x"}`, "缺少控制面地址"},
		{`{"server":"https://x:8080","password":"x"}`, "缺少证书指纹"},
		{`{"server":"https://x:8080","fingerprint":"SHA256:abc"}`, "请输入设备密码"},
		{`not json`, "请求体不是合法的 JSON"},
	}
	for _, tc := range cases {
		if w := doJSON(s, "POST", "/local/setup/connect", c, tc.body); w.Code != 400 ||
			!strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("校验 %q 应 400 含 %q,得到 %d %s", tc.body, tc.want, w.Code, w.Body)
		}
	}
	if w := doJSON(s, "POST", "/local/setup/connect/probe", c, `bad`); w.Code != 400 {
		t.Fatalf("probe 坏 JSON 应 400,得到 %d", w.Code)
	}

	// Connect 失败:502 原话,仍是 setup 模式。
	fc.mu.Lock()
	fc.connectFn = func(ctx context.Context, server, fp, password, name string) (*SetupResult, error) {
		return nil, errors.New("设备密码不对")
	}
	fc.mu.Unlock()
	if w := doJSON(s, "POST", "/local/setup/connect", c,
		`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"wrong-password"}`); w.Code != 502 ||
		!strings.Contains(w.Body.String(), "设备密码不对") {
		t.Fatalf("connect 失败应 502 原话,得到 %d %s", w.Code, w.Body)
	}
	if w := do(s, "GET", "/ui/meta", c); !strings.Contains(w.Body.String(), `"mode":"setup"`) {
		t.Fatalf("失败后应仍是 setup 模式: %s", w.Body)
	}
}

// 正常模式(进程带着控制面地址起)下,接入接口从一开就 409。
func TestSetupConnectConflictInNormalMode(t *testing.T) {
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer cp.Close()
	up, _ := url.Parse(cp.URL)
	fc := &fakeConnect{}
	s, err := New("127.0.0.1:7480", Options{Upstream: up, Token: "t", Allow: []string{"GET /api"},
		Setup: &fakeSetup{}, Connect: fc})
	if err != nil {
		t.Fatal(err)
	}
	c := loginCookie(t, s)
	if w := doJSON(s, "POST", "/local/setup/connect/probe", c, `{"addr":"x"}`); w.Code != http.StatusConflict {
		t.Fatalf("正常模式 probe 应 409,得到 %d %s", w.Code, w.Body)
	}
	if w := doJSON(s, "POST", "/local/setup/connect", c,
		`{"server":"https://x:8080","fingerprint":"SHA256:abc","password":"x"}`); w.Code != http.StatusConflict {
		t.Fatalf("正常模式 connect 应 409,得到 %d %s", w.Code, w.Body)
	}
}
