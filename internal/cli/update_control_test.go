package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// controlStep 是假控制面在 POST 之后某一次 GET 的回答。
// status 非 0 且非 200 时回一个错误(模拟控制面重启途中的 502)。
type controlStep struct {
	status int
	last   *model.UpdateRun
}

// fakeControl 是一个带状态机的假控制面:POST 之前 GET 回 preLast,POST 之后按
// steps 依次回,用完一直重复最后一个。用它把下载→502→健康检查→idle 这条真实
// 时间线压成几次调用。
type fakeControl struct {
	mu       sync.Mutex
	posted   bool
	getAfter int
	steps    []controlStep
	preLast  *model.UpdateRun
	current  string
	latest   string

	postCode  int
	postError string
	lastPost  map[string]any
}

// start 起服务并把控制面地址指过去。
func (cp *fakeControl) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(cp.handle))
	t.Cleanup(srv.Close)
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "secret-token")
	opts.server, opts.token = "", ""

	oldTTY := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = oldTTY })
}

func (cp *fakeControl) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/update":
		cp.mu.Lock()
		posted, idx, steps, preLast := cp.posted, cp.getAfter, cp.steps, cp.preLast
		if posted {
			cp.getAfter++
		}
		cp.mu.Unlock()

		if !posted {
			writeSnapshot(w, cp.current, cp.latest, preLast)
			return
		}
		if len(steps) == 0 {
			writeSnapshot(w, cp.current, cp.latest, nil)
			return
		}
		if idx >= len(steps) {
			idx = len(steps) - 1
		}
		st := steps[idx]
		if st.status != 0 && st.status != http.StatusOK {
			http.Error(w, `{"error":"控制面重启中"}`, st.status)
			return
		}
		writeSnapshot(w, cp.current, cp.latest, st.last)

	case r.Method == http.MethodPost && r.URL.Path == "/api/update/control":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cp.mu.Lock()
		cp.lastPost = body
		code, msg := cp.postCode, cp.postError
		if code == 0 {
			cp.posted = true
			cp.getAfter = 0
		}
		cp.mu.Unlock()

		if code != 0 {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from": cp.current, "to": cp.latest, "action": fmt.Sprint(body["action"]),
		})

	default:
		http.Error(w, `{"error":"没有这个接口"}`, http.StatusNotFound)
	}
}

func writeSnapshot(w http.ResponseWriter, current, latest string, last *model.UpdateRun) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(model.UpdateSnapshot{
		Control: model.UpdatePart{Current: current, Latest: latest, Last: last},
	})
}

// step 拼一个"这一轮"的阶段/结果,开始时间取当下(轮询按发请求前的时间判断新
// 旧,取当下最省事)。
func newControlStep(stage, result, from, to, errMsg string) controlStep {
	return controlStep{last: &model.UpdateRun{
		Component: "control", State: stage, Result: result,
		From: from, To: to, Error: errMsg, StartedAt: time.Now(),
	}}
}

// withControlPollTiming 把轮询间隔与总超时压到毫秒级,测试结束复原。
func withControlPollTiming(t *testing.T, timeout time.Duration) {
	t.Helper()
	oldInterval, oldTimeout := controlPollInterval, controlPollTimeout
	controlPollInterval = time.Millisecond
	controlPollTimeout = timeout
	t.Cleanup(func() { controlPollInterval, controlPollTimeout = oldInterval, oldTimeout })
}

// 正常一轮:下载→(502)→健康检查→idle/ok,阶段要打出来,最后报已升级。
func TestUpdateApplyControlUpgrades(t *testing.T) {
	withControlPollTiming(t, 5*time.Second)
	cp := &fakeControl{
		current: "v0.6.0",
		latest:  "v0.6.1",
		steps: []controlStep{
			newControlStep("downloading", "", "v0.6.0", "v0.6.1", ""),
			{status: http.StatusBadGateway},
			newControlStep("health-check", "", "v0.6.0", "v0.6.1", ""),
			newControlStep("idle", "ok", "v0.6.0", "v0.6.1", ""),
		},
	}
	cp.start(t)

	out := captureStdout(t, func() {
		runRoot(t, "update", "apply", "control", "--version", "v0.6.1", "-y")
	})

	if !strings.Contains(out, "控制面 v0.6.0 → v0.6.1") {
		t.Errorf("计划那一行不对:\n%s", out)
	}
	for _, want := range []string{"下载", "健康检查", "控制面重启中…", "已升级到 v0.6.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里缺少 %q:\n%s", want, out)
		}
	}
	if got := cp.lastPost["action"]; got != "upgrade" {
		t.Errorf("POST action = %v, 想要 upgrade", got)
	}
	if got := cp.lastPost["version"]; got != "v0.6.1" {
		t.Errorf("POST version = %v, 想要 v0.6.1", got)
	}
}

