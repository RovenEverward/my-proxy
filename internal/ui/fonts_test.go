package ui

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 中文字形一律用随程序带的 Noto Sans SC 子集:--sans 和 --mono 都要在通用族名
// 之前接上 "Noto Sans SC",而且 @font-face 指向本地 /fonts/ 下的文件(不连 CDN);
// 用户不喜欢微软雅黑,界面里不能再点它的名字。CSS 里也不能再有绕开这两个变量、
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
		generic := "sans-serif"
		if v == "--mono" {
			generic = "monospace"
		}
		if i, j := strings.Index(stack, `"Noto Sans SC"`), strings.LastIndex(stack, generic); i < 0 || j < i {
			t.Errorf("%s 要在通用族名 %s 之前接上 \"Noto Sans SC\": %s", v, generic, stack)
		}
	}
	if !regexp.MustCompile(`@font-face\{font-family:"Noto Sans SC";src:url\(/fonts/NotoSansSC-subset\.woff2\)`).Match(css) {
		t.Error(`app.css 里没有指向本地 /fonts/NotoSansSC-subset.woff2 的 "Noto Sans SC" @font-face`)
	}
	for _, f := range []string{"assets/fonts/MonaSansVF.woff2", "assets/fonts/MonaSansMonoVF.woff2", "assets/fonts/NotoSansSC-subset.woff2", "assets/fonts/OFL.txt", "assets/fonts/OFL-NotoSansSC.txt"} {
		if _, err := assets.ReadFile(f); err != nil {
			t.Errorf("没有随程序打包 %s: %v", f, err)
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

// 界面会用到的源码里不能出现微软雅黑(英文名或中文名都不行)。
func TestNoMicrosoftYaHei(t *testing.T) {
	bad := regexp.MustCompile(`(?i)microsoft\s*yahei|\x{5FAE}\x{8F6F}\x{96C5}\x{9ED1}`) // 微软雅黑
	files, _ := filepath.Glob("assets/*.*")
	files = append(files, "ui.go", "../server/page.go")
	for _, f := range files {
		if strings.HasSuffix(f, ".woff2") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := bad.FindIndex(b); loc != nil {
			t.Errorf("%s 里还有微软雅黑: %q", f, b[loc[0]:loc[1]])
		}
	}
}

// cjkUI 是要由 Noto Sans SC 子集画的字:中日韩各区、全角标点和常用标点。
func cjkUI(r rune) bool {
	return (r >= 0x2E80 && r <= 0x9FFF) || (r >= 0xF900 && r <= 0xFAFF) || (r >= 0xFE30 && r <= 0xFE4F) || (r >= 0xFF00 && r <= 0xFFEF) || (r >= 0x2010 && r <= 0x206F) || (r >= 0x20000 && r <= 0x3FFFF)
}

// 子集字表(NotoSansSC-subset.txt)第一行是字体文件的 sha256,第二行是子集里的全部字。
// 界面资产里每一个中文字、全角标点都得在子集里:改了文案冒出新字,这里就会失败,
// 按提示跑 scripts/build-cjk-font.py 重新生成即可。
func TestCJKSubsetCoversUI(t *testing.T) {
	list, err := os.ReadFile("assets/fonts/NotoSansSC-subset.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(list), "\n", 3)
	if len(lines) < 2 {
		t.Fatal("NotoSansSC-subset.txt 格式不对")
	}
	font, err := os.ReadFile("assets/fonts/NotoSansSC-subset.woff2")
	if err != nil {
		t.Fatal(err)
	}
	if sum := fmt.Sprintf("%x", sha256.Sum256(font)); sum != strings.TrimSpace(lines[0]) {
		t.Fatalf("字表和字体文件对不上(sha256 %s ≠ %s),重新跑 scripts/build-cjk-font.py", sum, strings.TrimSpace(lines[0]))
	}
	have := map[rune]bool{}
	for _, r := range lines[1] {
		have[r] = true
	}
	files, _ := filepath.Glob("assets/*.js")
	for _, pat := range []string{"assets/*.html", "assets/*.css"} {
		more, _ := filepath.Glob(pat)
		files = append(files, more...)
	}
	missing := map[rune]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range string(b) {
			if cjkUI(r) && r != 0xFFFD && !have[r] {
				if _, ok := missing[r]; !ok {
					missing[r] = filepath.ToSlash(f)
				}
			}
		}
	}
	for r, f := range missing {
		t.Errorf("%s 里的「%c」(U+%04X) 不在中文字体子集里;跑 scripts/build-cjk-font.py 重新生成", f, r, r)
	}
}
