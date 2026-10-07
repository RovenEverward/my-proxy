package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
	"gopkg.in/yaml.v3"
)

// 这一层的测试全都走真实 HTTP 请求,不直接调处理器:路由拼错、中间件包错位置
// 这类问题只有整条链路跑一遍才看得见。

const testToken = "test-management-token"

type env struct {
	t   *testing.T
	st  *store.Store
	srv *httptest.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, nil)
}

// newEnvWith 在组装控制面时接上一个假的节点通道。ops 为 nil 表示不接 ——
// 那种情况下跟节点有关的接口应该回 501,而不是假装做过了。
func newEnvWith(t *testing.T, ops NodeOps) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, testToken, logger)
	if ops != nil {
		s.SetNodeOps(ops)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	return &env{t: t, st: st, srv: srv}
}

// api 发一个带管理 token 的请求。body 为 nil 表示不带请求体。
func (e *env) api(method, path string, body any) (int, []byte) {
	e.t.Helper()
	return e.request(method, path, body, map[string]string{
		"Authorization": "Bearer " + testToken,
	})
}

func (e *env) request(method, path string, body any, headers map[string]string) (int, []byte) {
	e.t.Helper()

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("序列化请求体: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rdr)
	if err != nil {
		e.t.Fatalf("构造请求 %s %s: %v", method, path, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("请求 %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("读取 %s %s 的响应: %v", method, path, err)
	}
	return resp.StatusCode, data
}

// sub 取一次订阅,返回状态码、响应头与正文。
func (e *env) sub(path, userAgent string) (int, http.Header, []byte) {
	e.t.Helper()
	headers := map[string]string{}
	if userAgent != "" {
		headers["User-Agent"] = userAgent
	}
	req, err := http.NewRequest("GET", e.srv.URL+path, nil)
	if err != nil {
		e.t.Fatalf("构造订阅请求: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("请求订阅 %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("读取订阅 %s: %v", path, err)
	}
	return resp.StatusCode, resp.Header, data
}

// decodeJSON 把响应体解成 T,失败即测试失败。
func decodeJSON[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v\n%s", err, data)
	}
	return v
}

// ---- 建数据的便捷函数,都走 HTTP 接口,顺带验证写入口 ----

func (e *env) createNode(name, address string) model.Node {
	e.t.Helper()
	code, data := e.api("POST", "/api/nodes", model.Node{Name: name, Address: address})
	if code != http.StatusCreated {
		e.t.Fatalf("建节点: got %d %s", code, data)
	}
	return decodeJSON[model.Node](e.t, data)
}

// createInbound 建一个 vless + ws + tls 入站,这是最常见也最容易写错的组合。
func (e *env) createInbound(nodeID int64, tag string, port int) model.Inbound {
	e.t.Helper()
	in := model.Inbound{
		NodeID:   nodeID,
		Tag:      tag,
		Protocol: model.ProtocolVLESS,
		Port:     port,
		Network:  model.NetworkWS,
		Security: model.SecurityTLS,
		WSPath:   "/ws",
		SNI:      "node.example.com",
		ALPN:     []string{"h2", "http/1.1"},
		CertFile: "/etc/ssl/node.crt",
		KeyFile:  "/etc/ssl/node.key",
	}
	code, data := e.api("POST", "/api/inbounds", in)
	if code != http.StatusCreated {
		e.t.Fatalf("建入站: got %d %s", code, data)
	}
	return decodeJSON[model.Inbound](e.t, data)
}

func (e *env) createHost(inboundID int64, remark, address string) model.Host {
	e.t.Helper()
	h := model.Host{Remark: remark, Address: address, Port: 443}
	code, data := e.api("POST", pathf("/api/inbounds/%d/hosts", inboundID), h)
	if code != http.StatusCreated {
		e.t.Fatalf("建对外入口: got %d %s", code, data)
	}
	return decodeJSON[model.Host](e.t, data)
}

// createUser 建用户并返回它的 userView —— 订阅测试需要 sub_token,它只在这里出现。
func (e *env) createUser(username string) userView {
	e.t.Helper()
	code, data := e.api("POST", "/api/users", model.User{Username: username, Enabled: true})
	if code != http.StatusCreated {
		e.t.Fatalf("建用户: got %d %s", code, data)
	}
	return decodeJSON[userView](e.t, data)
}

// updateUser 走"读-改-写",和 CLI 的做法一致。
func (e *env) updateUser(username string, mutate func(*model.User)) {
	e.t.Helper()
	code, data := e.api("GET", "/api/users/"+username, nil)
	if code != http.StatusOK {
		e.t.Fatalf("读用户: got %d %s", code, data)
	}
	u := decodeJSON[model.User](e.t, data)
	mutate(&u)

	code, data = e.api("PUT", "/api/users/"+username, u)
	if code != http.StatusOK {
		e.t.Fatalf("写用户: got %d %s", code, data)
	}
}

func pathf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// base64Decode 解开订阅的 base64 格式。控制面用的是不带换行的标准编码。
func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---- 鉴权 ----

func TestManagementAPIRequiresToken(t *testing.T) {
	e := newEnv(t)

	code, _ := e.request("GET", "/api/nodes", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("不带 token 应当 401,实际 %d", code)
	}
	code, _ = e.request("GET", "/api/nodes", nil, map[string]string{"Authorization": "Bearer wrong"})
	if code != http.StatusUnauthorized {
		t.Fatalf("token 不对应当 401,实际 %d", code)
	}
	if code, _ := e.api("GET", "/api/nodes", nil); code != http.StatusOK {
		t.Fatalf("带对 token 应当 200,实际 %d", code)
	}
}

// 订阅路径不能要求管理 token:客户端手里只有一个订阅 token。
func TestSubscriptionDoesNotRequireManagementToken(t *testing.T) {
	e := newEnv(t)
	code, _, _ := e.sub("/sub/does-not-exist", "v2rayng")
	if code == http.StatusUnauthorized {
		t.Fatal("订阅路径不该走管理鉴权")
	}
}

func TestTokenEmptyDisablesAuthForLocalDev(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	defer st.Close()
	srv := httptest.NewServer(New(st, "", nil).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token 为空时应当放行,实际 %d", resp.StatusCode)
	}
}

// ---- 订阅全链路 ----

func TestSubscriptionRoundTrip(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "香港 ws tls", "node.example.com")
	user := e.createUser("alice")

	path := "/sub/" + user.SubToken

	t.Run("默认给分享链接", func(t *testing.T) {
		code, hdr, body := e.sub(path, "v2rayng/1.8")
		if code != http.StatusOK {
			t.Fatalf("订阅: got %d %s", code, body)
		}
		if !strings.Contains(string(body), "vless://") {
			t.Fatalf("正文里没有 vless 链接:\n%s", body)
		}
		if !strings.Contains(string(body), "@node.example.com:443") {
			t.Fatalf("链接没有用 Host 里的地址与端口:\n%s", body)
		}
		// Host 的地址是域名时 SNI 走入站的 SNI,ws 的 path 走 Host 覆盖值。
		if !strings.Contains(string(body), "path=%2Fws") {
			t.Fatalf("链接里没有 ws path:\n%s", body)
		}

		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("默认格式的 Content-Type: %q", ct)
		}
		if cd := hdr.Get("Content-Disposition"); cd != "attachment; filename=alice" {
			t.Fatalf("Content-Disposition: %q", cd)
		}
		if got := hdr.Get("Subscription-Userinfo"); got != "upload=0; download=0; total=0; expire=0" {
			t.Fatalf("Subscription-Userinfo: %q", got)
		}
		if got := hdr.Get("Profile-Title"); got == "" {
			t.Fatal("缺少 Profile-Title")
		}
	})

	t.Run("clash", func(t *testing.T) {
		code, hdr, body := e.sub(path+"/clash", "clash")
		if code != http.StatusOK {
			t.Fatalf("clash 订阅: got %d %s", code, body)
		}
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
			t.Fatalf("clash 的 Content-Type: %q", ct)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Fatalf("clash 正文不是合法 YAML: %v\n%s", err, body)
		}
		proxies, ok := doc["proxies"].([]any)
		if !ok || len(proxies) != 1 {
			t.Fatalf("clash 正文的 proxies 不对: %#v", doc["proxies"])
		}
	})

	t.Run("sing-box", func(t *testing.T) {
		code, hdr, body := e.sub(path+"/sing-box", "sing-box")
		if code != http.StatusOK {
			t.Fatalf("sing-box 订阅: got %d %s", code, body)
		}
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("sing-box 的 Content-Type: %q", ct)
		}
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("sing-box 正文不是合法 JSON: %v\n%s", err, body)
		}
		outbounds, ok := doc["outbounds"].([]any)
		if !ok || len(outbounds) == 0 {
			t.Fatalf("sing-box 正文没有 outbounds: %#v", doc["outbounds"])
		}
	})

	t.Run("base64", func(t *testing.T) {
		code, _, body := e.sub(path+"/base64-links", "v2rayng")
		if code != http.StatusOK {
			t.Fatalf("base64 订阅: got %d %s", code, body)
		}
		decoded, err := base64Decode(strings.TrimSpace(string(body)))
		if err != nil {
			t.Fatalf("base64 正文解不开: %v", err)
		}
		if !strings.Contains(decoded, "vless://") {
			t.Fatalf("base64 解出来的正文里没有链接:\n%s", decoded)
		}
	})
}

