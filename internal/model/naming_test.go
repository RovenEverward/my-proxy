package model

import "testing"

func TestLabelKey(t *testing.T) {
	for in, want := range map[string]string{
		"🇺🇸 洛杉矶 A":  "洛杉矶a",
		"洛杉矶a":      "洛杉矶a",
		" 洛杉矶 · A ": "洛杉矶a",
		"LA-1":      "la-1",
		"🇯🇵":        "",
	} {
		if got := LabelKey(in); got != want {
			t.Errorf("LabelKey(%q) = %q, want %q", in, got, want)
		}
	}
}
