package model

import "testing"

// 内置规则每条都要有展示名,界面「恢复内置」才有东西显示。
func TestBuiltinRulesHaveLabels(t *testing.T) {
	rules := BuiltinRules()
	if len(rules) != 9 {
		t.Fatalf("内置规则应当有 9 条,实际 %d", len(rules))
	}
	for i, r := range rules {
		if r.Label == "" {
			t.Errorf("第 %d 条内置规则(%s → %s)没有 Label", i, r.Pattern, r.Result)
		}
	}
}

// WithBuiltinLabels 给内置表补齐展示名,但不认自定义规则:改了 result 或换了
// pattern 的都不算内置,不能硬套一个内置名。
func TestWithBuiltinLabels(t *testing.T) {
	builtin := BuiltinRules()

	// 把内置表的 Label 清掉,补回来应与原表逐字段相同。
	noLabel := make([]SubscriptionRule, len(builtin))
	for i, r := range builtin {
		r.Label = ""
		noLabel[i] = r
	}
	filled := WithBuiltinLabels(noLabel)
	for i := range filled {
		if filled[i] != builtin[i] {
			t.Errorf("第 %d 条没补对:\n got %+v\nwant %+v", i, filled[i], builtin[i])
		}
	}
	// 不该改到传入的切片。
	if noLabel[0].Label != "" {
		t.Errorf("WithBuiltinLabels 改到了传入的切片: %+v", noLabel[0])
	}

	custom := []SubscriptionRule{
		// 内置 pattern 但改了 result:不算内置。
		{Pattern: `(?i)clash`, Result: FormatClashMeta},
		// 全新 pattern:不算内置。
		{Pattern: `(?i)my-client`, Result: FormatLinks},
	}
	got := WithBuiltinLabels(custom)
	for i, r := range got {
		if r.Label != "" {
			t.Errorf("自定义规则第 %d 条不该补 Label,实际 %q", i, r.Label)
		}
	}

	// 已经带 Label 的规则原样保留。
	withExisting := []SubscriptionRule{{Pattern: `(?i)clash`, Result: FormatClash, Label: "我自己的名字"}}
	if got := WithBuiltinLabels(withExisting); got[0].Label != "我自己的名字" {
		t.Errorf("已有 Label 被覆盖: %q", got[0].Label)
	}
}