// from == to 时不报"已升级",而是说已经是最新。
func TestUpdateApplyControlAlreadyLatest(t *testing.T) {
	withControlPollTiming(t, 5*time.Second)
	cp := &fakeControl{
		current: "v0.6.1",
		latest:  "v0.6.1",
		steps:   []controlStep{newControlStep("idle", "ok", "v0.6.1", "v0.6.1", "")},
	}
	cp.start(t)

	out := captureStdout(t, func() {
		runRoot(t, "update", "apply", "control", "-y")
	})
	if !strings.Contains(out, "控制面已是最新（v0.6.1）") {
		t.Errorf("该说已经是最新:\n%s", out)
	}
}

// 自动回退:错误里要带上退回到的版本与原因。
func TestUpdateApplyControlRolledBack(t *testing.T) {
	withControlPollTiming(t, 5*time.Second)
	cp := &fakeControl{
		current: "v0.6.1",
		latest:  "v0.6.2",
		steps: []controlStep{
			newControlStep("switching", "", "v0.6.1", "v0.6.2", ""),
			newControlStep("idle", "rolled-back", "v0.6.1", "v0.6.2", "健康检查没通过"),
		},
	}
	cp.start(t)

	err := runRootErr(t, "update", "apply", "control", "--version", "v0.6.2", "-y")
	if err == nil {
		t.Fatal("自动回退应当返回错误")
	}
	if !strings.Contains(err.Error(), "已自动回退到 v0.6.1") || !strings.Contains(err.Error(), "健康检查没通过") {
		t.Fatalf("错误不对: %v", err)
	}
}

// 直接失败:错误里带原因。
func TestUpdateApplyControlFailed(t *testing.T) {
	withControlPollTiming(t, 5*time.Second)
	cp := &fakeControl{
		current: "v0.6.0",
		latest:  "v0.6.1",
		steps:   []controlStep{newControlStep("idle", "failed", "v0.6.0", "v0.6.1", "下载失败")},
	}
	cp.start(t)

	err := runRootErr(t, "update", "apply", "control", "-y")
	if err == nil || !strings.Contains(err.Error(), "升级失败：下载失败") {
		t.Fatalf("err = %v, 想要「升级失败：下载失败」", err)
	}
}

// 一直等不到 idle 就超时,并提示去看 mpx update。
func TestUpdateApplyControlTimesOut(t *testing.T) {
	withControlPollTiming(t, 30*time.Millisecond)
	cp := &fakeControl{
		current: "v0.6.0",
		latest:  "v0.6.1",
		steps:   []controlStep{newControlStep("downloading", "", "v0.6.0", "v0.6.1", "")},
	}
	cp.start(t)

	err := runRootErr(t, "update", "apply", "control", "-y")
	if err == nil || !strings.Contains(err.Error(), "等待超时") {
		t.Fatalf("err = %v, 想要等待超时", err)
	}
}

// 旧控制面没有 /api/update:说清要先用 install.sh 升一次。
func TestUpdateApplyControlOldControlPlane(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"没有这个接口"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "secret-token")
	opts.server, opts.token = "", ""

	err := runRootErr(t, "update", "apply", "control", "-y")
	if err == nil || !strings.Contains(err.Error(), "控制面版本过旧") || !strings.Contains(err.Error(), "install.sh") {
		t.Fatalf("err = %v, 想要旧控制面提示", err)
	}
}

// 上一次还在进行时先拒绝,不去登记请求(否则只会撞 409)。
func TestUpdateApplyControlRejectsWhileRunning(t *testing.T) {
	cp := &fakeControl{
		current: "v0.6.0",
		latest:  "v0.6.1",
		preLast: &model.UpdateRun{Component: "control", State: "downloading", StartedAt: time.Now()},
	}
	cp.start(t)

	err := runRootErr(t, "update", "apply", "control", "-y")
	if err == nil || !strings.Contains(err.Error(), "控制面上一次升级还在进行") || !strings.Contains(err.Error(), "下载") {
		t.Fatalf("err = %v, 想要进行中提示", err)
	}
	if cp.lastPost != nil {
		t.Errorf("进行中不该发 POST: %+v", cp.lastPost)
	}
}

// 非终端又没有 -y:在发 POST 之前就拒绝。
func TestUpdateApplyControlRejectsNonInteractiveWithoutYes(t *testing.T) {
	cp := &fakeControl{current: "v0.6.0", latest: "v0.6.1"}
	cp.start(t)

	err := runRootErr(t, "update", "apply", "control")
	if err == nil || !strings.Contains(err.Error(), "非交互环境请加 -y") {
		t.Fatalf("err = %v, 想要「非交互环境请加 -y」", err)
	}
	if cp.lastPost != nil {
		t.Errorf("被拒绝了却发了 POST: %+v", cp.lastPost)
	}
}

