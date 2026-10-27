package server

import "testing"

// MPX_API_BURST 只给浏览器测试放宽桶容量;产品默认不变。
func TestAPILimiterBurstFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want float64
	}{
		{"有效", "1000", 1000},
		{"无效", "abc", apiBurst},
		{"非正数", "0", apiBurst},
		{"缺失", "", apiBurst},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MPX_API_BURST", c.env)
			if got := newAPILimiter().burst; got != c.want {
				t.Fatalf("burst = %v,想要 %v", got, c.want)
			}
		})
	}
}
