package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const adminPW = "correct horse battery"

// relaxAdmin 与 relaxConnect 一个道理:让登录闸让路,把被测的行为留给用例。
func relaxAdmin(e *env) {
	g := e.s.adminLogin
	g.perIP.rate, g.perIP.burst = 1e9, 1e9
	g.global.rate, g.global.burst = 1e9, 1e9
}

func (e *env) adminLogin(user, pw string) (int, []byte) {
	e.t.Helper()
	return e.request("POST", "/api/admin/login", adminLoginRequest{Username: user, Password: pw}, nil)
}

// adminCookieAuth 把 cookie 头拼出来。
func adminCookieAuth(tok string) map[string]string {
	return map[string]string{"Cookie": adminCookieName + "=" + tok}
}

func TestAdminLoginFlow(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)

	// 没账号:登录明确说没有,不报错、不假装成功。
	code, body := e.adminLogin("ops", adminPW)
	if code != http.StatusForbidden || !strings.Contains(string(body), "还没有管理员账号") {
		t.Fatalf("无账号登录应 403 说明情况,得到 %d %s", code, body)
	}

	// 建号:接口在 requireAuth 里,主钥匙直接过。
	if code, b := e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW}); code != http.StatusCreated {
		t.Fatalf("建号 %d %s", code, b)
	}
	// 同名再 POST 走改名分支,不重复建号。
	code, body = e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: "irrelevant-pw"})
	if code != http.StatusOK || !strings.Contains(string(body), `"created": false`) {
		t.Fatalf("同名应走改名 %d %s", code, body)
	}

	// 口令不对:401 且两个输入都说「不对」,不指出哪个错。
	code, body = e.adminLogin("ops", "wrong horse battery")
	if code != http.StatusUnauthorized || !strings.Contains(string(body), "用户名或密码不对") {
		t.Fatalf("错密码应 401,得到 %d %s", code, body)
	}
	code, body = e.adminLogin("nobody", adminPW)
	if code != http.StatusUnauthorized || !strings.Contains(string(body), "用户名或密码不对") {
		t.Fatalf("错用户名也应 401 同样说法,得到 %d %s", code, body)
	}

	// 正常登录:回 token、种 cookie;cookie 与 Bearer 都是同一会话。
	code, body = e.adminLogin("ops", adminPW)
	if code != http.StatusOK {
		t.Fatalf("登录 %d %s", code, body)
	}
	var lr adminLoginResponse
	_ = json.Unmarshal(body, &lr)
	if lr.Token == "" || time.Until(lr.ExpiresAt) > webSessionTTL {
		t.Fatalf("登录应答不对: %+v", lr)
	}
	// 用 Bearer 与 cookie 两种形态都能过 requireAuth。
	if code, _ := e.request("GET", "/api/nodes", nil, bearer(lr.Token)); code != http.StatusOK {
		t.Fatalf("Bearer 会话应放行,得到 %d", code)
	}
	if code, _ := e.request("GET", "/api/nodes", nil, adminCookieAuth(lr.Token)); code != http.StatusOK {
		t.Fatalf("cookie 会话应放行,得到 %d", code)
	}
	// 拼错的 token 不行。
	if code, _ := e.request("GET", "/api/nodes", nil, adminCookieAuth(lr.Token+"x")); code != http.StatusUnauthorized {
		t.Fatalf("错 token 应 401,得到 %d", code)
	}
}

