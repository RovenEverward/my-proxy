package server

import (
	"net/http"
	"strings"
	"testing"
)

// UC-26:设备模型整面退役。这里只钉一件事:旧的设备接口与凭据不再有任何
// 形状能混进来 —— 不是 401 装死,就是 404,绝不会被当成「还有效」。

func TestDeviceEndpointsAreGone(t *testing.T) {
	e := newEnv(t)
	for _, tc := range [][2]string{
		{"POST", "/api/connect"},
		{"GET", "/api/devices"},
		{"DELETE", "/api/devices/1"},
		{"PUT", "/api/device-password"},
		{"DELETE", "/api/device-password"},
	} {
		code, body := e.api(tc[0], tc[1], nil)
		// 带主钥匙的命中鉴权后路由:没挂上就是 404。POST /api/connect 挂在
		// 鉴权外、现在也没了,同样是 404。
		if code != http.StatusNotFound {
			t.Errorf("%s %s 应 404,得到 %d %s", tc[0], tc[1], code, body)
		}
	}
}

// 老库里的设备 token 随表一起没了;就算手里有旧 token,它也只是「错 token」。
func TestDeviceTokenRejected(t *testing.T) {
	e := newEnv(t)
	code, _ := e.request("GET", "/api/nodes", nil, bearer("old-device-token"))
	if code != http.StatusUnauthorized {
		t.Fatalf("旧设备 token 应 401,得到 %d", code)
	}
}

// 老 CLI 打 /api/connect 不带凭据,会撞上 /api/ 前缀 mux 的 requireAuth,
// 拿到 401「凭据不正确或会话已过期;重新 mpx connect」—— 比裸 404 更能指路:
// 升过级的老 CLI 照提示重跑新版 mpx connect 就走通了。
func TestConnectGone401(t *testing.T) {
	e := newEnv(t)
	code, body := e.request("POST", "/api/connect",
		strings.NewReader(`{"password":"whatever-1234","device_name":"x"}`), nil)
	if code != http.StatusUnauthorized || !strings.Contains(string(body), "mpx connect") {
		t.Fatalf("/api/connect 应 401 并指路重连,得到 %d %s", code, body)
	}
	// 带主钥匙打 /api/connect 也一样进不去(挂在鉴权内的 mux 没有这个路由)。
	code, _ = e.api("POST", "/api/connect", nil)
	if code != http.StatusNotFound {
		t.Fatalf("带凭据的 /api/connect 应 404,得到 %d", code)
	}
}
