package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

// TestConnectAndSaveProfile:mpx ui 正常模式「添加控制面」走它。两件事必须守住:
// 存进指名的新档并设为当前;档已存在时报错不落盘 —— default 里好好的 token 不能
// 被冲掉。
func TestConnectAndSaveProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/connect" {
			t.Errorf("请求打到了 %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = strings.NewReader(`{"device_id":7,"name":"pc","token":"tok-new"}`).WriteTo(w)
	}))
	defer cp.Close()

	// 先有 default 档(等价此前 login/connect 存下的)。
	if _, err := saveProfile(Profile{Server: "https://old.example:8080", Token: "tok-old", Fingerprint: "SHA256:old"}); err != nil {
		t.Fatal(err)
	}

	res, err := connectAndSaveProfile(cp.URL, "SHA256:new", "device-password-1234", "pc", "work")
	if err != nil {
		t.Fatalf("connectAndSaveProfile: %v", err)
	}
	if res.Token != "tok-new" {
		t.Fatalf("token 不对: %+v", res)
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
	if _, err := connectAndSaveProfile(cp.URL, "SHA256:new", "device-password-1234", "pc", "work"); err == nil ||
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
