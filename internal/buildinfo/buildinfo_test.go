package buildinfo

import "testing"

func TestIsDevelopment(t *testing.T) {
	for _, c := range []struct {
		version string
		want    bool
	}{
		{"dev-123456789abc-clean", true}, {"dev-123456789abc-dirty", true},
		{"v0.9.13", false}, {"v0.0.0-123456789abc", false}, {"", false},
	} {
		if got := IsDevelopment(c.version); got != c.want {
			t.Errorf("IsDevelopment(%q) = %v，想要 %v", c.version, got, c.want)
		}
	}
}

// 桌面标记只能来自编译期注入;测试里直接改包级变量,验证两种取值都认得。
func TestDesktop(t *testing.T) {
	old := desktop
	t.Cleanup(func() { desktop = old })

	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", false},
		{"1", true},
		{" 1 ", true},
	} {
		desktop = tc.in
		if got := Desktop(); got != tc.want {
			t.Errorf("desktop = %q 时 Desktop() = %v,想要 %v", tc.in, got, tc.want)
		}
	}
}
