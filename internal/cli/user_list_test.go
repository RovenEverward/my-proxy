package cli

import (
	"strings"
	"testing"
	"time"
)

// setListFlags 把 user list 的筛选条件摆成某一组值,并在测试结束时还原。
//
// userListNextCmd 读的是包级 userListFlags(它没有入参),所以只能这样摆。
func setListFlags(t *testing.T, pageSize int, status, search string, inbound int64) {
	t.Helper()
	old := userListFlags
	t.Cleanup(func() { userListFlags = old })
	userListFlags.pageSize = pageSize
	userListFlags.status = status
	userListFlags.search = search
	userListFlags.inbound = inbound
}

// TestUserListNextCmdCarriesFilters 断言下一页命令把这次的筛选条件重复了一遍。
//
// 不重复筛选,用户照着敲出来的"下一页"就是另一份列表 —— 分页最容易骗人的地方。
func TestUserListNextCmdCarriesFilters(t *testing.T) {
	cases := []struct {
		name    string
		page    int
		size    int
		status  string
		search  string
		inbound int64
		want    string
	}{
		{
			name: "没有筛选时只有分页参数",
			page: 2, size: 50,
			want: "mpx user list --page 2 --page-size 50",
		},
		{
			name: "状态筛选要带上",
			page: 3, size: 20, status: "expired",
			want: "mpx user list --page 3 --page-size 20 --status expired",
		},
		{
			name: "搜索词要带上",
			page: 2, size: 10, search: "li",
			want: "mpx user list --page 2 --page-size 10 --search li",
		},
		{
			name: "入站筛选要带上",
			page: 4, size: 100, inbound: 7,
			want: "mpx user list --page 4 --page-size 100 --inbound 7",
		},
		{
			name: "三个筛选一起带上",
			page: 5, size: 25, status: "active", search: "zhang", inbound: 12,
			want: "mpx user list --page 5 --page-size 25 --status active --search zhang --inbound 12",
		},
		{
			name: "入站为 0 表示不筛,不出现在命令里",
			page: 2, size: 50, inbound: 0,
			want: "mpx user list --page 2 --page-size 50",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setListFlags(t, tc.size, tc.status, tc.search, tc.inbound)
			got := userListNextCmd(tc.page)
			if got != tc.want {
				t.Fatalf("userListNextCmd(%d) =\n  %q\nwant\n  %q", tc.page, got, tc.want)
			}
		})
	}
}

// TestUserListHintLastPage 断言到底时只报总数,不再给"下一页"。
//
// 已经没有下一页却还提示一条命令,用户会照着敲然后拿到空列表。
func TestUserListHintLastPage(t *testing.T) {
	setListFlags(t, 50, "", "", 0)
	out := captureStdout(t, func() {
		users := make([]userRow, 3)
		if err := userListHint(userPage{Users: users, Total: 3}); err != nil {
			t.Errorf("userListHint: %v", err)
		}
	})
	if !strings.Contains(out, "共 3 个用户") {
		t.Fatalf("到底时的输出 = %q, want 含「共 3 个用户」", out)
	}
	if strings.Contains(out, "下一页") {
		t.Fatalf("到底了还在提示下一页: %q", out)
	}
}

// TestUserListHintHasNextPage 断言还有下一页时给出剩余数量与下一页命令,
// 并且那条命令里重复了本次的筛选条件。
func TestUserListHintHasNextPage(t *testing.T) {
	setListFlags(t, 20, "expired", "li", 0)
	out := captureStdout(t, func() {
		users := make([]userRow, 20)
		// offset 20 + 本页 20 = 看到 40,total 55 -> 还剩 15。
		if err := userListHint(userPage{Users: users, Total: 55, Limit: 20, Offset: 20}); err != nil {
			t.Errorf("userListHint: %v", err)
		}
	})
	if !strings.Contains(out, "共 55 个用户") {
		t.Fatalf("输出 = %q, want 含「共 55 个用户」", out)
	}
	if !strings.Contains(out, "还有 15 个") {
		t.Fatalf("输出 = %q, want 含「还有 15 个」", out)
	}
	next := userListNextCmd(2)
	if !strings.Contains(out, next) {
		t.Fatalf("输出里没有下一页命令 %q: %q", next, out)
	}
	// 关键:下一页命令必须重复本次筛选,否则跳到另一份列表上。
	for _, want := range []string{"--status expired", "--search li", "--page 2", "--page-size 20"} {
		if !strings.Contains(out, want) {
			t.Fatalf("下一页命令里少了 %q: %q", want, out)
		}
	}
}

