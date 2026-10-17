package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// copyPunctSource masks comments and regular-expression literals, preserving
// newlines and offsets. Quotes (including nested template expressions) protect
// URLs and comment-like text in visible copy from being treated as comments.
func copyPunctSource(source string) string {
	out := []byte(source)
	mask := func(start, end int) {
		for i := start; i < end; i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
	}
	var code func(int, bool) int
	var quoted func(int) int
	quoted = func(i int) int {
		quote := source[i]
		for i = i + 1; i < len(source); {
			switch {
			case source[i] == '\\':
				i += 2
			case source[i] == quote:
				return i + 1
			case quote == '`' && strings.HasPrefix(source[i:], "${"):
				i = code(i+2, true)
			default:
				i++
			}
		}
		return len(source)
	}
	code = func(i int, templateExpr bool) int {
		depth, prev := 0, byte(0)
		for i < len(source) {
			c := source[i]
			switch {
			case strings.HasPrefix(source[i:], "//"):
				end := i
				for end < len(source) && source[end] != '\n' {
					end++
				}
				mask(i, end)
				i = end
			case strings.HasPrefix(source[i:], "/*"):
				end := len(source)
				if n := strings.Index(source[i+2:], "*/"); n >= 0 {
					end = i + 2 + n + 2
				}
				mask(i, end)
				i = end
			case c == '\'' || c == '"' || c == '`':
				i = quoted(i)
				prev = 'x'
			case c == '/' && (prev == 0 || strings.ContainsRune("=(:,[!&|?;{", rune(prev))):
				// A regex can begin after these expression-opening tokens.
				end, inClass := i+1, false
				for end < len(source) && source[end] != '\n' {
					ch := source[end]
					if ch == '\\' {
						end += 2
						continue
					}
					if ch == '/' && !inClass {
						end++
						mask(i, end)
						break
					}
					if ch == '[' {
						inClass = true
					} else if ch == ']' {
						inClass = false
					}
					end++
				}
				i++
				if end <= len(source) && end > i && source[end-1] == '/' {
					i = end
				}
				prev = 'x'
			default:
				if c == '}' && templateExpr && depth == 0 {
					return i + 1
				}
				if c == '{' {
					depth++
				} else if c == '}' {
					depth--
				}
				if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
					prev = c
				}
				i++
			}
		}
		return i
	}
	code(0, false)
	return string(out)
}

func copyPunctFragments(line string) []string {
	// Only format syntax is exempt. Mask the colon alone, so punctuation
	// immediately before/after an allowed format is still checked.
	for _, format := range []string{"地址:端口", "域名:端口", "服务器 IP:端口"} {
		line = strings.ReplaceAll(line, format, strings.ReplaceAll(format, ":", "："))
	}
	runes := []rune(line)
	chinese := func(r rune) bool {
		return unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff01 && r <= 0xff65)
	}
	var fragments []string
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		// 三个半角点是中文省略号写错了(「安装...」「加载中 ...」「正在连接 ${name}...」)。
		// 三点前是汉字、空格或 ${…} 插值,但同一片段里得有汉字才算文案:展开运算符
		// (...x、[...a]、{...o}、f(...args))和纯英文(Loading...)都不会命中。
		if r == '.' && i+2 < len(runes) && runes[i+1] == '.' && runes[i+2] == '.' {
			if i > 0 && runes[i-1] == '.' {
				continue
			}
			before := false
			interp := false
			if i > 0 {
				j := i - 1
				for j >= 0 && unicode.IsSpace(runes[j]) {
					j--
				}
				if j >= 0 {
					if chinese(runes[j]) {
						before = true
					} else if runes[j] == '}' && endsInterp(runes, j) {
						interp = true
					}
				}
			}
			after := i+3 < len(runes) && chinese(runes[i+3])
			lo, hi := max(0, i-16), min(len(runes), i+17)
			hasHan := false
			for j := lo; j < hi; j++ {
				if unicode.Is(unicode.Han, runes[j]) {
					hasHan = true
					break
				}
			}
			if hasHan && (before || interp || after) {
				fragments = append(fragments, string(runes[lo:hi]))
			}
			continue
		}
		if r != ',' && r != '?' && r != ':' {
			continue
		}
		left, right := i-1, i+1
		for left >= 0 && unicode.IsSpace(runes[left]) {
			left--
		}
		for right < len(runes) && unicode.IsSpace(runes[right]) {
			right++
		}
		// 紧跟在 ${…} 插值后的标点(如「停用 ${name}?」):同一段模板字面量里有汉字就算文案。
		afterInterp := false
		if i > 0 && runes[i-1] == '}' && i+1 < len(runes) && (runes[i+1] == '`' || chinese(runes[i+1])) {
			for j := i - 1; j >= 0 && runes[j] != '`'; j-- {
				if unicode.Is(unicode.Han, runes[j]) {
					afterInterp = true
					break
				}
			}
		}
		if afterInterp || (left >= 0 && chinese(runes[left])) || (right < len(runes) && chinese(runes[right])) {
			fragments = append(fragments, string(runes[max(0, i-16):min(len(runes), i+17)]))
		}
	}
	return fragments
}

