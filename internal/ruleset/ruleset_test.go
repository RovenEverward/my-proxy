package ruleset

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// fakeStore 是 Store 接口的内存替身,记下发给 CompleteRulesetRefresh 的参数。
type fakeStore struct {
	mu      sync.Mutex
	snaps   map[string]*store.RulesetSnapshot
	writes  int
	lastKey string
}

func newFakeStore() *fakeStore {
	return &fakeStore{snaps: map[string]*store.RulesetSnapshot{}}
}

func (f *fakeStore) RulesetSnapshotBody(key string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap := f.snaps[key]
	if snap == nil || len(snap.Body) == 0 {
		return nil, "", store.ErrNotFound
	}
	return snap.Body, snap.ContentType, nil
}

func (f *fakeStore) CompleteRulesetRefresh(key, url string, at time.Time,
	body []byte, contentType, failure string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	f.lastKey = key
	snap := f.snaps[key]
	if snap == nil {
		snap = &store.RulesetSnapshot{Key: key}
		f.snaps[key] = snap
	}
	hadBody := len(snap.Body) > 0
	snap.URL = url
	snap.LastAttemptAt = at.UnixMilli()
	snap.LastError = failure
	if failure == "" && len(body) > 0 {
		snap.Body = body
		snap.ContentType = contentType
		snap.FetchedAt = at.UnixMilli()
	}
	return len(body) > 0 && !hadBody && failure == "", nil
}

func (f *fakeStore) ListRulesetSnapshots() (map[string]*store.RulesetSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]*store.RulesetSnapshot{}
	for k, v := range f.snaps {
		c := *v
		out[k] = &c
	}
	return out, nil
}

// snapshot 返回副本而不是活指针:刷新 goroutine 持锁写字段,测试无锁读
// 活指针会被 -race 抓到。
func (f *fakeStore) snapshot(key string) *store.RulesetSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.snaps[key]
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// toServer 把请求的 host 改写到 httptest 上游:Manager 里的上游地址全是真实
// CDN,测试不能直接出网。
type rewriteTransport struct{ target string }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL.Scheme = "http"
	r2.URL.Host = t.target
	return http.DefaultTransport.RoundTrip(r2)
}

