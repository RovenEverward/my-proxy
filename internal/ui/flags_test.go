package ui

import (
	"net/http"
	"strings"
	"testing"
)

// /flags/ 和 /fonts/ 一样不用登录:首次安装向导与登录转场里也要能画旗帜。
func TestFlagsNeedNoSession(t *testing.T) {
	s, _, sn, c := rig(t)
	w := do(s, "GET", "/flags/jp.svg", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("jp.svg 应 200,得到 %d %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if !strings.Contains(w.Body.String(), "<svg") {
		t.Error("响应不是 SVG")
	}
	if sn.count() != 0 {
		t.Fatal("国旗请求不能到控制面")
	}
	// 已登录也一样,只是走同一条内嵌资源路径。
	if w := do(s, "GET", "/flags/de.svg", c); w.Code != http.StatusOK {
		t.Errorf("登录后 de.svg: %d", w.Code)
	}
}

func TestFlagNamesAreValidated(t *testing.T) {
	s, _, sn, c := rig(t)
	cases := []struct {
		path string
		want int
	}{
		{"/flags/jp.svg", http.StatusOK},
		{"/flags/gb-eng.svg", http.StatusOK},
		{"/flags/zz.svg", http.StatusNotFound},       // 合法名字但没有这个文件
		{"/flags/JP.svg", http.StatusNotFound},       // 大写不收
		{"/flags/jp.png", http.StatusNotFound},       // 只出 SVG
		{"/flags/jp", http.StatusNotFound},           // 缺后缀
		{"/flags/../app.js", http.StatusNotFound},    // 路径穿越
		{"/flags/..%2fapp.js", http.StatusNotFound},  // 编码过的穿越
		{"/flags/jp/extra.svg", http.StatusNotFound}, // 目录层
		{"/flags/jp.svg?x=1", http.StatusOK},         // 查询串不影响
	}
	for _, tc := range cases {
		if w := do(s, "GET", tc.path, c); w.Code != tc.want {
			t.Errorf("%s: 得到 %d,应 %d", tc.path, w.Code, tc.want)
		}
	}
	if sn.count() != 0 {
		t.Fatal("国旗请求不能到控制面")
	}
	// 只有 GET/HEAD;写请求不能借这条路径。
	if w := do(s, "POST", "/flags/jp.svg", c); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 应 405,得到 %d", w.Code)
	}
}
