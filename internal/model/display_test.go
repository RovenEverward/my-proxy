package model

import "testing"

func TestNodeDisplay(t *testing.T) {
	cases := []struct {
		m    LocationMetadata
		want string
	}{
		{LocationMetadata{}, ""},
		{LocationMetadata{Region: "US", City: "洛杉矶", Letter: "A"}, "🇺🇸 洛杉矶 A"},
		{LocationMetadata{Region: "JP", Letter: "B"}, "🇯🇵 日本 B"},
		{LocationMetadata{Region: "HK"}, "🇭🇰 香港"},
		{LocationMetadata{Region: "亚太"}, "亚太"},
	}
	for _, c := range cases {
		if got := NodeDisplay(c.m); got != c.want {
			t.Errorf("NodeDisplay(%+v) = %q, want %q", c.m, got, c.want)
		}
	}
	if LabelKey(NodeDisplay(LocationMetadata{Region: "US", City: "洛杉矶", Letter: "A"})) != LabelKey("洛杉矶A") {
		t.Error("显示名应能按 LabelKey 匹配")
	}
}

func TestNextLetter(t *testing.T) {
	if got := NextLetter(map[string]bool{"A": true, "C": true}); got != "B" {
		t.Errorf("got %q", got)
	}
	used := map[string]bool{}
	for i := 0; i < 26; i++ {
		used[LetterAt(i)] = true
	}
	if got := NextLetter(used); got != "AA" {
		t.Errorf("Z 之后应为 AA,got %q", got)
	}
	if LetterAt(26+26*26-1) != "ZZ" {
		t.Error("最后一个应为 ZZ")
	}
}
