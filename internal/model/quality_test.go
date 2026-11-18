package model

import "testing"

func TestQualityScore(t *testing.T) {
	score, ded := QualityScore(map[string]bool{}, nil)
	if score != 100 || len(ded) != 0 {
		t.Fatalf("干净 IP 应得满分: %d %v", score, ded)
	}
	score, ded = QualityScore(map[string]bool{"is_datacenter": true}, nil)
	if score != 90 || len(ded) != 1 || ded[0].Flag != "is_datacenter" {
		t.Fatalf("机房扣 10: %d %v", score, ded)
	}
	abuse := 50
	score, ded = QualityScore(map[string]bool{"is_tor": true, "is_vpn": true, "is_abuser": true}, &abuse)
	// 100 - 50 - 25 - 30 - 15 = -20 → 0
	if score != 0 {
		t.Fatalf("分数应被压到 0: %d", score)
	}
	zero := 0
	score, _ = QualityScore(map[string]bool{"is_datacenter": true}, &zero)
	if score != 90 {
		t.Fatalf("abuse 0 不扣分: %d", score)
	}
}

func TestNormalizeRegion(t *testing.T) {
	cases := []struct {
		stored, detected, want string
		conflict               bool
	}{
		{"", "US", "US", false},
		{"美西", "US", "US", false},
		{"us", "US", "US", false},
		{"US", "JP", "US", true},
		{"US", "US", "US", false},
		{"美西", "", "美西", false},
		{"US", "", "US", false},
	}
	for _, c := range cases {
		got, conflict := NormalizeRegion(c.stored, c.detected)
		if got != c.want || conflict != c.conflict {
			t.Fatalf("NormalizeRegion(%q,%q) = %q,%v want %q,%v", c.stored, c.detected, got, conflict, c.want, c.conflict)
		}
	}
}