// TestUserListHintCountsShownWithOffset 断言"还剩多少"是按 offset+本页条数算的,
// 不是按本页满没满猜的。
func TestUserListHintCountsShownWithOffset(t *testing.T) {
	setListFlags(t, 10, "", "", 0)
	// 第二页只回 1 条(不满),但 offset 10 + 1 = 11,, total 12 -> 还剩 1。
	out := captureStdout(t, func() {
		if err := userListHint(userPage{Users: make([]userRow, 1), Total: 12, Limit: 10, Offset: 10}); err != nil {
			t.Errorf("userListHint: %v", err)
		}
	})
	if !strings.Contains(out, "还有 1 个") {
		t.Fatalf("输出 = %q, want 含「还有 1 个」(按 offset+本页条数算)", out)
	}
}

// TestRenewTargetExtendsFromLaterBase 钉住顺延的基准:取"现在"与"当前到期日"
// 里较晚的那个。
//
// 三种情形各有各的道理:没到期时顺延要在原到期日上加(不然"今天续的"会把剩余
// 时间抹掉);已过期或本来不过期时从今天起算(不然会算出过去的日期)。
func TestRenewTargetExtendsFromLaterBase(t *testing.T) {
	now := time.Date(2026, 5, 10, 14, 30, 0, 0, time.Local)
	at := func(y int, m time.Month, d int) *time.Time {
		v := time.Date(y, m, d, 12, 0, 0, 0, time.Local)
		return &v
	}
	cases := []struct {
		name    string
		current *time.Time
		days    int
		want    string // YYYY-MM-DD
	}{
		{
			name:    "未到期:从原到期日往后数",
			current: at(2026, time.June, 1),
			days:    30,
			want:    "2026-07-01",
		},
		{
			name:    "已过期:从今天起算",
			current: at(2026, time.April, 1),
			days:    10,
			want:    "2026-05-20",
		},
		{
			name:    "不过期:从今天起算",
			current: nil,
			days:    7,
			want:    "2026-05-17",
		},
		{
			name:    "到期日就是今天:算作不晚于现在,从今天起算",
			current: at(2026, time.May, 10),
			days:    1,
			want:    "2026-05-11",
		},
		{
			name:    "顺延 0 天:结果是基准当天的 23:59:59",
			current: nil,
			days:    0,
			want:    "2026-05-10",
		},
		{
			// 1 月 31 日 12:00 早于 now(5 月 10 日),所以基准是 now:
			// 5 月 10 日 + 30 天 = 6 月 9 日。过期日不该被当成基准。
			name:    "已过期的到期日不作基准:从今天起算",
			current: at(2026, time.January, 31),
			days:    30,
			want:    "2026-06-09",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renewTarget(tc.current, tc.days, now)
			if got.Format("2006-01-02") != tc.want {
				t.Fatalf("renewTarget(%v, %d) = %s, want %s",
					tc.current, tc.days, got.Format("2006-01-02"), tc.want)
			}
		})
	}
}

// TestRenewTargetTimeOfDay 断言结果的时分秒固定是当天 23:59:59、当地时区。
//
// 零点会让用户在到期日当天就被断掉;非本地时区又会让"到期时间"这一列显示成
// 前一天。
func TestRenewTargetTimeOfDay(t *testing.T) {
	now := time.Date(2026, 5, 10, 8, 15, 30, 0, time.Local)
	current := time.Date(2026, 6, 1, 3, 4, 5, 0, time.Local)
	got := renewTarget(&current, 5, now)

	h, m, s := got.Clock()
	if h != 23 || m != 59 || s != 59 {
		t.Fatalf("时分秒 = %02d:%02d:%02d, want 23:59:59", h, m, s)
	}
	if got.Nanosecond() != 0 {
		t.Fatalf("纳秒 = %d, want 0", got.Nanosecond())
	}
	if got.Location() != time.Local {
		t.Fatalf("时区 = %v, want 当地时区", got.Location())
	}
	// 没有 current 时同样。
	got = renewTarget(nil, 0, now)
	if h, m, s := got.Clock(); h != 23 || m != 59 || s != 59 {
		t.Fatalf("不过期时的时分秒 = %02d:%02d:%02d, want 23:59:59", h, m, s)
	}
}

