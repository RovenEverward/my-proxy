package ui

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/selfupdate"
)

// fakeControlPlane 把一键更新会走到的控制面接口做成可编排的假实现:current 在
// POST /api/update/control 之后翻到 latest,节点任务按「第一次拉 running、之后
// done/failed」递进,让每一步的成功与失败都能单独摆出来。
type fakeControlPlane struct {
	mu       sync.Mutex
	current  string
	latest   string
	rawNodes string // /api/update 的 nodes 原文,默认空数组

	controlReject bool          // POST /api/update/control 直接 400
	controlHang   chan struct{} // 非空时 POST /api/update/control 等它关掉才返回
	nodesMissing  bool          // POST /api/update/nodes 回 404(旧控制面)
	nodesFirst    string
	nodesThen     string
	nodesEvents   []model.UpdateNodeEvent

	controlPosts int
	nodesPosts   int
	nodesGets    int
}

func (f *fakeControlPlane) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/update" && r.Method == http.MethodGet:
		nodes := f.rawNodes
		if nodes == "" {
			nodes = "[]"
		}
		fmt.Fprintf(w, `{"control":{"current":%q,"latest":%q},"nodes":%s}`, f.current, f.latest, nodes)
	case r.URL.Path == "/api/status":
		_, _ = w.Write([]byte(`{"update":{}}`))
	case r.URL.Path == "/api/update/control" && r.Method == http.MethodPost:
		f.controlPosts++
		if f.controlReject {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"版本号不对"}`))
			return
		}
		if f.controlHang != nil {
			hang := f.controlHang
			f.mu.Unlock()
			<-hang
			f.mu.Lock()
		}
		f.current = f.latest
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	case r.URL.Path == "/api/update/nodes" && r.Method == http.MethodPost:
		f.nodesPosts++
		f.nodesGets = 0
		if f.nodesMissing {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"没有这个接口"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	case r.URL.Path == "/api/update/nodes" && r.Method == http.MethodGet:
		f.nodesGets++
		state := f.nodesFirst
		if f.nodesGets >= 2 {
			state = f.nodesThen
		}
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		events := []model.UpdateNodeEvent{}
		for _, ev := range f.nodesEvents {
			if ev.Seq > since {
				events = append(events, ev)
			}
		}
		_ = json.NewEncoder(w).Encode(model.UpdateNodesProgress{
			State: state, Seq: int64(len(f.nodesEvents)), Events: events,
		})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeControlPlane) counts() (control, nodes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.controlPosts, f.nodesPosts
}

// allRig 用假控制面起一个 ui.Server,返回已登录的 cookie。
func allRig(t *testing.T, cp *fakeControlPlane) (*Server, *http.Cookie) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(cp.handle))
	t.Cleanup(srv.Close)
	up, _ := url.Parse(srv.URL)
	s, err := New("127.0.0.1:7480", Options{Upstream: up, Token: "secret-token", Allow: []string{"GET /api"}})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/?k="+s.Key(), nil)
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	if c == nil {
		t.Fatal("没拿到会话 cookie")
	}
	return s, c
}

// withUpdateAllTiming 把编排的节拍调快,测试不用真等 2 秒一拍。
func withUpdateAllTiming(t *testing.T) {
	t.Helper()
	op, oc, on, oa, od := updatePollInterval, updateControlWait, updateNodesWait, updateRestartAck, updateRestartDelay
	updatePollInterval = 2 * time.Millisecond
	updateControlWait = 2 * time.Second
	updateNodesWait = 2 * time.Second
	updateRestartAck = 50 * time.Millisecond
	updateRestartDelay = time.Millisecond
	t.Cleanup(func() {
		updatePollInterval, updateControlWait, updateNodesWait, updateRestartAck, updateRestartDelay = op, oc, on, oa, od
	})
}

// tarGzWithMPX 打一个只装着一个 mpx 文件的 tar.gz,内容是 content。
func tarGzWithMPX(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "mpx", Mode: 0o755, Size: int64(len(content))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withFakeRelease 接管本机 CLI 升级的两处外部依赖:发布件来源指向假服务器,
// 本机可执行文件指向临时目录里的一份。返回的计数器记录假服务器被问了几次,
// 失败路径的测试用它证明「后面的步骤没有做」。
func withFakeRelease(t *testing.T, latest, newBin string) *int32 {
	t.Helper()
	var hits int32
	archive := tarGzWithMPX(t, newBin)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":` + strconv.Quote(latest) + `}`))
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			// 旧发布没有清单:404 走「未校验也继续」的分支,不用真算摘要。
			http.NotFound(w, r)
		default:
			_, _ = w.Write(archive)
		}
	}))
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "mpx")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho mpx v0.6.4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSrc, oldExe := updateSource, selfExecutable
	updateSource = func() selfupdate.Source {
		return selfupdate.Source{Repo: "test/mpx", APIBase: srv.URL, DownloadBase: srv.URL}
	}
	selfExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { updateSource, selfExecutable = oldSrc, oldExe })
	return &hits
}

