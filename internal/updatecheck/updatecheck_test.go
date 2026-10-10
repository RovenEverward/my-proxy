package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"v0.2.0", [3]int{0, 2, 0}, true},
		{"0.2.0", [3]int{0, 2, 0}, true},
		{"v1.10.2", [3]int{1, 10, 2}, true},
		{" v0.2.0 ", [3]int{0, 2, 0}, true},
		{"v0.2", [3]int{0, 2, 0}, true},
		// 带后缀的不比大小:rc 与 dirty 都不是可以按数字排的东西。
		{"v0.2.0-rc1", [3]int{}, false},
		{"v0.0.0-abc123-dirty", [3]int{}, false},
		{"v0.2.0+meta", [3]int{}, false},
		{"", [3]int{}, false},
		{"latest", [3]int{}, false},
		{"v1.2.3.4", [3]int{}, false},
	}
	for _, c := range cases {
		got, ok := parseVersion(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseVersion(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestAhead(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.2.0", "v0.1.0", false},
		{"v0.1.0", "v0.10.0", true},
		{"v1.0.0", "v0.9.9", false},
		// 比不出来时一律不报"有新版本":宁可不说,也不要因为一个不认识的
		// 版本号天天提示。
		{"", "v0.2.0", false},
		{"v0.2.0", "", false},
		{"v0.0.0-abc-dirty", "v0.2.0", false},
		{"v0.1.0", "v0.2.0-rc1", false},
	}
	for _, c := range cases {
		if got := Ahead(c.current, c.latest); got != c.want {
			t.Errorf("Ahead(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestTruncateNotes(t *testing.T) {
	if got := truncateNotes("  短说明  "); got != "短说明" {
		t.Errorf("短正文应原样返回,得到 %q", got)
	}

	// 按 rune 截:中文三字节,按字节切会切出半个字符。
	long := ""
	for i := 0; i < 3000; i++ {
		long += "改"
	}
	got := truncateNotes(long)
	if len([]rune(got)) < maxNotes {
		t.Errorf("截断后应保留 %d 个 rune 以上,得到 %d", maxNotes, len([]rune(got)))
	}
	if !strings.Contains(got, "截断") {
		t.Errorf("截断后应提示去看发布页,得到 %q", got[len(got)-40:])
	}
}

// fakeGitHub 起一个假 GitHub:记录被请求了几次,并给出 latest 与列表。
func fakeGitHub(t *testing.T, latestTag string, tags []string, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if r.Header.Get("User-Agent") == "" {
			// 真接口不带 User-Agent 会 403,这里如实模拟。
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path2(r.URL.Path) == "latest":
			json.NewEncoder(w).Encode(map[string]any{
				"tag_name":     latestTag,
				"html_url":     "https://example.test/releases/" + latestTag,
				"body":         "修了几个东西",
				"published_at": "2026-01-02T03:04:05Z",
			})
		case path2(r.URL.Path) == "releases":
			out := make([]map[string]any, 0, len(tags))
			for _, tg := range tags {
				out = append(out, map[string]any{"tag_name": tg})
			}
			json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// path2 取路径的最后一段:/repos/o/n/releases/latest → latest,
// /repos/o/n/releases → releases。两个接口靠它区分。
func path2(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func TestCheckCachesWithinInterval(t *testing.T) {
	var hits int32
	srv := fakeGitHub(t, "v0.9.0", []string{"v0.9.0", "v0.2.0"}, &hits)

	c := New("owner/name", time.Hour)
	c.base = srv.URL
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	first := c.Check(context.Background(), false)
	if first.Latest != "v0.9.0" {
		t.Fatalf("第一次查询应拿到最新版,得到 %q (err=%q)", first.Latest, first.Error)
	}
	if first.Error != "" {
		t.Fatalf("第一次查询不该有错: %s", first.Error)
	}
	after := atomic.LoadInt32(&hits)

	// 静默期内的第二次不该发请求。
	now = now.Add(30 * time.Minute)
	c.Check(context.Background(), false)
	if got := atomic.LoadInt32(&hits); got != after {
		t.Errorf("静默期内不该再发请求: %d → %d", after, got)
	}

	// 过了静默期就该再问。
	now = now.Add(2 * time.Hour)
	c.Check(context.Background(), false)
	if got := atomic.LoadInt32(&hits); got <= after {
		t.Errorf("过了静默期应重新请求: %d → %d", after, got)
	}
}

func TestCheckForceBypassesInterval(t *testing.T) {
	var hits int32
	srv := fakeGitHub(t, "v0.9.0", []string{"v0.9.0"}, &hits)

	c := New("owner/name", time.Hour)
	c.base = srv.URL
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	c.Check(context.Background(), false)
	before := atomic.LoadInt32(&hits)
	c.Check(context.Background(), true)
	if atomic.LoadInt32(&hits) <= before {
		t.Errorf("force 应绕过静默期")
	}
}

func TestCheckKeepsLastResultOnFailure(t *testing.T) {
	var hits int32
	srv := fakeGitHub(t, "v0.9.0", []string{"v0.9.0"}, &hits)

	c := New("owner/name", 0) // 间隔为 0:每次 Check 都真的去问
	c.base = srv.URL
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	if r := c.Check(context.Background(), false); r.Latest != "v0.9.0" {
		t.Fatalf("第一次查询应成功,得到 %+v", r)
	}

	// 接口挂掉之后,上一次知道的版本不该被抹掉 —— 断网不等于"没有新版本"。
	srv.Close()
	got := c.Check(context.Background(), true)
	if got.Latest != "v0.9.0" {
		t.Errorf("失败后应保留上一次的结论,得到 Latest=%q", got.Latest)
	}
	if got.Error == "" {
		t.Errorf("失败后应记下错误")
	}
}

func TestSnapshotDoesNotFetch(t *testing.T) {
	var hits int32
	srv := fakeGitHub(t, "v0.9.0", []string{"v0.9.0"}, &hits)

	c := New("owner/name", time.Hour)
	c.base = srv.URL
	if r := c.Snapshot(); r.Latest != "" {
		t.Errorf("还没查过时快照应为空,得到 %q", r.Latest)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("Snapshot 不该发请求,却发了 %d 次", hits)
	}
}

// 版本比上游新(或者根本比不出来)时不能报"有更新"。这一条是实机踩出来的:
// 从源码编出来的版本是 v0.2.1-0.2026...+dirty 这种伪版本,按数字段比 v0.2.0 大,
// 但它出现在发布列表里时会被数成"落后 1 个发布" —— 比大小与数数用了两套判据。
func TestBehindIsZeroWhenNotActuallyOlder(t *testing.T) {
	srv := fakeGitHub(t, "v0.2.0", []string{"v0.2.0"}, new(int32))

	c := New("owner/name", 0)
	c.base = srv.URL
	c.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	// 比不出来或者不比上游旧的,一律 0。最后一个是实机踩出来的:从源码编出来
	// 的伪版本 v0.2.1-<日期>-<哈希>+dirty 按数字段比 v0.2.0 大,一度被数成
	// "落后 1 个发布"。
	for _, cur := range []string{"v0.2.0", "v0.3.0", "v0.2.1-0.20261010034134-92dab67fe92f+dirty", ""} {
		c.version = func() string { return cur }
		if got := c.countBehind(context.Background(), "v0.2.0"); got != 0 {
			t.Errorf("当前版本 %q 相对 v0.2.0 数出落后 %d,应当是 0", cur, got)
		}
	}
}

// 真落后时要数得出来,并且 Check 给出的 Behind 与 Ahead 一致。
func TestBehindCountsRealGap(t *testing.T) {
	var hits int32
	// 上游最新 v0.4.0,列表里依次是 v0.4.0 / v0.3.0 / v0.2.0 / v0.1.0
	srv := fakeGitHub(t, "v0.4.0", []string{"v0.4.0", "v0.3.0", "v0.2.0", "v0.1.0"}, &hits)

	c := New("owner/name", 0)
	c.base = srv.URL
	c.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	c.version = func() string { return "v0.2.0" }

	got := c.Check(context.Background(), true)
	if !got.Available() {
		t.Fatalf("v0.2.0 相对 v0.4.0 应当报有更新,得到 %+v", got)
	}
	if got.Behind != 2 {
		t.Errorf("v0.2.0 到 v0.4.0 之间隔 2 个发布,得到 %d", got.Behind)
	}
	if !Ahead("v0.2.0", got.Latest) {
		t.Errorf("Ahead 与 Behind 不一致:Behind=%d 但 Ahead 说不新", got.Behind)
	}
}

// 已是最新时不该再去数(少打一次接口),也不该报有更新。
func TestUpToDateSkipsListRequest(t *testing.T) {
	var hits int32
	srv := fakeGitHub(t, "v0.2.0", []string{"v0.2.0", "v0.1.0"}, &hits)

	c := New("owner/name", 0)
	c.base = srv.URL
	c.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	c.version = func() string { return "v0.2.0" }

	got := c.Check(context.Background(), true)
	if got.Behind != 0 || got.Available() {
		t.Errorf("已是最新却报 %+v", got)
	}
	// 只该打 latest 一次;列表那次要被 Ahead 挡掉。
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("已是最新时应只请求 latest 一次,实际 %d 次", n)
	}
}
