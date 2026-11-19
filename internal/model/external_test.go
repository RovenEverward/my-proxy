package model

import "testing"

func TestRegionFromRemark(t *testing.T) {
	cases := []struct {
		remark string
		want   string
	}{
		{"🇯🇵 日本 AWS 01 随机刷新 0倍率", "JP"},
		{"香港 IEPL 专线", "HK"},
		{"us west premium", "US"},
		{"Los Angeles 03", "US"},
		{"Tokyo relay", "JP"},
		{"Singapore | 0.5x", "SG"},
		{"随机节点 99", ""},
		{"relay-01", ""},
		{"🇩🇪 FRA", "DE"}, // 旗帜优先于文字匹配
	}
	for _, c := range cases {
		if got := RegionFromRemark(c.remark); got != c.want {
			t.Fatalf("RegionFromRemark(%q) = %q, want %q", c.remark, got, c.want)
		}
	}
}

func TestExternalRemark(t *testing.T) {
	// 有清洗层:拼 源名 + 旗帜 + 地区 + 字母
	got := ExternalRemark("星空", "原名很长", ExternalMetadata{Region: "JP", City: "东京", Letter: "A"}, true)
	if got != "星空 · 🇯🇵 东京 A" {
		t.Fatalf("cleaned: %q", got)
	}
	// 没记录:回退 源名 + 原名
	got = ExternalRemark("星空", "原名很长", ExternalMetadata{}, false)
	if got != "星空 · 原名很长" {
		t.Fatalf("raw: %q", got)
	}
	// 有记录但没地区(理论上不生成):同样回退原名
	got = ExternalRemark("星空", "原名很长", ExternalMetadata{SourceID: 1, SourceKey: "k", Kind: "auto"}, true)
	if got != "星空 · 原名很长" {
		t.Fatalf("no-region: %q", got)
	}
}

func TestAssignExternalLetters(t *testing.T) {
	existing := map[string]ExternalMetadata{
		// graded 的记录原样保留,字母占住池位。
		"g1": {SourceID: 7, SourceKey: "g1", Region: "JP", City: "东京", Letter: "A", Kind: "graded"},
		// auto 已排好的也不动。
		"a1": {SourceID: 7, SourceKey: "a1", Region: "JP", City: "东京", Letter: "B", Kind: "auto"},
	}
	regions := map[string]string{"g1": "JP", "a1": "JP", "new1": "JP", "new2": "JP", "noreg": ""}
	got := AssignExternalLetters(7, []string{"new2", "new1", "noreg", "g1", "a1"}, regions, existing)
	if got["g1"].Letter != "A" || got["g1"].Kind != "graded" {
		t.Fatalf("graded 不动: %+v", got["g1"])
	}
	if got["a1"].Letter != "B" {
		t.Fatalf("auto 保持: %+v", got["a1"])
	}
	// new1/new2 进入 (JP,东京) 组,A、B 已被占,按 key 排序取 C、D。
	if got["new1"].Letter != "C" || got["new2"].Letter != "D" {
		t.Fatalf("new: %+v %+v", got["new1"], got["new2"])
	}
	if _, ok := got["noreg"]; ok {
		t.Fatal("无地区端点不生成记录")
	}
	// 不同来源隔离:同组同地区也能拿 A。
	other := AssignExternalLetters(8, []string{"x"}, map[string]string{"x": "JP"}, map[string]ExternalMetadata{})
	if other["x"].Letter != "A" {
		t.Fatalf("来源隔离: %+v", other["x"])
	}
}
