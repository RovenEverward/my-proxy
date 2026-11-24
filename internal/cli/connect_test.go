package cli

import (
	"net/http"
	"net/http/httptest"
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

func TestConnectWithTokenSavesProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			t.Errorf("请求打到了 %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"v1"}`))
	}))
	defer cp.Close()

	origProfile := opts.profile
	opts.profile = "work"
	t.Cleanup(func() { opts.profile = origProfile })

	if err := connectWithToken(cp.URL, trustDecision{fp: "SHA256:new"}, "tok-secret"); err != nil {
		t.Fatalf("connectWithToken: %v", err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Profiles["work"]
	if got.Token != "tok-secret" || got.Server != cp.URL || got.Fingerprint != "SHA256:new" || cfg.Current != "work" {
		t.Fatalf("profile 没存对: %+v current=%q", got, cfg.Current)
	}

	if err := connectWithToken(cp.URL, trustDecision{fp: "SHA256:new"}, "wrong"); err == nil {
		t.Fatal("错误 token 应报错")
	}
}
