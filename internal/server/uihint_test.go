package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// ui_hint 是只增的字段:原来的 error / hint 文字一个字不改(命令行照旧用),
// 界面另取 ui_hint,里面不能出现命令。

func TestWriteErrorHint(t *testing.T) {
	w := httptest.NewRecorder()
	writeErrorHint(w, http.StatusForbidden, "原话:mpx x", "界面说法")
	var m map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusForbidden || m["error"] != "原话:mpx x" || m["ui_hint"] != "界面说法" {
		t.Fatalf("得到 %d %v", w.Code, m)
	}
	w = httptest.NewRecorder()
	writeErrorHint(w, http.StatusBadRequest, "只有原话", "")
	if strings.Contains(w.Body.String(), "ui_hint") {
		t.Fatalf("ui_hint 为空时回答应与 writeError 一样: %s", w.Body)
	}
}

func checkUIHint(t *testing.T, body []byte, wantError string) {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if m["error"] != wantError {
		t.Errorf("error 文字变了:%q", m["error"])
	}
	if m["ui_hint"] == "" || strings.Contains(m["ui_hint"], "mpx ") {
		t.Errorf("ui_hint 缺失或带命令:%q", m["ui_hint"])
	}
}

func TestUIHintOnAuthAndLogin(t *testing.T) {
	e := newEnv(t)
	// 过期 / 错凭据。
	code, body := e.request("GET", "/api/users", nil, map[string]string{"Authorization": "Bearer nope"})
	if code != http.StatusUnauthorized {
		t.Fatalf("错 token 应 401,得到 %d", code)
	}
	checkUIHint(t, body, "凭据不正确或会话已过期;重新登录(浏览器)或重新 mpx connect(CLI)")

	// 还没建管理员账号。
	relaxAdmin(e)
	r := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"username":"ops","password":"`+adminPW+`"}`))
	r.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("没建账号登录应 403,得到 %d %s", w.Code, w.Body)
	}
	checkUIHint(t, w.Body.Bytes(), "还没有管理员账号:在服务器上用 mpx admin create 建号(或由 mpx init 建)")

	// 公网明文。
	e.api("POST", "/api/admin/account", adminAccountRequest{Username: "ops", Password: adminPW})
	r = httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"username":"ops","password":"`+adminPW+`"}`))
	r.RemoteAddr = "203.0.113.9:5000"
	w = httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, r)
	checkUIHint(t, w.Body.Bytes(), "账号密码只能经 HTTPS 登录")
}

func TestDoctorUIHint(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	code, raw := e.api("GET", "/api/doctor", nil)
	if code != 200 {
		t.Fatalf("doctor: %d %s", code, raw)
	}
	report := decodeJSON[model.DoctorReport](t, raw)
	seen := 0
	for _, c := range report.Checks {
		if !strings.Contains(c.Hint, "mpx ") {
			continue
		}
		seen++
		if c.UIHint == "" || strings.Contains(c.UIHint, "mpx ") {
			t.Errorf("%s:hint 带命令,ui_hint 应给界面说法,得到 %q", c.Name, c.UIHint)
		}
	}
	if seen == 0 {
		t.Fatalf("空库的自检里应至少有一条带命令的 hint(没节点、没用户): %+v", report.Checks)
	}
}
