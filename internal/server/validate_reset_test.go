package server

import (
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 不再允许把认不出的重置策略写进库:模型层已经不会被它卡死,但存一个语义不明的
// 值会让「看到什么」和「实际发生什么」对不上。留空则按 none 收下。
func TestValidateUserResetStrategy(t *testing.T) {
	cases := []struct {
		in      model.ResetStrategy
		want    model.ResetStrategy
		wantMsg bool
	}{
		{in: "", want: model.ResetNone},
		{in: model.ResetNone, want: model.ResetNone},
		{in: model.ResetDaily, want: model.ResetDaily},
		{in: model.ResetWeekly, want: model.ResetWeekly},
		{in: model.ResetMonthly, want: model.ResetMonthly},
		{in: "None", wantMsg: true},
		{in: "每天", wantMsg: true},
		{in: "hourly", wantMsg: true},
	}
	for _, c := range cases {
		u := &model.User{Username: "alice", ResetStrategy: c.in}
		msg := validateUser(u)
		if c.wantMsg {
			if msg == "" {
				t.Errorf("策略 %q 应当被拒绝", c.in)
			}
			continue
		}
		if msg != "" {
			t.Errorf("策略 %q 不该被拒绝: %s", c.in, msg)
		}
		if u.ResetStrategy != c.want {
			t.Errorf("策略 %q 应归一化成 %q,实际 %q", c.in, c.want, u.ResetStrategy)
		}
	}
}