// endsInterp 判断 runes[close] 这个 '}' 是不是 ${…} 插值的收尾:从右往左配对花括号,
// 找到配对的 '{' 时看它前面是不是 '$'。
func endsInterp(runes []rune, close int) bool {
	depth := 0
	for j := close; j >= 0; j-- {
		switch runes[j] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				return j > 0 && runes[j-1] == '$'
			}
		}
	}
	return false
}

func TestCopyPunct(t *testing.T) {
	t.Run("assets", func(t *testing.T) {
		files, err := filepath.Glob("assets/*.js")
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatal("no JavaScript assets found")
		}
		files = append(files, "assets/index.html")
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(copyPunctSource(string(data)), "\n") {
				for _, fragment := range copyPunctFragments(line) {
					t.Errorf("%s:%d: %s", file, i+1, fragment)
				}
			}
		}
	})
	t.Run("detector", func(t *testing.T) {
		for _, tc := range []struct {
			name, source string
			want         int
		}{
			{"comma", `"不设的话,链接"`, 1},
			{"question", `"换一条新链接?"`, 1},
			{"colon", `"可选:让它更好用"`, 1},
			{"whitespace", `"提示 \t: 继续"`, 1},
			{"brackets", `"「MPX」?"`, 1},
			{"punctuation", `"完成。?"`, 1},
			{"fullwidth", `"不设的话，链接。换一条新链接？可选：继续"`, 0},
			{"ternary", `a ? b : c`, 0},
			{"object", `({ label: "中文", enabled: true })`, 0},
			{"url", `"http://x"`, 0},
			{"urlThenCopy", `const url = "http://x"; toast("提示,继续"); // 注释,忽略`, 1},
			{"commentInsideString", `"中文 // 提示,继续"`, 1},
			{"blockInsideString", `"中文 /* 提示,继续 */"`, 1},
			{"lineComment", `toast("正常");// 注释,忽略`, 0},
			{"blockComment", "/* 注释,忽略\n提示:忽略 */\n\"正常\"", 0},
			{"address", `"地址:端口"`, 0},
			{"domain", `"域名:端口"`, 0},
			{"serverAddress", `"http://服务器 IP:端口/sub/token"`, 0},
			{"formatThenCopy", `"地址:端口,继续"`, 1},
			{"english", `"B / KB / MB, 100GB: unlimited?"`, 0},
			{"timeAndDate", `"YYYY-MM-DD 12:34"`, 0},
			{"key", "`${a}:${b}`", 0},
			{"regex", `const sec = /(?:v[\d.]+\s+)?更新要点/; toast("提示,继续");`, 1},
			{"regexSlashes", `const r = /https?:\/\//; toast("提示,继续");`, 1},
			{"nestedTemplate", "H`标题${ok ? H`提示,继续` : \"正常\"}`", 1},
			{"templateComment", "H`标题${/* 注释,忽略 */ '正常'}内容`", 0},
			{"afterInterpolation", "confirmBox({ title: `停用 ${d.name}?` })", 1},
			{"afterInterpolationCopy", "toast(`第 ${i},共 ${n} 条`)", 1},
			{"interpolationTernary", "H`${a ? H`甲` : \"乙\"}`", 0},
			{"interpolationEnglish", "`${a}?x=1`", 0},
			{"ellipsisAfterHan", `"安装..."`, 1},
			{"ellipsisAfterHan2", `"加载中..."`, 1},
			{"ellipsisSpace", `"加载中 ..."`, 1},
			{"ellipsisAfterInterp", "`正在连接 ${name}...`", 1},
			{"ellipsisAfterInterpSpace", "`正在连接 ${name} ...`", 1},
			{"ellipsisSpread", `[...a, ...b]`, 0},
			{"ellipsisObjectSpread", `({ ...o })`, 0},
			{"ellipsisCallArgs", `f(...args)`, 0},
			{"ellipsisEnglish", `"Loading..."`, 0},
			{"ellipsisEnglishSpace", `"Loading ..."`, 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var got []string
				for _, line := range strings.Split(copyPunctSource(tc.source), "\n") {
					got = append(got, copyPunctFragments(line)...)
				}
				if len(got) != tc.want {
					t.Errorf("got %d violations, want %d: %v", len(got), tc.want, got)
				}
			})
		}
	})
}

