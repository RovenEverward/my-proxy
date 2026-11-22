package server

import (
	"net/http"
	"testing"
)

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

// isRulesetPath 只认 /sub/{token}/ruleset/{fmt}/{name} 五段;段数不对或 token 里
// 碰巧含 ruleset 字样的都不算 —— 按段比对,不靠子串。
func TestIsRulesetPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/sub/tok123/ruleset/clash/lan", true},
		{"/sub/tok123/ruleset/singbox/geoip-cn", true},
		{"/sub/tok123", false},
		{"/sub/tok123/clash-meta", false},
		{"/sub/-/fonts/x", false},
		{"/sub/ruleset/ruleset/clash/lan", true}, // token 恰为 "ruleset" 仍按段命中
		{"/sub/tok123/ruleset", false},
		{"/sub/tok123/ruleset/clash", false},
		{"/sub/tok123/ruleset/clash/lan/extra", false},
		{"/api/rulesets", false},
		{"", false},
		{"/", false},
	}
	for _, c := range cases {
		if got := isRulesetPath(c.path); got != c.want {
			t.Fatalf("isRulesetPath(%q) = %v,想要 %v", c.path, got, c.want)
		}
	}
}

// 规则集镜像文件与订阅本体分桶(ISS-18):订阅桶见底时 ruleset 照走自己的桶,
// 反过来 ruleset 桶见底也不拖垮订阅文档。
func TestSubRateLimitSplitBuckets(t *testing.T) {
	e := newEnvWith(t, nil)

	// 订阅桶掐死:第一份文档就该 429,但 ruleset 路径不受影响(404 是业务
	// 结果,说明请求过了限流进到处理器)。
	e.s.subLimiter.rate, e.s.subLimiter.burst = 1e-9, 0
	if code, _ := e.request(http.MethodGet, "/sub/tok123", nil, nil); code != http.StatusTooManyRequests {
		t.Fatalf("订阅桶打死后 GET /sub/tok123 应 429,得到 %d", code)
	}
	if code, _ := e.request(http.MethodGet, "/sub/tok123/ruleset/clash/lan", nil, nil); code == http.StatusTooManyRequests {
		t.Fatal("订阅桶打死不应连累 ruleset 路径,得到 429")
	}

	// 恢复订阅桶,掐死 ruleset 桶:订阅照过,ruleset 429。
	e.s.subLimiter.rate, e.s.subLimiter.burst = 1e9, 1e9
	e.s.rulesetLimiter.rate, e.s.rulesetLimiter.burst = 1e-9, 0
	if code, _ := e.request(http.MethodGet, "/sub/tok123", nil, nil); code == http.StatusTooManyRequests {
		t.Fatal("ruleset 桶打死不应连累订阅路径,得到 429")
	}
	code, _ := e.request(http.MethodGet, "/sub/tok123/ruleset/clash/lan", nil, nil)
	if code != http.StatusTooManyRequests {
		t.Fatalf("ruleset 桶打死后应 429,得到 %d", code)
	}
}
