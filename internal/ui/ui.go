// Package ui 是 mpx ui 的本地页面:操作机上一个只听回环地址的小服务,把内嵌的
// 页面交给浏览器,把页面发来的 /api 请求带上 CLI 的 token 转给控制面。
//
// 它不另开一套能力:页面能做的,就是控制面 /api 本来能做的;token 只在这个进程的
// 内存里,不下发到页面。设计与取舍见 docs/internal/ui.md。
package ui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
)

//go:embed assets
var assets embed.FS

// CSP 只放行同源脚本与样式。行内 style 属性(进度条宽度这类)单独放行;
// 行内脚本、eval、外部资源一律不行。
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Options 是一个页面服务的全部输入。
type Options struct {
	Upstream *url.URL // 控制面地址,如 https://cp.example.net
	Token    string   // 管理 token,只用在转发出去的 Authorization 上
	// Allow 是转发层放行的请求,每条形如 "PUT /api/users":方法相同、路径等于
	// 它或在它下面才放行。没列出的一律 405,不发到控制面。页面能做的事以此为界。
	Allow []string
	// Profile 只用于页面上显示"连的是哪个控制面"。
	Profile string
	// Transport 是转发到控制面用的传输;https 控制面要用只认证书指纹的那种
	// (见 internal/pin)。为空时用默认传输。
	Transport http.RoundTripper
	// Setup 非空时启用 /local/setup/*;Upstream 为空时页面进入首次安装模式。
	Setup SetupRunner
	// Connect 非空时启用 /local/setup/connect*(setup 模式下接入已有控制面,等价
	// mpx connect);再实现 ConnectNamedRunner 时,/local/profiles/connect* 也开
	// (正常模式下添加新控制面)。为 nil 时这些接口都 404。
	Connect ConnectRunner
	// Profiles 非空时启用 /local/profiles(列出)、/local/profiles/use(切换)与
	// /local/profiles/connect*(添加控制面,还要 Connect 在)。为 nil 时 404。
	Profiles ProfileSource
	// Relabel 非空时启用 /local/relabel/plan 与 /local/relabel/apply。为 nil 时 404。
	Relabel RelabelRunner
	// Session 非空时沿用这个会话值,而不是新发一个:一键更新重启 mpx ui 时的交接
	// 用(见 restart.go),让浏览器的 cookie 不作废。为空时行为与从前完全一样。
	Session string
	// CLIUpdated 非空时,本机 CLI 的升级尝试(换掉了二进制,或下载/替换失败)结束
	// 后调用一次。ui 包不能引用 cli 包(cli 依赖 ui),update.json 由 cli 侧在这个
	// 回调里写;为 nil 时什么也不记。
	CLIUpdated func(from, to string, started time.Time, err error)
}

// Server 是一个已经绑定好端口的页面服务。
type Server struct {
	opts    Options
	port    string
	addr    string // 监听地址(host:port),重启交接时交给新进程
	key     string // 一次性登录 key,用掉即清空
	session string // cookie 里的值
	mu      sync.Mutex
	proxy   *httputil.ReverseProxy
	static  http.Handler
	allow   []rule
	job     *setupJob
	all     updateAllState
}

type rule struct{ method, prefix string }

func (s *Server) allowed(method, path string) bool {
	for _, r := range s.allow {
		if r.method == method && (path == r.prefix || strings.HasPrefix(path, strings.TrimSuffix(r.prefix, "/")+"/")) {
			return true
		}
	}
	return false
}

// New 为监听在 addr(已经解析出的 host:port)上的服务生成 key 与会话。
func New(addr string, opts Options) (*Server, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if opts.Upstream == nil || opts.Upstream.Host == "" {
		if opts.Setup == nil && opts.Profiles == nil {
			return nil, errors.New("没有控制面地址")
		}
	}
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return nil, err
	}
	session := randomToken()
	if opts.Session != "" {
		// 重启交接:旧进程把这个值发进了浏览器 cookie,新进程认同一个会话。
		session = opts.Session
	}
	s := &Server{opts: opts, port: port, addr: addr, key: randomToken(), session: session,
		static: http.FileServer(http.FS(sub))}
	for _, a := range opts.Allow {
		m, p, ok := strings.Cut(a, " ")
		if !ok || !strings.HasPrefix(p, "/api/") && p != "/api" {
			return nil, errors.New("放行规则要写成 \"METHOD /api/...\": " + a)
		}
		s.allow = append(s.allow, rule{m, p})
	}
	if opts.Upstream != nil && opts.Upstream.Host != "" {
		s.setUpstream(opts.Upstream, opts.Token, opts.Transport)
	}
	return s, nil
}

// setUpstream 换(或第一次装)控制面:更新 opts 与转发代理。装完切正常模式时调用。
func (s *Server) setUpstream(up *url.URL, token string, tr http.RoundTripper) {
	proxy := &httputil.ReverseProxy{
		Transport: tr,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(up)
			r.Out.Host = up.Host
			// 浏览器带来的凭据与来源头一概不往外送;控制面只认 Bearer。
			for _, h := range []string{"Cookie", "Authorization", "Origin", "Referer"} {
				r.Out.Header.Del(h)
			}
			if token != "" {
				r.Out.Header.Set("Authorization", "Bearer "+token)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			resp.Header.Set("Cache-Control", "no-store")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadGateway, "连不上控制面 "+up.Redacted()+": "+err.Error())
		},
	}
	s.mu.Lock()
	s.proxy = proxy
	s.opts.Upstream = up
	s.opts.Token = token
	s.opts.Transport = tr
	s.mu.Unlock()
}

