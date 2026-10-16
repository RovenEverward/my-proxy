package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSetup 是测试用的 SetupRunner。
type fakeSetup struct {
	mu       sync.Mutex
	probe    map[string]string
	checkErr error
	runFn    func(context.Context, SetupParams, SetupProgress) (*SetupResult, error)
	checked  []SetupParams
	ran      []SetupParams
}

func (f *fakeSetup) Check(ctx context.Context, p SetupParams) (map[string]string, error) {
	f.mu.Lock()
	f.checked = append(f.checked, p)
	f.mu.Unlock()
	if p.Password == "" {
		return nil, errors.New("没收到密码")
	}
	return f.probe, f.checkErr
}

func (f *fakeSetup) Run(ctx context.Context, p SetupParams, prog SetupProgress) (*SetupResult, error) {
	f.mu.Lock()
	f.ran = append(f.ran, p)
	fn := f.runFn
	f.mu.Unlock()
	if fn == nil {
		return &SetupResult{}, nil
	}
	return fn(ctx, p, prog)
}

// setupRig 起一个 setup 模式的 Server 并返回已登录的 cookie。
func setupRig(t *testing.T, f *fakeSetup, allow ...string) (*Server, *http.Cookie) {
	t.Helper()
	if len(allow) == 0 {
		allow = []string{"GET /api"}
	}
	s, err := New("127.0.0.1:7480", Options{Setup: f, Allow: allow})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/?k="+s.Key(), nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("登录应 303,得到 %d %s", w.Code, w.Body)
	}
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	return s, c
}

