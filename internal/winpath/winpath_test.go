package winpath

import (
	"strings"
	"testing"
)

// 长 PATH(远超 NSIS 的 1024/8192 缓冲)原样保留,只在末尾加一项。
func TestAddKeepsLongPathIntact(t *testing.T) {
	var parts []string
	for i := 0; len(strings.Join(parts, ";")) < 12000; i++ {
		parts = append(parts, `C:\Tools\some-long-directory-name-`+strings.Repeat("x", i%40))
	}
	parts = append(parts, `%USERPROFILE%\bin`, "", `%LOCALAPPDATA%\Microsoft\WindowsApps`)
	cur := strings.Join(parts, ";")
	got, changed := Add(cur, `C:\Program Files\mpx`)
	if !changed || got != cur+`;C:\Program Files\mpx` {
		t.Fatalf("changed=%v, 新串不是「原串;目录」(len %d → %d)", changed, len(cur), len(got))
	}
	back, changed := Remove(got, `C:\Program Files\mpx`)
	if !changed || back != cur {
		t.Fatal("加了再删不等于原串")
	}
}

func TestAddEdgeCases(t *testing.T) {
	for _, c := range []struct {
		cur, want string
		changed   bool
	}{
		{"", `C:\Program Files\mpx`, true},
		{`C:\a;`, `C:\a;C:\Program Files\mpx`, true},
		{`C:\a;c:\program files\MPX\`, `C:\a;c:\program files\MPX\`, false},
		{`"C:\Program Files\mpx"`, `"C:\Program Files\mpx"`, false},
		{`%ProgramFiles%\mpx;C:\a`, `%ProgramFiles%\mpx;C:\a;C:\Program Files\mpx`, true}, // %VAR% 不展开,也不改
	} {
		got, changed := Add(c.cur, `C:\Program Files\mpx`)
		if got != c.want || changed != c.changed {
			t.Errorf("Add(%q) = %q,%v; want %q,%v", c.cur, got, changed, c.want, c.changed)
		}
	}
}

func TestRemoveOnlyOurEntry(t *testing.T) {
	cur := `%USERPROFILE%\bin;C:\Program Files\mpx;;C:\b;c:\program files\mpx\;C:\Program Files\mpx-other`
	got, changed := Remove(cur, `C:\Program Files\mpx`)
	want := `%USERPROFILE%\bin;;C:\b;C:\Program Files\mpx-other`
	if !changed || got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if same, changed := Remove(`C:\a;C:\b`, `C:\Program Files\mpx`); changed || same != `C:\a;C:\b` {
		t.Fatal("没有我们那一项时不该改动")
	}
	if got, _ := Remove(`C:\Program Files\mpx`, `C:\Program Files\mpx`); got != "" {
		t.Fatalf("只剩我们一项时删完应为空,got %q", got)
	}
}
