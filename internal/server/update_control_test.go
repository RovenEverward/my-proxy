package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/selfupdate"
)

// controlDirEnv 起一个控制面并把升级目录指到临时目录,返回那个目录。
func controlDirEnv(t *testing.T) (*env, string) {
	t.Helper()
	e := newEnv(t)
	dir := filepath.Join(t.TempDir(), "update")
	e.s.SetUpdateDir(dir)
	return e, dir
}

// 202 之后 update 目录里要有一个内容正确的请求文件 —— root 侧执行器就靠它。
func TestUpdateControlWritesRequest(t *testing.T) {
	e, dir := controlDirEnv(t)

	code, body := e.api("POST", "/api/update/control", map[string]any{
		"version":         "v0.7.0",
		"allow_downgrade": true,
	})
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/update/control: got %d: %s", code, body)
	}
	resp := decodeJSON[map[string]string](t, body)
	if resp["to"] != "v0.7.0" || resp["action"] != "upgrade" {
		t.Errorf("回应不对:%+v", resp)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "request"))
	if err != nil {
		t.Fatalf("读请求文件: %v", err)
	}
	var req selfupdate.ControlRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("解析请求文件: %v\n%s", err, raw)
	}
	if req.Action != "upgrade" || req.To != "v0.7.0" || !req.AllowDowngrade {
		t.Errorf("请求文件内容不对:%+v", req)
	}
	if req.RequestedAt.IsZero() {
		t.Errorf("请求文件该带 requested_at:%+v", req)
	}
}

// 版本号留空表示最新,回应里写 latest。
func TestUpdateControlDefaultsToLatest(t *testing.T) {
	e, dir := controlDirEnv(t)

	code, body := e.api("POST", "/api/update/control", map[string]any{"action": "rollback"})
	if code != http.StatusAccepted {
		t.Fatalf("got %d: %s", code, body)
	}
	resp := decodeJSON[map[string]string](t, body)
	if resp["to"] != "latest" || resp["action"] != "rollback" {
		t.Errorf("回应不对:%+v", resp)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "request"))
	if err != nil {
		t.Fatalf("读请求文件: %v", err)
	}
	var req selfupdate.ControlRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("解析请求文件: %v", err)
	}
	if req.To != "" || req.Action != "rollback" {
		t.Errorf("请求文件内容不对:%+v", req)
	}
}

// 版本号不合法是调用方的问题,400,且不能落请求文件。
func TestUpdateControlRejectsBadVersion(t *testing.T) {
	e, dir := controlDirEnv(t)

	for _, v := range []string{"0.7.0", "v0.7", "v1.2.3.4", "latest", "v1.2.x"} {
		code, body := e.api("POST", "/api/update/control", map[string]any{"version": v})
		if code != http.StatusBadRequest {
			t.Errorf("version=%q 应当 400,实际 %d:%s", v, code, body)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "request")); !os.IsNotExist(err) {
		t.Errorf("坏输入不该留下请求文件: err=%v", err)
	}
}

// action 只认 upgrade/rollback,别的 400。
func TestUpdateControlRejectsBadAction(t *testing.T) {
	e, _ := controlDirEnv(t)

	code, body := e.api("POST", "/api/update/control", map[string]any{"action": "reinstall"})
	if code != http.StatusBadRequest {
		t.Fatalf("坏 action 应当 400,实际 %d:%s", code, body)
	}
}

// 已有请求文件时返回 409,不能把 root 还没处理的请求盖掉。
func TestUpdateControlConflictsOnPendingRequest(t *testing.T) {
	e, dir := controlDirEnv(t)

	code, body := e.api("POST", "/api/update/control", map[string]any{"version": "v0.7.0"})
	if code != http.StatusAccepted {
		t.Fatalf("第一次应当 202,实际 %d:%s", code, body)
	}
	code, body = e.api("POST", "/api/update/control", map[string]any{"version": "v0.8.0"})
	if code != http.StatusConflict {
		t.Fatalf("已有请求应当 409,实际 %d:%s", code, body)
	}
	// 409 之后原请求保持原样。
	raw, err := os.ReadFile(filepath.Join(dir, "request"))
	if err != nil {
		t.Fatalf("读请求文件: %v", err)
	}
	var req selfupdate.ControlRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("解析请求文件: %v", err)
	}
	if req.To != "v0.7.0" {
		t.Errorf("原请求被改动了:%+v", req)
	}
}