// kind=cli 是 mpx connect 的形状:发 'cli' 会话、不种 cookie,token 按
// Bearer 用;写错 kind 趁早 400。
func TestAdminLoginKindCLI(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})

	code, body := e.request("POST", "/api/admin/login",
		adminLoginRequest{Username: "ops", Password: adminPW, Kind: "cli"}, nil)
	if code != http.StatusOK {
		t.Fatalf("cli 登录 %d %s", code, body)
	}
	var lr adminLoginResponse
	_ = json.Unmarshal(body, &lr)
	if lr.Token == "" {
		t.Fatal("没回 token")
	}
	if sess, err := e.st.AdminSessionByTokenHash(hashToken(lr.Token), time.Now()); err != nil || sess.Kind != "cli" {
		t.Fatalf("会话 kind 应是 cli: %v %+v", err, sess)
	}
	// Bearer 直接可用。
	if code, _ := e.request("GET", "/api/nodes", nil, bearer(lr.Token)); code != http.StatusOK {
		t.Fatalf("cli 会话 Bearer 应放行,得到 %d", code)
	}
	// cli 登录不种 cookie。
	resp, err := http.Post(e.srv.URL+"/api/admin/login", "application/json",
		strings.NewReader(`{"username":"ops","password":"`+adminPW+`","kind":"cli"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == adminCookieName {
			t.Fatalf("cli 登录不该种 cookie: %+v", c)
		}
	}
	// kind 写错要 400,而不是悄悄当 web。
	code, _ = e.request("POST", "/api/admin/login",
		adminLoginRequest{Username: "ops", Password: adminPW, Kind: "desktop"}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("kind 写错应 400,得到 %d", code)
	}
}

func TestAdminLoginSetsCookie(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	resp, err := http.Post(e.srv.URL+"/api/admin/login", "application/json",
		strings.NewReader(`{"username":"ops","password":"`+adminPW+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录 %d", resp.StatusCode)
	}
	var c *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == adminCookieName {
			c = ck
		}
	}
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("cookie 形状不对: %+v", c)
	}
}

func TestAdminLogoutRevokes(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	code, body := e.adminLogin("ops", adminPW)
	if code != http.StatusOK {
		t.Fatalf("登录 %d %s", code, body)
	}
	var lr adminLoginResponse
	_ = json.Unmarshal(body, &lr)

	// logout 不要凭据也能走,但带上 cookie 时把那枚会话删了。
	if code, _ := e.request("POST", "/api/admin/logout", nil, adminCookieAuth(lr.Token)); code != http.StatusNoContent {
		t.Fatalf("登出 %d", code)
	}
	if code, _ := e.request("GET", "/api/nodes", nil, bearer(lr.Token)); code != http.StatusUnauthorized {
		t.Fatalf("登出后会话应失效,得到 %d", code)
	}
	// 再登出一次也不出错(幂等)。
	if code, _ := e.request("POST", "/api/admin/logout", nil, adminCookieAuth(lr.Token)); code != http.StatusNoContent {
		t.Fatalf("重复登出应 204,得到 %d", code)
	}
}

func TestAdminPasswordRevokesAllSessions(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})

	login := func() string {
		_, body := e.adminLogin("ops", adminPW)
		var lr adminLoginResponse
		_ = json.Unmarshal(body, &lr)
		return lr.Token
	}
	tokA, tokB := login(), login()
	if code, _ := e.request("GET", "/api/nodes", nil, bearer(tokA)); code != http.StatusOK {
		t.Fatalf("tokA 应可用")
	}
	// 主钥匙改密码:免验旧密码(rescue 语义),旧密码传个占位值。
	code, body := e.api("PUT", "/api/admin/password", adminPasswordRequest{OldPassword: "whatever", NewPassword: adminPW + "2"})
	if code != http.StatusOK || !strings.Contains(string(body), `"revoked": 2`) {
		t.Fatalf("改密码 %d %s", code, body)
	}
	// 两枚会话全部吊销。
	for _, tok := range []string{tokA, tokB} {
		if code, _ := e.request("GET", "/api/nodes", nil, bearer(tok)); code != http.StatusUnauthorized {
			t.Fatalf("改密码后会话应全吊销,得到 %d", code)
		}
	}
	// 新密码能登录,旧的不行。
	if code, _ := e.adminLogin("ops", adminPW); code != http.StatusUnauthorized {
		t.Fatalf("旧密码应失效,得到 %d", code)
	}
	if code, _ := e.adminLogin("ops", adminPW+"2"); code != http.StatusOK {
		t.Fatalf("新密码应能登录,得到 %d", code)
	}
}

