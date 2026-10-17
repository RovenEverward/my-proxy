package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

const goodPW = "correct horse battery"

func (e *env) connectAs(pw, name string) (int, connectResponse) {
	e.t.Helper()
	code, body := e.request("POST", "/api/connect", connectRequest{Password: pw, DeviceName: name}, nil)
	var r connectResponse
	_ = json.Unmarshal(body, &r)
	return code, r
}

func relaxConnect(e *env) {
	g := e.s.connect
	g.perIP.rate, g.perIP.burst = 1e9, 1e9
	g.global.rate, g.global.burst = 1e9, 1e9
}

func TestConnectFlow(t *testing.T) {
	e := newEnv(t)
	relaxConnect(e)

	if code, _ := e.connectAs(goodPW, "mac"); code != http.StatusForbidden {
		t.Fatalf("没设密码应 403,得到 %d", code)
	}
	if code, _ := e.api("PUT", "/api/device-password", devicePasswordRequest{Password: "short"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("短密码应 422,得到 %d", code)
	}
	if code, b := e.api("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW}); code != http.StatusOK {
		t.Fatalf("设密码 %d %s", code, b)
	}
	if code, _ := e.request("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW}, nil); code != http.StatusUnauthorized {
		t.Fatalf("改密码必须已接入,得到 %d", code)
	}
	if code, _ := e.connectAs("wrong password!!", "mac"); code != http.StatusUnauthorized {
		t.Fatalf("错密码应 401,得到 %d", code)
	}
	if code, _ := e.connectAs(goodPW, "  "); code != http.StatusBadRequest {
		t.Fatalf("空设备名应 400,得到 %d", code)
	}
	code, r := e.connectAs(goodPW, "mac")
	if code != http.StatusCreated || r.Token == "" || r.Name != "mac" {
		t.Fatalf("接入 %d %+v", code, r)
	}
	if code, _ := e.request("GET", "/api/nodes", nil, bearer(r.Token)); code != http.StatusOK {
		t.Fatalf("新 token 应可用,得到 %d", code)
	}
	code, body := e.request("GET", "/api/status", nil, bearer(r.Token))
	st := decodeJSON[model.ControlStatus](t, body)
	if code != 200 || st.Devices != 1 || !st.DevicePasswordSet {
		t.Fatalf("status: %d %+v", code, st)
	}

	// 改密码不吊销已接入的设备;带 revoke_all 才吊销。
	e.request("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW + "2"}, bearer(r.Token))
	if code, _ := e.request("GET", "/api/nodes", nil, bearer(r.Token)); code != http.StatusOK {
		t.Fatalf("改密码后旧设备应仍可用,得到 %d", code)
	}
	e.api("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW, RevokeAll: true})
	if code, _ := e.request("GET", "/api/nodes", nil, bearer(r.Token)); code != http.StatusUnauthorized {
		t.Fatalf("revoke_all 后应 401,得到 %d", code)
	}

	// 关闭后不能再接入。
	if code, _ := e.api("DELETE", "/api/device-password", nil); code != http.StatusNoContent {
		t.Fatalf("关闭 %d", code)
	}
	if code, _ := e.connectAs(goodPW, "pc"); code != http.StatusForbidden {
		t.Fatalf("关闭后应 403,得到 %d", code)
	}
}

func TestConnectLockout(t *testing.T) {
	e := newEnv(t)
	relaxConnect(e)
	e.api("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW})
	for i := 0; i < connectLockAfter; i++ {
		if code, _ := e.connectAs("nope nope nope", "x"); code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次应 401,得到 %d", i+1, code)
		}
	}
	if code, _ := e.connectAs(goodPW, "x"); code != http.StatusTooManyRequests {
		t.Fatalf("锁定后连对的密码也应 429,得到 %d", code)
	}
	// 已接入的(主 token)不受影响。
	if code, _ := e.api("GET", "/api/nodes", nil); code != http.StatusOK {
		t.Fatalf("锁定不该影响已有 token,得到 %d", code)
	}
}

func TestConnectPerIPLimit(t *testing.T) {
	e := newEnv(t)
	e.api("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW})
	var codes []int
	for i := 0; i < 6; i++ {
		c, _ := e.connectAs("nope nope nope", "x")
		codes = append(codes, c)
	}
	if codes[4] != http.StatusUnauthorized || codes[5] != http.StatusTooManyRequests {
		t.Fatalf("每 IP 前 5 次 401、第 6 次 429,得到 %v", codes)
	}
}

func TestConnectRefusesPlaintextFromPublic(t *testing.T) {
	e := newEnv(t)
	relaxConnect(e)
	e.api("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW})
	r := httptest.NewRequest("POST", "/api/connect", strings.NewReader(`{"password":"`+goodPW+`","device_name":"x"}`))
	r.RemoteAddr = "203.0.113.9:5000"
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("公网明文接入应 403,得到 %d %s", w.Code, w.Body)
	}
}

func TestDevicePasswordHashRoundTrip(t *testing.T) {
	h, err := hashDevicePassword(goodPW)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Fatalf("PHC 形状不对: %s", h)
	}
	if ok, err := verifyDevicePassword(goodPW, h); !ok || err != nil {
		t.Fatalf("对的密码没过: %v", err)
	}
	if ok, _ := verifyDevicePassword(goodPW+"x", h); ok {
		t.Fatal("错的密码过了")
	}
}

// 密码不对要告诉人怎么重设;第 20 次错把锁触发时,回答里就要说清已经锁上、
// 锁到什么时候;锁定期内的 429 带 Retry-After、locked_until 与「N 分钟后再试」。
// error 原话不变(只增)。
func TestConnectWrongAndLockedHints(t *testing.T) {
	e := newEnv(t)
	relaxConnect(e)
	e.api("PUT", "/api/device-password", devicePasswordRequest{Password: goodPW})
	type ans struct {
		Error       string `json:"error"`
		UIHint      string `json:"ui_hint"`
		LockedUntil string `json:"locked_until"`
	}
	var last ans
	for i := 0; i < connectLockAfter; i++ {
		_, body := e.request("POST", "/api/connect", connectRequest{Password: "nope nope nope", DeviceName: "x"}, nil)
		last = ans{}
		_ = json.Unmarshal(body, &last)
		if last.Error != "设备密码不对" {
			t.Fatalf("第 %d 次 error 应保持原话,得到 %q", i+1, last.Error)
		}
		if i == 0 && (!strings.Contains(last.UIHint, "设置 → 设备") || last.LockedUntil != "") {
			t.Fatalf("密码不对应提示怎么重设: %+v", last)
		}
	}
	if !strings.Contains(last.UIHint, "已锁定") || !strings.Contains(last.UIHint, "15 分钟") || last.LockedUntil == "" {
		t.Fatalf("触发锁定的那次要说清已锁定: %+v", last)
	}
	resp, err := http.Post(e.srv.URL+"/api/connect", "application/json", strings.NewReader(`{"password":"`+goodPW+`","device_name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var locked ans
	_ = json.NewDecoder(resp.Body).Decode(&locked)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" || locked.LockedUntil == "" ||
		!strings.Contains(locked.UIHint, "分钟后再试") || !strings.HasPrefix(locked.Error, "设备密码连续输错太多次,接入已锁定,") {
		t.Fatalf("锁定期内: %d %s %+v", resp.StatusCode, resp.Header.Get("Retry-After"), locked)
	}
}

func TestWaitText(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "1 秒", 1500 * time.Millisecond: "2 秒", 59 * time.Second: "59 秒", time.Minute: "1 分钟", 14*time.Minute + time.Second: "15 分钟", 15 * time.Minute: "15 分钟"} {
		if got := waitText(d); got != want {
			t.Errorf("waitText(%v)=%q want %q", d, got, want)
		}
	}
}