func TestSubscriptionUnknownTokenIs404(t *testing.T) {
	e := newEnv(t)
	code, _, _ := e.sub("/sub/nope", "v2rayng")
	if code != http.StatusNotFound {
		t.Fatalf("未知 token 应当 404,实际 %d", code)
	}
}

func TestSubscriptionBlockFormatIs404(t *testing.T) {
	e := newEnv(t)
	user := e.createUser("alice")
	code, _, _ := e.sub("/sub/"+user.SubToken+"/block", "v2rayng")
	if code != http.StatusNotFound {
		t.Fatalf("block 格式应当 404,实际 %d", code)
	}
}

func TestSubscriptionUnknownFormatIs400(t *testing.T) {
	e := newEnv(t)
	user := e.createUser("alice")
	code, _, _ := e.sub("/sub/"+user.SubToken+"/nonsense", "v2rayng")
	if code != http.StatusBadRequest {
		t.Fatalf("未知格式应当 400,实际 %d", code)
	}
}

// 不可用的用户拿到占位节点,而不是一份连不上的真配置。
func TestDisabledUserGetsPlaceholder(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "香港", "node.example.com")
	user := e.createUser("alice")

	e.updateUser("alice", func(u *model.User) { u.Enabled = false })

	code, _, body := e.sub("/sub/"+user.SubToken, "v2rayng")
	if code != http.StatusOK {
		t.Fatalf("停用用户的订阅仍应可下载: got %d %s", code, body)
	}
	if !strings.Contains(string(body), "0.0.0.0:1") {
		t.Fatalf("停用用户应当拿到占位节点:\n%s", body)
	}
	if strings.Contains(string(body), "node.example.com") {
		t.Fatalf("停用用户不该拿到真节点地址:\n%s", body)
	}
}

