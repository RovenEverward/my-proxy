package server

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"rsc.io/qr"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/ui"
)

// htmlString 是已经拼好的 HTML 片段(二维码 SVG)。页面模板用 text/template,
// 它把 string 原样写出,所以这里只需要一个有名字的类型,不必再挂过滤器。
type htmlString = template.HTML

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
	Capabilities []subCapability
	Rotated      string
	// FontBase 是 @font-face 里字体路径的相对前缀,按当前请求路径算(见 fontBase)。
	FontBase string
	Links    []subPageLink
	// QRSVG 是 Links 第一条地址的二维码,在服务端画成内联 SVG,页面不依赖外部脚本。
	QRSVG  htmlString
	QRName string
}

// 页面模板不用 HTML 版(它按上下文自动转义 HTML 模板):模板里的 URL 写在 JS
// 字符串和 <code> 里,自动转义会把地址中的 & 改写成 &amp;,复制出去就不再是
// 原来那条地址。用户输入只有用户名,在模板里用 html 过滤器显式转义;二维码
// SVG 以 htmlString 原样写出。
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
		FontBase:   fontBase(r.URL.Path),
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

	if len(data.Links) > 0 {
		data.QRName = data.Links[0].Name
		data.QRSVG = qrSVG(data.Links[0].URL)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 页面里带着订阅地址,不让中间层缓存。
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := subPageTmpl.Execute(w, data); err != nil {
		s.logger.Error("渲染订阅页失败", "username", user.Username, "error", err)
	}
}

// fontBase 按当前页面地址算字体路径的相对前缀。
//
// 订阅页可能落在 /sub/<token>(订阅本体)或 /sub/<token>/rotate(换地址后这一次
// 渲染),而字体固定挂在 /sub/-/fonts/ 下 —— 用相对路径,只转发 /sub/ 的反向
// 代理也能取到字体,不必知道控制面挂在哪一层前缀后面。
func fontBase(path string) string {
	rest := strings.Trim(strings.TrimPrefix(path, "/sub/"), "/")
	n := 0
	for _, seg := range strings.Split(rest, "/") {
		if seg != "" {
			n++
		}
	}
	if n < 1 {
		n = 1
	}
	return strings.Repeat("../", n-1) + "-/fonts/"
}

