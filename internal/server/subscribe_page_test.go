package server

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/ui"
)

// ---- 订阅页 ----

// browserHeaders 是浏览器打开地址时会带的头:Accept 里点名要 text/html。
func browserHeaders() map[string]string {
	return map[string]string{
		"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/126",
		"Accept":     "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}
}

func (e *env) createSubReadyUser(username string) (userView, string) {
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "香港 ws tls", "node.example.com")
	user := e.createUser(username)
	return user, "/sub/" + user.SubToken
}

func TestSubscriptionPageServedToBrowsers(t *testing.T) {
	e := newEnv(t)
	user, path := e.createSubReadyUser("alice")

	code, body := e.request("GET", path, nil, browserHeaders())
	if code != http.StatusOK {
		t.Fatalf("浏览器打开订阅应 200,实际 %d %s", code, body)
	}
	html := string(body)
	for _, want := range []string{
		"alice 的订阅",
		"状态",
		"可用",
		"分享链接",
		"换个订阅地址",
		path, // 自动识别链接就是当前地址
		"/sub/" + user.SubToken + "/clash",
		"/sub/" + user.SubToken + "/sing-box",
		"/sub/" + user.SubToken + "/base64-links",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("页面里缺 %q:\n%s", want, html)
		}
	}
	// 页面自包含:二维码在服务端画成内联 SVG,不加载外部脚本。
	if strings.Contains(html, "<script src=") || !strings.Contains(html, "<svg") {
		t.Errorf("订阅页应内联二维码 SVG 且不引外部脚本:\n%s", html)
	}
	// 页面是给人看的,不能把 vless:// 明文撒在 HTML 里。
	if strings.Contains(html, "vless://") {
		t.Fatalf("页面里不该有分享链接明文:\n%s", html)
	}
}

// 订阅页用随程序带的字体:两个 @font-face 都指向 /sub/ 下的字体入口(只转发
// /sub/ 的反向代理也取得到),--sans 第一个是 Mona Sans VF。
func TestSubscriptionPageUsesBundledFonts(t *testing.T) {
	e := newEnv(t)
	_, path := e.createSubReadyUser("alice")

	code, body := e.request("GET", path, nil, browserHeaders())
	if code != http.StatusOK {
		t.Fatalf("浏览器打开订阅应 200,实际 %d %s", code, body)
	}
	html := string(body)
	for _, want := range []string{
		`@font-face { font-family: "Mona Sans VF"; src: url(-/fonts/MonaSansVF.woff2) format("woff2"); font-weight: 200 900; font-display: swap; }`,
		`@font-face { font-family: "Noto Sans SC"; src: url(-/fonts/NotoSansSC-subset.woff2) format("woff2"); font-weight: 100 900; font-display: swap; unicode-range: U+2010-206F,U+2E80-9FFF,U+F900-FAFF,U+FE30-FE4F,U+FF00-FFEF; }`,
		`--sans:"Mona Sans VF",-apple-system`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("订阅页里缺 %q:\n%s", want, html)
		}
	}

	// 换了地址的那一次渲染落在 /sub/<token>/rotate 上,字体路径要多退一层。
	code, body = e.request("POST", path+"/rotate", nil, browserHeaders())
	if code != http.StatusOK {
		t.Fatalf("换地址应 200,实际 %d %s", code, body)
	}
	if !strings.Contains(string(body), "url(../-/fonts/MonaSansVF.woff2)") {
		t.Errorf("换地址后的页面里字体路径应相对到 /sub/-/fonts/:\n%s", body)
	}
}