// 超限的用户同样只能拿到占位节点 —— 凭证在节点上已经被摘掉了。
func TestLimitedUserGetsPlaceholder(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "香港", "node.example.com")
	user := e.createUser("alice")

	e.updateUser("alice", func(u *model.User) {
		u.DataLimit = 100
		u.UsedTraffic = 100
	})

	code, _, body := e.sub("/sub/"+user.SubToken, "v2rayng")
	if code != http.StatusOK {
		t.Fatalf("超限用户的订阅仍应可下载: got %d %s", code, body)
	}
	if !strings.Contains(string(body), "0.0.0.0:1") {
		t.Fatalf("超限用户应当拿到占位节点:\n%s", body)
	}
}

// 三种不可用状态在客户端里看到的说明不一样:停用要申诉、过期要续费、超限要加
// 流量,都写成「已停用」用户就不知道该做什么。
func TestUnusableUserSeesWhy(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.User)
		want   string
	}{
		{"停用", func(u *model.User) { u.Enabled = false }, "订阅已停用"},
		{"过期", func(u *model.User) { past := time.Now().Add(-time.Hour); u.ExpireAt = &past }, "订阅已过期"},
		{"超限", func(u *model.User) { u.DataLimit = 100; u.UsedTraffic = 100 }, "订阅已超限"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			n := e.createNode("hk", "node.example.com")
			in := e.createInbound(n.ID, "vless-ws", 8443)
			e.createHost(in.ID, "香港", "node.example.com")
			user := e.createUser("alice")

			e.updateUser("alice", tc.mutate)

			// 走 xray 格式:节点名原样落在 remarks 里,不用再解一遍 URL 编码。
			code, _, body := e.sub("/sub/"+user.SubToken+"/xray", "v2rayng")
			if code != http.StatusOK {
				t.Fatalf("不可用的用户仍应能下载订阅: got %d %s", code, body)
			}
			if !strings.Contains(string(body), tc.want) {
				t.Fatalf("订阅里应当写明 %q:\n%s", tc.want, body)
			}
		})
	}
}

// ---- 订阅规则 ----

