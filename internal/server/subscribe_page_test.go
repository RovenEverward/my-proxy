package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
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
	// 页面是给人看的,不能把 vless:// 明文撒在 HTML 里。
	if strings.Contains(html, "vless://") {
		t.Fatalf("页面里不该有分享链接明文:\n%s", html)
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