// 字体入口只发白名单里的两个字体文件,带着长缓存与 nosniff,不要登录。
func TestSubscriptionFontRoute(t *testing.T) {
	e := newEnv(t)

	fonts := ui.Fonts()
	for _, name := range []string{"MonaSansVF.woff2", "NotoSansSC-subset.woff2"} {
		want, err := fs.ReadFile(fonts, name)
		if err != nil {
			t.Fatalf("读内嵌字体 %s: %v", name, err)
		}
		req, err := http.NewRequest("GET", e.srv.URL+"/sub/-/fonts/"+name, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("取字体 %s: %v", name, err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s 应 200,实际 %d", name, resp.StatusCode)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s 发出的字节与内嵌文件不一致", name)
		}
		for h, v := range map[string]string{
			"Content-Type":           "font/woff2",
			"Cache-Control":          "public, max-age=31536000, immutable",
			"X-Content-Type-Options": "nosniff",
		} {
			if hv := resp.Header.Get(h); hv != v {
				t.Errorf("%s 的 %s: %q,应为 %q", name, h, hv, v)
			}
		}
	}

	for _, name := range []string{"../ui.go", "OFL.txt", "OFL-NotoSansSC.txt", "NotoSansSC-subset.txt", "MonaSansMonoVF.woff2", "nope.woff2"} {
		code, body := e.request("GET", "/sub/-/fonts/"+name, nil, nil)
		if code != http.StatusNotFound {
			t.Errorf("/sub/-/fonts/%s 应 404,实际 %d %s", name, code, body)
		}
	}
	// 字体路由是四段路径,不能被三段式的 /sub/{token}/{format} 抢走。
	if code, body := e.request("GET", "/sub/-/fonts", nil, nil); code == http.StatusOK {
		t.Errorf("/sub/-/fonts 不该是字体入口,实际 200 %s", body)
	}
}

func TestSubscriptionPageDoesNotStealClientRequests(t *testing.T) {
	e := newEnv(t)
	_, path := e.createSubReadyUser("alice")

	t.Run("没被规则认出的客户端拿分享链接", func(t *testing.T) {
		// 用一条内置规则都不认的 UA:验的是"没命中规则就回落 links",不是分流。
		code, hdr, body := e.sub(path, "SomeVpn/1.0")
		if code != http.StatusOK {
			t.Fatalf("got %d %s", code, body)
		}
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("Content-Type: %q", ct)
		}
		if !strings.Contains(string(body), "vless://") {
			t.Fatalf("正文里没有分享链接:\n%s", body)
		}
	})

	t.Run("Accept */* 不触发订阅页", func(t *testing.T) {
		// clash-verge 会被内置规则认成 clash-meta,所以这里不该有明文链接;
		// 这一条要的是"Accept 不带 text/html 就不给页面"。
		code, body := e.request("GET", path, nil, map[string]string{
			"User-Agent": "clash-verge/1.7.7",
			"Accept":     "*/*",
		})
		if code != http.StatusOK {
			t.Fatalf("got %d %s", code, body)
		}
		if strings.Contains(string(body), "<html") {
			t.Fatalf("Accept 没点名 text/html,不该给订阅页:\n%s", body)
		}
		if !strings.Contains(string(body), "proxies:") {
			t.Fatalf("clash-verge 应当拿到 Clash 配置:\n%s", body)
		}
	})

	t.Run("路径点名格式时浏览器也拿配置", func(t *testing.T) {
		code, hdr, body := e.sub(path+"/clash", "Mozilla/5.0 (Macintosh)")
		if code != http.StatusOK {
			t.Fatalf("got %d %s", code, body)
		}
		if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
			t.Fatalf("路径点名 clash 时不该给页面,Content-Type: %q", ct)
		}
		if !strings.Contains(string(body), "proxies:") {
			t.Fatalf("正文不像 Clash 配置:\n%s", body)
		}
	})
}

func TestSubscriptionPageLetsRuleThatNamesLinksWin(t *testing.T) {
	e := newEnv(t)
	_, path := e.createSubReadyUser("alice")

	// 规则显式点名 links:浏览器也拿 links,页面不顶上来 —— 规则命中就是命中,
	// 哪怕它命中的恰好是默认格式。
	settings := model.Settings{Rules: []model.SubscriptionRule{
		{Pattern: `(?i)mozilla`, Result: model.FormatLinks},
	}}
	if code, data := e.api("PUT", "/api/settings", settings); code != http.StatusOK {
		t.Fatalf("存设置: got %d %s", code, data)
	}

	code, body := e.request("GET", path, nil, browserHeaders())
	if code != http.StatusOK {
		t.Fatalf("got %d %s", code, body)
	}
	if !strings.Contains(string(body), "vless://") {
		t.Fatalf("正文里没有分享链接:\n%s", body)
	}
}

