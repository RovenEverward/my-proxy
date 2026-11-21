package ui

import (
	"io"
	"strings"
	"testing"
)

// Flag() 是控制面 /flags/{code} 处理器的取旗入口(见
// internal/server/admin_page.go,HTTP 语义另有那边测试)。这里卡两件事:
// 名字不合法 / 路径穿越一律 (nil,false),合法名字出的是真 SVG。
func TestFlagNamesAreValidated(t *testing.T) {
	ok := []string{"jp", "de", "cn", "gb-eng"}
	for _, name := range ok {
		rc, found := Flag(name)
		if !found {
			t.Errorf("%s 应能取到", name)
			continue
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || !strings.Contains(string(b), "<svg") {
			t.Errorf("%s 内容不是 SVG(err=%v)", name, err)
		}
	}
	bad := []string{
		"zz",          // 合法名字但 zip 里没有
		"JP",          // 大写不收
		"jp.svg",      // 名字本身不带后缀
		"../app.js",   // 路径穿越
		"..%2fapp.js", // 编码过的穿越(到不了这也要拒)
		"jp/extra",    // 目录层
		"",            // 空
		"🇯🇵",         // 非 ASCII
	}
	for _, name := range bad {
		if rc, found := Flag(name); found {
			rc.Close()
			t.Errorf("%q 不该取到", name)
		}
	}
}
