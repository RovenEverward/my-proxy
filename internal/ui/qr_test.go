package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// qrRig 起一个假控制面,按用户名给 /api/users/<name>/links,并返回已登录的 ui。
func qrRig(t *testing.T) (*Server, *http.Cookie, *seen) {
	t.Helper()
	sn := &seen{}
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sn.mu.Lock()
		sn.reqs = append(sn.reqs, r.Clone(r.Context()))
		sn.mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/api/users/") || !strings.HasSuffix(r.URL.Path, "/links") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/users/"), "/links")
		if name != "alice" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"用户不存在"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"auto":  "https://cp.example.com/sub/token",
			"clash": "https://cp.example.com/sub/token/clash",
		})
	}))
	t.Cleanup(cp.Close)

	up, err := url.Parse(cp.URL)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New("127.0.0.1:7480", Options{Upstream: up, Token: "secret-token"})
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
	if c == nil {
		t.Fatal("没拿到会话 cookie")
	}
	return s, c, sn
}

// TestQRSVGByUsername 验证 ?u= 会带上 token 去控制面取 auto 链接并画码。
func TestQRSVGByUsername(t *testing.T) {
	s, c, sn := qrRig(t)

	w := do(s, "GET", "/local/qr.svg?u=alice", c)
	if w.Code != http.StatusOK {
		t.Fatalf("qr.svg?u=alice 应 200,得到 %d %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if body := w.Body.String(); !strings.HasPrefix(body, "<svg") || !strings.Contains(body, "<path") {
		t.Fatalf("不是二维码 SVG: %.120s", body)
	}
	if sn.count() != 1 {
		t.Fatalf("应只问控制面一次,实际 %d", sn.count())
	}
	req := sn.last()
	if req.URL.Path != "/api/users/alice/links" {
		t.Fatalf("请求路径 = %q", req.URL.Path)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestQRSVGUnknownUser(t *testing.T) {
	s, c, _ := qrRig(t)
	w := do(s, "GET", "/local/qr.svg?u=nobody", c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在的用户应 404,得到 %d %s", w.Code, w.Body)
	}
}

func TestQRSVGUpstreamError(t *testing.T) {
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(cp.Close)
	up, _ := url.Parse(cp.URL)
	s, err := New("127.0.0.1:7480", Options{Upstream: up, Token: "secret-token"})
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
	if w := do(s, "GET", "/local/qr.svg?u=alice", c); w.Code != http.StatusBadGateway {
		t.Fatalf("上游出错应 502,得到 %d %s", w.Code, w.Body)
	}
}

func TestQRSVGDStillWorks(t *testing.T) {
	s, c, sn := qrRig(t)
	w := do(s, "GET", "/local/qr.svg?d="+url.QueryEscape("hello"), c)
	if w.Code != http.StatusOK {
		t.Fatalf("qr.svg?d=hello 应 200,得到 %d %s", w.Code, w.Body)
	}
	if !strings.HasPrefix(w.Body.String(), "<svg") {
		t.Fatal("d 参数没有画成 SVG")
	}
	if sn.count() != 0 {
		t.Fatal("d 参数不应访问控制面")
	}
}