// handleSubFont 把内置字体发给浏览器看的订阅页。名字只认白名单里的两个文件:
// 字体目录里还有字表和许可文件,它们不该从这个公网可达、无鉴权的入口发出去。
//
// 字体随二进制一起内置、内容不变,可以长缓存。订阅页可能落在 /sub/ 下的任意
// 深层(换地址后的页面是 /sub/<token>/rotate),但不管从哪一层取,发出去的都是
// 同一份文件。
func (s *Server) handleSubFont(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != "MonaSansVF.woff2" && name != "NotoSansSC-subset.woff2" {
		writeError(w, http.StatusNotFound, "字体不存在")
		return
	}
	data, err := fs.ReadFile(ui.Fonts(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, "字体不存在")
		return
	}
	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
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

// qrSVG 把一段文本画成二维码 SVG(每个模块一格,四周留 4 格静区)。编码失败
// 时返回空,页面就不显示二维码卡片,和以前脚本加载失败时一样。
func qrSVG(text string) template.HTML {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	n := code.Size
	const quiet = 4
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="订阅地址二维码"><rect width="100%%" height="100%%" fill="#fff"/><path fill="#0a0a0a" d="`, n+2*quiet, n+2*quiet)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String())
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

// 订阅页的样式与文档站一致:黑白无彩、细线卡片、8px 圆角;只有状态徽章和
// 危险操作带颜色。全部内联,字体只取同源 /sub/-/fonts/ 下的内置文件,不引外部
// 脚本和样式。
const subPageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>{{.Username | html}} 的订阅</title>
<style>
@font-face { font-family: "Mona Sans VF"; src: url({{.FontBase}}MonaSansVF.woff2) format("woff2"); font-weight: 200 900; font-display: swap; }
@font-face { font-family: "Noto Sans SC"; src: url({{.FontBase}}NotoSansSC-subset.woff2) format("woff2"); font-weight: 100 900; font-display: swap; unicode-range: U+2010-206F,U+2E80-9FFF,U+F900-FAFF,U+FE30-FE4F,U+FF00-FFEF; }
:root { color-scheme: light dark;
  --canvas:#fff; --stone:#f4f4f4; --ink:#0a0a0a; --muted:#737373; --faint:#a3a3a3; --line:rgba(0,0,0,.07);
  --card:0 0 0 1px #0000000f,0 1px 2px -1px #0000000f,0 2px 4px 0 #0000000a;
  --ok:#1a7f37; --warn:#9a6700; --bad:#c4342d; --off:#737373; --ok-bg:#1a7f3714; --warn-bg:#9a670014; --bad-bg:#c4342d12; --off-bg:#7373731a;
  --sans:"Mona Sans VF",-apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans SC","PingFang SC","Noto Sans",Helvetica,Arial,sans-serif;
  --mono:"Geist Mono",ui-monospace,"SF Mono",Menlo,Consolas,monospace; }
@media (prefers-color-scheme: dark) { :root {
  --canvas:#08090a; --stone:#16171a; --ink:#ededef; --muted:#9a9aa0; --faint:#5b5d64; --line:rgba(255,255,255,.08);
  --card:0 0 0 1px #ffffff14,0 1px 2px -1px #0000004d,0 2px 4px #00000040;
  --ok:#3fb950; --warn:#d29922; --bad:#f0625a; --off:#9a9aa0; --ok-bg:#3fb9501a; --warn-bg:#d299221a; --bad-bg:#f0625a1a; --off-bg:#9a9aa01a; } }
* { box-sizing: border-box; }
html { -webkit-text-size-adjust: 100%; }
body { margin: 0; background: var(--stone); color: var(--ink); font: 14px/1.6 var(--sans); -webkit-font-smoothing: antialiased; }
main { max-width: 640px; margin: 0 auto; padding: 32px 16px 56px; display: flex; flex-direction: column; gap: 16px; }
.eyebrow { margin: 0 0 2px; font: 11px var(--mono); letter-spacing: .04em; color: var(--faint); text-transform: uppercase; }
.card { background: var(--canvas); border-radius: 14px; box-shadow: var(--card); padding: 20px; }
.card > h2 { margin: 0 0 12px; font-size: 13px; font-weight: 600; color: var(--ink); }
.muted { color: var(--muted); font-size: 12.5px; margin: 8px 0 0; }
a { color: var(--ink); text-decoration: none; border-bottom: 1px solid var(--faint); }
code { font-family: var(--mono); }
.head { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
h1 { margin: 0; font-size: 22px; font-weight: 600; letter-spacing: -.01em; overflow-wrap: anywhere; }
.badge { display: inline-flex; align-items: center; gap: 6px; height: 24px; padding: 0 10px; border-radius: 999px; font-size: 12px; font-weight: 500; }
.badge::before { content: ""; width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.badge.active { color: var(--ok); background: var(--ok-bg); }
.badge.disabled { color: var(--off); background: var(--off-bg); }
.badge.expired { color: var(--warn); background: var(--warn-bg); }
.badge.limited { color: var(--bad); background: var(--bad-bg); }
.stats { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin-top: 18px; padding-top: 16px; border-top: 1px solid var(--line); }
.stat .k { display: block; font-size: 12px; color: var(--muted); }
.stat .v { display: block; margin-top: 2px; font-size: 15px; font-weight: 500; font-variant-numeric: tabular-nums; }
.bar { display: block; height: 6px; border-radius: 3px; background: var(--stone); overflow: hidden; margin-top: 8px; box-shadow: inset 0 0 0 1px var(--line); }
.bar > i { display: block; height: 100%; background: var(--ink); border-radius: 3px; }
.notice { margin: 0; padding: 14px 16px; border-radius: 12px; background: var(--canvas); box-shadow: inset 3px 0 0 var(--ink), var(--card); }
.notice code { display: block; margin-top: 6px; font-size: 12px; overflow-wrap: anywhere; }
.notice.alert { box-shadow: inset 3px 0 0 var(--bad), var(--card); }
button { font: inherit; font-size: 13px; font-weight: 500; height: 30px; padding: 0 12px; border-radius: 8px; cursor: pointer;
  border: 0; background: var(--stone); color: var(--ink); box-shadow: inset 0 0 0 1px var(--line); transition: transform 160ms cubic-bezier(.23,1,.32,1), background 160ms; }
button:hover { background: var(--line); }
button:active { transform: scale(.97); }
button.primary { background: var(--ink); color: var(--canvas); box-shadow: none; }
ul.links { list-style: none; margin: 0 -8px; padding: 0; }
ul.links li { display: grid; grid-template-columns: 6.5rem minmax(0,1fr) auto; align-items: center; gap: 12px; padding: 8px; border-radius: 8px; }
ul.links li + li { box-shadow: 0 -1px 0 var(--line); }
ul.links li:hover { background: var(--stone); }
ul.links .name { font-size: 13px; font-weight: 500; }
ul.links code { font-size: 12px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
button.copy { min-width: 64px; }
button.copy.done { background: var(--ink); color: var(--canvas); box-shadow: none; }
.qr { display: flex; gap: 20px; align-items: center; }
.qr svg { flex: 0 0 auto; width: 148px; height: 148px; border-radius: 8px; box-shadow: 0 0 0 1px var(--line); }
.qr p { margin: 0; }
.qr .for { font-size: 15px; font-weight: 500; }
.qr code { display: block; margin-top: 6px; font-size: 11.5px; color: var(--muted); overflow-wrap: anywhere; }
details { border-top: 1px solid var(--line); padding: 8px 0; }
details:last-child { padding-bottom: 0; }
summary { cursor: pointer; font-size: 13px; }
details p { margin: 6px 0 0; font-size: 12.5px; color: var(--muted); }
.danger { box-shadow: 0 0 0 1px var(--bad-bg), var(--card); }
.danger .row { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.danger h2 { color: var(--bad); margin: 0; font-size: 13px; }
.danger p { margin: 0; }
.danger form { margin: 0; flex: 0 0 auto; }
button.warn { color: var(--bad); background: var(--bad-bg); box-shadow: inset 0 0 0 1px var(--bad-bg); }
button.warn:hover { background: var(--bad); color: #fff; }
@media (max-width: 520px) {
  main { padding: 20px 12px 40px; gap: 12px; }
  .card { padding: 16px; }
  ul.links li { grid-template-columns: minmax(0,1fr) auto; gap: 2px 10px; }
  ul.links .name { grid-column: 1; }
  ul.links code { grid-column: 1; grid-row: 2; }
  ul.links button { grid-column: 2; grid-row: 1 / span 2; }
  .stats { grid-template-columns: 1fr; gap: 12px; }
  .qr { flex-direction: column; align-items: flex-start; }
  .danger .row { flex-direction: column; align-items: stretch; }
  .danger button { width: 100%; }
}
</style>
</head>
<body>
<main>
{{if .Rotated}}<p class="notice" role="status">订阅地址已更换,旧地址立刻失效。把新地址保存好:<code>{{.Rotated}}</code></p>
{{end}}
<section class="card">
<p class="eyebrow">MPX 订阅</p>
<div class="head"><h1>{{.Username | html}} 的订阅</h1><span class="badge {{.Status}}" aria-label="状态:{{.StatusText}}">{{.StatusText}}</span></div>
<div class="stats">
<div class="stat"><span class="k">流量</span><span class="v">{{.Usage}}</span>{{if .BarShow}}<span class="bar" title="{{.Percent}}%"><i style="width:{{.Percent}}%"></i></span>{{end}}</div>
<div class="stat"><span class="k">到期</span><span class="v">{{.ExpireText}}</span></div>
</div>
</section>

<section class="card">
<h2>分享链接</h2>
<ul class="links">
{{range .Links}}<li><span class="name">{{.Name}}</span><code title="{{.URL}}">{{.URL}}</code><button type="button" class="copy" data-url="{{.URL}}">复制</button></li>
{{end}}</ul>
</section>

{{if .QRSVG}}<section class="card qr">
{{.QRSVG}}
<div><p class="eyebrow">扫码导入</p><p class="for">「{{.QRName}}」链接</p><code>{{(index .Links 0).URL}}</code><p class="muted">用手机客户端扫描,导入的就是上面列表里的第一条。</p></div>
</section>{{end}}

{{if .Capabilities}}<section class="card">
<h2>节点能力</h2>
<p class="muted" style="margin:0 0 8px">结果来自指定入口的代理出口。YouTube 表示连通；Netflix 表示目录检测，不能保证登录播放。过期或变化的结果显示未知。</p>
{{range .Capabilities}}<details><summary>{{.Entrance}} · {{.Target}} · {{.Status}}{{if .Region}} · {{.Region}}{{end}}</summary>
<p>{{if .ExitIP}}出口 IP:{{.ExitIP}}<br>{{end}}检测时间:{{.Checked}}<br>有效至:{{.Expires}}<br>检测位置:{{.Location}}{{if .Reason}}<br>{{.Reason}}{{end}}</p></details>{{end}}
</section>{{end}}

<section class="card danger">
<div class="row">
<div><h2>危险操作</h2><p class="muted">换地址会让当前链接立刻失效;换完请把新地址保存好。</p></div>
<form class="rotate" method="post" action="/sub/{{.Token}}/rotate" onsubmit="return confirm('换地址会让当前链接立刻失效,确定要换吗?')">
<button type="submit" class="warn">换个订阅地址</button>
</form>
</div>
</section>
</main>
<script>
function mpxCopy(text) {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  return new Promise(function (ok, fail) {
    var t = document.createElement('textarea');
    t.value = text; t.setAttribute('readonly', ''); t.style.position = 'fixed'; t.style.opacity = '0';
    document.body.appendChild(t); t.select();
    try { document.execCommand('copy') ? ok() : fail(); } catch (e) { fail(e); }
    t.remove();
  });
}
document.querySelectorAll('.links button').forEach(function (b) {
  b.addEventListener('click', function () {
    var reset = function () { b.textContent = '复制'; b.classList.remove('done'); };
    mpxCopy(b.dataset.url).then(function () {
      b.textContent = '已复制'; b.classList.add('done');
      setTimeout(reset, 1500);
    }, function () {
      b.textContent = '复制失败';
      setTimeout(reset, 1500);
    });
  });
});
</script>
</body>
</html>
`