func TestSubscriptionRulePicksFormatByUserAgent(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "香港", "node.example.com")
	user := e.createUser("alice")

	settings := model.Settings{
		ProfileTitle:   "MPX",
		UpdateInterval: 12,
		Rules: []model.SubscriptionRule{
			{Pattern: `(?i)clash`, Result: model.FormatClashMeta},
			{Pattern: `(?i)sing-box`, Result: model.FormatSingBox},
			{Pattern: `(?i)xray`, Result: model.FormatXray},
		},
	}
	if code, data := e.api("PUT", "/api/settings", settings); code != http.StatusOK {
		t.Fatalf("存设置: got %d %s", code, data)
	}

	t.Run("clash 的 UA 拿到 YAML", func(t *testing.T) {
		code, hdr, body := e.sub("/sub/"+user.SubToken, "clash-verge/1.7.7")
		if code != http.StatusOK {
			t.Fatalf("got %d %s", code, body)
		}
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
			t.Fatalf("Content-Type: %q", ct)
		}
		if !strings.Contains(string(body), "proxies:") {
			t.Fatalf("正文不像 Clash 配置:\n%s", body)
		}
		if got := hdr.Get("Profile-Update-Interval"); got != "12" {
			t.Fatalf("Profile-Update-Interval: %q", got)
		}
	})

	t.Run("sing-box 的 UA 拿到 JSON", func(t *testing.T) {
		code, hdr, body := e.sub("/sub/"+user.SubToken, "sing-box 1.10")
		if code != http.StatusOK {
			t.Fatalf("got %d %s", code, body)
		}
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type: %q", ct)
		}
	})

	t.Run("xray 的 UA 拿到 JSON 数组", func(t *testing.T) {
		code, hdr, body := e.sub("/sub/"+user.SubToken, "xray 26.3")
		if code != http.StatusOK {
			t.Fatalf("got %d %s", code, body)
		}
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type: %q", ct)
		}
		var configs []map[string]any
		if err := json.Unmarshal(body, &configs); err != nil {
			t.Fatalf("正文不是 JSON 数组: %v\n%s", err, body)
		}
		if len(configs) == 0 {
			t.Fatal("xray 订阅里一个节点都没有")
		}
	})

	t.Run("没命中规则的 UA 拿分享链接", func(t *testing.T) {
		code, _, body := e.sub("/sub/"+user.SubToken, "v2rayng/1.8")
		if code != http.StatusOK {
			t.Fatalf("got %d %s", code, body)
		}
		if !strings.Contains(string(body), "vless://") {
			t.Fatalf("正文里没有分享链接:\n%s", body)
		}
	})

	t.Run("路径里的格式压过规则", func(t *testing.T) {
		code, hdr, _ := e.sub("/sub/"+user.SubToken+"/clash", "sing-box 1.10")
		if code != http.StatusOK {
			t.Fatalf("got %d", code)
		}
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
			t.Fatalf("路径里的格式应当压过 UA 规则,Content-Type: %q", ct)
		}
	})
}

func TestSaveSettingsRejectsBadRule(t *testing.T) {
	e := newEnv(t)

	code, _ := e.api("PUT", "/api/settings", model.Settings{
		Rules: []model.SubscriptionRule{{Pattern: "([", Result: model.FormatClash}},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("坏正则应当 400,实际 %d", code)
	}

	code, _ = e.api("PUT", "/api/settings", model.Settings{
		Rules: []model.SubscriptionRule{{Pattern: "clash", Result: "nonsense"}},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("非法格式应当 400,实际 %d", code)
	}
}

// ---- 校验 ----

func TestInboundValidation(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")

	cases := []struct {
		name string
		in   model.Inbound
		want int
	}{
		{
			name: "reality 不能配 ws",
			in: model.Inbound{NodeID: n.ID, Protocol: model.ProtocolVLESS, Port: 443,
				Network: model.NetworkWS, Security: model.SecurityReality,
				RealityDest: "www.microsoft.com:443", RealityPriv: "priv",
				RealityPub: "pub", SNI: "www.microsoft.com"},
			want: http.StatusBadRequest,
		},
		{
			name: "grpc 必须填 serviceName",
			in: model.Inbound{NodeID: n.ID, Protocol: model.ProtocolVLESS, Port: 443,
				Network: model.NetworkGRPC, Security: model.SecurityNone},
			want: http.StatusBadRequest,
		},
		{
			name: "tls 必须有证书",
			in: model.Inbound{NodeID: n.ID, Protocol: model.ProtocolVLESS, Port: 443,
				Network: model.NetworkTCP, Security: model.SecurityTLS},
			want: http.StatusBadRequest,
		},
		{
			name: "flow 只有 vless 能用",
			in: model.Inbound{NodeID: n.ID, Protocol: model.ProtocolTrojan, Port: 443,
				Network: model.NetworkTCP, Security: model.SecurityNone, Flow: "xtls-rprx-vision"},
			want: http.StatusBadRequest,
		},
		{
			name: "端口越界",
			in: model.Inbound{NodeID: n.ID, Protocol: model.ProtocolVLESS, Port: 0,
				Network: model.NetworkTCP, Security: model.SecurityNone},
			want: http.StatusBadRequest,
		},
		{
			name: "不认识的协议",
			in: model.Inbound{NodeID: n.ID, Protocol: "shadowsocks", Port: 443,
				Network: model.NetworkTCP, Security: model.SecurityNone},
			want: http.StatusBadRequest,
		},
		{
			name: "tcp + reality + vless 是合法的",
			in: model.Inbound{NodeID: n.ID, Protocol: model.ProtocolVLESS, Port: 443,
				Network: model.NetworkTCP, Security: model.SecurityReality,
				RealityDest: "www.microsoft.com:443", RealityPriv: "priv",
				RealityPub: "pub", SNI: "www.microsoft.com"},
			want: http.StatusCreated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, data := e.api("POST", "/api/inbounds", tc.in)
			if code != tc.want {
				t.Fatalf("got %d want %d: %s", code, tc.want, data)
			}
		})
	}
}

func TestUsernameValidation(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"", "has space", "中文", strings.Repeat("a", 65), "a/b"} {
		if code, data := e.api("POST", "/api/users", model.User{Username: name, Enabled: true}); code != http.StatusBadRequest {
			t.Fatalf("用户名 %q 应当被拒,实际 %d: %s", name, code, data)
		}
	}
	for _, name := range []string{"alice", "a.b-c_1", strings.Repeat("a", 64)} {
		if code, data := e.api("POST", "/api/users", model.User{Username: name, Enabled: true}); code != http.StatusCreated {
			t.Fatalf("用户名 %q 应当被接受,实际 %d: %s", name, code, data)
		}
	}
}

