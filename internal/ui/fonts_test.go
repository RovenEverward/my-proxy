package ui

import (
	"regexp"
	"strings"
	"testing"
)

// Windows 上 font-family 里没有中文字体时,中文会掉到宋体。--sans 和 --mono
// 都要在通用族名之前接上中文无衬线字体;CSS 里也不能再有绕开这两个变量、
// 自己写死字体的地方。
func TestFontStacksHaveCJKFallback(t *testing.T) {
	css, err := assets.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"--sans", "--mono"} {
		m := regexp.MustCompile(v + `:([^;}]+)`).FindStringSubmatch(string(css))
		if m == nil {
			t.Fatalf("app.css 里没有 %s", v)
		}
		stack := m[1]
		for _, want := range []string{`"Microsoft YaHei UI"`, `"PingFang SC"`, `"Noto Sans CJK SC"`} {
			if !strings.Contains(stack, want) {
				t.Errorf("%s 缺中文回退 %s: %s", v, want, stack)
			}
		}
		generic := "sans-serif"
		if v == "--mono" {
			generic = "monospace"
		}
		if i, j := strings.Index(stack, `"Microsoft YaHei UI"`), strings.LastIndex(stack, generic); i < 0 || j < i {
			t.Errorf("%s 的中文回退要排在通用族名 %s 之前: %s", v, generic, stack)
		}
	}
	for _, f := range []string{"assets/app.css", "assets/wizard.css"} {
		b, _ := assets.ReadFile(f)
		for _, decl := range regexp.MustCompile(`font(-family)?:[^;}]+`).FindAllString(string(b), -1) {
			if strings.Contains(decl, "var(--") || strings.Contains(decl, "inherit") || strings.HasPrefix(decl, "font-family:\"") {
				continue
			}
			if regexp.MustCompile(`(?i)serif|mono|sans|menlo|courier|arial|helvetica`).MatchString(decl) {
				t.Errorf("%s 里写死了字体,没走 --sans / --mono: %s", f, decl)
			}
		}
	}
}
