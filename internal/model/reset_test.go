package model

import (
	"testing"
	"time"
)

// userAt 造一个 CreatedAt 固定的用户,便于对照周期边界。
func userAt(created time.Time, r ResetStrategy, resetAt *time.Time) *User {
	return &User{Username: "alice", CreatedAt: created, ResetStrategy: r, TrafficResetAt: resetAt}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("解析时间 %q: %v", s, err)
	}
	return parsed
}

func TestResetNeverDueWhenStrategyNone(t *testing.T) {
	created := mustTime(t, "2026-01-01T00:00:00Z")
	u := userAt(created, ResetNone, nil)

	if !u.NextResetAt().IsZero() {
		t.Fatalf("none 策略不该有下一个重置点: %v", u.NextResetAt())
	}
	if u.ResetDue(created.Add(100 * 24 * time.Hour)) {
		// AdvanceReset 对 none 返回零值,调用方据此清掉起算点。若它返回了时间,
		// ResetDueUsers 会把起算点写成一个毫无意义的时刻。
		t.Fatal("none 策略永远不该到期")
	}
	if !u.AdvanceReset(created.Add(100 * 24 * time.Hour)).IsZero() {
		t.Fatal("none 策略的下一个起算点应是零值")
	}
}

func TestResetDueAtBoundary(t *testing.T) {
	created := mustTime(t, "2026-01-01T00:00:00Z")
	u := userAt(created, ResetDaily, nil)

	if got := u.NextResetAt(); !got.Equal(mustTime(t, "2026-01-02T00:00:00Z")) {
		t.Fatalf("下一个重置点: %v", got)
	}
	if u.ResetDue(mustTime(t, "2026-01-01T23:59:59Z")) {
		t.Fatal("还没到点就不该重置")
	}
	// 边界上(now == 重置点)就算到期:用户等的就是这一刻。
	if !u.ResetDue(mustTime(t, "2026-01-02T00:00:00Z")) {
		t.Fatal("到点应当重置")
	}
	if !u.ResetDue(mustTime(t, "2026-01-05T00:00:00Z")) {
		t.Fatal("过点应当重置")
	}
}

func TestAdvanceResetKeepsPhase(t *testing.T) {
	created := mustTime(t, "2026-01-01T00:00:00Z")

	// 只晚了几小时:起算点推到下一个整点边界,相位不变。
	u := userAt(created, ResetDaily, nil)
	got := u.AdvanceReset(mustTime(t, "2026-01-02T03:00:00Z"))
	if want := mustTime(t, "2026-01-03T00:00:00Z"); !got.Equal(want) {
		t.Fatalf("起算点: got %v want %v", got, want)
	}

	// 停机三天再起来:一次跨过三个周期,而不是让用户连续被重置三次。
	got = u.AdvanceReset(mustTime(t, "2026-01-05T03:00:00Z"))
	if want := mustTime(t, "2026-01-06T00:00:00Z"); !got.Equal(want) {
		t.Fatalf("跨多个周期后的起算点: got %v want %v", got, want)
	}
	if !got.After(mustTime(t, "2026-01-05T03:00:00Z")) {
		t.Fatal("新的起算点必须严格晚于 now,否则下一轮会立刻再次重置")
	}
}

func TestResetAnchorPrefersTrafficResetAt(t *testing.T) {
	created := mustTime(t, "2026-01-01T00:00:00Z")
	resetAt := mustTime(t, "2026-02-10T00:00:00Z")
	u := userAt(created, ResetWeekly, &resetAt)

	if got := u.ResetAnchor(); !got.Equal(resetAt) {
		t.Fatalf("起算点应取上次重置时间: %v", got)
	}
	// 手动重置把周期整体挪到了 2 月 10 日那一周,而不是还按 1 月 1 日排。
	if got := u.NextResetAt(); !got.Equal(mustTime(t, "2026-02-17T00:00:00Z")) {
		t.Fatalf("下一个重置点: %v", got)
	}
}