// 建用户时不该让调用方决定 Key:密钥由控制面生成,请求体里给了也没用。
func TestCreateUserResponseHidesKey(t *testing.T) {
	e := newEnv(t)
	code, data := e.api("POST", "/api/users", model.User{Username: "alice", Key: "attacker-chosen", Enabled: true})
	if code != http.StatusCreated {
		t.Fatalf("建用户: got %d %s", code, data)
	}
	if strings.Contains(string(data), "attacker-chosen") {
		t.Fatalf("响应里泄露了 Key:\n%s", data)
	}

	stored, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatalf("读用户: %v", err)
	}
	if stored.Key == "" || stored.Key == "attacker-chosen" {
		t.Fatalf("Key 应当由控制面生成,实际 %q", stored.Key)
	}
	if stored.SubToken == "" {
		t.Fatal("订阅 token 没有被生成")
	}
}

func TestDuplicateUsernameIsConflict(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice")
	if code, data := e.api("POST", "/api/users", model.User{Username: "alice", Enabled: true}); code != http.StatusConflict {
		t.Fatalf("重名应当 409,实际 %d: %s", code, data)
	}
}

// ---- 用户周边动作 ----

func TestRevokeSubChangesTokenOnly(t *testing.T) {
	e := newEnv(t)
	user := e.createUser("alice")
	before, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatalf("读用户: %v", err)
	}

	code, data := e.api("POST", "/api/users/alice/revoke-sub", nil)
	if code != http.StatusOK {
		t.Fatalf("换订阅 token: got %d %s", code, data)
	}
	after := decodeJSON[userView](t, data)
	if after.SubToken == user.SubToken {
		t.Fatal("订阅 token 没有变")
	}
	if after.SubToken == "" {
		t.Fatal("新订阅 token 是空的")
	}

	// 协议凭证不能跟着变,否则换一条订阅链接就得让所有客户端重配。
	stored, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatalf("读用户: %v", err)
	}
	if stored.Key != before.Key {
		t.Fatal("换订阅 token 不该改用户的协议凭证")
	}

	if code, _, _ := e.sub("/sub/"+user.SubToken, "v2rayng"); code != http.StatusNotFound {
		t.Fatalf("旧 token 应当立即失效,实际 %d", code)
	}
	if code, _, _ := e.sub("/sub/"+after.SubToken, "v2rayng"); code != http.StatusOK {
		t.Fatalf("新 token 应当可用,实际 %d", code)
	}
}

func TestResetTrafficEndpointKeepsLifetime(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice")
	if err := e.st.AddUserTraffic("alice", 300, 200); err != nil {
		t.Fatalf("累加流量: %v", err)
	}

	if code, data := e.api("POST", "/api/users/alice/reset-traffic", nil); code != http.StatusOK {
		t.Fatalf("重置流量: got %d %s", code, data)
	}

	stored, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatalf("读用户: %v", err)
	}
	if stored.UsedTraffic != 0 {
		t.Fatalf("本周期用量没有归零: %d", stored.UsedTraffic)
	}
	if stored.LifetimeUsedTraffic != 500 {
		t.Fatalf("累计用量不该被重置: %d", stored.LifetimeUsedTraffic)
	}
	if stored.TrafficResetAt == nil {
		t.Fatal("手动重置应当同时把周期起算点设上")
	}
}