// 409 等服务端错误原样透传。
func TestUpdateApplyControlPassesThroughConflict(t *testing.T) {
	withControlPollTiming(t, 5*time.Second)
	cp := &fakeControl{
		current:   "v0.6.0",
		latest:    "v0.6.1",
		postCode:  http.StatusConflict,
		postError: "已有待执行的升级请求",
	}
	cp.start(t)

	err := runRootErr(t, "update", "apply", "control", "-y")
	if err == nil || err.Error() != "已有待执行的升级请求" {
		t.Fatalf("err = %v, 想要原样透传 409", err)
	}
}

// rollback control:确认后发 action=rollback,ok 时报已回退到目标版本。
func TestUpdateRollbackControl(t *testing.T) {
	withControlPollTiming(t, 5*time.Second)
	cp := &fakeControl{
		current: "v0.6.1",
		latest:  "v0.6.0",
		steps:   []controlStep{newControlStep("idle", "ok", "v0.6.1", "v0.6.0", "")},
	}
	cp.start(t)

	out := captureStdout(t, func() {
		runRoot(t, "update", "rollback", "control", "-y")
	})
	if !strings.Contains(out, "已回退到 v0.6.0") {
		t.Errorf("回退输出不对:\n%s", out)
	}
	if got := cp.lastPost["action"]; got != "rollback" {
		t.Errorf("POST action = %v, 想要 rollback", got)
	}
}

// update 表格里控制面那一行要带上最近一次结果,进行中则报阶段。
func TestUpdateShowsControlLastResult(t *testing.T) {
	opts.output = outputTable
	t.Cleanup(func() { opts.output = outputTable })
	withLocalVersion(t, "v0.6.1")

	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.UpdateSnapshot{
			Control: model.UpdatePart{
				Current: "v0.6.1", Latest: "v0.6.2", Available: true,
				Last: &model.UpdateRun{
					Component: "control", State: "idle", Result: "ok",
					From: "v0.6.0", To: "v0.6.1",
					FinishedAt: time.Date(2026, 5, 1, 3, 2, 0, 0, time.Local),
				},
			},
		})
	})
	out := captureStdout(t, func() { runRoot(t, "update") })
	if !strings.Contains(out, "上次") || !strings.Contains(out, "v0.6.0→v0.6.1") || !strings.Contains(out, "成功") {
		t.Errorf("控制面那行没带上上次结果:\n%s", out)
	}

	// 换一个还在跑的假控制面:进行中要报阶段,不说"上次"。
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.UpdateSnapshot{
			Control: model.UpdatePart{
				Current: "v0.6.1",
				Last:    &model.UpdateRun{Component: "control", State: "downloading", StartedAt: time.Now()},
			},
		})
	})
	out = captureStdout(t, func() { runRoot(t, "update") })
	if !strings.Contains(out, "进行中：下载") {
		t.Errorf("进行中该报阶段:\n%s", out)
	}
}

// 阶段名与结果词的翻译各测一遍,免得改了映射没人发现。
func TestControlStageName(t *testing.T) {
	cases := map[string]string{
		"downloading":  "下载",
		"verifying":    "校验",
		"switching":    "切换",
		"health-check": "健康检查",
		"other":        "other",
	}
	for in, want := range cases {
		if got := controlStageName(in); got != want {
			t.Errorf("controlStageName(%q) = %q, 想要 %q", in, in, want)
		}
	}
}

// 发请求前刚结束的上一轮(一分钟内)不能被当成这一轮的结果。
func TestUpdateApplyControlIgnoresRecentPreviousRun(t *testing.T) {
	withControlPollTiming(t, 5*time.Second)
	old := &model.UpdateRun{Component: "control", State: "idle", Result: "failed",
		From: "v0.6.0", To: "v0.6.1", Error: "上一轮的失败", StartedAt: time.Now().Add(-10 * time.Second)}
	newRun := newControlStep("idle", "ok", "v0.6.0", "v0.6.1", "")
	newRun.last.StartedAt = time.Now().Add(time.Second)
	cp := &fakeControl{
		current: "v0.6.0", latest: "v0.6.1", preLast: old,
		steps: []controlStep{{last: old}, {last: old}, newRun},
	}
	cp.start(t)
	out := captureStdout(t, func() {
		runRoot(t, "update", "apply", "control", "-y")
	})
	if !strings.Contains(out, "已升级到 v0.6.1") {
		t.Fatalf("应等到这一轮的结果:\n%s", out)
	}
}