// 状态文件说上一次还在进行时返回 409,不落新请求。
func TestUpdateControlConflictsWhileRunning(t *testing.T) {
	e, dir := controlDirEnv(t)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建 update 目录: %v", err)
	}
	if err := selfupdate.SaveState(filepath.Join(dir, "state.json"), selfupdate.State{
		Component: "control",
		State:     "downloading",
	}); err != nil {
		t.Fatalf("写状态文件: %v", err)
	}

	code, body := e.api("POST", "/api/update/control", map[string]any{"version": "v0.7.0"})
	if code != http.StatusConflict {
		t.Fatalf("进行中应当 409,实际 %d:%s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "request")); !os.IsNotExist(err) {
		t.Errorf("进行中不该留下请求文件: err=%v", err)
	}
}

// 目录写不进去时 500,且信息要指向 install.sh。root 下 chmod 拦不住写入,跳过。
func TestUpdateControlUnwritableDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 Unix 文件权限位")
	}
	if os.Geteuid() == 0 {
		t.Skip("root 无视目录权限,跳过不可写用例")
	}
	e, dir := controlDirEnv(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建 update 目录: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("改目录权限: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	code, body := e.api("POST", "/api/update/control", map[string]any{"version": "v0.7.0"})
	if code != http.StatusInternalServerError {
		t.Fatalf("不可写应当 500,实际 %d:%s", code, body)
	}
	var e2 struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e2); err != nil {
		t.Fatalf("解析错误响应: %v", err)
	}
	if want := "install.sh"; !strings.Contains(e2.Error, want) {
		t.Errorf("错误信息该提到 %q:%q", want, e2.Error)
	}
}

// 没配库路径时接口禁用,返回 503。
func TestUpdateControlDisabledWithoutDBPath(t *testing.T) {
	e := newEnv(t)
	e.s.SetUpdateDir("")

	code, body := e.api("POST", "/api/update/control", map[string]any{"version": "v0.7.0"})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("未配置应当 503,实际 %d:%s", code, body)
	}
}

// 状态文件不存在时 GET /api/update 不带 last。
func TestUpdateSnapshotOmitsLastWhenNoState(t *testing.T) {
	e, _ := controlDirEnv(t)

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	if strings.Contains(string(body), `"last"`) {
		t.Errorf("没有状态时不该出现 last:%s", body)
	}
}

// 有状态文件时 GET /api/update 的 control 部分带上 last,字段一一对上。
func TestUpdateSnapshotIncludesLastRun(t *testing.T) {
	e, dir := controlDirEnv(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建 update 目录: %v", err)
	}
	if err := selfupdate.SaveState(filepath.Join(dir, "state.json"), selfupdate.State{
		Component: "control",
		State:     "idle",
		From:      "v0.6.0",
		To:        "v0.7.0",
		Result:    "ok",
		Trigger:   "manual",
		Error:     "",
	}); err != nil {
		t.Fatalf("写状态文件: %v", err)
	}

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	snap := decodeJSON[model.UpdateSnapshot](t, body)
	last := snap.Control.Last
	if last == nil {
		t.Fatalf("该带上 last 状态:%s", body)
	}
	if last.Component != "control" || last.State != "idle" || last.From != "v0.6.0" ||
		last.To != "v0.7.0" || last.Result != "ok" || last.Trigger != "manual" {
		t.Errorf("last 内容不对:%+v", *last)
	}
}

// 和已有接口一样,控制面升级接口也要 token。
func TestUpdateControlRequiresToken(t *testing.T) {
	e, _ := controlDirEnv(t)

	if code, _ := e.request("POST", "/api/update/control", map[string]any{"version": "v0.7.0"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("不带 token 应当 401,实际 %d", code)
	}
	code, _ := e.request("POST", "/api/update/control", map[string]any{"version": "v0.7.0"}, map[string]string{"Authorization": "Bearer wrong"})
	if code != http.StatusUnauthorized {
		t.Fatalf("token 不对应当 401,实际 %d", code)
	}
}

// 用 -ldflags 注入开发版运行此项，核对真实路由先拒绝、没有落请求文件。
func TestDevelopmentControlRejectsReleaseUpdate(t *testing.T) {
	if !buildinfo.IsDevelopment(buildinfo.Version()) {
		t.Skip("需要开发版构建")
	}
	e, dir := controlDirEnv(t)
	for _, action := range []string{"upgrade", "rollback"} {
		code, body := e.api("POST", "/api/update/control", map[string]any{"action": action})
		if code != http.StatusConflict || !strings.Contains(string(body), "mpx-dev deploy") {
			t.Fatalf("未拒绝发布件更新：%d", code)
		}
		if _, err := os.Stat(filepath.Join(dir, "request")); !os.IsNotExist(err) {
			t.Fatal("开发版不应落请求文件")
		}
	}
}
