package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) last() *http.Request { s.mu.Lock(); defer s.mu.Unlock(); return s.reqs[len(s.reqs)-1] }
func (s *seen) count() int          { s.mu.Lock(); defer s.mu.Unlock(); return len(s.reqs) }

// rig 起一个假控制面和一个 ui.Server,返回已登录的 cookie。
func rig(t *testing.T, methods ...string) (*Server, *httptest.Server, *seen, *http.Cookie) {
	t.Helper()
	sn := &seen{}
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sn.mu.Lock()
		sn.reqs = append(sn.reqs, r.Clone(r.Context()))
		sn.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "cp", Value: "x"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(cp.Close)
	up, _ := url.Parse(cp.URL)
	if len(methods) == 0 {
		methods = []string{"GET"}
	}
	s, err := New("127.0.0.1:7480", Options{Upstream: up, Token: "secret-token", Methods: methods})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/?k="+s.Key(), nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("登录应 303,得到 %d %s", w.Code, w.Body)
	}
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Name != "mpx_ui_7480" {
		t.Fatalf("cookie 不对: %+v", c)
	}
	return s, cp, sn, c
}

func do(s *Server, method, path string, c *http.Cookie, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7480"+path, strings.NewReader("{}"))
	if c != nil {
		r.AddCookie(c)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestLoginKeyIsOneTime(t *testing.T) {
	s, _, _, c := rig(t)
	if s.Key() != "" {
		t.Fatal("用过的 key 应该清空")
	}
	// 没 cookie 再拿旧链接:拒绝
	if w := do(s, "GET", "/?k=whatever", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("旧 key 应 401,得到 %d", w.Code)
	}
	// 已登录的浏览器点旧链接:放回首页
	if w := do(s, "GET", "/?k=whatever", c); w.Code != http.StatusSeeOther {
		t.Fatalf("已登录时应 303,得到 %d", w.Code)
	}
}

func TestNeedsSession(t *testing.T) {
	s, _, sn, _ := rig(t)
	for _, p := range []string{"/", "/app.js", "/api/status", "/ui/meta"} {
		if w := do(s, "GET", p, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s 无会话应 401,得到 %d", p, w.Code)
		}
	}
	if w := do(s, "GET", "/api/status", &http.Cookie{Name: "mpx_ui_7480", Value: "forged"}); w.Code != http.StatusUnauthorized {
		t.Errorf("伪造 cookie 应 401,得到 %d", w.Code)
	}
	if sn.count() != 0 {
		t.Fatal("未登录的请求不能到控制面")
	}
}

func TestHostCheck(t *testing.T) {
	s, _, sn, c := rig(t)
	for _, host := range []string{"evil.example:7480", "127.0.0.1:9999", "192.168.1.2:7480"} {
		r := httptest.NewRequest("GET", "http://"+host+"/api/status", nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusMisdirectedRequest {
			t.Errorf("Host %s 应 421,得到 %d", host, w.Code)
		}
	}
	if sn.count() != 0 {
		t.Fatal("Host 不对的请求不能到控制面")
	}
}

func TestProxyInjectsTokenAndStripsBrowserCredentials(t *testing.T) {
	s, _, sn, c := rig(t)
	w := do(s, "GET", "/api/users?limit=2", c, "Authorization", "Bearer from-page")
	if w.Code != 200 {
		t.Fatalf("转发应 200,得到 %d %s", w.Code, w.Body)
	}
	r := sn.last()
	if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if r.Header.Get("Cookie") != "" {
		t.Fatal("浏览器 cookie 不能转给控制面")
	}
	if r.URL.Path != "/api/users" || r.URL.RawQuery != "limit=2" {
		t.Fatalf("路径不对: %s", r.URL)
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("控制面的 Set-Cookie 不能透给浏览器")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("API 响应应 no-store")
	}
	if strings.Contains(w.Body.String(), "secret-token") {
		t.Fatal("token 出现在响应里")
	}
}

func TestMethodAllowlist(t *testing.T) {
	s, _, sn, c := rig(t, "GET")
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		w := do(s, m, "/api/users", c, "Origin", "http://127.0.0.1:7480")
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s 应 405,得到 %d", m, w.Code)
		}
	}
	if sn.count() != 0 {
		t.Fatal("没放行的方法不能到控制面")
	}
}

func TestOriginCheck(t *testing.T) {
	s, _, sn, c := rig(t, "GET", "POST")
	cases := []struct {
		method string
		hdr    []string
		want   int
	}{
		{"POST", nil, http.StatusForbidden}, // 写请求必须带 Origin
		{"POST", []string{"Origin", "http://evil.example"}, http.StatusForbidden},
		{"POST", []string{"Origin", "http://127.0.0.1:7480", "Sec-Fetch-Site", "cross-site"}, http.StatusForbidden},
		{"GET", []string{"Origin", "http://evil.example"}, http.StatusForbidden},
		{"GET", []string{"Sec-Fetch-Site", "same-site"}, http.StatusForbidden},
		{"POST", []string{"Origin", "http://127.0.0.1:7480", "Sec-Fetch-Site", "same-origin"}, http.StatusOK},
		{"GET", nil, http.StatusOK},
	}
	for _, tc := range cases {
		if w := do(s, tc.method, "/api/users", c, tc.hdr...); w.Code != tc.want {
			t.Errorf("%s %v: 得到 %d,应 %d", tc.method, tc.hdr, w.Code, tc.want)
		}
	}
	if sn.count() != 2 {
		t.Fatalf("只有两条合法请求该到控制面,实际 %d", sn.count())
	}
}

func TestSecurityHeadersAndAssets(t *testing.T) {
	s, _, _, c := rig(t)
	w := do(s, "GET", "/", c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/app.js") {
		t.Fatalf("首页: %d", w.Code)
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP 缺 %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("CSP 不能放行行内脚本或 eval")
	}
	for _, p := range []string{"/app.js", "/app.css", "/boot.js", "/fonts/MonaSansVF.woff2"} {
		if w := do(s, "GET", p, c); w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := do(s, "GET", "/nope", c); w.Code != 404 {
		t.Errorf("不存在的路径应 404,得到 %d", w.Code)
	}
	// 页面里不能有行内脚本(CSP 会拦,写了也白写)
	idx, _ := assets.ReadFile("assets/index.html")
	if strings.Contains(string(idx), "<script>") || strings.Contains(string(idx), " onclick=") {
		t.Error("index.html 里有行内脚本")
	}
	js, _ := assets.ReadFile("assets/app.js")
	if strings.Contains(string(js), "onclick=") {
		t.Error("app.js 生成了行内事件处理器")
	}
}

func TestMetaHasNoToken(t *testing.T) {
	s, _, _, c := rig(t)
	w := do(s, "GET", "/ui/meta", c)
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "secret-token") {
		t.Fatal("meta 泄露了 token")
	}
	if m["methods"] == nil || m["server"] == "" {
		t.Fatalf("meta: %s", w.Body)
	}
}

func TestUpstreamDownIs502(t *testing.T) {
	s, cp, _, c := rig(t)
	cp.Close()
	w := do(s, "GET", "/api/status", c)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "连不上控制面") {
		t.Fatalf("得到 %d %s", w.Code, w.Body)
	}
}
