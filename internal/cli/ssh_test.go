package cli

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSSH 在 PATH 前头放一个假的 ssh:把收到的参数写进 argsFile,stdout 输出
// out,exit code 是 code。
func fakeSSH(t *testing.T, out string, code int) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		"printf '%s' '" + strings.ReplaceAll(out, "'", `'\''`) + "'\n" +
		"exit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

func TestSSHSessionCmd(t *testing.T) {
	cmd := sshSessionCmd(9443)
	for _, want := range []string{"MPX_TOKEN", "/etc/mpx/env", "127.0.0.1:9443", "admin session", "--kind ssh"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("远端铸会话命令缺 %q: %s", want, cmd)
		}
	}
}

func TestSSHTargetFromServer(t *testing.T) {
	for in, want := range map[string]string{
		"https://mpx.example.net:8080": "mpx.example.net",
		"https://203.0.113.10:8080":    "203.0.113.10",
		"https://mpx.example.net":      "mpx.example.net",
		"https://[2001:db8::1]:8080":   "2001:db8::1",
	} {
		if got := sshTargetFromServer(in); got != want {
			t.Errorf("sshTargetFromServer(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAPIPortFromServer(t *testing.T) {
	for in, want := range map[string]int{
		"https://mpx.example.net:9443": 9443,
		"https://mpx.example.net:8080": 8080,
		"https://mpx.example.net":      defaultControlPort,
		"https://mpx.example.net:bad":  defaultControlPort,
	} {
		if got := apiPortFromServer(in); got != want {
			t.Errorf("apiPortFromServer(%q) = %d, want %d", in, got, want)
		}
	}
}

// UC-28:ssh 返回的 token 被取回,目标串带 -p 之类参数时被原样转给 ssh。
func TestMintSessionViaSSH(t *testing.T) {
	argsFile := fakeSSH(t, "tok-from-ssh\n", 0)
	tok, _, err := mintSessionViaSSH(context.Background(), "root@cp.example -p 5522", "", 8080)
	if err != nil {
		t.Fatalf("mintSessionViaSSH: %v", err)
	}
	if tok != "tok-from-ssh" {
		t.Fatalf("token = %q", tok)
	}
	args, _ := os.ReadFile(argsFile)
	s := string(args)
	for _, want := range []string{"root@cp.example", "-p", "5522", "admin session --kind ssh"} {
		if !strings.Contains(s, want) {
			t.Errorf("ssh 参数里缺 %q: %s", want, s)
		}
	}
}

// UC-28:ssh 失败时透传 stderr,不落 token。
func TestMintSessionViaSSHFailure(t *testing.T) {
	fakeSSH(t, "", 255)
	if _, _, err := mintSessionViaSSH(context.Background(), "no-such-host", "", 8080); err == nil {
		t.Fatal("ssh 失败时应报错")
	}
}

// UC-28:profile 记了 SSH 目标时,401 自动续期重试一次,全程不需要密码。
func TestClientRenewsOn401(t *testing.T) {
	var calls atomic.Int32
	var auths []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer new-token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"凭据不正确或会话已过期"}`))
	})
	var renews atomic.Int32
	c.renew = func() (string, error) {
		renews.Add(1)
		c.token = "new-token"
		return "new-token", nil
	}
	if _, err := c.do("GET", "/api/nodes", nil); err != nil {
		t.Fatalf("续期重试后仍失败: %v", err)
	}
	if renews.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("renew=%d 次,请求=%d 次;期望各一次+重试", renews.Load(), calls.Load())
	}
	if auths[0] != "Bearer secret-token" || auths[1] != "Bearer new-token" {
		t.Fatalf("Authorization 头: %v", auths)
	}
}

// 续期后还是 401:不循环,错误原样回来。
func TestClientRenewOnceOnly(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"凭据不正确"}`))
	})
	var renews atomic.Int32
	c.renew = func() (string, error) {
		renews.Add(1)
		c.token = "new-token"
		return "new-token", nil
	}
	var apiErr *APIError
	_, err := c.do("GET", "/api/nodes", nil)
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("期望 401 原样回来,得到 %v", err)
	}
	if renews.Load() != 1 {
		t.Fatalf("续期只该发生一次,实际 %d", renews.Load())
	}
}

// 续期本身失败(SSH 不通):报续期的错,不吞原 401 的线索。
func TestClientRenewFailure(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"凭据不正确"}`))
	})
	c.renew = func() (string, error) {
		return "", errors.New("ssh la-1: connect timeout")
	}
	_, err := c.do("GET", "/api/nodes", nil)
	if err == nil || !strings.Contains(err.Error(), "ssh la-1") {
		t.Fatalf("期望带 ssh 原因的错,得到 %v", err)
	}
}

// profile 换了地址后,旧 SSH 目标不沿用(那台机器多半不是这台控制面)。
func TestSaveProfileDropsSSHOnServerChange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if _, err := saveProfile(Profile{Server: "https://a:1", Token: "t1", SSH: "la-1"}); err != nil {
		t.Fatal(err)
	}
	// 同地址重 connect(没带 SSH):续期目标保留。
	if _, err := saveProfile(Profile{Server: "https://a:1", Token: "t2"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if cfg.Profiles["default"].SSH != "la-1" {
		t.Fatalf("同地址重连不该丢 SSH 目标: %+v", cfg.Profiles["default"])
	}
	// 换地址:SSH 目标丢弃,不留错指。
	if _, err := saveProfile(Profile{Server: "https://b:2", Token: "t3"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = loadConfig()
	if cfg.Profiles["default"].SSH != "" {
		t.Fatalf("换地址后 SSH 目标应丢弃: %+v", cfg.Profiles["default"])
	}
}

// resolveSSH:只有生效地址与 profile 一致时才沿用 SSH 目标。
func TestResolveSSH(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if _, err := saveProfile(Profile{Server: "https://a:8080", Token: "t", SSH: "la-1", SSHCmd: "custom"}); err != nil {
		t.Fatal(err)
	}
	target, cmd, name := resolveSSH("https://a:8080")
	if target != "la-1" || cmd != "custom" || name != "default" {
		t.Fatalf("resolveSSH = %q %q %q", target, cmd, name)
	}
	if target, _, _ := resolveSSH("https://b:9"); target != "" {
		t.Fatalf("地址不一致时不该沿用 SSH 目标,得到 %q", target)
	}
}

// 编译期确认:UC-28 的时间字段不引入未用导入。
var _ = time.Second
