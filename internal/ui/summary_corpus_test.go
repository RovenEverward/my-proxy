package ui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 控制面每新增一种体检结论句式,界面都要有对应的人话翻译:这里要求 checkup.go 里每个
// Summary 句式的固定开头都在 e2e 的 SUMMARY_CORPUS 里有样例,D1 再检查翻译结果里
// 没有内部叫法和英文原话(文案检查管不到服务端字符串,靠这一对测试兜住)。
func TestSummaryCorpusCoversCheckup(t *testing.T) {
	src, err := os.ReadFile("../control/checkup.go")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("e2e/polish.spec.mjs")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(spec), "const SUMMARY_CORPUS")
	if i < 0 {
		t.Fatal("polish.spec.mjs 里没有 SUMMARY_CORPUS")
	}
	corpus := string(spec[i:])
	corpus = corpus[:strings.Index(corpus, "];")]
	re := regexp.MustCompile(`(?m)[sS]ummary = append\([^"\n]*"([^"]+)"`)
	n := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		frag := strings.TrimSpace(strings.Split(strings.Split(m[1], "%")[0], ":")[0])
		if frag == "" {
			continue
		}
		n++
		if !strings.Contains(corpus, frag) {
			t.Errorf("checkup.go 的结论「%s」在 SUMMARY_CORPUS 里没有样例,界面可能没翻译它", m[1])
		}
	}
	if n < 20 {
		t.Fatalf("只认出 %d 个结论句式,正则可能失效了", n)
	}
}
