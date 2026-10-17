package ui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// themeRecorder 记录 SetTheme 收到的值。
type themeRecorder struct {
	mu     sync.Mutex
	themes []string
	err    error
}

func (r *themeRecorder) set(theme string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.themes = append(r.themes, theme)
	return r.err
}

func (r *themeRecorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.themes) == 0 {
		return ""
	}
	return r.themes[len(r.themes)-1]
}

func TestThemeCallsBack(t *testing.T) {
	rec := &themeRecorder{}
	s, _, _, c := rig(t)
	s.opts.SetTheme = rec.set
	for _, theme := range []string{"light", "dark", "system"} {
		w := postJSON(s, c, "/local/app/theme", `{"theme":"`+theme+`"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("%s 应 200,得到 %d %s", theme, w.Code, w.Body)
		}
		if rec.last() != theme {
			t.Fatalf("回调应收到 %q,实际 %q", theme, rec.last())
		}
	}
	if len(rec.themes) != 3 {
		t.Fatalf("应调用三次,实际 %v", rec.themes)
	}
}

func TestThemeRejectsBadValue(t *testing.T) {
	rec := &themeRecorder{}
	s, _, _, c := rig(t)
	s.opts.SetTheme = rec.set
	for _, body := range []string{`{"theme":"blue"}`, `{"theme":""}`, `{}`, `bad`} {
		if w := postJSON(s, c, "/local/app/theme", body); w.Code != http.StatusBadRequest {
			t.Errorf("body %s 应 400,得到 %d %s", body, w.Code, w.Body)
		}
	}
	if len(rec.themes) != 0 {
		t.Fatalf("非法值不能回调,实际 %v", rec.themes)
	}
}

func TestThemeNilIs404(t *testing.T) {
	s, _, _, c := rig(t)
	if w := postJSON(s, c, "/local/app/theme", `{"theme":"dark"}`); w.Code != http.StatusNotFound {
		t.Fatalf("没有回调应 404,得到 %d %s", w.Code, w.Body)
	}
}

func TestThemeNeedsSessionAndPost(t *testing.T) {
	s, _, _, c := rig(t)
	s.opts.SetTheme = (&themeRecorder{}).set
	if w := doJSON(s, http.MethodPost, "/local/app/theme", nil, `{"theme":"dark"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("无会话应 401,得到 %d", w.Code)
	}
	if w := postJSON(s, &http.Cookie{Name: "mpx_ui_7480", Value: "forged"}, "/local/app/theme", `{"theme":"dark"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("伪造会话应 401,得到 %d", w.Code)
	}
	if w := do(s, http.MethodGet, "/local/app/theme", c); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405,得到 %d", w.Code)
	}
	// 跨站来源 403(与其它写接口同规矩)。
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7480/local/app/theme", strings.NewReader(`{"theme":"dark"}`))
	r.AddCookie(c)
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("跨站应 403,得到 %d", w.Code)
	}
}

func TestThemeCallbackErrorIs500(t *testing.T) {
	rec := &themeRecorder{err: errors.New("没有窗口")}
	s, _, _, c := rig(t)
	s.opts.SetTheme = rec.set
	if w := postJSON(s, c, "/local/app/theme", `{"theme":"dark"}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("回调失败应 500,得到 %d %s", w.Code, w.Body)
	}
}
