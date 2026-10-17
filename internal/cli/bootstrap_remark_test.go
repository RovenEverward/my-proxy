package cli

import "testing"

func TestBootstrapRemark(t *testing.T) {
	defer func(set bool, r string) { bootstrapRemarkSet, bootstrapFlags.remark = set, r }(bootstrapRemarkSet, bootstrapFlags.remark)
	cases := []struct {
		set    bool
		remark string
		want   string
	}{
		{false, "", "node-1"}, // 没写 --remark:沿用节点名(老行为)
		{false, "东京", "东京"},   // 写了名字
		{true, "", ""},        // --remark= :留空,订阅跟随节点显示名
		{true, "  东京 ", "东京"},
	}
	for _, c := range cases {
		bootstrapRemarkSet, bootstrapFlags.remark = c.set, c.remark
		if got := bootstrapRemark("node-1"); got != c.want {
			t.Errorf("set=%v remark=%q: got %q want %q", c.set, c.remark, got, c.want)
		}
	}
}
