package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/pin"
	"github.com/RovenEverward/my-proxy/internal/server"
)

// controlHealthEnv 起一个假控制面:自签 TLS、按 token 鉴权、/api/status 回版本。
//
// 指纹写到库旁边的 tls-fingerprint,token 写到临时 env 文件 —— 正是 controlHealth
// 生产上读取的两个位置。
func controlHealthEnv(t *testing.T, token, version string) (addr, db, envFile string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	db = filepath.Join(dir, "mpx.db")
	if err := os.WriteFile(server.FingerprintPath(db), []byte(pin.Fingerprint(srv.Certificate())+"\n"), 0o644); err != nil {
		t.Fatalf("写指纹文件: %v", err)
	}
	envFile = filepath.Join(dir, "env")
	if err := os.WriteFile(envFile, []byte("MPX_TOKEN="+token+"\n"), 0o600); err != nil {
		t.Fatalf("写 env 文件: %v", err)
	}
	return strings.TrimPrefix(srv.URL, "https://"), db, envFile
}

// controlHealth 要能按指纹连上假控制面并读出它自报的版本。
func TestControlHealthReadsVersion(t *testing.T) {
	addr, db, envFile := controlHealthEnv(t, "secret-token", "v0.7.0")

	got, err := controlHealth(addr, envFile, db)(context.Background())
	if err != nil {
		t.Fatalf("健康检查报错: %v", err)
	}
	if got != "v0.7.0" {
		t.Errorf("版本 = %q, 期望 v0.7.0", got)
	}
}

// token 不对时控制面回 401,健康检查必须报错而不是把空版本当成功。
func TestControlHealthRejectsWrongToken(t *testing.T) {
	addr, db, envFile := controlHealthEnv(t, "right-token", "v0.7.0")
	// 改掉 env 文件里的 token,模拟机器上 token 过期/写错。
	if err := os.WriteFile(envFile, []byte("MPX_TOKEN=wrong-token\n"), 0o600); err != nil {
		t.Fatalf("改写 env 文件: %v", err)
	}

	if _, err := controlHealth(addr, envFile, db)(context.Background()); err == nil {
		t.Fatal("token 不对时该报错,实际成功")
	}
}

// env 文件里没有 MPX_TOKEN 时要明确报错,不能拿空 token 去连。
func TestControlHealthMissingToken(t *testing.T) {
	addr, db, envFile := controlHealthEnv(t, "secret-token", "v0.7.0")
	if err := os.WriteFile(envFile, []byte("OTHER=1\n"), 0o600); err != nil {
		t.Fatalf("改写 env 文件: %v", err)
	}

	if _, err := controlHealth(addr, envFile, db)(context.Background()); err == nil {
		t.Fatal("没有 MPX_TOKEN 时该报错,实际成功")
	}
}

// 「mpx update run control」必须能被解析成这条命令(单元文件里就是这么写的)。
func TestUpdateRunParsesControlArg(t *testing.T) {
	cmd, args, err := rootCmd.Find([]string{"update", "run", "control"})
	if err != nil || cmd != updateRunCmd {
		t.Fatalf("找不到 update run: %v", err)
	}
	if err := cmd.Args(cmd, args); err != nil {
		t.Fatalf("control 应当被接受: %v", err)
	}
	if err := cmd.Args(cmd, []string{"nodes"}); err == nil {
		t.Fatal("nodes 不该被接受")
	}
}