// Key 是登录链接里那一次性的 key;用掉之后返回空串。
func (s *Server) Key() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.key
}

func (s *Server) cookieName() string { return "mpx_ui_" + s.port }

// ServeHTTP 依次做:Host 校验 → 登录 key 换 cookie → 会话校验 → 来源校验 → 分发。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")

	// DNS rebinding:别的域名解析到 127.0.0.1 时,Host 头是那个域名。
	if !s.hostOK(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, "只接受 127.0.0.1 / localhost 上的访问")
		return
	}
	if k := r.URL.Query().Get("k"); k != "" && r.URL.Path == "/" {
		s.login(w, r, k)
		return
	}
	// 国旗来自内嵌 zip,不碰控制面、也不需要会话:首次安装向导和登录转场里也会
	// 先画旗帜。名字限制在 /flags/<xx>.svg,其余一律 404。
	if strings.HasPrefix(r.URL.Path, "/flags/") {
		s.serveFlag(w, r)
		return
	}
	if !s.sessionOK(r) {
		if r.URL.Path == "/" {
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(loginPage))
			return
		}
		writeError(w, http.StatusUnauthorized, sessionInvalidMsg())
		return
	}
	if !originOK(r) {
		writeError(w, http.StatusForbidden, "拒绝跨站请求")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/local/setup") {
		s.serveSetup(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/local/update") {
		s.serveUpdate(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/local/profiles") {
		s.serveProfiles(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/local/relabel") {
		s.serveRelabel(w, r)
		return
	}
	if r.URL.Path == "/local/qr.svg" {
		s.serveQR(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.mu.Lock()
		proxy := s.proxy
		s.mu.Unlock()
		if proxy == nil {
			writeError(w, http.StatusServiceUnavailable, "还没有控制面:先完成首次安装")
			return
		}
		if !s.allowed(r.Method, r.URL.Path) {
			writeError(w, http.StatusMethodNotAllowed, "管理界面不放行 "+r.Method+" "+r.URL.Path)
			return
		}
		proxy.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/ui/meta" {
		s.mu.Lock()
		proxy := s.proxy
		up := s.opts.Upstream
		profile := s.opts.Profile
		s.mu.Unlock()
		mode := "normal"
		server := ""
		if proxy == nil {
			mode = "setup"
		} else if up != nil {
			server = up.Redacted()
		}
		writeJSON(w, map[string]any{
			"server":      server,
			"profile":     profile,
			"allow":       s.opts.Allow,
			"setup":       s.opts.Setup != nil,
			"profiles":    s.opts.Profiles != nil,
			"relabel":     s.opts.Relabel != nil,
			"mode":        mode,
			"cli_version": buildinfo.Version(),
			// fingerprint 是当前控制面记下的控制面证书指纹,http 控制面为空;
			// connect 报告能不能在页面里接入新控制面。都是只加的字段,旧页面忽略。
			"fingerprint": s.currentFingerprint(),
			"connect":     s.opts.Connect != nil,
		})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "静态资源只能 GET")
		return
	}
	h.Set("Cache-Control", "no-cache")
	s.static.ServeHTTP(w, r)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request, k string) {
	s.mu.Lock()
	ok := s.key != "" && subtle.ConstantTimeCompare([]byte(k), []byte(s.key)) == 1
	if ok {
		s.key = "" // 一次性:链接被转发、进了历史记录也不能再用
	}
	s.mu.Unlock()
	if !ok {
		if s.sessionOK(r) { // 已登录的浏览器再点一次旧链接,别把人挡在外面
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusUnauthorized, loginLinkInvalidMsg())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: s.session, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) sessionOK(r *http.Request) bool {
	c, err := r.Cookie(s.cookieName())
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.session)) == 1
}

func (s *Server) hostOK(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil || p != s.port {
		return false
	}
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

// originOK:写请求必须带同源的 Origin;读请求带了 Origin 也得是同源。
// Sec-Fetch-Site 能给出时,只认 same-origin 和用户直接打开(none)。
func originOK(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	u, err := url.Parse(o)
	return err == nil && u.Scheme == "http" && u.Host == r.Host
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

const loginPage = `<!doctype html><meta charset="utf-8"><title>MPX</title>
<body style="font:14px/1.6 'PingFang SC','Microsoft YaHei UI','Microsoft YaHei','Noto Sans CJK SC',system-ui,sans-serif;margin:15vh auto;max-width:420px;padding:0 20px;color:#3f3f46">
<h1 style="font-size:18px;color:#0a0a0a">需要重新打开</h1>
<p>这个页面的会话已失效。在终端运行 <code>mpx ui</code>,用它打印的链接打开。</p></body>`

// 会话 / 登录链接失效时的提示:桌面窗口里没有「重新运行 mpx ui」这回事,
// 说界面上能做的;浏览器版 mpx ui 才提命令。
func sessionInvalidMsg() string {
	if updateDesktop() {
		return "这个窗口的会话已失效:关掉 MPX 再重新打开"
	}
	return "这个页面的会话无效:在终端重新运行 mpx ui,用它打印的链接打开"
}

func loginLinkInvalidMsg() string {
	if updateDesktop() {
		return "登录已失效:关掉 MPX 再重新打开"
	}
	return "登录链接无效或已经用过:重新运行 mpx ui 拿一条新的"
}