// 读-改-写不丢字段:CLI 就是这么做启用/停用的。
func TestUpdateUserRoundTripPreservesCounters(t *testing.T) {
	e := newEnv(t)
	user := e.createUser("alice")
	if err := e.st.AddUserTraffic("alice", 1000, 500); err != nil {
		t.Fatalf("累加流量: %v", err)
	}
	if err := e.st.ResetUserTraffic("alice", time.Now()); err != nil {
		t.Fatalf("重置以设置起算点: %v", err)
	}

	e.updateUser("alice", func(u *model.User) { u.Note = "老客户" })

	stored, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatalf("读用户: %v", err)
	}
	if stored.Key != "" && user.SubToken != stored.SubToken {
		t.Fatalf("订阅 token 被改掉了: %q", stored.SubToken)
	}
	if stored.LifetimeUsedTraffic != 1500 {
		t.Fatalf("累计用量在回写中丢了: %d", stored.LifetimeUsedTraffic)
	}
	if stored.TrafficResetAt == nil {
		t.Fatal("周期起算点在回写中丢了")
	}
	if stored.Note != "老客户" {
		t.Fatalf("改动没有生效: %q", stored.Note)
	}
}

func TestUserLinksUseConfiguredPrefix(t *testing.T) {
	e := newEnv(t)
	user := e.createUser("alice")
	if code, data := e.api("PUT", "/api/settings", model.Settings{SubURLPrefix: "https://sub.example.com/"}); code != http.StatusOK {
		t.Fatalf("存设置: got %d %s", code, data)
	}

	code, data := e.api("GET", "/api/users/alice/links", nil)
	if code != http.StatusOK {
		t.Fatalf("取订阅地址: got %d %s", code, data)
	}
	links := decodeJSON[map[string]string](t, data)

	want := "https://sub.example.com/sub/" + user.SubToken
	if links["auto"] != want {
		t.Fatalf("auto 链接: got %q want %q", links["auto"], want)
	}
	if links["clash"] != want+"/clash" {
		t.Fatalf("clash 链接: %q", links["clash"])
	}
	for _, key := range []string{"links", "base64-links", "clash", "clash-meta", "sing-box"} {
		if links[key] == "" {
			t.Fatalf("缺少 %s 的订阅地址", key)
		}
	}

	// 没配前缀时回落到请求里的 Host,本机开发不开设置也能用。
	if code, _ := e.api("PUT", "/api/settings", model.Settings{}); code != http.StatusOK {
		t.Fatalf("清空设置失败")
	}
	code, data = e.api("GET", "/api/users/alice/links", nil)
	if code != http.StatusOK {
		t.Fatalf("取订阅地址: got %d", code)
	}
	links = decodeJSON[map[string]string](t, data)
	if !strings.HasPrefix(links["auto"], "http://127.0.0.1:") {
		t.Fatalf("没配前缀时应当回落到请求的 Host: %q", links["auto"])
	}
}

// ---- 节点配置预览 ----

func TestPreviewNodeConfig(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	e.createInbound(n.ID, "vless-ws", 8443)
	e.createUser("alice")

	code, data := e.api("GET", pathf("/api/nodes/%d/config", n.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("预览配置: got %d %s", code, data)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("预览的配置不是合法 JSON: %v", err)
	}
	for _, key := range []string{"inbounds", "outbounds", "routing", "api", "stats", "policy"} {
		if _, ok := cfg[key]; !ok {
			t.Fatalf("配置里缺少 %s:\n%s", key, data)
		}
	}
	// 非活跃用户不该出现在下发到节点的配置里。
	if strings.Contains(string(data), "alice") {
		return // 活跃用户出现是对的
	}
	t.Fatal("活跃用户没有写进配置")
}

func TestPreviewNodeConfigExcludesDisabledUser(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	e.createInbound(n.ID, "vless-ws", 8443)
	e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.Enabled = false })

	_, data := e.api("GET", pathf("/api/nodes/%d/config", n.ID), nil)
	if strings.Contains(string(data), "alice") {
		t.Fatalf("停用用户不该写进节点配置:\n%s", data)
	}
}

// 不给 tag 时默认 tag 要落库,而且一旦定下来就不再变:节点上的增删用户、日志、
// 渲染都按 tag 认入站,让它们各自去推同一个默认值迟早推出分歧。
func TestInboundDefaultTagIsPersisted(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")

	code, data := e.api("POST", "/api/inbounds", model.Inbound{
		NodeID: n.ID, Protocol: model.ProtocolVLESS, Port: 8443,
		Network: model.NetworkTCP, Security: model.SecurityNone,
	})
	if code != http.StatusCreated {
		t.Fatalf("建入站: got %d %s", code, data)
	}
	in := decodeJSON[model.Inbound](t, data)
	if in.Tag != "vless-8443" {
		t.Fatalf("默认 tag 应当落库,实际 %q", in.Tag)
	}

	code, data = e.api("PUT", pathf("/api/inbounds/%d", in.ID), model.Inbound{
		Protocol: model.ProtocolVLESS, Port: 8444,
		Network: model.NetworkTCP, Security: model.SecurityNone,
	})
	if code != http.StatusOK {
		t.Fatalf("改入站: got %d %s", code, data)
	}
	updated := decodeJSON[model.Inbound](t, data)
	if updated.Tag != "vless-8443" {
		t.Fatalf("改端口不该悄悄换掉 tag,实际 %q", updated.Tag)
	}
	if updated.NodeID != n.ID {
		t.Fatalf("改入站不该改变它属于哪个节点: %d", updated.NodeID)
	}
}

