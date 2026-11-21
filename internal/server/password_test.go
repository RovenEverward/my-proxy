package server

import (
	"strings"
	"testing"
	"time"
)

func TestAdminPasswordHashRoundTrip(t *testing.T) {
	h, err := hashAdminPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Fatalf("PHC 形状不对: %s", h)
	}
	if ok, err := verifyAdminPassword("correct horse battery", h); !ok || err != nil {
		t.Fatalf("对的密码没过: %v", err)
	}
	if ok, _ := verifyAdminPassword("correct horse batterx", h); ok {
		t.Fatal("错的密码过了")
	}
}

func TestWaitText(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "1 秒", 1500 * time.Millisecond: "2 秒", 59 * time.Second: "59 秒", time.Minute: "1 分钟", 14*time.Minute + time.Second: "15 分钟", 15 * time.Minute: "15 分钟"} {
		if got := waitText(d); got != want {
			t.Errorf("waitText(%v)=%q want %q", d, got, want)
		}
	}
}
