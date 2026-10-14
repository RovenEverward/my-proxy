package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// issueDevice 直接在库里发一枚设备 token(发放接口在 M3 的 /api/connect)。
func (e *env) issueDevice(name string) (int64, string) {
	e.t.Helper()
	tok, err := newDeviceToken()
	if err != nil {
		e.t.Fatal(err)
	}
	d, err := e.st.CreateDevice(name, hashToken(tok), time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return d.ID, tok
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func TestDeviceTokenAuthListAndRevoke(t *testing.T) {
	e := newEnv(t)
	idA, tokA := e.issueDevice("laptop")
	idB, tokB := e.issueDevice("desktop")

	if code, _ := e.request("GET", "/api/nodes", nil, bearer(tokA)); code != http.StatusOK {
		t.Fatalf("设备 token 应能访问,得到 %d", code)
	}

	code, body := e.request("GET", "/api/devices", nil, bearer(tokA))
	if code != http.StatusOK {
		t.Fatalf("列设备 %d %s", code, body)
	}
	ds := decodeJSON[[]model.Device](t, body)
	if len(ds) != 2 || !ds[0].Current || ds[1].Current || ds[0].LastUsedAt == nil || ds[0].LastIP == "" {
		t.Fatalf("列表不对: %+v", ds)
	}

	// 用 A 吊销 B:B 立刻失效,A 照常。
	if code, _ := e.request("DELETE", fmt.Sprintf("/api/devices/%d", idB), nil, bearer(tokA)); code != http.StatusNoContent {
		t.Fatalf("吊销 %d", code)
	}
	if code, _ := e.request("GET", "/api/nodes", nil, bearer(tokB)); code != http.StatusUnauthorized {
		t.Fatalf("吊销后应 401,得到 %d", code)
	}
	if code, _ := e.request("DELETE", fmt.Sprintf("/api/devices/%d", idB), nil, bearer(tokA)); code != http.StatusNotFound {
		t.Fatalf("重复吊销应 404,得到 %d", code)
	}
	// 管理 token 是主钥匙,不在列表里,永远有效。
	code, body = e.api("GET", "/api/devices", nil)
	ds = decodeJSON[[]model.Device](t, body)
	if code != http.StatusOK || len(ds) != 1 || ds[0].ID != idA || ds[0].Current {
		t.Fatalf("主钥匙看到的列表不对: %d %+v", code, ds)
	}
}

// 撞 token 必须吃限流:限流包在鉴权外层。
func TestWrongTokensAreRateLimited(t *testing.T) {
	e := newEnv(t)
	e.s.apiLimiter.rate, e.s.apiLimiter.burst = 0.001, 3
	var got []int
	for i := 0; i < 5; i++ {
		code, _ := e.request("GET", "/api/nodes", nil, bearer("wrong"))
		got = append(got, code)
	}
	if got[0] != http.StatusUnauthorized || got[4] != http.StatusTooManyRequests {
		t.Fatalf("要先 401 后 429,得到 %v", got)
	}
}