// TestRenewTargetKeepsRemainingTime 断言未到期时顺延不会抹掉剩余时间。
//
// 这是 renewTarget 存在的理由:基准若是"今天",一个还剩半年到期、用户只想续
// 30 天的账号会被砍到 30 天。
func TestRenewTargetKeepsRemainingTime(t *testing.T) {
	now := time.Date(2026, 5, 10, 0, 0, 0, 0, time.Local)
	expire := time.Date(2026, 11, 10, 23, 59, 59, 0, time.Local)
	got := renewTarget(&expire, 30, now)
	if !got.After(expire) {
		t.Fatalf("顺延后 %s 没有晚于原到期日 %s", got, expire)
	}
	if want := time.Date(2026, 12, 10, 23, 59, 59, 0, time.Local); !got.Equal(want) {
		t.Fatalf("顺延 30 天得到 %s, want %s", got, want)
	}
}

// TestParseEnabledAcceptsCommonSpellings 断言各写法都认。
func TestParseEnabledAcceptsCommonSpellings(t *testing.T) {
	yes := []string{"启用", "是", "true", "TRUE", "True", "enabled", "yes", "Y", "1", " 启用 "}
	no := []string{"停用", "否", "false", "FALSE", "disabled", "no", "N", "0", " 停用 "}
	for _, s := range yes {
		t.Run("启用/"+s, func(t *testing.T) {
			got, err := parseEnabled(s)
			if err != nil {
				t.Fatalf("parseEnabled(%q) 报错: %v", s, err)
			}
			if !got {
				t.Fatalf("parseEnabled(%q) = false, want true", s)
			}
		})
	}
	for _, s := range no {
		t.Run("停用/"+s, func(t *testing.T) {
			got, err := parseEnabled(s)
			if err != nil {
				t.Fatalf("parseEnabled(%q) 报错: %v", s, err)
			}
			if got {
				t.Fatalf("parseEnabled(%q) = true, want false", s)
			}
		})
	}
}

// TestParseEnabledRejectsUnknown 是关键的一条:认不出的写法必须报错,
// 绝不能默认成启用 —— 一个拼错的"停用"被当成启用,账号就悄悄开了。
func TestParseEnabledRejectsUnknown(t *testing.T) {
	// "停用"的常见错拼、以及别的语言/写法。
	bad := []string{"停 用", "停用用", "禁用", "enable", "disable", "ture", "flase",
		"ok", "-", "2", "启用中", "nope", "是的"}
	for _, s := range bad {
		t.Run(s, func(t *testing.T) {
			got, err := parseEnabled(s)
			if err == nil {
				t.Fatalf("parseEnabled(%q) 没有报错(got %v);认不出的写法不能默认成启用", s, got)
			}
			if got {
				t.Fatalf("parseEnabled(%q) 在报错的同时回了 true", s)
			}
			// 报错要说清可选值,不然用户不知道写成什么才对。
			if !strings.Contains(err.Error(), "启用") || !strings.Contains(err.Error(), "停用") {
				t.Fatalf("parseEnabled(%q) 的错误信息 %q 没有列出可选值", s, err)
			}
		})
	}
}

// TestParseEnabledEmptyIsRejected 断言空串也走报错这条路。
//
// 空单元格在 csvRowToUser 里已经被挡在外面(表示"不改"),直接调到这里说明
// 调用方漏了那一步,报错比默认启用安全。
func TestParseEnabledEmptyIsRejected(t *testing.T) {
	if got, err := parseEnabled(""); err == nil {
		t.Fatalf("parseEnabled(%q) 没有报错(got %v)", "", got)
	}
	if got, err := parseEnabled("   "); err == nil {
		t.Fatalf("parseEnabled(空白) 没有报错(got %v)", got)
	}
}
