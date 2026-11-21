package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestControlURL(t *testing.T) {
	ok := map[string]string{
		"203.0.113.10":                "https://203.0.113.10:8080",
		"203.0.113.10:9443":           "https://203.0.113.10:9443",
		" cp.example.net ":            "https://cp.example.net:8080",
		"https://cp.example.net:8080": "https://cp.example.net:8080",
		"https://cp.example.net":      "https://cp.example.net:8080",
		"[2001:db8::1]:8080":          "https://[2001:db8::1]:8080",
		"2001:db8::1":                 "https://[2001:db8::1]:8080",
	}
	for in, want := range ok {
		got, _, err := controlURL(in)
		if err != nil || got != want {
			t.Errorf("controlURL(%q) = %q, %v;want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"http://203.0.113.10:8080", "", "https://"} {
		if _, _, err := controlURL(bad); err == nil {
			t.Errorf("controlURL(%q) 应当报错", bad)
		}
	}
}

// UC-26:mpx connect 登录的是 /api/admin/login,请求体必须带 kind=cli(不种
// cookie 的 CLI 会话);token 落进指名的控制面档。
func TestConnectAndSaveProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	var gotBody map[string]string
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/admin/login" {
			t.Errorf("请求打到了 %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = strings.NewReader(`{"token":"tok-new","expires_at":"2030-01-01T00:00:00Z"}`).WriteTo(w)
	}))
	defer cp.Close()

	// 先有 default 档(等价此前 login/connect 存下的)。
	if _, err := saveProfile(Profile{Server: "https://old.example:8080", Token: "tok-old", Fingerprint: "SHA256:old"}); err != nil {
		t.Fatal(err)
	}

	res, err := connectAndSaveProfile(cp.URL, "SHA256:new", "admin", "admin-password-123", "work")
	if err != nil {
		t.Fatalf("connectAndSaveProfile: %v", err)
	}
	if res.Token != "tok-new" {
		t.Fatalf("token 不对: %+v", res)
	}
	if gotBody["username"] != "admin" || gotBody["password"] != "admin-password-123" || gotBody["kind"] != "cli" {
		t.Fatalf("登录请求体不对: %+v", gotBody)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["work"]; got.Token != "tok-new" || got.Server != cp.URL || got.Fingerprint != "SHA256:new" {
		t.Fatalf("work 档没存对: %+v", got)
	}
	if cfg.Current != "work" {
		t.Fatalf("current 应为 work,得到 %q", cfg.Current)
	}
	if got := cfg.Profiles["default"]; got.Token != "tok-old" {
		t.Fatalf("default 档被动了: %+v", got)
	}

	// 重名报错,已有的档原样保留。
	if _, err := connectAndSaveProfile(cp.URL, "SHA256:new", "admin", "admin-password-123", "work"); err == nil ||
		!strings.Contains(err.Error(), "已存在") {
		t.Fatalf("重名应报错,得到 %v", err)
	}
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["work"]; got.Token != "tok-new" {
		t.Fatalf("重名失败后 work 档不该被改: %+v", got)
	}
}

// 登录撞上锁定:只发一次(不干等 Retry-After),说出本机几点解锁、还要等多久;
// 密码不对:说出忘了密码怎么救援。窗口用 UIHint,不提命令。
func TestPostAdminLoginLockedAndWrong(t *testing.T) {
	calls := 0
	until := time.Now().Add(10*time.Minute + 5*time.Second).UTC().Format(time.RFC3339)
	locked := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if locked {
			w.Header().Set("Retry-After", "605")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"用户名或密码连续输错太多次,登录已锁定,10m0s后再试","locked_until":"` + until + `"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"用户名或密码不对"}`))
	}))
	defer srv.Close()
	c := &Client{server: srv.URL, http: srv.Client()}
	start := time.Now()
	_, err := postAdminLogin(c, "admin", "pw")
	if time.Since(start) > 2*time.Second || calls != 1 {
		t.Fatalf("锁定不该重试/干等: calls=%d %v", calls, time.Since(start))
	}
	var ae *APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.Message, "解锁") || !strings.Contains(ae.Message, "11 分钟") ||
		!strings.Contains(ae.Message, "mpx admin password") || strings.Contains(ae.UIHint, "mpx ") {
		t.Fatalf("锁定说法: %v / %+v", err, ae)
	}
	locked = false
	_, err = postAdminLogin(c, "admin", "pw")
	if !errors.As(err, &ae) || !strings.Contains(ae.Message, "mpx admin password") ||
		strings.Contains(ae.UIHint, "mpx ") || ae.StatusCode != http.StatusUnauthorized {
		t.Fatalf("密码不对说法: %v / %+v", err, ae)
	}
}
