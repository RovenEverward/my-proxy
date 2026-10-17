package ui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/selfupdate"
)

// 退避序列:30s → 1m → 2m → 4m → 8m → 16m → 30m,之后一直是 30m。
func TestPrefetchBackoffSequence(t *testing.T) {
	want := []time.Duration{
		30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute,
		8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute, 30 * time.Minute,
	}
	for i, w := range want {
		if got := prefetchBackoff(i); got != w {
			t.Errorf("prefetchBackoff(%d) = %s,想要 %s", i, got, w)
		}
	}
}

// 开发版/版本未知/非桌面构建都不预下载。
func TestPrefetchDisabledStates(t *testing.T) {
	for _, v := range []string{"", "v0.0.0-abc123", "v0.9.6-dirty", "0.0.0-20260101"} {
		if !prefetchDevVersion(v) {
			t.Errorf("%q 应算开发版", v)
		}
	}
	if prefetchDevVersion("v0.9.6") {
		t.Error("正式版本不该算开发版")
	}

	withLocalVersion(t, "v0.6.4")
	cp := &fakeControlPlane{current: "v0.6.4", latest: "v0.6.5"}
	s, c := allRig(t, cp)
	if v := doJSON(s, http.MethodGet, "/local/update", c, ""); v.Code != http.StatusOK {
		t.Fatalf("GET /local/update 应 200: %d %s", v.Code, v.Body)
	} else if strings.Contains(v.Body.String(), `"prefetch"`) {
		t.Fatalf("非桌面构建不该有 prefetch 字段: %s", v.Body)
	}

	withDesktopBuild(t)
	t.Setenv("MPX_NO_PREFETCH", "1")
	w := doJSON(s, http.MethodGet, "/local/update", c, "")
	if !strings.Contains(w.Body.String(), `"state":"disabled"`) {
		t.Fatalf("MPX_NO_PREFETCH 时 prefetch 应为 disabled: %s", w.Body)
	}
	if w := doJSON(s, http.MethodPost, "/local/update/prefetch", c, "{}"); w.Code != http.StatusConflict {
		t.Fatalf("关闭预下载时立即重试应 409,得到 %d %s", w.Code, w.Body)
	}
}

// prefetchRig 起一个假发布站(桌面归档 + 配对 SHA256SUMS),把预下载暂存目录
// 指到临时目录,并切到桌面构建。archiveHits 记录归档被下载了几次。
func prefetchRig(t *testing.T, latest, newBin string) (*Server, *http.Cookie, *int32, string) {
	t.Helper()
	archive := tarGzWithMPX(t, newBin)
	name := selfupdate.DesktopArchiveName(runtime.GOOS, runtime.GOARCH)
	sums := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), name)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":` + strconv.Quote(latest) + `}`))
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			_, _ = w.Write([]byte(sums))
		case strings.HasSuffix(r.URL.Path, "/"+name):
			atomic.AddInt32(&hits, 1)
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "mpx")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho mpx v0.6.4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldSrc, oldExe, oldDesk, oldStager := updateSource, selfExecutable, updateDesktop, newPrefetchStager
	updateSource = func() selfupdate.Source {
		return selfupdate.Source{Repo: "test/mpx", APIBase: srv.URL, DownloadBase: srv.URL, Desktop: true}
	}
	selfExecutable = func() (string, error) { return exe, nil }
	updateDesktop = func() bool { return true }
	dir := t.TempDir()
	newPrefetchStager = func(src selfupdate.Source) *selfupdate.Stager {
		return &selfupdate.Stager{Dir: dir, Source: src}
	}
	t.Cleanup(func() {
		updateSource, selfExecutable, updateDesktop, newPrefetchStager = oldSrc, oldExe, oldDesk, oldStager
	})

	cp := &fakeControlPlane{current: "v0.6.4", latest: latest, nodesFirst: "running", nodesThen: "done"}
	s, c := allRig(t, cp)
	return s, c, &hits, exe
}