// 凭会话(非 master)改密码必须带对旧密码;master 免验(rescue)。
func TestAdminPasswordRequiresOldUnlessMaster(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	_, body := e.adminLogin("ops", adminPW)
	var lr adminLoginResponse
	_ = json.Unmarshal(body, &lr)
	sess := bearer(lr.Token)

	// 错旧密码:403。
	if code, b := e.request("PUT", "/api/admin/password", adminPasswordRequest{OldPassword: "nope", NewPassword: adminPW + "3"}, sess); code != http.StatusForbidden {
		t.Fatalf("错旧密码应 403,得到 %d %s", code, b)
	}
	// 会话改:对旧密码 + 新密码,改完自己被吊销。
	code, body := e.request("PUT", "/api/admin/password", adminPasswordRequest{OldPassword: adminPW, NewPassword: adminPW + "3"}, sess)
	if code != http.StatusOK || !strings.Contains(string(body), `"revoked": 1`) {
		t.Fatalf("会话改密码 %d %s", code, body)
	}
	if code, _ := e.request("GET", "/api/nodes", nil, sess); code != http.StatusUnauthorized {
		t.Fatalf("改完本会话应吊销,得到 %d", code)
	}
	if code, _ := e.adminLogin("ops", adminPW+"3"); code != http.StatusOK {
		t.Fatalf("新密码应能登录,得到 %d", code)
	}
}

// 登录连续失败触发锁定:锁定期内连对的密码也 429,且带 Retry-After 与
// locked_until(与 connect 同口径的只增字段)。
func TestAdminLoginLockout(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	for i := 0; i < loginLockAfter; i++ {
		if code, _ := e.adminLogin("ops", "nope nope nope"); code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次应 401,得到 %d", i+1, code)
		}
	}
	resp, err := http.Post(e.srv.URL+"/api/admin/login", "application/json",
		strings.NewReader(`{"username":"ops","password":"`+adminPW+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var ans struct {
		Error       string `json:"error"`
		LockedUntil string `json:"locked_until"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&ans)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" || ans.LockedUntil == "" {
		t.Fatalf("锁定期内应 429 + Retry-After + locked_until,得到 %d %q %+v", resp.StatusCode, resp.Header.Get("Retry-After"), ans)
	}
	// 锁定不影响已有凭据(主钥匙照常)。
	if code, _ := e.api("GET", "/api/nodes", nil); code != http.StatusOK {
		t.Fatalf("锁定不该影响已有凭据,得到 %d", code)
	}
}

func TestAdminLoginPerIPLimit(t *testing.T) {
	e := newEnv(t)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	var codes []int
	for i := 0; i < 6; i++ {
		code, _ := e.adminLogin("ops", "nope nope nope")
		codes = append(codes, code)
	}
	if codes[4] != http.StatusUnauthorized || codes[5] != http.StatusTooManyRequests {
		t.Fatalf("每 IP 前 5 次 401、第 6 次 429,得到 %v", codes)
	}
}

// 跨源浏览器写请求必须被拒:带 Origin 但不同源、Sec-Fetch-Site=cross-site 都拦;
// 同源或没有这些头(CLI)放行。
func TestAdminOriginCheck(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})

	bad := map[string]string{
		"Origin":           "https://evil.example",
		"Sec-Fetch-Site":   "cross-site",
		"Authorization":    "Bearer " + testToken,
		"Content-Type":     "application/json",
		"X-Requested-With": "test",
	}
	if code, _ := e.request("POST", "/api/admin/login", adminLoginRequest{Username: "ops", Password: adminPW}, bad); code != http.StatusForbidden {
		t.Fatalf("跨源登录应 403,得到 %d", code)
	}
	delete(bad, "Sec-Fetch-Site")
	if code, _ := e.request("POST", "/api/admin/login", adminLoginRequest{Username: "ops", Password: adminPW}, bad); code != http.StatusForbidden {
		t.Fatalf("异源 Origin 应 403,得到 %d", code)
	}
	// 同源:scheme 必须一致(httptest 是 http)。
	good := map[string]string{"Origin": e.srv.URL, "Sec-Fetch-Site": "same-origin"}
	if code, _ := e.request("POST", "/api/admin/login", adminLoginRequest{Username: "ops", Password: adminPW}, good); code != http.StatusOK {
		t.Fatalf("同源应放行,得到 %d", code)
	}
	// 无 Origin(CLI):照旧放行。
	if code, _ := e.adminLogin("ops", adminPW); code != http.StatusOK {
		t.Fatalf("无 Origin 应放行,得到 %d", code)
	}
}

// 公网明文来的登录不吃:只能在 TLS 或回环上受理。
func TestAdminLoginRefusesPlaintextFromPublic(t *testing.T) {
	e := newEnv(t)
	relaxAdmin(e)
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	r := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"username":"ops","password":"`+adminPW+`"}`))
	r.RemoteAddr = "203.0.113.9:5000"
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("公网明文登录应 403,得到 %d %s", w.Code, w.Body)
	}
}
