package model

import (
	"testing"
	"time"
)

// TestStatusValid 是 Status.Valid 的表驱动测试。
//
// 关键在于空串:它在查询参数和筛选条件里表示"不限",不能被当成一个合法状态,
// 否则 API 会把 status= 当成"只查某个叫空字符串的状态"从而永远筛出空结果。
func TestStatusValid(t *testing.T) {
	cases := []struct {
		name string
		s    Status
		want bool
	}{
		{"active", StatusActive, true},
		{"disabled", StatusDisabled, true},
		{"expired", StatusExpired, true},
		{"limited", StatusLimited, true},
		{"空串表示不限,不算合法", Status(""), false},
		{"拼错的 active", Status("Active"), false},
		{"拼错的 enabled", Status("enabled"), false},
		{"多余空白", Status(" active"), false},
		{"完全无关的串", Status("whatever"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Valid(); got != tc.want {
				t.Fatalf("Status(%q).Valid() = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestUserStatusesMatchesValid 守住"取值列表"与"校验"是同一份事实。
//
// statusChoices() 与 --status 的提示都从 UserStatuses 取;如果谁新加了一个状态
// 常量却忘了加进列表,Valid 会把它判成非法,而提示里也看不到它 —— 这条测试让
// 两者必须同时被想到。
func TestUserStatusesMatchesValid(t *testing.T) {
	if len(UserStatuses) == 0 {
		t.Fatal("UserStatuses 是空的")
	}
	seen := make(map[Status]bool, len(UserStatuses))
	for _, s := range UserStatuses {
		if !s.Valid() {
			t.Fatalf("UserStatuses 里的 %q 被判成非法", s)
		}
		if s == "" {
			t.Fatal("UserStatuses 不该包含空串:它表示不限")
		}
		if seen[s] {
			t.Fatalf("UserStatuses 里 %q 出现了两次", s)
		}
		seen[s] = true
	}
}

// TestUserStatusBoundaries 钉住判定优先级与两个边界。
//
// 优先级(停用 > 过期 > 超限 > 可用)是用户看得见的行为:一个既停用又过期的账号
// 该显示 disabled 而不是 expired。到期时刻**正好等于** now 时不算过期 —— 判定用的是
// now.After(expire),到期的"最后一秒"仍然可用,与 parseExpire 把到期日定在
// 当天 23:59:59 的意图一致。
func TestUserStatusBoundaries(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time {
		v := now.Add(d)
		return &v
	}
	cases := []struct {
		name string
		u    User
		want Status
	}{
		{
			name: "一切正常",
			u:    User{Enabled: true, DataLimit: 100, UsedTraffic: 99},
			want: StatusActive,
		},
		{
			name: "未配置上限,用量再多也不超限",
			u:    User{Enabled: true, DataLimit: 0, UsedTraffic: 1 << 40},
			want: StatusActive,
		},
		{
			name: "刚好用满上限算超限",
			u:    User{Enabled: true, DataLimit: 100, UsedTraffic: 100},
			want: StatusLimited,
		},
		{
			name: "到期时刻正好等于 now 还不算过期",
			u:    User{Enabled: true, ExpireAt: at(0)},
			want: StatusActive,
		},
		{
			name: "过了到期时刻才算过期",
			u:    User{Enabled: true, ExpireAt: at(-time.Second)},
			want: StatusExpired,
		},
		{
			name: "停用优先于过期与超限",
			u:    User{Enabled: false, ExpireAt: at(-24 * time.Hour), DataLimit: 100, UsedTraffic: 200},
			want: StatusDisabled,
		},
		{
			name: "过期优先于超限",
			u:    User{Enabled: true, ExpireAt: at(-time.Hour), DataLimit: 100, UsedTraffic: 200},
			want: StatusExpired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.Status(now); got != tc.want {
				t.Fatalf("Status = %q, want %q", got, tc.want)
			}
		})
	}
}