// waitPrefetch 轮询 GET /local/update 直到 prefetch.state 是 want。
func waitPrefetch(t *testing.T, s *Server, c *http.Cookie, want string) prefetchStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		w := doJSON(s, http.MethodGet, "/local/update", c, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /local/update 应 200: %d %s", w.Code, w.Body)
		}
		var view updateViewBody
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatalf("解析响应失败: %v (%s)", err, w.Body)
		}
		if view.Prefetch != nil && view.Prefetch.State == want {
			return *view.Prefetch
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 prefetch=%s 超时,最后看到 %+v", want, view.Prefetch)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 发现新版本后后台下载就绪;点一键更新时本机一步直接用暂存件,不再发下载请求。
func TestPrefetchReadyThenUpdateAllUsesStaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假本机二进制是 sh 脚本,Windows 上无法执行")
	}
	withUpdateAllTiming(t)
	withLocalVersion(t, "v0.6.4")
	s, c, hits, exe := prefetchRig(t, "v0.6.5", "#!/bin/sh\necho mpx v0.6.5\n")
	restarted := stubRestart(t)

	st := waitPrefetch(t, s, c, prefetchReady)
	if st.Version != "v0.6.5" || st.Total == 0 || st.Bytes != st.Total {
		t.Fatalf("就绪状态不对: %+v", st)
	}
	afterPrefetch := atomic.LoadInt32(hits)
	if afterPrefetch != 1 {
		t.Fatalf("预下载应只下一次归档,实际 %d 次", afterPrefetch)
	}

	if w := doJSON(s, http.MethodPost, "/local/update/all", c, "{}"); w.Code != http.StatusAccepted {
		t.Fatalf("启动一键更新应 202: %d %s", w.Code, w.Body)
	}
	select {
	case call := <-restarted:
		if call.exe != exe {
			t.Fatalf("重启应 exec %s,得到 %s", exe, call.exe)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("没有走到重启")
	}
	if got := atomic.LoadInt32(hits); got != afterPrefetch {
		t.Fatalf("已就绪时 update/all 不该再下载归档: %d → %d", afterPrefetch, got)
	}
}

// 失败按退避重试:注入的 sleep 记录间隔,到第 4 次时换掉目标把这一轮停掉。
func TestPrefetchRetriesWithBackoff(t *testing.T) {
	withLocalVersion(t, "v0.6.4")
	oldStager := newPrefetchStager
	newPrefetchStager = func(src selfupdate.Source) *selfupdate.Stager {
		// 地址不可用:Ensure 每次都在找 SHA256SUMS 时失败。
		return &selfupdate.Stager{Dir: t.TempDir(),
			Source: selfupdate.Source{Repo: "test/mpx", DownloadBase: "http://127.0.0.1:1"}}
	}
	t.Cleanup(func() { newPrefetchStager = oldStager })

	pf := newPrefetcher(selfupdate.Source{})
	var mu sync.Mutex
	var waits []time.Duration
	stop := make(chan struct{})
	pf.sleep = func(ctx context.Context, d time.Duration) bool {
		mu.Lock()
		waits = append(waits, d)
		n := len(waits)
		mu.Unlock()
		if n >= 4 {
			close(stop)
			return false
		}
		return true
	}
	pf.start("v0.6.5")
	select {
	case <-stop:
	case <-time.After(10 * time.Second):
		t.Fatal("等待退避序列超时")
	}
	mu.Lock()
	got := append([]time.Duration(nil), waits...)
	mu.Unlock()
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute}
	if len(got) != len(want) {
		t.Fatalf("退避序列 = %v,想要 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 次退避 = %s,想要 %s(全序列 %v)", i, got[i], want[i], got)
		}
	}
	if st := pf.snapshot(); st.State != prefetchFailed || st.Error == "" || st.Version != "v0.6.5" {
		t.Fatalf("失败状态应带上目标与原因: %+v", st)
	}
}