// REALITY 的密钥对与 shortId 由控制面生成:私钥不出现在任何响应里,公钥与
// shortId 出现在响应里(客户端配置需要它们),改别的字段时不许换掉。
func TestRealityKeypairGeneratedAndKept(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")

	in := model.Inbound{
		NodeID: n.ID, Protocol: model.ProtocolVLESS, Port: 443,
		Network: model.NetworkTCP, Security: model.SecurityReality,
		RealityDest: "www.microsoft.com:443", SNI: "www.microsoft.com",
	}
	code, data := e.api("POST", "/api/inbounds", in)
	if code != http.StatusCreated {
		t.Fatalf("建 REALITY 入站: got %d %s", code, data)
	}
	created := decodeJSON[model.Inbound](t, data)

	if created.RealityPub == "" {
		t.Fatal("控制面应当生成 REALITY 公钥")
	}
	if created.RealityPriv != "" {
		t.Fatalf("响应里泄露了 REALITY 私钥: %q", created.RealityPriv)
	}
	if strings.Contains(string(data), "private_key") || strings.Contains(string(data), "reality_priv") {
		t.Fatalf("响应里出现了私钥字段:\n%s", data)
	}
	if len(created.ShortIDs) == 0 {
		t.Fatal("控制面应当生成一个 shortId:列表为空会让自带 sid 的客户端握手失败")
	}

	// 改一个无关字段:已经发出去的密钥对与 shortId 必须原样留住。
	created.SNI = "www.cloudflare.com"
	code, data = e.api("PUT", pathf("/api/inbounds/%d", created.ID), created)
	if code != http.StatusOK {
		t.Fatalf("改入站: got %d %s", code, data)
	}
	updated := decodeJSON[model.Inbound](t, data)
	if updated.RealityPub != created.RealityPub {
		t.Fatalf("改别的字段换掉了公钥: %q → %q", created.RealityPub, updated.RealityPub)
	}
	if len(updated.ShortIDs) != len(created.ShortIDs) || updated.ShortIDs[0] != created.ShortIDs[0] {
		t.Fatalf("改别的字段换掉了 shortId: %v → %v", created.ShortIDs, updated.ShortIDs)
	}

	// 节点侧配置里必须有私钥(REALITY 起不来),且不该有公钥 —— 公钥是客户端
	// 才用的值,出现在节点配置里说明两个方向的东西搞混了。
	_, cfg := e.api("GET", pathf("/api/nodes/%d/config", n.ID), nil)
	if !strings.Contains(string(cfg), "privateKey") {
		t.Fatalf("节点配置里没有 REALITY 私钥:\n%s", cfg)
	}
	if strings.Contains(string(cfg), created.RealityPub) {
		t.Fatalf("节点配置里出现了只该给客户端的公钥:\n%s", cfg)
	}
}

// ---- 级联删除 ----

func TestDeleteNodeCascades(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	h := e.createHost(in.ID, "香港", "node.example.com")

	if code, data := e.api("DELETE", pathf("/api/nodes/%d", n.ID), nil); code != http.StatusOK {
		t.Fatalf("删节点: got %d %s", code, data)
	}
	if code, _ := e.api("GET", pathf("/api/inbounds/%d", in.ID), nil); code != http.StatusNotFound {
		t.Fatalf("入站应当跟着节点一起消失,实际 %d", code)
	}
	if code, _ := e.api("DELETE", pathf("/api/hosts/%d", h.ID), nil); code != http.StatusNotFound {
		t.Fatalf("对外入口应当跟着入站一起消失,实际 %d", code)
	}
}

func TestNotFoundAndBadID(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.api("GET", "/api/nodes/42", nil); code != http.StatusNotFound {
		t.Fatalf("不存在的节点应当 404,实际 %d", code)
	}
	if code, _ := e.api("GET", "/api/nodes/abc", nil); code != http.StatusBadRequest {
		t.Fatalf("非数字 id 应当 400,实际 %d", code)
	}
	if code, _ := e.api("GET", "/api/users/nobody", nil); code != http.StatusNotFound {
		t.Fatalf("不存在的用户应当 404,实际 %d", code)
	}
}

// ---- 多个节点 / 多个入口 ----

