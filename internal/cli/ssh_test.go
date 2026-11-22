package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSSH 把一个可执行脚本放到 PATH 最前的临时目录,返回记录调用参数的文件
// 路径。stdout 是原样写出的内容(一条完整 HTTP 响应报文),stderr 是写往
// 错误输出的内容(模拟 ssh 自己的报错)。
func fakeSSH(t *testing.T, stdout, stderr string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := filepath.Join(dir, "ssh")
	content := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\ncat > /dev/null\nprintf '%%s' %s\nprintf '%%s' %s >&2\nexit %d\n",
		argsFile, shellQuote(stdout), shellQuote(stderr), exitCode)
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return argsFile
}

// shellQuote 把任意字节塞进单引号:单引号自身换成 '\'' 即可。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestSSHRelayCmd(t *testing.T) {
	cmd := sshRelayCmd(8080)
	if !strings.Contains(cmd, "api-relay") || !strings.Contains(cmd, "--port 8080") {
		t.Fatalf("远端命令没指向 api-relay: %s", cmd)
	}
	if !strings.Contains(cmd, "command -v mpx") {
		t.Fatalf("远端命令丢了 mpx 查找: %s", cmd)
	}
}

func TestSSHTargetFromServer(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://mpx.example.net:8080", "mpx.example.net"},
		{"https://203.0.113.10:8080", "203.0.113.10"},
		{"https://[2001:db8::1]:8443", "2001:db8::1"},
		{"https://example.net", "example.net"},
	} {
		if got := sshTargetFromServer(tc.in); got != tc.want {
			t.Errorf("sshTargetFromServer(%q) = %q,期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestAPIPortFromServer(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"https://mpx.example.net:8080", 8080},
		{"https://mpx.example.net:9443", 9443},
		{"https://mpx.example.net", defaultControlPort},
	} {
		if got := apiPortFromServer(tc.in); got != tc.want {
			t.Errorf("apiPortFromServer(%q) = %d,期望 %d", tc.in, got, tc.want)
		}
	}
}

// 通道端到端的一半:请求序列化经 ssh 出去,响应报文原样回来。
func TestSSHRequestRoundTrip(t *testing.T) {
	respBody := "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\nhello relay"
	argsFile := fakeSSH(t, respBody, "", 0)

	req, err := newRelayRequest(context.Background(), http.MethodGet, "/api/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := sshRequest(context.Background(), "root@example -p 5522", "mpx api-relay", req)
	if err != nil {
		t.Fatalf("sshRequest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "hello relay" {
		t.Fatalf("响应体 = %q", data)
	}
	// 参数:BatchMode + 目标按空白拆开 + 远端命令。
	got, _ := os.ReadFile(argsFile)
	for _, want := range []string{"BatchMode=yes", "root@example", "-p", "5522", "mpx api-relay"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("ssh 参数缺 %q: %s", want, got)
		}
	}
}

// ssh 失败时 stderr 原样进错误。
func TestSSHRequestFailure(t *testing.T) {
	fakeSSH(t, "", "ssh: Could not resolve hostname bad-target", 255)
	req, _ := newRelayRequest(context.Background(), http.MethodGet, "/api/status", nil)
	_, err := sshRequest(context.Background(), "bad-target", "mpx api-relay", req)
	if err == nil || !strings.Contains(err.Error(), "bad-target") {
		t.Fatalf("应报 ssh 目标错: %v", err)
	}
}

// 远端不认 api-relay 时给升级提示。
func TestSSHRequestOldRemote(t *testing.T) {
	fakeSSH(t, "", `Error: unknown command "api-relay" for "mpx"`, 1)
	req, _ := newRelayRequest(context.Background(), http.MethodGet, "/api/status", nil)
	_, err := sshRequest(context.Background(), "la-1", "mpx api-relay", req)
	if err == nil || !strings.Contains(err.Error(), "升级") {
		t.Fatalf("应提示升级: %v", err)
	}
}

// sshRoundTrip 走通道拿 200;429 时带回 Retry-After。
func TestSSHRoundTripThroughClient(t *testing.T) {
	argsFile := fakeSSH(t, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}", "", 0)
	c := &Client{server: "https://cp.example:8080", sshTarget: "la-1"}
	data, _, err := c.sshRoundTrip(context.Background(), http.MethodGet, "/api/nodes", nil, false)
	if err != nil {
		t.Fatalf("sshRoundTrip: %v", err)
	}
	if string(data) != "{}" {
		t.Fatalf("响应 = %q", data)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "api-relay --port 8080") {
		t.Fatalf("默认远端命令没带端口: %s", got)
	}

	fakeSSH(t, "HTTP/1.1 429 Too Many Requests\r\nRetry-After: 5\r\nContent-Length: 2\r\n\r\n{}", "", 0)
	_, wait, err := c.sshRoundTrip(context.Background(), http.MethodGet, "/api/nodes", nil, false)
	if err == nil || wait != 5*time.Second {
		t.Fatalf("429 应带 5s retryAfter: wait=%v err=%v", wait, err)
	}
}

// resolveSSH:server 匹配才沿用 profile 目标;不匹配返回空。
func TestResolveSSH(t *testing.T) {
	path := useTempConfig(t)
	writeRaw(t, path, `{"current":"default","profiles":{"default":{"server":"https://cp.example:8080","ssh":"la-1","ssh_cmd":"custom cmd"}}}`)

	target, remote := resolveSSH("https://cp.example:8080")
	if target != "la-1" || remote != "custom cmd" {
		t.Fatalf("resolveSSH = (%q,%q)", target, remote)
	}
	if target, _ := resolveSSH("https://other.example:8080"); target != "" {
		t.Fatalf("server 不匹配时应返回空,得到 %q", target)
	}
}

// saveProfile:带 SSH 写入时清掉凭据字段;不带 SSH 且同地址时保留旧目标。
func TestSaveProfileSSHSemantics(t *testing.T) {
	path := useTempConfig(t)
	writeRaw(t, path, `{"current":"default","profiles":{"default":{"server":"https://cp.example:8080","token":"old","ssh":"la-1","ssh_cmd":"cmd"}}}`)

	// 不带 SSH 的重写(密码路径):同地址保留通道。
	if _, err := saveProfile(Profile{Server: "https://cp.example:8080", Token: "t2"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["default"]
	if p.SSH != "la-1" || p.SSHCmd != "cmd" {
		t.Fatalf("同地址重写丢了 SSH 目标: %+v", p)
	}

	// 带 SSH 的重写:凭据字段清空。
	if _, err := saveProfile(Profile{Server: "https://cp.example:8080", Token: "t3", Fingerprint: "SHA256:x", CA: true, SSH: "la-2"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	p = cfg.Profiles["default"]
	if p.Token != "" || p.Fingerprint != "" || p.CA {
		t.Fatalf("通道模式不该存凭据: %+v", p)
	}
	if p.SSH != "la-2" {
		t.Fatalf("SSH 目标没更新: %+v", p)
	}

	// 换地址重写:旧目标丢弃。
	if _, err := saveProfile(Profile{Server: "https://new.example:8080", Token: "t4"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	p = cfg.Profiles["default"]
	if p.SSH != "" {
		t.Fatalf("换地址应丢弃旧目标: %+v", p)
	}
}

// runAPIRelay:注入主钥匙、改写目标到 127.0.0.1:<port>,响应原样回吐。
func TestAPIRelay(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("X-Test", "ok")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	apiRelayFlags.port = port
	defer func() { apiRelayFlags.port = 0 }()
	t.Setenv("MPX_TOKEN", "master-secret")

	req, _ := newRelayRequest(context.Background(), http.MethodGet, "/api/status", nil)
	var in bytes.Buffer
	if err := req.WriteProxy(&in); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runAPIRelay(&in, &out); err != nil {
		t.Fatalf("runAPIRelay: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(&out), req)
	if err != nil {
		t.Fatalf("relay 响应解析不了: %v\n%s", err, out.String())
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("X-Test") != "ok" {
		t.Fatalf("响应 = %d %v", resp.StatusCode, resp.Header)
	}
	if gotAuth != "Bearer master-secret" {
		t.Fatalf("主钥匙没注入: %q", gotAuth)
	}
	if gotPath != "/api/status" {
		t.Fatalf("路径没转对: %q", gotPath)
	}
}
