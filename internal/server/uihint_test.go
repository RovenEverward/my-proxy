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

func TestUIHintOnAuthAndConnect(t *testing.T) {
	e := newEnv(t)
	// 吊销 / 错 token。
	code, body := e.request("GET", "/api/users", nil, map[string]string{"Authorization": "Bearer nope"})
	if code != http.StatusUnauthorized {
		t.Fatalf("错 token 应 401,得到 %d", code)
	}
	checkUIHint(t, body, "token 不正确或这台设备已被吊销;重新 mpx connect")

	// 还没设设备密码。
	relaxConnect(e)
	r := httptest.NewRequest("POST", "/api/connect", strings.NewReader(`{"password":"`+goodPW+`","device_name":"x"}`))
	r.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("没设设备密码应 403,得到 %d %s", w.Code, w.Body)
	}
	checkUIHint(t, w.Body.Bytes(), "这个控制面还没设设备密码;在已接入的设备上跑 mpx device password")

	// 公网明文。
	e.api("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW})
	r = httptest.NewRequest("POST", "/api/connect", strings.NewReader(`{"password":"`+goodPW+`","device_name":"x"}`))
	r.RemoteAddr = "203.0.113.9:5000"
	w = httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, r)
	checkUIHint(t, w.Body.Bytes(), "设备密码只能经 HTTPS 发送:用 mpx connect 接入(它走 HTTPS 并核对证书指纹)")
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