// stubRestart 把重启换成只记录交接文件路径的替身,返回拿到路径的通道。
func stubRestart(t *testing.T) <-chan string {
	t.Helper()
	called := make(chan string, 1)
	old := restartSelf
	restartSelf = func(stateFile string) error {
		called <- stateFile
		return nil
	}
	t.Cleanup(func() { restartSelf = old })
	return called
}

// waitAllState 轮询进度接口直到 state 是 want 之一;先到 failed 而没在等它时,
// 把已积累的事件打出来,别让测试只报一个超时。
func waitAllState(t *testing.T, s *Server, c *http.Cookie, want ...string) updateAllSnapshot {
	t.Helper()
	wantFailed := false
	for _, x := range want {
		wantFailed = wantFailed || x == updateAllFailed
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		w := doJSON(s, http.MethodGet, "/local/update/all", c, "")
		if w.Code != http.StatusOK {
			t.Fatalf("进度接口应 200,得到 %d %s", w.Code, w.Body)
		}
		var snap updateAllSnapshot
		if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
			t.Fatal(err)
		}
		for _, x := range want {
			if snap.State == x {
				return snap
			}
		}
		if snap.State == updateAllFailed && !wantFailed {
			t.Fatalf("任务失败了(在等 %v): step=%s 事件=%+v", want, snap.Step, snap.Events)
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 state=%v 超时,现在 state=%s step=%s", want, snap.State, snap.Step)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestUpdateAllHappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假本机二进制是 sh 脚本,Windows 上无法执行")
	}
	withUpdateAllTiming(t)
	withLocalVersion(t, "v0.6.4")
	hits := withFakeRelease(t, "v0.6.5", "#!/bin/sh\necho mpx v0.6.5\n")
	cp := &fakeControlPlane{
		current: "v0.6.4", latest: "v0.6.5",
		nodesFirst: "running", nodesThen: "done",
		nodesEvents: []model.UpdateNodeEvent{
			{Seq: 1, Node: "la-1", Core: model.CoreXray, From: "26.3.27", To: "26.4.1", Status: "start"},
			{Seq: 2, Node: "la-1", Core: model.CoreXray, From: "26.3.27", To: "26.4.1", Status: "ok"},
		},
	}
	s, c := allRig(t, cp)
	restarted := stubRestart(t)

	w := doJSON(s, http.MethodPost, "/local/update/all", c, "{}")
	if w.Code != http.StatusAccepted {
		t.Fatalf("启动应 202,得到 %d %s", w.Code, w.Body)
	}

	var path string
	select {
	case path = <-restarted:
	case <-time.After(15 * time.Second):
		t.Fatal("没有走到重启这一步")
	}
	// 交接文件要能被新进程读回,内容正是本服务的地址与会话(cookie 的值)。
	st, err := ReadRestartState(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Addr != "127.0.0.1:7480" || st.Session == "" || st.Session != s.sessionValue() || st.Session != c.Value {
		t.Fatalf("交接文件不对: %+v", st)
	}

	snap := waitAllState(t, s, c, updateAllRestarting)
	if snap.Step != updateStepRestart {
		t.Fatalf("重启时 step 应是 restart,得到 %q", snap.Step)
	}
	want := []updateAllEvent{
		{Seq: 1, Step: "control", Status: "ok", Msg: "控制面 v0.6.4 → v0.6.5"},
		{Seq: 2, Step: "nodes", Status: "start", Msg: "la-1 xray 26.3.27 → 26.4.1 start"},
		{Seq: 3, Step: "nodes", Status: "ok", Msg: "la-1 xray 26.3.27 → 26.4.1 ok"},
		{Seq: 4, Step: "cli", Status: "ok", Msg: "本机 CLI v0.6.4 → v0.6.5"},
		{Seq: 5, Step: "restart", Status: "start", Msg: "重启 mpx ui 启用新版本"},
	}
	if !sameEvents(snap.Events, want) {
		t.Fatalf("事件不对:\n得到 %+v\n应为 %+v", snap.Events, want)
	}
	if atomic.LoadInt32(hits) == 0 {
		t.Fatal("本机升级没有走假发布服务器")
	}
	if ctl, nodes := cp.counts(); ctl != 1 || nodes != 1 {
		t.Fatalf("控制面升了 %d 次、节点任务起了 %d 次,都应正好一次", ctl, nodes)
	}
}

