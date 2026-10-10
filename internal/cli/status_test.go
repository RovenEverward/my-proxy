package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// captureStdout 抓住命令直接往 os.Stdout 打的表格 —— 那正是使用者看到的东西。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("建管道: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func TestStatusCommandIsRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"status"})
	if err != nil {
		t.Fatalf("mpx status 没有注册: %v", err)
	}
	if cmd != statusCmd {
		t.Fatalf("status 落到了别的命令上: %s", cmd.CommandPath())
	}
}

// 走完整命令:flag 接线、请求路径、鉴权头、表格渲染。
func TestStatusCommandPrintsTable(t *testing.T) {
	started := time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local)
	lastTick := started.Add(2 * time.Minute)
	payload, err := json.Marshal(model.ControlStatus{
		StartedAt:       started,
		UptimeSeconds:   3661,
		IntervalSeconds: 30,
		Ticks:           7,
		LastTickAt:      &lastTick,
		LastTickSeconds: 1.25,
		Counts:          model.Counts{Nodes: 2, Inbounds: 3, Hosts: 4, Users: 5},
		Backoffs: []model.NodeBackoff{{
			NodeID: 1, NodeName: "hk", Failures: 2, SkipUntil: time.Now().Add(3 * time.Minute),
		}},
	})
	if err != nil {
		t.Fatalf("造响应: %v", err)
	}

	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/status" {
			t.Errorf("意外的请求: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write(payload)
	})

	opts.output = outputTable
	out := captureStdout(t, func() { runRoot(t, "status") })

	for _, want := range []string{
		"字段", "启动时间", "已运行", "后台周期", "上次 Tick", "上一轮耗时",
		"Tick 次数", "节点", "入站", "对外入口", "用户", "退避中的节点",
		"1 台", "hk", "2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里缺少 %q:\n%s", want, out)
		}
	}
}

// 交接给下一层的入口:每一列的值都对得上,空状态说人话。
func TestStatusPairs(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	at := now.Add(-30 * time.Second)
	got := map[string]string{}
	for _, p := range statusPairs(model.ControlStatus{
		StartedAt:       now.Add(-3660 * time.Second),
		UptimeSeconds:   3660,
		IntervalSeconds: 30,
		Ticks:           7,
		LastTickAt:      &at,
		LastTickSeconds: 1.25,
		Counts:          model.Counts{Nodes: 2, Inbounds: 3, Hosts: 4, Users: 5},
	}, now) {
		got[p[0]] = p[1]
	}

	want := map[string]string{
		"启动时间":    now.Add(-3660 * time.Second).Format("2006-01-02 15:04:05"),
		"已运行":     "1 小时 1 分",
		"后台周期":    "30 秒",
		"上次 Tick": "30 秒前(" + at.Format("2006-01-02 15:04:05") + ")",
		"上一轮耗时":   "1.25 秒",
		"Tick 次数": "7",
		"节点":      "2",
		"入站":      "3",
		"对外入口":    "4",
		"用户":      "5",
		"退避中的节点":  "无",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, 想要 %q", k, got[k], v)
		}
	}

	// 一次都没跑过时要说出来,而不是显示成 0 秒。
	fresh := map[string]string{}
	for _, p := range statusPairs(model.ControlStatus{IntervalSeconds: 30}, now) {
		fresh[p[0]] = p[1]
	}
	if fresh["上次 Tick"] != "还没跑过" {
		t.Errorf("一次都没跑过时 = %q", fresh["上次 Tick"])
	}
}

func TestStatusBackoffRow(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	got := statusBackoffRow(model.NodeBackoff{
		NodeID: 3, NodeName: "hk", Failures: 2, SkipUntil: now.Add(90 * time.Second),
	}, now)
	want := []string{"3", "hk", "2", "12:01:30(还剩 1 分)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("退避行 = %q, 想要 %q", got, want)
	}
	if len(got) != len(statusBackoffHeaders) {
		t.Errorf("行有 %d 列,表头有 %d 列", len(got), len(statusBackoffHeaders))
	}
}

