package selfupdate

import "testing"

func TestControlDevelopmentUpdateGuard(t *testing.T) {
	for _, v := range []string{"dev-123456789abc-clean", "dev-123456789abc"} {
		if CheckControlUpdate(v) == nil {
			t.Fatal("开发版未被拦截")
		}
	}
	for _, v := range []string{"v0.9.13", ""} {
		if CheckControlUpdate(v) != nil {
			t.Fatal("正式版行为改变")
		}
	}
}
