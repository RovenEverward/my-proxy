package server

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 订阅页是给浏览器看的那一屏,和客户端的订阅共用同一个地址:只有当这次请求
// 本来就要拿到默认的 links、而且对方报明自己是浏览器(Accept 带 text/html)
// 时,这一屏才顶上来(见 decisions 26)。客户端和点名要格式的请求永远拿不到它。

type subPageLink struct {
	Name string
	URL  string
}

type subPageData struct {
	Username   string
	Status     string
	StatusText string
	Usage      string
	Percent    int
	BarShow    bool
	ExpireText string
	Token      string
	// Rotated 非空表示刚换过地址,页面上要把新地址亮出来 —— 这一次渲染是用户
	// 保存新地址的唯一机会。
	Capabilities       []subCapability
	Rotated            string
	Links              []subPageLink
	Purposes           []model.Purpose
	SelectedPurpose    string
	PurposeDescription string
	DefaultURL         string
	PurposeError       string
}

var subPageTmpl = template.Must(template.New("subpage").Parse(subPageHTML))

// serveSubPage 渲染浏览器看的订阅页。用户不可用时照常给页面,只是徽章上写明
// 是停用、过期还是超限 —— 页面本身不含凭证,给出去没有风险,而这是用户唯一
// 能看到"我为什么连不上"的地方。
func (s *Server) serveSubPage(w http.ResponseWriter, r *http.Request, user *model.User, settings *model.Settings, rotated string) {
	now := time.Now()
	status := user.Status(now)

	data := subPageData{
		Username:   user.Username,
		Status:     string(status),
		StatusText: statusText(status),
		ExpireText: expireText(user.ExpireAt, now),
		Token:      user.SubToken,
		Rotated:    rotated,
	}
	if user.DataLimit > 0 {
		data.BarShow = true
		data.Percent = int(clamp(user.UsedTraffic*100/user.DataLimit, 0, 100))
		data.Usage = formatBytes(user.UsedTraffic) + " / " + formatBytes(user.DataLimit)
	} else {
		data.Usage = formatBytes(user.UsedTraffic) + " / 不限"
	}

	sub := subBaseURL(settings, r) + "/sub/" + user.SubToken
	data.Links = []subPageLink{
		{Name: "自动识别", URL: sub},
		{Name: "clash", URL: sub + "/clash"},
		{Name: "clash-meta", URL: sub + "/clash-meta"},
		{Name: "sing-box", URL: sub + "/sing-box"},
		{Name: "xray", URL: sub + "/xray"},
		{Name: "base64", URL: sub + "/base64-links"},
	}

	var err error
	data.Capabilities, err = s.subscriberCapabilities(user, now)
	if err != nil {
		s.writeStoreError(w, err, "节点能力")
		return
	}
	data.DefaultURL = sub
	purposes, err := s.store.ListPurposes()
	if err != nil {
		s.writeStoreError(w, err, "用途")
		return
	}
	data.Purposes = purposes
	data.SelectedPurpose = r.URL.Query().Get("purpose")
	if data.SelectedPurpose != "" {
		for _, p := range purposes {
			if p.Slug == data.SelectedPurpose {
				data.PurposeDescription = p.Description
				if _, err := s.renderPurposeSubscription(user, p, model.FormatClashMeta); err != nil {
					data.PurposeError = err.Error()
				}
			}
		}
		data.Links = []subPageLink{
			{Name: "clash-meta", URL: sub + "/clash-meta?purpose=" + data.SelectedPurpose},
			{Name: "sing-box", URL: sub + "/sing-box?purpose=" + data.SelectedPurpose},
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 页面里带着订阅地址,不让中间层缓存。
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := subPageTmpl.Execute(w, data); err != nil {
		s.logger.Error("渲染订阅页失败", "username", user.Username, "error", err)
	}
}

// handleRotateSub 是页面上"换个订阅地址"按钮的落点:用路径里的 token 换一个新
// token,旧地址当场作废。
//
// 它不挂管理接口的鉴权 —— 拿着这条链接的人本来就能花这份流量,让他能换掉它不
// 引入新的风险;而真正想收回访问权的人(而不是换地址的人)该走管理接口的
// revoke-sub。换完不能再按旧 token 去查用户,所以用手上这个对象渲染页面。
func (s *Server) handleRotateSub(w http.ResponseWriter, r *http.Request) {
	user, err := s.store.GetUserBySubToken(r.PathValue("token"))
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}

	token, err := model.NewSubscriptionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成订阅 token 失败")
		return
	}
	user.SubToken = token
	if err := s.store.UpdateUser(user); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	s.invalidateSubCache()

	s.serveSubPage(w, r, user, settings, subBaseURL(settings, r)+"/sub/"+token)
}

// wantsHTML 报告这次请求是不是浏览器发的:Accept 里点名要 text/html。
//
// 只认点名。客户端要么不带 Accept、要么是 */*,两者都不含 text/html,所以
// 客户端永远拿不到订阅页。
func wantsHTML(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		if strings.HasPrefix(strings.TrimSpace(part), "text/html") {
			return true
		}
	}
	return false
}

func statusText(status model.Status) string {
	switch status {
	case model.StatusDisabled:
		return "已停用"
	case model.StatusExpired:
		return "已过期"
	case model.StatusLimited:
		return "已超限"
	default:
		return "可用"
	}
}

// expireText 把到期时间写成"还剩几天"。用天数而不是时刻,是因为用户真正关心
// 的只是还有多久;到期日按本地日期显示,和用户看到的日历一致。
func expireText(t *time.Time, now time.Time) string {
	if t == nil {
		return "不过期"
	}
	date := t.Local().Format("2006-01-02")
	if !t.After(now) {
		return date + "(已过期)"
	}
	days := int(t.Sub(now).Hours() / 24)
	if days == 0 {
		return date + "(今天到期)"
	}
	return fmt.Sprintf("%s(还剩 %d 天)", date, days)
}