// 版本那一行要能把「不知道」和「知道」分开说 —— 从源码编出来的那份报"未知"时,
// 得让人看得出这是缺版本号,而不是命令坏了。
func TestStatusVersionText(t *testing.T) {
	if got := versionText(model.ControlStatus{Version: "v0.2.0"}); got != "v0.2.0" {
		t.Errorf("有版本时 = %q", got)
	}
	got := versionText(model.ControlStatus{})
	if !strings.Contains(got, "未知") || !strings.Contains(got, "没带版本号") {
		t.Errorf("没版本时要说明为什么,得到 %q", got)
	}
}

// 「更新」那一行的四种情形必须分得开:要人做的事完全不同。最容易出错的是把
// 「没问成」说成「已经最新」—— 那会让人放心,而真相只是不知道。
func TestStatusUpdateText(t *testing.T) {
	checked := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   model.UpdateInfo
		want string
		miss string // 不该出现的说法
	}{
		{
			name: "落后若干发布",
			in:   model.UpdateInfo{Current: "v0.1.0", Latest: "v0.2.0", Behind: 2, URL: "https://example.test/r", CheckedAt: &checked},
			want: "落后 2 个发布",
		},
		{
			name: "已是最新",
			in:   model.UpdateInfo{Current: "v0.2.0", Latest: "v0.2.0", CheckedAt: &checked},
			want: "已是最新",
			miss: "落后",
		},
		{
			name: "还没查过",
			in:   model.UpdateInfo{Current: "v0.2.0"},
			want: "还没查过",
			miss: "已是最新", // 没查过不等于最新
		},
		{
			name: "检查失败但上次查到有新版",
			in:   model.UpdateInfo{Current: "v0.1.0", Latest: "v0.2.0", Behind: 1, URL: "https://example.test/r", CheckedAt: &checked, Error: "请求 GitHub: HTTP 403"},
			want: "检查失败",
		},
		{
			name: "检查失败且没有旧结论",
			in:   model.UpdateInfo{Current: "v0.1.0", Error: "请求 GitHub: HTTP 403"},
			want: "检查失败",
			miss: "已是最新", // 问不成不是最新
		},
	}
	for _, c := range cases {
		got := updateText(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: = %q, 应含 %q", c.name, got, c.want)
		}
		if c.miss != "" && strings.Contains(got, c.miss) {
			t.Errorf("%s: = %q, 不该含 %q", c.name, got, c.miss)
		}
	}

	// 检查失败时上一次查到的版本与地址要一并留着,否则用户只看到"失败了",
	// 而其实他知道有新版这件事仍然成立。
	failed := updateText(model.UpdateInfo{
		Current: "v0.1.0", Latest: "v0.2.0", Behind: 1,
		URL: "https://example.test/r", CheckedAt: &checked, Error: "请求 GitHub: 拨号失败",
	})
	if !strings.Contains(failed, "v0.2.0") || !strings.Contains(failed, "https://example.test/r") {
		t.Errorf("检查失败时应保留上次查到的版本与地址,得到 %q", failed)
	}
}

// 状态表里必须有「版本」与「更新」两行,不然上面那些文字没人看得到。
func TestStatusPairsCarriesVersionAndUpdate(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	checked := now.Add(-time.Hour)
	got := map[string]string{}
	for _, p := range statusPairs(model.ControlStatus{
		Version: "v0.1.0",
		Update:  model.UpdateInfo{Current: "v0.1.0", Latest: "v0.2.0", Behind: 1, URL: "https://example.test/r", CheckedAt: &checked},
	}, now) {
		got[p[0]] = p[1]
	}
	if got["版本"] != "v0.1.0" {
		t.Errorf("版本 = %q", got["版本"])
	}
	if !strings.Contains(got["更新"], "v0.2.0") {
		t.Errorf("更新 = %q, 应指向新版本", got["更新"])
	}
}