func sameEvents(got, want []updateAllEvent) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestUpdateAllControlFailureStops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假本机二进制是 sh 脚本,Windows 上无法执行")
	}
	withUpdateAllTiming(t)
	withLocalVersion(t, "v0.6.4")
	// 不该被碰到:控制面失败后,节点与本机都不能开始。
	hits := withFakeRelease(t, "v0.6.5", "#!/bin/sh\necho mpx v0.6.5\n")
	cp := &fakeControlPlane{current: "v0.6.4", latest: "v0.6.5", controlReject: true}
	s, c := allRig(t, cp)
	restarted := stubRestart(t)

	if w := doJSON(s, http.MethodPost, "/local/update/all", c, "{}"); w.Code != http.StatusAccepted {
		t.Fatalf("启动应 202,得到 %d %s", w.Code, w.Body)
	}
	snap := waitAllState(t, s, c, updateAllFailed)
	if snap.Step != updateStepControl {
		t.Fatalf("失败应停在控制面这一步,得到 step=%s", snap.Step)
	}
	if len(snap.Events) != 1 || snap.Events[0].Step != updateStepControl || snap.Events[0].Status != "failed" ||
		!strings.Contains(snap.Events[0].Msg, "控制面拒绝升级") {
		t.Fatalf("应只有一条控制面 failed 事件,得到 %+v", snap.Events)
	}
	if ctl, nodes := cp.counts(); nodes != 0 || ctl != 1 {
		t.Fatalf("节点任务不该启动(nodes=%d),控制面请求正好一次(control=%d)", nodes, ctl)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("控制面失败后不该碰本机升级的发布服务器")
	}
	select {
	case p := <-restarted:
		t.Fatalf("不该重启,却拿到了交接文件 %s", p)
	default:
	}
}

func TestUpdateAllNodesFailureStops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假本机二进制是 sh 脚本,Windows 上无法执行")
	}
	withUpdateAllTiming(t)
	withLocalVersion(t, "v0.6.4")
	hits := withFakeRelease(t, "v0.6.5", "#!/bin/sh\necho mpx v0.6.5\n")
	cp := &fakeControlPlane{
		current: "v0.6.4", latest: "v0.6.5",
		nodesFirst: "running", nodesThen: "failed",
		nodesEvents: []model.UpdateNodeEvent{
			{Seq: 1, Node: "la-1", Core: model.CoreXray, From: "26.3.27", To: "26.4.1", Status: "start"},
			{Seq: 2, Node: "la-1", Core: model.CoreXray, From: "26.3.27", To: "26.4.1", Status: "failed", Msg: "ssh 超时"},
		},
	}
	s, c := allRig(t, cp)
	restarted := stubRestart(t)

	if w := doJSON(s, http.MethodPost, "/local/update/all", c, "{}"); w.Code != http.StatusAccepted {
		t.Fatalf("启动应 202,得到 %d %s", w.Code, w.Body)
	}
	snap := waitAllState(t, s, c, updateAllFailed)
	if snap.Step != updateStepNodes {
		t.Fatalf("失败应停在节点这一步,得到 step=%s", snap.Step)
	}
	var sawControlOK, sawNodesFailed bool
	for _, ev := range snap.Events {
		if ev.Step == updateStepCLI {
			t.Fatalf("节点失败后不该有本机事件: %+v", snap.Events)
		}
		if ev.Step == updateStepControl && ev.Status == "ok" {
			sawControlOK = true
		}
		if ev.Step == updateStepNodes && ev.Status == "failed" {
			sawNodesFailed = true
		}
	}
	if !sawControlOK || !sawNodesFailed {
		t.Fatalf("应有控制面成功与节点失败的事件: %+v", snap.Events)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("节点失败后不该碰本机升级的发布服务器")
	}
	select {
	case p := <-restarted:
		t.Fatalf("不该重启,却拿到了交接文件 %s", p)
	default:
	}
}

func TestUpdateAllNodesUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假本机二进制是 sh 脚本,Windows 上无法执行")
	}
	withUpdateAllTiming(t)
	withLocalVersion(t, "v0.6.4")
	hits := withFakeRelease(t, "v0.6.5", "#!/bin/sh\necho mpx v0.6.5\n")
	cp := &fakeControlPlane{current: "v0.6.4", latest: "v0.6.5", nodesMissing: true}
	s, c := allRig(t, cp)

	if w := doJSON(s, http.MethodPost, "/local/update/all", c, "{}"); w.Code != http.StatusAccepted {
		t.Fatalf("启动应 202,得到 %d %s", w.Code, w.Body)
	}
	snap := waitAllState(t, s, c, updateAllFailed)
	if snap.Step != updateStepNodes {
		t.Fatalf("失败应停在节点这一步,得到 step=%s", snap.Step)
	}
	last := snap.Events[len(snap.Events)-1]
	if !strings.Contains(last.Msg, "控制面不支持节点升级") {
		t.Fatalf("应说明旧控制面不支持节点升级,得到 %+v", snap.Events)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("旧控制面不支持时不该碰本机升级的发布服务器")
	}
}

func TestUpdateAllConflictWhileRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假本机二进制是 sh 脚本,Windows 上无法执行")
	}
	withUpdateAllTiming(t)
	withLocalVersion(t, "v0.6.4")
	// 本机已是最新:这轮跑到 CLI 那步记 skip 就收尾,不用重启。
	hits := withFakeRelease(t, "v0.6.4", "#!/bin/sh\necho mpx v0.6.4\n")
	cp := &fakeControlPlane{current: "v0.6.4", latest: "v0.6.5", controlHang: make(chan struct{}),
		nodesFirst: "running", nodesThen: "done"}
	s, c := allRig(t, cp)
	restarted := stubRestart(t)

	if w := doJSON(s, http.MethodPost, "/local/update/all", c, "{}"); w.Code != http.StatusAccepted {
		t.Fatalf("第一次启动应 202,得到 %d %s", w.Code, w.Body)
	}
	// 任务挂在控制面升级那一拍上:此刻再点一次必须 409,而不是并起第二轮。
	w := doJSON(s, http.MethodPost, "/local/update/all", c, "{}")
	if w.Code != http.StatusConflict {
		t.Fatalf("跑着的时候再启动应 409,得到 %d %s", w.Code, w.Body)
	}
	if w := doJSON(s, http.MethodGet, "/local/update/all", c, ""); w.Code != http.StatusOK {
		t.Fatalf("进度接口应 200,得到 %d %s", w.Code, w.Body)
	}
	close(cp.controlHang)

	snap := waitAllState(t, s, c, updateAllDone)
	var cliSkip, restartEv bool
	for _, ev := range snap.Events {
		if ev.Step == updateStepCLI && ev.Status == "skip" {
			cliSkip = true
		}
		if ev.Step == updateStepRestart {
			restartEv = true
		}
	}
	if !cliSkip || restartEv {
		t.Fatalf("本机已是最新应记 skip 且不重启: %+v", snap.Events)
	}
	if ctl, nodes := cp.counts(); ctl != 1 || nodes != 1 {
		t.Fatalf("只有一轮任务:控制面 %d 次、节点 %d 次", ctl, nodes)
	}
	if atomic.LoadInt32(hits) == 0 {
		t.Fatal("本机升级结论没有去问发布服务器")
	}
	select {
	case p := <-restarted:
		t.Fatalf("二进制没换不该重启,却拿到了交接文件 %s", p)
	default:
	}
}