func doJSON(s *Server, method, path string, c *http.Cookie, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7480"+path, strings.NewReader(body))
	if c != nil {
		r.AddCookie(c)
	}
	if method != http.MethodGet && r.Header.Get("Origin") == "" {
		r.Header.Set("Origin", "http://127.0.0.1:7480")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestSetupModeMetaAndAPI(t *testing.T) {
	s, c := setupRig(t, &fakeSetup{probe: map[string]string{"os": "linux"}})

	w := do(s, "GET", "/ui/meta", c)
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["mode"] != "setup" || m["setup"] != true || m["server"] != "" {
		t.Fatalf("setup meta 不对: %s", w.Body)
	}
	if w := do(s, "GET", "/api/status", c); w.Code != http.StatusServiceUnavailable ||
		!strings.Contains(w.Body.String(), "还没有控制面") {
		t.Fatalf("setup 下 /api 应 503,得到 %d %s", w.Code, w.Body)
	}
}

func TestSetupAuthAndOrigin(t *testing.T) {
	f := &fakeSetup{}
	s, err := New("127.0.0.1:7480", Options{Setup: f, Allow: []string{"GET /api"}})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(s, "GET", "/local/setup", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("无会话应 401,得到 %d", w.Code)
	}
	// 登录拿 cookie。
	w := do(s, "GET", "/?k="+s.Key(), nil)
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	if w := do(s, "GET", "/local/setup", c, "Origin", "http://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("跨站应 403,得到 %d", w.Code)
	}
}

func TestSetupCheck(t *testing.T) {
	const pw = "hunter2-secret-pw"
	f := &fakeSetup{probe: map[string]string{"os": "linux", "arch": "amd64"}}
	s, c := setupRig(t, f)

	body := `{"host":"1.2.3.4","ssh_port":22,"ssh_user":"root","password":"` + pw + `"}`
	w := doJSON(s, "POST", "/local/setup/check", c, body)
	if w.Code != 200 {
		t.Fatalf("check 应 200,得到 %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), pw) {
		t.Fatal("check 响应泄露了密码")
	}
	var resp struct {
		Probe map[string]string `json:"probe"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Probe["os"] != "linux" {
		t.Fatalf("probe 不对: %s", w.Body)
	}
	f.mu.Lock()
	got := f.checked
	f.mu.Unlock()
	if len(got) != 1 || got[0].Password != pw || got[0].SSHUser != "root" {
		t.Fatalf("runner 收到的参数不对: %+v", got)
	}

	// 失败:502 且是原话。
	f.checkErr = errors.New("SSH 连不上: 拒绝连接")
	body2 := `{"host":"1.2.3.4","ssh_port":22,"password":"` + pw + `"}`
	w = doJSON(s, "POST", "/local/setup/check", c, body2)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "SSH 连不上: 拒绝连接") {
		t.Fatalf("check 失败应 502 原话,得到 %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), pw) {
		t.Fatal("失败响应泄露了密码")
	}

	// 校验:host 必填、端口范围。
	if w := doJSON(s, "POST", "/local/setup/check", c, `{"ssh_port":22}`); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "请填写服务器地址") {
		t.Fatalf("host 校验: %d %s", w.Code, w.Body)
	}
	if w := doJSON(s, "POST", "/local/setup/check", c, `{"host":"x","ssh_port":70000}`); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "端口要在 1–65535 之间") {
		t.Fatalf("端口校验: %d %s", w.Code, w.Body)
	}
}

func TestSetupRunAndSwitchToNormal(t *testing.T) {
	var authMu sync.Mutex
	var authHdr string
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.Lock()
		authHdr = r.Header.Get("Authorization")
		authMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer cp.Close()
	up, _ := url.Parse(cp.URL)

	f := &fakeSetup{probe: map[string]string{"os": "linux"}}
	f.runFn = func(ctx context.Context, p SetupParams, prog SetupProgress) (*SetupResult, error) {
		prog.Step("连接")
		prog.Line("connecting...")
		prog.Step("安装")
		prog.Line("done")
		return &SetupResult{Upstream: up, Token: "setup-token-xyz", Profile: "装好了", Connected: true}, nil
	}
	s, c := setupRig(t, f)

	// 带 password 的 run 直接拒绝。
	bad := `{"host":"h","name":"n","username":"u","password":"p"}`
	if w := doJSON(s, "POST", "/local/setup/run", c, bad); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "密码只在检查那一步用") {
		t.Fatalf("run 带密码应 400,得到 %d %s", w.Code, w.Body)
	}
	// 设备密码太短。
	short := `{"host":"h","name":"n","username":"u","device_password":"short"}`
	if w := doJSON(s, "POST", "/local/setup/run", c, short); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "设备密码至少 12 个字符") {
		t.Fatalf("短设备密码应 400,得到 %d %s", w.Code, w.Body)
	}
	// 名字/用户名为空。
	if w := doJSON(s, "POST", "/local/setup/run", c, `{"host":"h"}`); w.Code != 400 {
		t.Fatalf("缺 name/username 应 400,得到 %d", w.Code)
	}

	ok := `{"host":"h","ssh_port":22,"name":"n","username":"u","device_password":"123456789012"}`
	w := doJSON(s, "POST", "/local/setup/run", c, ok)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run 应 202,得到 %d %s", w.Code, w.Body)
	}
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil || started.ID == "" {
		t.Fatalf("run 返回 id 不对: %s", w.Body)
	}

	// 轮询到 done。
	d := waitJob(t, s, c, started.ID)
	if d.State != "done" {
		t.Fatalf("任务应 done,得到 %q err=%q", d.State, d.Error)
	}

	// 切到 normal。
	wm := do(s, "GET", "/ui/meta", c)
	var meta map[string]any
	if err := json.Unmarshal(wm.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["mode"] != "normal" || meta["server"] != cp.URL || meta["profile"] != "装好了" {
		t.Fatalf("装完后 meta 不对: %s", wm.Body)
	}
	if w := do(s, "GET", "/api/x", c); w.Code != 200 {
		t.Fatalf("转发应 200,得到 %d %s", w.Code, w.Body)
	}
	authMu.Lock()
	gotAuth := authHdr
	authMu.Unlock()
	if gotAuth != "Bearer setup-token-xyz" {
		t.Fatalf("转发 Authorization = %q", gotAuth)
	}
}

func TestSetupRunConflictWhileRunning(t *testing.T) {
	release := make(chan struct{})
	f := &fakeSetup{}
	f.runFn = func(ctx context.Context, p SetupParams, prog SetupProgress) (*SetupResult, error) {
		prog.Step("慢慢来")
		<-release
		return &SetupResult{}, nil
	}
	s, c := setupRig(t, f)
	body := `{"host":"h","name":"n","username":"u"}`
	w := doJSON(s, "POST", "/local/setup/run", c, body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("第一次 run 应 202,得到 %d %s", w.Code, w.Body)
	}
	w = doJSON(s, "POST", "/local/setup/run", c, body)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "已经在安装了") {
		t.Fatalf("运行中再 run 应 409,得到 %d %s", w.Code, w.Body)
	}
	var conflict struct {
		Error string `json:"error"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conflict); err != nil || conflict.ID == "" {
		t.Fatalf("409 应带 id: %s", w.Body)
	}
	close(release)
}

func TestSetupDeltaIncremental(t *testing.T) {
	f := &fakeSetup{}
	f.runFn = func(ctx context.Context, p SetupParams, prog SetupProgress) (*SetupResult, error) {
		prog.Step("长输出")
		for i := 0; i < 3000; i++ {
			prog.Line(strings.Repeat("x", 800))
		}
		return &SetupResult{Connected: true}, nil
	}
	s, c := setupRig(t, f)
	w := doJSON(s, "POST", "/local/setup/run", c, `{"host":"h","name":"n","username":"u"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run 应 202,得到 %d", w.Code)
	}
	var started struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &started)

	var since int64
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("轮询超时")
		default:
		}
		rec := do(s, "GET", "/local/setup/jobs/"+started.ID+"?since="+itoa(since), c)
		if rec.Body.Len() >= 4096 {
			t.Fatalf("每次增量应 < 4096 字节,得到 %d", rec.Body.Len())
		}
		var d setupDelta
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		since = d.Seq
		if d.State != "running" && !d.More {
			// done 之后用最后的 seq 再拉:很小且不含 lines/steps。
			rec = do(s, "GET", "/local/setup/jobs/"+started.ID+"?since="+itoa(since), c)
			if rec.Body.Len() >= 200 {
				t.Fatalf("done 后增量应 < 200 字节,得到 %d %s", rec.Body.Len(), rec.Body)
			}
			if strings.Contains(rec.Body.String(), "lines") || strings.Contains(rec.Body.String(), "steps") {
				t.Fatalf("done 后不该有 lines/steps: %s", rec.Body)
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestSetupJobIDMismatch(t *testing.T) {
	s, c := setupRig(t, &fakeSetup{})
	if w := do(s, "GET", "/local/setup/jobs/nope", c); w.Code != http.StatusNotFound {
		t.Fatalf("id 不匹配应 404,得到 %d", w.Code)
	}
}

// waitJob 轮询 jobs 接口直到任务结束。
func waitJob(t *testing.T, s *Server, c *http.Cookie, id string) setupDelta {
	t.Helper()
	var since int64
	for i := 0; i < 2000; i++ {
		rec := do(s, "GET", "/local/setup/jobs/"+id+"?since="+itoa(since), c)
		if rec.Code != 200 {
			t.Fatalf("轮询应 200,得到 %d %s", rec.Code, rec.Body)
		}
		var d setupDelta
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		since = d.Seq
		if d.State != "running" && !d.More {
			return d
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("任务一直没结束")
	return setupDelta{}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