func TestSubscriptionPageShowsStatusForInactiveUser(t *testing.T) {
	e := newEnv(t)
	_, path := e.createSubReadyUser("alice")

	e.updateUser("alice", func(u *model.User) { u.Enabled = false })

	code, body := e.request("GET", path, nil, browserHeaders())
	if code != http.StatusOK {
		t.Fatalf("停用用户的订阅页应 200,实际 %d %s", code, body)
	}
	if !strings.Contains(string(body), "已停用") {
		t.Fatalf("页面应写明状态:\n%s", body)
	}
}

// ---- 自助换地址 ----

func TestSubscriptionCarriesProfileWebPageURL(t *testing.T) {
	e := newEnv(t)
	_, path := e.createSubReadyUser("alice")

	code, hdr, _ := e.sub(path, "v2rayng/1.8")
	if code != http.StatusOK {
		t.Fatalf("订阅应 200,实际 %d", code)
	}
	got := hdr.Get("Profile-Web-Page-Url")
	if !strings.HasSuffix(got, path) {
		t.Fatalf("Profile-Web-Page-Url 应指向订阅页: %q", got)
	}
}

func TestSubscriptionRotateChangesTokenAndKillsOldAddress(t *testing.T) {
	e := newEnv(t)
	user, path := e.createSubReadyUser("alice")
	oldToken := user.SubToken

	code, body := e.request("POST", path+"/rotate", nil, browserHeaders())
	if code != http.StatusOK {
		t.Fatalf("换地址应 200,实际 %d %s", code, body)
	}
	html := string(body)
	if !strings.Contains(html, "订阅地址已更换") {
		t.Fatalf("页面应亮出新地址:\n%s", html)
	}

	// 页面里的自动链接与表单 action 都该指向新 token。
	newToken := subTokenFrom(html)
	if newToken == "" || newToken == oldToken {
		t.Fatalf("页面里的新 token 应为新值,old=%q new=%q", oldToken, newToken)
	}
	if !strings.Contains(html, "/sub/"+newToken+"/rotate") {
		t.Fatalf("表单 action 应指向新 token:\n%s", html)
	}

	// 旧地址当场死掉。
	if code, _, _ := e.sub("/sub/"+oldToken, "v2rayng/1.8"); code != http.StatusNotFound {
		t.Fatalf("旧地址应 404,实际 %d", code)
	}
	// 新地址能用,而且确实是同一个人的流量。
	if code, hdr, body := e.sub("/sub/"+newToken, "v2rayng/1.8"); code != http.StatusOK {
		t.Fatalf("新地址应 200,实际 %d %s", code, body)
	} else if cd := hdr.Get("Content-Disposition"); cd != "attachment; filename=alice" {
		t.Fatalf("新地址应是 alice 的订阅,Content-Disposition: %q", cd)
	}
}

func TestSubscriptionRotateRequiresRealToken(t *testing.T) {
	e := newEnv(t)

	code, body := e.request("POST", "/sub/does-not-exist/rotate", nil, browserHeaders())
	if code != http.StatusNotFound {
		t.Fatalf("假 token 换地址应 404,实际 %d %s", code, body)
	}
}

// subTokenFrom 从订阅页 HTML 里抠出自动链接里的 token —— 页面渲染的就是权威
// 的新值,直接用它的输出做断言。
func subTokenFrom(html string) string {
	const mark = "/sub/"
	i := strings.Index(html, mark)
	if i < 0 {
		return ""
	}
	rest := html[i+len(mark):]
	j := strings.IndexAny(rest, "\"/<")
	if j < 0 {
		return ""
	}
	return rest[:j]
}
