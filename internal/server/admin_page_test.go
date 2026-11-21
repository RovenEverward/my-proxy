package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loginToken 走完一遍真实登录拿回会话 token,供 cookie/Bearer 两条路分别带。
func (e *env) loginToken(t *testing.T) string {
	t.Helper()
	code, body := e.adminLogin("ops", adminPW)
	if code != http.StatusOK {
		t.Fatalf("登录 %d %s", code, body)
	}
	var lr adminLoginResponse
	_ = json.Unmarshal(body, &lr)
	if lr.Token == "" {
		t.Fatal("登录应答没有 token")
	}
	return lr.Token
}

func TestAdminPageServes(t *testing.T) {
	e := newEnv(t)

	// /admin 301 到 /admin/;入口与产物都可读,安全头齐全。
	req, err := http.NewRequest("GET", e.srv.URL+"/admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/admin/" {
		t.Fatalf("/admin 应 301 到 /admin/,得到 %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	code, body := e.request("GET", "/admin/", nil, nil)
	if code != http.StatusOK || !strings.Contains(string(body), "<html") {
		t.Fatalf("/admin/ 应伺服 index.html,得到 %d", code)
	}

	// 产物里的哈希文件名给 immutable;入口给 no-cache。
	req, _ = http.NewRequest("GET", e.srv.URL+"/admin/index.html", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Fatalf("缺 CSP: %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("入口缓存头应是 no-cache,得到 %q", got)
	}
	if got := resp.Header.Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Fatalf("缺 CORP: %q", got)
	}

	// 共享资源:flags(两位代码)、styles、fonts 都可读;越界名字 404。
	if code, _ := e.request("GET", "/flags/cn.svg", nil, nil); code != http.StatusOK {
		t.Fatalf("/flags/cn.svg 应 200,得到 %d", code)
	}
	if code, _ := e.request("GET", "/flags/../../etc/passwd.svg", nil, nil); code == http.StatusOK {
		t.Fatal("越界旗名不应伺服")
	}
	if code, _ := e.request("GET", "/styles/tokens.css", nil, nil); code != http.StatusOK {
		t.Fatalf("/styles/tokens.css 应 200,得到 %d", code)
	}
	if code, _ := e.request("GET", "/fonts/MonaSansVF.woff2", nil, nil); code != http.StatusOK {
		t.Fatalf("/fonts/MonaSansVF.woff2 应 200,得到 %d", code)
	}
}

func TestAdminPageTLSGate(t *testing.T) {
	// 直接对 handler 断言门禁:httptest 的 r.TLS 为 nil,RemoteAddr 可造。
	next := adminTLSOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 回环明文放行(SSH 隧道 / mpx init 的场景)。
	r, err := http.NewRequest("GET", "/admin/", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	next.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("回环应放行,得到 %d", w.Code)
	}

	// 非回环明文拒掉(公网 http 上不能伺服管理台)。
	r, _ = http.NewRequest("GET", "/admin/", nil)
	r.RemoteAddr = "8.8.8.8:12345"
	w = httptest.NewRecorder()
	next.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("公网明文应 403,得到 %d", w.Code)
	}
}

func TestAdminMeta(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)

	// 未登录:统一 401,不泄露有没有账号。
	if code, _ := e.request("GET", "/api/admin/meta", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401,得到 %d", code)
	}
	code, body := e.api("GET", "/api/admin/meta", nil)
	if code != http.StatusOK {
		t.Fatalf("meta %d %s", code, body)
	}
	var m adminMeta
	_ = json.Unmarshal(body, &m)
	if m.AdminExists {
		t.Fatalf("未建号时 admin_exists 应为 false: %+v", m)
	}

	// 建号后 admin_exists 与 username 出现。
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	code, body = e.api("GET", "/api/admin/meta", nil)
	_ = json.Unmarshal(body, &m)
	if code != http.StatusOK || !m.AdminExists || m.Username != "ops" {
		t.Fatalf("建号后 meta 不对: %d %+v", code, m)
	}
}

func TestCookieWriteOriginCheck(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	tok := e.loginToken(t)

	host := strings.TrimPrefix(e.srv.URL, "http://")
	cross := func(h map[string]string) map[string]string {
		for k, v := range adminCookieAuth(tok) {
			h[k] = v
		}
		return h
	}

	// 跨源写(Sec-Fetch-Site 报 cross-site):403,凭据还没到查库那步。
	code, _ := e.request("POST", "/api/nodes", map[string]any{"name": "x"}, cross(map[string]string{
		"Sec-Fetch-Site": "cross-site",
	}))
	if code != http.StatusForbidden {
		t.Fatalf("跨源 cookie 写应 403,得到 %d", code)
	}

	// 跨源 Origin:403。
	code, _ = e.request("POST", "/api/nodes", map[string]any{"name": "x"}, cross(map[string]string{
		"Origin": "https://evil.example",
	}))
	if code != http.StatusForbidden {
		t.Fatalf("异源 Origin 应 403,得到 %d", code)
	}

	// 同源写通过(Origin 与 Host 一致;scheme 是 http —— 测试服务不是 TLS)。
	code, _ = e.request("POST", "/api/settings/test-webhook", map[string]any{}, cross(map[string]string{
		"Origin": "http://" + host,
	}))
	if code == http.StatusForbidden {
		t.Fatalf("同源 cookie 写不应被 Origin 闸拦,得到 %d", code)
	}

	// 读请求不受此闸(就算伪造了坏 Origin,读也不会写)。
	if code, _ := e.request("GET", "/api/nodes", nil, cross(map[string]string{"Origin": "https://evil.example"})); code != http.StatusOK {
		t.Fatalf("cookie 读应放行,得到 %d", code)
	}

	// 同一枚会话走 Bearer 不查 Origin(CLI 没有 Origin 一说)。
	code, _ = e.request("POST", "/api/settings/test-webhook", map[string]any{}, map[string]string{
		"Authorization":  "Bearer " + tok,
		"Sec-Fetch-Site": "cross-site",
	})
	if code == http.StatusForbidden {
		t.Fatalf("Bearer 不应被同源闸拦,得到 %d", code)
	}
}