func TestUpdateAllProgressPaging(t *testing.T) {
	s, _, _, c := rig(t)
	// 不走任务,直接塞事件:这一层只测进度接口的增量与大小收口。
	for i := 0; i < 60; i++ {
		s.addAllEvent("nodes", "ok", fmt.Sprintf("n%d %s", i, strings.Repeat("好", 10)))
	}
	var got []int64
	for since := int64(0); len(got) < 60; {
		w := doJSON(s, http.MethodGet, "/local/update/all?since="+strconv.FormatInt(since, 10), c, "")
		if w.Code != http.StatusOK {
			t.Fatalf("since=%d 应 200,得到 %d %s", since, w.Code, w.Body)
		}
		if w.Body.Len() >= 4096 {
			t.Fatalf("since=%d 响应 %d 字节,超过 4KB", since, w.Body.Len())
		}
		var snap updateAllSnapshot
		if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
			t.Fatal(err)
		}
		if snap.Seq != 60 {
			t.Fatalf("seq 应是事件总数 60,得到 %d", snap.Seq)
		}
		if len(snap.Events) == 0 || len(snap.Events) > updateAllMaxEvents {
			t.Fatalf("since=%d 一次回 %d 条,应在 1..20 之间", since, len(snap.Events))
		}
		for i, ev := range snap.Events {
			if ev.Seq != since+int64(i)+1 {
				t.Fatalf("事件不连续: since=%d 第 %d 条 seq=%d", since, i, ev.Seq)
			}
			got = append(got, ev.Seq)
		}
		since = snap.Events[len(snap.Events)-1].Seq
	}

	// 追加一批超长消息:单条先被截到 200 字节,一页再被 4KB 收口提前截断。
	for i := 0; i < 30; i++ {
		s.addAllEvent("nodes", "ok", strings.Repeat("好", 110)+strconv.Itoa(i))
	}
	w := doJSON(s, http.MethodGet, "/local/update/all?since=60", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("应 200,得到 %d %s", w.Code, w.Body)
	}
	if w.Body.Len() >= 4096 {
		t.Fatalf("超长消息的一页 %d 字节,超过 4KB", w.Body.Len())
	}
	var snap updateAllSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Events) >= updateAllMaxEvents {
		t.Fatalf("按大小收口应少于 20 条,得到 %d", len(snap.Events))
	}
	if n := len(snap.Events[0].Msg); n > updateAllMsgLimit {
		t.Fatalf("消息应截到 %d 字节,得到 %d", updateAllMsgLimit, n)
	}
	// 截断不能切碎多字节字符:过了 JSON 编码就看不出来了,直接看存下来的那条。
	s.all.mu.Lock()
	lastMsg := s.all.events[len(s.all.events)-1].Msg
	s.all.mu.Unlock()
	if !utf8.ValidString(lastMsg) {
		t.Fatalf("截断切碎了多字节字符: %q", lastMsg)
	}

	for _, q := range []string{"-1", "abc"} {
		if w := doJSON(s, http.MethodGet, "/local/update/all?since="+q, c, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("since=%s 应 400,得到 %d %s", q, w.Code, w.Body)
		}
	}
}

func TestRestartStateFileRoundTrip(t *testing.T) {
	path, err := writeRestartState("127.0.0.1:7480", "会话值-abc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// 文件里是浏览器的登录凭据,只能本人可读。
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("交接文件权限应是 0600,得到 %v", fi.Mode().Perm())
	}
	st, err := ReadRestartState(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Addr != "127.0.0.1:7480" || st.Session != "会话值-abc" {
		t.Fatalf("读回的交接状态不对: %+v", st)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("交接文件读完应删除,stat 得到 %v", err)
	}
	if _, err := ReadRestartState(path); err == nil {
		t.Fatal("交接文件只有一份,第二次读应该报错")
	}

	// 环境拼装:残留的旧值要被替换掉,不能出现两个 MPX_UI_RESTART。
	t.Setenv(RestartEnv, "/tmp/旧文件.json")
	env := restartEnviron("/tmp/新文件.json")
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, RestartEnv+"=") {
			n++
			if e != RestartEnv+"=/tmp/新文件.json" {
				t.Fatalf("交接环境变量不对: %q", e)
			}
		}
	}
	if n != 1 {
		t.Fatalf("环境里应正好一个 %s,遇到 %d 个", RestartEnv, n)
	}
}

func TestUpdateViewCarriesNodes(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	cp := &fakeControlPlane{current: "v0.6.4", latest: "v0.6.5",
		rawNodes: `[{"id":1,"name":"la-1","core":"xray","current":"26.3.27","target":"26.4.1"}]`}
	s, c := allRig(t, cp)
	w := doJSON(s, http.MethodGet, "/local/update", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /local/update 应 200,得到 %d %s", w.Code, w.Body)
	}
	var view updateViewBody
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 1 {
		t.Fatalf("应原样带上控制面的节点版本行: %s", w.Body)
	}
	n := view.Nodes[0]
	if n.Name != "la-1" || n.Core != model.CoreXray || n.Current != "26.3.27" || n.Target != "26.4.1" {
		t.Fatalf("节点版本行不对: %+v", n)
	}
}