func TestResetMonthlyUsesMonthStart(t *testing.T) {
	// 月中创建的用户,第一个重置点是下一个月初(首段不足一月,之后每段都是整月)。
	jan15 := mustTime(t, "2026-01-15T00:00:00Z")
	u := userAt(jan15, ResetMonthly, nil)
	if got := u.NextResetAt(); !got.Equal(mustTime(t, "2026-02-01T00:00:00Z")) {
		t.Fatalf("每月重置的下一个点: %v", got)
	}

	// 月末创建的用户同样落在下一个月初,不会像 AddDate(0,1,0) 那样跳到 3 月。
	jan31 := mustTime(t, "2026-01-31T00:00:00Z")
	u = userAt(jan31, ResetMonthly, nil)
	if got := u.NextResetAt(); !got.Equal(mustTime(t, "2026-02-01T00:00:00Z")) {
		t.Fatalf("月末创建的下一个点: %v", got)
	}

	// 重置过一次之后起算点就是月初,往后每段都是整月,永远不会漂。
	feb1 := mustTime(t, "2026-02-01T00:00:00Z")
	u = userAt(jan31, ResetMonthly, &feb1)
	if got := u.NextResetAt(); !got.Equal(mustTime(t, "2026-03-01T00:00:00Z")) {
		t.Fatalf("月初起算的下一个点: %v", got)
	}
	if got := u.AdvanceReset(mustTime(t, "2026-04-20T00:00:00Z")); !got.Equal(mustTime(t, "2026-05-01T00:00:00Z")) {
		t.Fatalf("停机两个月后应一次跨到 5 月 1 日: %v", got)
	}
}

// TestUnknownResetStrategyNeverHangs 钉住一个真的把控制面卡死过的取值。
//
// 策略为空串(或任何拼错的值)时,ResetPeriod 返回 0、addResetPeriod 原地不动,
// 于是 NextResetAt 算出来的正是起算点本身、ResetDue 当场为真,而 AdvanceReset
// 每轮"推进"完还是同一个时间 —— 后台任务会卡在这个用户上永远转下去,再不做任何
// 采样与收敛。所以不认得的策略必须在 NextResetAt 那一层就被挡掉。
func TestUnknownResetStrategyNeverHangs(t *testing.T) {
	created := mustTime(t, "2026-01-01T00:00:00Z")

	for _, bad := range []ResetStrategy{"", "None", "每天", "hourly"} {
		u := userAt(created, bad, nil)

		if !u.NextResetAt().IsZero() {
			t.Errorf("%q 不是认得的策略,不该有下一个重置点: %v", bad, u.NextResetAt())
		}
		if u.ResetDue(created.Add(100 * 24 * time.Hour)) {
			t.Errorf("%q 不该被判定为到期", bad)
		}
		// 这一行在修好之前不会返回。用 goroutine + 超时兜着,让它报成一条
		// 失败而不是把整个测试二进制挂住。
		done := make(chan time.Time, 1)
		go func() { done <- u.AdvanceReset(created.Add(100 * 24 * time.Hour)) }()
		select {
		case got := <-done:
			if !got.IsZero() {
				t.Errorf("%q 的起算点应是零值,实际 %v", bad, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%q 让 AdvanceReset 转不出来了", bad)
		}
	}
}

// TestAdvanceResetHandlesVeryOldAnchor 起算点若是个很早的时间,推进必须是常数级
// 的:一天一步地挪 20 年要转七千多次(月周期更少,但道理一样)。
func TestAdvanceResetHandlesVeryOldAnchor(t *testing.T) {
	u := userAt(mustTime(t, "1970-01-01T00:00:00Z"), ResetDaily, nil)
	now := mustTime(t, "2026-03-15T08:30:00Z")

	got := u.AdvanceReset(now)
	if !got.After(now) {
		t.Fatalf("新的起算点必须严格晚于 now: %v", got)
	}
	// 相位仍然保持在 00:00。
	if got.Hour() != 0 || got.Minute() != 0 {
		t.Fatalf("每天重置的相位应当是 00:00,实际 %v", got)
	}
}

func TestResetPeriod(t *testing.T) {
	if got := ResetPeriod(ResetDaily); got != 24*time.Hour {
		t.Fatalf("每天: %v", got)
	}
	if got := ResetPeriod(ResetWeekly); got != 7*24*time.Hour {
		t.Fatalf("每周: %v", got)
	}
	// 日历月没有固定长度,返回 0 由 addResetPeriod 特判。
	if got := ResetPeriod(ResetMonthly); got != 0 {
		t.Fatalf("每月应当返回 0: %v", got)
	}
	if got := ResetPeriod(ResetNone); got != 0 {
		t.Fatalf("不重置应当返回 0: %v", got)
	}
}

// TestStatusUsesPeriodTrafficNotLifetime 钉住一个容易搞错的地方:配额比的是本周
// 期用量。拿累计用量去比,用户用满一次之后就再也回不来了。
func TestStatusUsesPeriodTrafficNotLifetime(t *testing.T) {
	u := &User{
		Username:            "alice",
		Enabled:             true,
		DataLimit:           1000,
		UsedTraffic:         10,
		LifetimeUsedTraffic: 999999,
	}
	if got := u.Status(time.Now()); got != StatusActive {
		t.Fatalf("本周期只用了一点,应当是可用的: %v", got)
	}

	u.UsedTraffic = 1000
	if got := u.Status(time.Now()); got != StatusLimited {
		t.Fatalf("本周期用满,应当超限: %v", got)
	}
}
