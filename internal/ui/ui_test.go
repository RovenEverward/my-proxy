package ui

import (
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/html"
)

type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) last() *http.Request { s.mu.Lock(); defer s.mu.Unlock(); return s.reqs[len(s.reqs)-1] }
func (s *seen) count() int          { s.mu.Lock(); defer s.mu.Unlock(); return len(s.reqs) }

// rig 起一个假控制面和一个 ui.Server,返回已登录的 cookie。
func rig(t *testing.T, allow ...string) (*Server, *httptest.Server, *seen, *http.Cookie) {
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
	if len(allow) == 0 {
		allow = []string{"GET /api"}
	}
	s, err := New("127.0.0.1:7480", Options{Upstream: up, Token: "secret-token", Allow: allow})
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
	s, _, sn, c := rig(t, "GET /api", "PUT /api/users", "POST /api/users")
	o := []string{"Origin", "http://127.0.0.1:7480"}
	for _, tc := range []struct {
		m, p string
		ok   bool
	}{
		{"PUT", "/api/users/alice", true},
		{"POST", "/api/users", true},
		{"POST", "/api/users/alice/reset-traffic", true},
		{"DELETE", "/api/users/alice", false},
		{"PUT", "/api/usersx", false},
		{"PUT", "/api/nodes/1", false},
		{"POST", "/api/restore", false},
		{"PATCH", "/api/users/alice", false},
	} {
		w := do(s, tc.m, tc.p, c, o...)
		if got := w.Code != http.StatusMethodNotAllowed; got != tc.ok {
			t.Errorf("%s %s: 得到 %d,放行应为 %v", tc.m, tc.p, w.Code, tc.ok)
		}
	}
	if sn.count() != 3 {
		t.Fatalf("应只有 3 条到控制面,实际 %d", sn.count())
	}
	if _, err := New("127.0.0.1:1", Options{Upstream: &url.URL{Scheme: "http", Host: "x"}, Allow: []string{"GET /"}}); err == nil {
		t.Fatal("放行 /api 之外的路径应该报错")
	}
}

func TestOriginCheck(t *testing.T) {
	s, _, sn, c := rig(t, "GET /api", "POST /api/users")
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
	if w.Code != http.StatusOK {
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
	checked := make(map[string]bool)
	checkAsset := func(assetPath string) {
		if checked[assetPath] {
			return
		}
		checked[assetPath] = true
		response := do(s, "GET", assetPath, c)
		if response.Code != http.StatusOK {
			t.Errorf("页面资产 %s: %d", assetPath, response.Code)
			return
		}
		parsed, err := url.Parse(assetPath)
		if err != nil {
			t.Fatal(err)
		}
		mediaType, _, err := mime.ParseMediaType(response.Header().Get("Content-Type"))
		if err != nil {
			t.Errorf("%s 的 Content-Type 无效: %v", assetPath, err)
			return
		}
		switch path.Ext(parsed.Path) {
		case ".js", ".mjs":
			if mediaType != "text/javascript" && mediaType != "application/javascript" {
				t.Errorf("%s 要以 JavaScript 返回,实际 %s", assetPath, mediaType)
			}
		case ".css":
			if mediaType != "text/css" {
				t.Errorf("%s 要以 CSS 返回,实际 %s", assetPath, mediaType)
			}
		}
	}
	for _, entryPath := range []string{"/", "/components.html"} {
		response := do(s, "GET", entryPath, c)
		if response.Code != http.StatusOK {
			t.Fatalf("页面 %s: %d", entryPath, response.Code)
		}
		base := &url.URL{Scheme: "http", Host: "127.0.0.1:7480", Path: entryPath}
		tokens := html.NewTokenizer(strings.NewReader(response.Body.String()))
		scripts, styles := 0, 0
		for {
			kind := tokens.Next()
			if kind == html.ErrorToken {
				if err := tokens.Err(); err != io.EOF {
					t.Fatalf("读取 %s 的 HTML: %v", entryPath, err)
				}
				break
			}
			if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
				continue
			}
			tag := tokens.Token()
			attributes := make(map[string]string)
			for _, attribute := range tag.Attr {
				attributes[attribute.Key] = attribute.Val
				if strings.HasPrefix(attribute.Key, "on") {
					t.Errorf("%s 的 %s 有行内事件 %s", entryPath, tag.Data, attribute.Key)
				}
			}
			var reference string
			switch tag.Data {
			case "script":
				scripts++
				reference = attributes["src"]
				if strings.TrimSpace(reference) == "" {
					t.Errorf("%s 有行内脚本", entryPath)
					continue
				}
			case "link":
				reference = attributes["href"]
				if attributes["rel"] == "stylesheet" {
					styles++
				}
			default:
				continue
			}
			if reference == "" {
				continue
			}
			ref, err := url.Parse(reference)
			if err != nil {
				t.Errorf("%s 的资产引用 %q 无效: %v", entryPath, reference, err)
				continue
			}
			resolved := base.ResolveReference(ref)
			if resolved.Scheme != base.Scheme || resolved.Host != base.Host {
				t.Errorf("%s 引用了非同源资产 %q", entryPath, reference)
				continue
			}
			checkAsset(resolved.RequestURI())
		}
		if scripts == 0 || styles == 0 {
			t.Errorf("%s 必须引用产品脚本和样式,实际 %d 个脚本、%d 份样式", entryPath, scripts, styles)
		}
	}
	// Include split chunks, even when an entry uses a dynamic import.
	if err := fs.WalkDir(assets, "assets", func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (path.Ext(file) == ".js" || path.Ext(file) == ".mjs" || path.Ext(file) == ".css") {
			checkAsset(strings.TrimPrefix(file, "assets"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, assetPath := range []string{"/styles/tokens.css", "/icons/lucide/house.svg", "/fonts/MonaSansVF.woff2", "/flags/jp.svg"} {
		checkAsset(assetPath)
	}
	if w := do(s, "GET", "/nope", c); w.Code != http.StatusNotFound {
		t.Errorf("不存在的路径应 404,得到 %d", w.Code)
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
	if m["allow"] == nil || m["server"] == "" {
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