func TestSubscriptionAggregatesAllNodesAndHosts(t *testing.T) {
	e := newEnv(t)
	hk := e.createNode("hk", "hk.example.com")
	us := e.createNode("us", "us.example.com")
	hkIn := e.createInbound(hk.ID, "vless-hk", 8443)
	usIn := e.createInbound(us.ID, "vless-us", 8443)
	e.createHost(hkIn.ID, "香港 1", "hk1.example.com")
	e.createHost(hkIn.ID, "香港 2", "hk2.example.com")
	e.createHost(usIn.ID, "美国", "us.example.com")
	user := e.createUser("alice")

	_, _, body := e.sub("/sub/"+user.SubToken, "v2rayng")
	text := string(body)
	for _, want := range []string{"hk1.example.com", "hk2.example.com", "us.example.com"} {
		if !strings.Contains(text, want) {
			t.Fatalf("订阅里缺少 %s:\n%s", want, text)
		}
	}
	if got := strings.Count(text, "vless://"); got != 3 {
		t.Fatalf("应当有 3 个节点,实际 %d:\n%s", got, text)
	}
}

// 没有对外入口的入站用自身参数作为唯一入口,这是合法状态。
func TestInboundWithoutHostUsesItself(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	e.createInbound(n.ID, "vless-ws", 8443)
	user := e.createUser("alice")

	_, _, body := e.sub("/sub/"+user.SubToken, "v2rayng")
	text := string(body)
	if !strings.Contains(text, "@node.example.com:8443") {
		t.Fatalf("没有 Host 时应当用入站自身的地址与端口:\n%s", text)
	}
}

// 用户被限定入站时,订阅里只能出现被授权的那些。
func TestInboundRestrictionFiltersSubscription(t *testing.T) {
	e := newEnv(t)
	hk := e.createNode("hk", "hk.example.com")
	us := e.createNode("us", "us.example.com")
	hkIn := e.createInbound(hk.ID, "vless-hk", 8443)
	usIn := e.createInbound(us.ID, "vless-us", 8443)

	user := e.createUser("alice")
	e.updateUser("alice", func(u *model.User) { u.InboundIDs = []int64{hkIn.ID} })

	_, _, body := e.sub("/sub/"+user.SubToken, "v2rayng")
	text := string(body)
	if !strings.Contains(text, "hk.example.com") {
		t.Fatalf("被授权的入站不见了:\n%s", text)
	}
	if strings.Contains(text, "us.example.com") {
		t.Fatalf("未授权的入站不该出现:\n%s", text)
	}
	_ = usIn
}

// 同名入口在 Clash 里会撞车:名字必须唯一。
func TestDuplicateHostRemarksGetDistinctNames(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "同一个名字", "a.example.com")
	e.createHost(in.ID, "同一个名字", "b.example.com")
	user := e.createUser("alice")

	code, _, body := e.sub("/sub/"+user.SubToken+"/clash", "clash")
	if code != http.StatusOK {
		t.Fatalf("clash 订阅: got %d %s", code, body)
	}
	var doc struct {
		Proxies []struct {
			Name string `yaml:"name"`
		} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("解析 clash 正文: %v", err)
	}
	if len(doc.Proxies) != 2 {
		t.Fatalf("应当有两个节点: %d", len(doc.Proxies))
	}
	if doc.Proxies[0].Name == doc.Proxies[1].Name {
		t.Fatalf("两个节点重名了: %q", doc.Proxies[0].Name)
	}
}

// 对外入口带要上生效值:只填了备注时,光看那几个空字段看不出客户端最终会连到
// 哪里。回落规则本身在 render 里测,这里只测 API 有没有把它带出来、有没有把存
// 下来的空字段改掉。
func TestHostCarriesEffectiveValues(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "203.0.113.9")
	in := e.createInbound(n.ID, "vless-ws", 8443)

	code, data := e.api("POST", pathf("/api/inbounds/%d/hosts", in.ID), model.Host{Remark: "裸备注"})
	if code != http.StatusCreated {
		t.Fatalf("建对外入口: got %d %s", code, data)
	}
	created := decodeJSON[model.HostView](t, data)
	if created.ID == 0 {
		t.Fatalf("创建返回里该有 id: %s", data)
	}
	if got := created.Effective; got.Address != "203.0.113.9" || got.Port != 8443 || got.Path != "/ws" {
		t.Errorf("创建返回该带回落后的地址/端口/路径: %+v", got)
	}

	code, data = e.api("GET", pathf("/api/inbounds/%d/hosts", in.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("列对外入口: got %d %s", code, data)
	}
	views := decodeJSON[[]model.HostView](t, data)
	if len(views) != 1 {
		t.Fatalf("期望 1 个对外入口,got %d", len(views))
	}
	if got := views[0].Effective; got.SNI != "node.example.com" || len(got.ALPN) != 2 {
		t.Errorf("列表该带回落后的 SNI/ALPN: %+v", got)
	}
	// 填进去的字段原样返回。要是被生效值覆盖了,就分不清哪些是自己定的了。
	if views[0].Address != "" || views[0].Port != 0 {
		t.Errorf("存下来的空字段该还是空: address=%q port=%d", views[0].Address, views[0].Port)
	}
}
