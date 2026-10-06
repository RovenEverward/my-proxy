package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testClient 起一个假控制面,并把指向它的 Client 交给测试。
func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{server: srv.URL, token: "secret-token", http: srv.Client()}
}

func TestClientTranslatesAPIError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("{\"error\": \"节点不存在\"}"))
	})

	_, err := c.do("GET", "/api/nodes/404", nil)
	if err == nil {
		t.Fatal("期望报错,实际成功")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误类型 = %T, 期望 *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("状态码 = %d, 期望 404", apiErr.StatusCode)
	}
	if got := err.Error(); got != "节点不存在" {
		t.Errorf("错误文本 = %q, 期望服务端那句 %q", got, "节点不存在")
	}
}

func TestClientErrorWithoutJSONBody(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	})

	_, err := c.do("GET", "/api/nodes", nil)
	if err == nil {
		t.Fatal("期望报错,实际成功")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误类型 = %T, 期望 *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("状态码 = %d, 期望 502", apiErr.StatusCode)
	}
	// 不是 JSON 也不能把响应内容吞掉。
	if got := err.Error(); got != "HTTP 502: <html>bad gateway</html>" {
		t.Errorf("错误文本 = %q", got)
	}
}

func TestClientSuccessReturnsResponseBytes(t *testing.T) {
	// 服务端已经缩进过的响应必须原样带回来,交给 -o json 重排。
	const body = "[\n  {\n    \"id\": 1,\n    \"name\": \"hk-1\"\n  }\n]"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	raw, err := c.do("GET", "/api/nodes", nil)
	if err != nil {
		t.Fatalf("报错: %v", err)
	}
	if string(raw) != body {
		t.Errorf("原始响应被改动了:\n得到 %q\n期望 %q", raw, body)
	}
}

func TestClientSendsAuthAndJSONBody(t *testing.T) {
	type captured struct {
		method string
		path   string
		auth   string
		ctype  string
		body   map[string]any
	}
	got := make(chan captured, 1)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		got <- captured{
			method: r.Method,
			path:   r.URL.Path,
			auth:   r.Header.Get("Authorization"),
			ctype:  r.Header.Get("Content-Type"),
			body:   body,
		}
		_, _ = w.Write([]byte("{}"))
	})

	if _, err := c.do("POST", "/api/nodes", map[string]any{"name": "hk-1"}); err != nil {
		t.Fatalf("报错: %v", err)
	}
	req := <-got
	if req.method != "POST" || req.path != "/api/nodes" {
		t.Errorf("请求 = %s %s, 期望 POST /api/nodes", req.method, req.path)
	}
	if req.auth != "Bearer secret-token" {
		t.Errorf("Authorization = %q", req.auth)
	}
	if req.ctype != "application/json" {
		t.Errorf("Content-Type = %q", req.ctype)
	}
	if req.body["name"] != "hk-1" {
		t.Errorf("请求体 = %v", req.body)
	}
}

func TestClientSkipsAuthHeaderWithoutToken(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("null"))
	}))
	defer srv.Close()

	c := &Client{server: srv.URL, http: srv.Client()}
	if _, err := c.do("GET", "/api/settings", nil); err != nil {
		t.Fatalf("报错: %v", err)
	}
	if auth != "" {
		t.Errorf("没配 token 时不该带 Authorization,得到 %q", auth)
	}
}

func TestClientDeleteSendsNoBody(t *testing.T) {
	var method string
	var body []byte
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		body, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("null"))
	})

	raw, err := c.do("DELETE", "/api/nodes/3", nil)
	if err != nil {
		t.Fatalf("报错: %v", err)
	}
	if method != "DELETE" {
		t.Errorf("方法 = %s, 期望 DELETE", method)
	}
	if len(body) != 0 {
		t.Errorf("DELETE 不该带请求体,得到 %q", body)
	}
	if string(raw) != "null" {
		t.Errorf("响应 = %q, 期望 null", raw)
	}
}
