package model

import "testing"

// TestAllowsInboundWithoutPlan 覆盖没挂套餐时的旧语义:InboundIDs 为空表示全部,
// 非空则只放行列出的入站。
func TestAllowsInboundWithoutPlan(t *testing.T) {
	all := &User{}
	for _, id := range []int64{1, 2, 999} {
		if !all.AllowsInbound(id) {
			t.Fatalf("InboundIDs 为空时入站 %d 应当可用", id)
		}
	}

	limited := &User{InboundIDs: []int64{1, 3}}
	for _, tc := range []struct {
		id   int64
		want bool
	}{{1, true}, {2, false}, {3, true}, {999, false}} {
		if got := limited.AllowsInbound(tc.id); got != tc.want {
			t.Fatalf("旧语义下入站 %d: got %v want %v", tc.id, got, tc.want)
		}
	}
}

// TestAllowsInboundWithPlan 覆盖 RFC 的判定规则:挂在套餐上时,all_nodes 就是全部;
// 否则是套餐入站与用户自己额外授权的并集;空套餐且没有额外授权时什么都不可用。
func TestAllowsInboundWithPlan(t *testing.T) {
	one := int64(2)

	t.Run("套餐范围内", func(t *testing.T) {
		u := &User{PlanID: &one}
		u.SetPlanAuth(false, []int64{1, 2})
		for _, tc := range []struct {
			id   int64
			want bool
		}{{1, true}, {2, true}, {3, false}, {999, false}} {
			if got := u.AllowsInbound(tc.id); got != tc.want {
				t.Fatalf("套餐入站 %d: got %v want %v", tc.id, got, tc.want)
			}
		}
	})

	t.Run("套餐加额外授权", func(t *testing.T) {
		u := &User{PlanID: &one, InboundIDs: []int64{3}}
		u.SetPlanAuth(false, []int64{1, 2})
		for _, tc := range []struct {
			id   int64
			want bool
		}{{1, true}, {2, true}, {3, true}, {4, false}} {
			if got := u.AllowsInbound(tc.id); got != tc.want {
				t.Fatalf("套餐+额外授权入站 %d: got %v want %v", tc.id, got, tc.want)
			}
		}
	})

	t.Run("全节点套餐", func(t *testing.T) {
		u := &User{PlanID: &one}
		u.SetPlanAuth(true, nil)
		for _, id := range []int64{1, 2, 999} {
			if !u.AllowsInbound(id) {
				t.Fatalf("all_nodes 套餐下入站 %d 应当可用", id)
			}
		}
	})

	t.Run("空套餐不等于全开", func(t *testing.T) {
		u := &User{PlanID: &one}
		u.SetPlanAuth(false, nil)
		for _, id := range []int64{1, 2, 999} {
			if u.AllowsInbound(id) {
				t.Fatalf("空套餐下入站 %d 不应当可用", id)
			}
		}
	})

	t.Run("空套餐加额外授权", func(t *testing.T) {
		u := &User{PlanID: &one, InboundIDs: []int64{5}}
		u.SetPlanAuth(false, nil)
		if !u.AllowsInbound(5) || u.AllowsInbound(6) {
			t.Fatalf("空套餐+额外授权应当只放行 5")
		}
	})

	t.Run("无套餐但已解析", func(t *testing.T) {
		u := &User{}
		u.SetPlanAuth(false, nil)
		if !u.AllowsInbound(1) || !u.AllowsInbound(999) {
			t.Fatal("没挂套餐且入站为空时应当全部可用")
		}
	})
}

// TestAllowsInboundUnresolvedPlanAuth 覆盖「有 plan_id 但授权没解析」:从 JSON
// 解码出来的用户就是这样。判定要以「授权已解析」为依据,不能因为 plan_id 非空
// 却拿不到套餐授权,就把用户判成一个入站都不能用。
func TestAllowsInboundUnresolvedPlanAuth(t *testing.T) {
	one := int64(1)

	// 带 plan_id 但没解析、也没有额外授权:按旧语义,全部可用。
	all := &User{PlanID: &one}
	for _, id := range []int64{1, 2, 999} {
		if !all.AllowsInbound(id) {
			t.Fatalf("未解析的 plan_id 不该让入站 %d 不可用", id)
		}
	}

	// 带 plan_id、未解析、有额外授权:按旧语义只放行列出的入站。
	limited := &User{PlanID: &one, InboundIDs: []int64{1}}
	if !limited.AllowsInbound(1) || limited.AllowsInbound(2) {
		t.Fatal("未解析时应当沿用旧语义只放行 inbound_ids")
	}
}

// TestPlanNormalizeAndValidate 覆盖去重排序与名字校验。
func TestPlanNormalizeAndValidate(t *testing.T) {
	p := &Plan{Name: "  全球  ", NodeIDs: []int64{3, 1, 3, 0}, InboundIDs: []int64{5, 2, 5}}
	p.Normalize()
	if p.Name != "全球" {
		t.Fatalf("名字没去空白: %q", p.Name)
	}
	if len(p.NodeIDs) != 2 || p.NodeIDs[0] != 1 || p.NodeIDs[1] != 3 {
		t.Fatalf("节点 id 没去重排序: %v", p.NodeIDs)
	}
	if len(p.InboundIDs) != 2 || p.InboundIDs[0] != 2 || p.InboundIDs[1] != 5 {
		t.Fatalf("入站 id 没去重排序: %v", p.InboundIDs)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("合法套餐不该报错: %v", err)
	}
	empty := &Plan{}
	if err := empty.Validate(); err == nil {
		t.Fatal("空套餐名应当报错")
	}
}