func upstreamClient(url string) *http.Client {
	return &http.Client{Transport: rewriteTransport{target: strings.TrimPrefix(url, "http://")}}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

// ---- 文件清单 ----

func TestFilesForRules(t *testing.T) {
	rules := []model.RouteRule{
		{Type: model.RuleSetRef, Value: "telegram", Enabled: true},
		{Type: model.RuleSetRef, Value: "telegram", Enabled: true}, // 重复引用,按 Key 去重
		{Type: model.RuleSetRef, Value: "ai", Enabled: false},      // 停用不算
		{Type: model.RuleGeoIP, Value: "CN", Enabled: true},
		{Type: model.RuleSuffix, Value: "x.com", Enabled: true}, // 不产生文件
	}
	files := FilesForRules(rules)
	keys := map[string]bool{}
	for _, f := range files {
		if keys[f.Key] {
			t.Fatalf("重复 key: %s", f.Key)
		}
		keys[f.Key] = true
	}
	// telegram: 1 个 clash 文件 + 1 个 singbox 文件;geoip-cn: 1 个 singbox。
	if len(files) != 3 {
		t.Fatalf("文件数应为 3: %v", files)
	}
	var geoip *File
	for i, f := range files {
		if f.Key == "singbox/geoip-cn" {
			geoip = &files[i]
		}
	}
	if geoip == nil {
		t.Fatal("geoip-cn 文件缺失")
	}
	if geoip.Fmt != "singbox" || geoip.Name != "geoip-cn" ||
		geoip.Upstream != "https://cdn.jsdelivr.net/gh/SagerNet/sing-geoip@rule-set/geoip-cn.srs" {
		t.Fatalf("geoip 文件不对: %+v", *geoip)
	}
}

func TestFilesByGroup(t *testing.T) {
	rules := []model.RouteRule{
		{Type: model.RuleSetRef, Value: "telegram", Enabled: true},
		{Type: model.RuleGeoIP, Value: "CN", Enabled: true},
	}
	groups := FilesByGroup(rules)
	if len(groups["telegram"]) != 2 {
		t.Fatalf("telegram 组应 2 个文件: %v", groups["telegram"])
	}
	if len(groups[GeoIPGroupKey]) != 1 || groups[GeoIPGroupKey][0].Key != "singbox/geoip-cn" {
		t.Fatalf("geoip 合成组不对: %v", groups[GeoIPGroupKey])
	}
}

func TestFileByKey(t *testing.T) {
	if f, ok := FileByKey("clash/openai"); !ok || f.Fmt != "clash" || f.Name != "openai" {
		t.Fatalf("目录文件解不出: %v %v", f, ok)
	}
	if f, ok := FileByKey("singbox/geoip-jp"); !ok || f.Upstream == "" {
		t.Fatalf("geoip 文件解不出: %v %v", f, ok)
	}
	if _, ok := FileByKey("singbox/geoip-cnx"); ok {
		t.Fatal("非法 geoip key 不该解出来")
	}
	if _, ok := FileByKey("clash/nope"); ok {
		t.Fatal("不存在的 key 不该解出来")
	}
}

// ---- Manager ----

func TestManagerRefreshSuccess(t *testing.T) {
	var hits int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if r.Header.Get("User-Agent") != "mihomo" {
			t.Errorf("UA 应为 mihomo,实际 %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte("payload: 1"))
	}))
	defer up.Close()

	fs := newFakeStore()
	var changed []bool
	var mu sync.Mutex
	m := New(fs, upstreamClient(up.URL), func(ready bool) {
		mu.Lock()
		changed = append(changed, ready)
		mu.Unlock()
	})
	defer m.Close()

	if err := m.StartRefresh("clash/openai"); err != nil {
		t.Fatalf("启动刷新: %v", err)
	}
	waitFor(t, "快照写入", func() bool {
		snap := fs.snapshot("clash/openai")
		return snap != nil && len(snap.Body) > 0
	})
	body, ct, err := fs.RulesetSnapshotBody("clash/openai")
	if err != nil || string(body) != "payload: 1" || ct != "text/yaml" {
		t.Fatalf("快照正文: %q %q %v", body, ct, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changed) != 1 || !changed[0] {
		t.Fatalf("首次就绪应回调 changed(true): %v", changed)
	}
}

func TestManagerRefreshFailureKeepsOldBody(t *testing.T) {
	var fail int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&fail, 1) == 1 {
			w.Header().Set("Content-Type", "text/yaml")
			_, _ = w.Write([]byte("payload: good"))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer up.Close()

	fs := newFakeStore()
	var changedCount int64
	m := New(fs, upstreamClient(up.URL), func(ready bool) {
		atomic.AddInt64(&changedCount, 1)
	})
	defer m.Close()

	if err := m.StartRefresh("clash/openai"); err != nil {
		t.Fatalf("首次刷新: %v", err)
	}
	waitFor(t, "首次就绪", func() bool {
		snap := fs.snapshot("clash/openai")
		return snap != nil && len(snap.Body) > 0
	})
	if err := m.StartRefresh("clash/openai"); err != nil {
		t.Fatalf("第二次刷新: %v", err)
	}
	waitFor(t, "失败落库", func() bool {
		snap := fs.snapshot("clash/openai")
		return snap != nil && snap.LastError != ""
	})
	body, _, err := fs.RulesetSnapshotBody("clash/openai")
	if err != nil || string(body) != "payload: good" {
		t.Fatalf("失败应保留旧正文: %q %v", body, err)
	}
	snap := fs.snapshot("clash/openai")
	if !strings.Contains(snap.LastError, "502") {
		t.Fatalf("last_error 应记 HTTP 502: %q", snap.LastError)
	}
	if atomic.LoadInt64(&changedCount) != 1 {
		t.Fatalf("失败不该再触发 changed: %d", changedCount)
	}
}

func TestManagerSingleFlight(t *testing.T) {
	var hits int64
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		<-release
		_, _ = w.Write([]byte("payload: 1"))
	}))
	defer up.Close()

	fs := newFakeStore()
	m := New(fs, upstreamClient(up.URL), nil)
	defer m.Close()

	if err := m.StartRefresh("clash/openai"); err != nil {
		t.Fatalf("第一次启动: %v", err)
	}
	if !m.Running("clash/openai") {
		t.Fatal("作业应在跑")
	}
	// 第二次调用不报错、不重复发请求。
	if err := m.StartRefresh("clash/openai"); err != nil {
		t.Fatalf("单飞合并应返回 nil: %v", err)
	}
	close(release)
	waitFor(t, "作业完成", func() bool { return !m.Running("clash/openai") })
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Fatalf("单飞应只发 1 个请求,实际 %d", n)
	}

	if err := m.StartRefresh("clash/不存在"); err == nil {
		t.Fatal("未知 key 应报错")
	}
}

func TestManagerRefreshDue(t *testing.T) {
	var hits int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte("payload: 1"))
	}))
	defer up.Close()

	fs := newFakeStore()
	m := New(fs, upstreamClient(up.URL), nil)
	defer m.Close()

	// 两条启用规则引用 telegram 组(2 个文件)+ geoip-jp;再加一条停用规则引 ai,
	// 停用的不该被拉。
	m.SetRefs(func() ([]model.RouteRule, error) {
		return []model.RouteRule{
			{Type: model.RuleSetRef, Value: "telegram", Enabled: true},
			{Type: model.RuleGeoIP, Value: "JP", Enabled: true},
			{Type: model.RuleSetRef, Value: "ai", Enabled: false},
		}, nil
	})

	m.RefreshDue(time.Now())
	waitFor(t, "3 个文件写入", func() bool {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		return fs.writes >= 3
	})
	if n := atomic.LoadInt64(&hits); n != 3 {
		t.Fatalf("应拉 3 个文件,实际 %d", n)
	}
	if fs.snapshot("clash/openai") != nil {
		t.Fatal("停用规则引用的文件不该被拉")
	}

	// 拉过一次的、attempt 在 24h 内的不再拉;把 attempt 改老一点才该重拉。
	atomic.StoreInt64(&hits, 0)
	m.RefreshDue(time.Now())
	time.Sleep(50 * time.Millisecond)
	if n := atomic.LoadInt64(&hits); n != 0 {
		t.Fatalf("24h 内不该重拉,实际 %d", n)
	}
	fs.mu.Lock()
	for _, snap := range fs.snaps {
		snap.LastAttemptAt = time.Now().Add(-25 * time.Hour).UnixMilli()
	}
	fs.mu.Unlock()
	m.RefreshDue(time.Now())
	waitFor(t, "到期重拉", func() bool { return atomic.LoadInt64(&hits) == 3 })
}