// formatBytes 把字节数写成人能读的大小。
// CLI 里那份 humanBytes 在另一个包(server 不能 import cli),两边各自写一份。
func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n) / 1024
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

const subPageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Username}} 的订阅</title>
<style>
:root { color-scheme: light dark; }
body { margin: 0 auto; padding: 24px 16px 48px; max-width: 720px;
  font: 15px/1.6 -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; }
h1 { font-size: 20px; margin: 0 0 6px; }
h2 { font-size: 15px; margin: 28px 0 8px; }
.badge { display: inline-block; padding: 1px 8px; border-radius: 10px; font-size: 13px;
  border: 1px solid currentColor; }
.badge.active { color: #1a7f37; }
.badge.disabled { color: #6e7781; }
.badge.expired { color: #9a6700; }
.badge.limited { color: #cf222e; }
.muted { color: #6e7781; font-size: 13px; }
.bar { display: block; height: 6px; border-radius: 3px; background: #d0d7de;
  overflow: hidden; margin: 6px 0 2px; }
.bar > i { display: block; height: 100%; background: #1a7f37; }
ul.links { list-style: none; margin: 0; padding: 0; }
ul.links li { display: flex; align-items: center; gap: 8px; padding: 7px 0;
  border-bottom: 1px solid #d8dee4; }
ul.links .name { flex: 0 0 6.5em; font-size: 13px; color: #57606a; }
ul.links code { flex: 1 1 auto; overflow-wrap: anywhere; font-size: 12px; }
button { font: inherit; padding: 4px 10px; border-radius: 6px; cursor: pointer;
  border: 1px solid #d0d7de; background: #f6f8fa; color: inherit; }
#qr { margin-top: 16px; }
#qr img, #qr canvas { width: 160px; height: 160px; }
.notice { margin: 0 0 16px; padding: 10px 12px; border-radius: 8px;
  background: #ddf4ff; border: 1px solid #54aeff; }
.notice code { overflow-wrap: anywhere; }
.rotate { margin-top: 28px; }
</style>
</head>
<body>
<h1>{{.Username}} 的订阅</h1>
<p>状态:<span class="badge {{.Status}}">{{.StatusText}}</span></p>
{{if .Rotated}}<p class="notice">订阅地址已更换,旧地址立刻失效。把新地址保存好:<br><code>{{.Rotated}}</code></p>
{{end}}<p>流量:{{.Usage}}{{if .BarShow}}<span class="bar"><i style="width:{{.Percent}}%"></i></span>{{end}}</p>
<p>到期:{{.ExpireText}}</p>

{{if .Purposes}}<h2>选择用途</h2>
<form method="get" action="/sub/{{.Token}}">
<label for="purpose">用途</label> <select id="purpose" name="purpose">
<option value="">默认订阅</option>
{{range .Purposes}}<option value="{{.Slug}}" {{if eq $.SelectedPurpose .Slug}}selected{{end}}>{{.Name}}</option>{{end}}
</select> <button type="submit">查看订阅</button>
</form>
{{if .PurposeDescription}}<p>{{.PurposeDescription}}</p>{{end}}
<p class="muted">用途订阅支持 clash-meta 与 sing-box。流媒体、下载和游戏的指定域名走用途组，其余流量沿用日常路由。客户端 url-test 测量 HTTP 延迟；下载吞吐、游戏 RTT、抖动与丢包需在本地实际观察。</p>
{{end}}
{{if .PurposeError}}<p class="notice" role="alert">{{.PurposeError}}。<a href="{{.DefaultURL}}">使用默认订阅</a></p>{{end}}
{{if .SelectedPurpose}}<p><a href="{{.DefaultURL}}">返回默认订阅</a></p>{{end}}
<h2>分享链接</h2>
<ul class="links">
{{range .Links}}<li><span class="name">{{.Name}}</span><code>{{.URL}}</code><button type="button" data-url="{{.URL}}">复制</button></li>
{{end}}</ul>
<div id="qr"></div>

{{if .Capabilities}}<h2>节点能力</h2>
<p class="muted">结果来自指定入口的代理出口。YouTube 表示连通；Netflix 表示目录检测，不能保证登录播放。过期或变化的结果显示未知。</p>
{{range .Capabilities}}<details><summary>{{.Entrance}} · {{.Target}} · {{.Status}}{{if .Region}} · {{.Region}}{{end}}</summary>
<p>{{if .ExitIP}}出口 IP:{{.ExitIP}}<br>{{end}}检测时间:{{.Checked}}<br>有效至:{{.Expires}}<br>检测位置:{{.Location}}{{if .Reason}}<br>{{.Reason}}{{end}}</p></details>{{end}}
{{end}}

<form class="rotate" method="post" action="/sub/{{.Token}}/rotate">
<button type="submit">换个订阅地址</button>
</form>
<p class="muted">换地址会让当前链接立刻失效;换完请把新地址保存好。</p>

<script src="https://cdnjs.cloudflare.com/ajax/libs/qrcodejs/1.0.0/qrcode.min.js"></script>
<script>
document.querySelectorAll('.links button').forEach(function (b) {
  b.addEventListener('click', function () {
    navigator.clipboard.writeText(b.dataset.url).then(function () {
      b.textContent = '已复制';
      setTimeout(function () { b.textContent = '复制'; }, 1500);
    });
  });
});
var first = document.querySelector('.links button');
var box = document.getElementById('qr');
if (window.QRCode && first) {
  new QRCode(box, { text: first.dataset.url, width: 160, height: 160 });
} else {
  box.remove();
}
</script>
</body>
</html>
`