// 批量替换标点时曾把「？」写成了按 Latin-1 二次编码的「ï¼」。资产里不该出现这种乱码。
func TestAssetsNoMojibake(t *testing.T) {
	files, _ := filepath.Glob("assets/*.js")
	css, _ := filepath.Glob("assets/*.css")
	files = append(append(files, css...), "assets/index.html")
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, bad := range []string{"\u00ef\u00bc", "\u00e3\u0080", "\u00e2\u0080"} {
				if strings.Contains(line, bad) {
					t.Errorf("%s:%d: mojibake %q", file, i+1, bad)
				}
			}
		}
	}
}

// copyBannedTerms 是界面上不再使用的旧词。可见文案里出现任何一个都算失败;
// 确有技术含义的地方按文件 + 子串在 copyBannedAllowed 里点名放行。
var copyBannedTerms = []string{"入站", "入口", "巡检", "下发", "排水"}

// copyBannedAllowed 是文件 → 放行的子串。掩码后的某一行命中其中任一子串就整行放行。
// 只用于「高级」里确实要展示入站/入口技术细节的地方,以及必须与控制面原文逐字
// 匹配的匹配键(doctor.js 的漂移文案),不是给漏改兜底。
var copyBannedAllowed = map[string][]string{
	"assets/conn.js": {
		"协议、端口、",
		"订阅里每个入口一行",
		"编辑这个入口",
		"还没有入口 · 订阅会用入站自己的地址",
		"个入口一起删掉",
	},
	"assets/doctor.js": {
		"配置和下发的不一致(节点上被改过)",
		"sing-box 配置未读取或与下发内容不一致",
	},
}

// copyBannedLineOK 判断某一行是否整行放行:资产里可在原始行写 `// copy-ok: 原因`
// 标记,或者原行(掩码后)命中 copyBannedAllowed 里该文件的子串。
func copyBannedLineOK(file, original, masked string) bool {
	if strings.Contains(original, "// copy-ok:") {
		return true
	}
	for _, sub := range copyBannedAllowed[filepath.ToSlash(file)] {
		if strings.Contains(masked, sub) {
			return true
		}
	}
	return false
}

// TestCopyBannedTerms 只看可见文案:先用 copyPunctSource 掩掉注释与正则,
// 再逐行找旧词,和 TestCopyPunct 用同一套掩码。
func TestCopyBannedTerms(t *testing.T) {
	files, err := filepath.Glob("assets/*.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no JavaScript assets found")
	}
	html, err := filepath.Glob("assets/*.html")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, html...)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		maskedLines := strings.Split(copyPunctSource(src), "\n")
		srcLines := strings.Split(src, "\n")
		for i, masked := range maskedLines {
			original := ""
			if i < len(srcLines) {
				original = srcLines[i]
			}
			if copyBannedLineOK(file, original, masked) {
				continue
			}
			for _, term := range copyBannedTerms {
				if strings.Contains(masked, term) {
					t.Errorf("%s:%d: 可见文案里还有旧词 %q: %s", file, i+1, term, strings.TrimSpace(original))
				}
			}
		}
	}
}
