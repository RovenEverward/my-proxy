package cli

import (
	"testing"

	"github.com/egoist/mygo"
)

// TestAppWindowOptions 钉住各平台的窗口选项:Windows/macOS 隐藏标题栏并让出
// 52 的高度给页面自己画,Linux 保持原生标题栏;所有平台都用同一份底色。
func TestAppWindowOptions(t *testing.T) {
	for _, tc := range []struct {
		goos string
		bar  mygo.TitleBarStyle
		h    int
	}{
		{"windows", mygo.TitleBarHidden, 52},
		{"darwin", mygo.TitleBarHidden, 52},
		{"linux", "", 0},
	} {
		o := appWindowOptions(tc.goos, "http://127.0.0.1:1234/?k=x")
		if o.TitleBarStyle != tc.bar || o.TitleBarHeight != tc.h {
			t.Errorf("%s: TitleBarStyle=%q Height=%d,期望 %q/%d", tc.goos, o.TitleBarStyle, o.TitleBarHeight, tc.bar, tc.h)
		}
		if o.BackgroundColor != "light-dark(#f6f5f2, #161615)" {
			t.Errorf("%s: BackgroundColor=%q", tc.goos, o.BackgroundColor)
		}
		if o.Title != "MPX" || o.Width != 1280 || o.Height != 820 || o.URL == "" {
			t.Errorf("%s: 基本选项不对: %+v", tc.goos, o)
		}
	}
}
