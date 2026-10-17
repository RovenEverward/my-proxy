package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/spf13/cobra"
)

// appSmokeEnv 让 mpx app 在打开窗口后自动检查页面、截图并退出,给 CI 和本机
// 穿刺用。值是截图的输出路径。
const appSmokeEnv = "MPX_APP_SMOKE"

// appSmokeRouteEnv 让冒烟在进入主界面后再跳到某一页(如 nodes)才截图。跳到节点页时
// 还要等到内置 SVG 国旗真的画出来(页面上有 🇯🇵 这类旗帜换成的 img.flag),证明
// Windows 上国旗能显示。
const appSmokeRouteEnv = "MPX_APP_SMOKE_ROUTE"

// appSmokeThemeEnv 让冒烟在截图前切到指定主题并等一秒:light 或 dark。
// mygo 的页面主题与窗口底色一起换,Windows 的标题栏截图才能同时看到两套配色。
const appSmokeThemeEnv = "MPX_APP_SMOKE_THEME"

// appBackgroundColor 是窗口在页面绘制前先填的底色,避免打开时闪一下别的颜色;
// 浅/深两套跟页面 --base 一起换。
const appBackgroundColor = "light-dark(#f6f5f2, #161615)"

// appSmokeRouteSelector:目标页标题已出现、且至少一张 SVG 国旗已经加载完成
// (complete 且 naturalWidth > 0)时返回非空。只看 DOM 不够:图片可能还在加载。
const appSmokeRouteSelector = `(() => { if (!document.querySelector(".ph h1")) return "";
  const f = [...document.querySelectorAll("img.flag")].find(i => i.complete && i.naturalWidth > 0);
  return f ? document.title + " flagsvg=loaded" : ""; })()`

// appNoWebViewExit 是系统缺 WebView 时的退出码:2 与用法错分开,脚本能一眼
// 认出是环境缺件而不是参数写错。
const appNoWebViewExit = 2

// errAppNoWebView 只是把「缺 WebView」这条路的退出码带到 Execute;提示已经
// 由 reportNoWebView 打过了,不再重复打印。
var errAppNoWebView = errors.New("系统 WebView 不可用")

// 冒烟的时间参数,与 desktop/s1 保持一致:每秒探一次,看到目标页面后留两秒
// 渲染时间再截图,最多等 60 秒。
var (
	appSmokeEvery   = time.Second
	appSmokeSettle  = 2 * time.Second
	appSmokeTimeout = 60 * time.Second
)

// appSmokeSelector 在页面进入主界面或安装向导时返回非空字符串。
//
// 主界面有 .side(侧边栏)和 nav;首次安装向导由 setup.js 在 body 下挂一个
// .setup,此时主界面还没渲染。两者都算"页面已经能看了",冒烟只关心这一点。
const appSmokeSelector = `document.querySelector(".setup, #nav, nav, .side") ? document.title : ""`

var appFlags struct {
	addr string
}

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "打开桌面窗口(和 mpx ui 同一套页面)",
	Long: `在本机开一个桌面窗口,里面就是 mpx ui 的页面。

它和 mpx ui 用同一份配置、同一个本地转发层、同一条一次性登录链接,区别只是
把页面放进系统 WebView 的窗口里,不打开浏览器。窗口关掉,本地服务随之停止。

桌面版安装包不带参数启动时自动进入这个命令。没有图形环境、或系统缺 WebView
时,改用 mpx ui 在浏览器里打开。

Linux 需要 WebKitGTK 4.1(Debian/Ubuntu 装 libwebkit2gtk-4.1-0,Fedora 装
webkit2gtk4.1);Windows 需要 WebView2 Runtime。`,
	Example: `  mpx app
  mpx app --addr 127.0.0.1:0`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !mygo.WebViewAvailable() {
			reportNoWebView()
			return errAppNoWebView
		}
		l, err := startUI(appFlags.addr, false, appSetTheme)
		if err != nil {
			return err
		}
		link := "http://" + l.listener.Addr().String() + "/?k=" + l.server.Key()

		srv := &http.Server{Handler: l.server, ReadHeaderTimeout: 10 * time.Second}
		serveErr := make(chan error, 1)
		go func() {
			err := srv.Serve(l.listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- err
				// 页面服务已经不可用,窗口留着也没有意义。
				mygo.App.Quit()
				return
			}
			serveErr <- nil
		}()

		mygo.App.WhenReady(func() {
			win := mygo.NewWindow(appWindowOptions(runtime.GOOS, link))
			if out := os.Getenv(appSmokeEnv); out != "" {
				go runAppSmoke(win, out)
			}
		})
		if err := mygo.App.Run(); err != nil {
			shutdownApp(srv)
			return fmt.Errorf("桌面窗口退出: %w", err)
		}
		shutdownApp(srv)

		// Serve 在窗口关掉后可能还差一拍才返回;给它一个短窗口,拿真正的服务错误。
		select {
		case err := <-serveErr:
			return err
		case <-time.After(3 * time.Second):
			return nil
		}
	},
}

// appWindowOptions 把桌面窗口的选项拼成一个纯函数,方便按平台做测试。
//
// Windows 与 macOS 让页面一直铺到窗口顶部:标题栏由 mygo 的隐藏样式保留
// 最小化/最大化/关闭(Windows 是 MyGo 自绘的 Win11 风格按钮,支持 Snap 布局;
// macOS 是左上角的红绿灯),高度和页面 .top 顶栏一样是 52。Linux 保持原生
// 标题栏,不设 TitleBarStyle。
func appWindowOptions(goos, link string) mygo.WindowOptions {
	o := mygo.WindowOptions{
		Title:           "MPX",
		URL:             link,
		Width:           1280,
		Height:          820,
		BackgroundColor: appBackgroundColor,
	}
	if goos != "linux" {
		o.TitleBarStyle = mygo.TitleBarHidden
		o.TitleBarHeight = 52
	}
	return o
}

// appSetTheme 把页面报来的主题交给 mygo:light/dark 强制对应外观,其余
// (system)跟随系统。页面在浏览器模式里没有这个接口,但回调照样是安全的。
func appSetTheme(theme string) error {
	switch theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
	return nil
}

// reportNoWebView 说清楚缺什么、怎么补,以及还能用什么。
//
// Windows 的 -H windowsgui 构建没有控制台,只往 stderr 写等于没说,所以在
// Windows 上再弹一个系统对话框。对话框必须在 App.Run 之后才有,这里就地起
// 一次事件循环,用户点掉后退出。
func reportNoWebView() {
	hint := "mpx app: 系统 WebView 不可用,打不开桌面窗口。\n" +
		"Linux 需要 WebKitGTK 4.1:Debian/Ubuntu 装 libwebkit2gtk-4.1-0,Fedora 装 webkit2gtk4.1;\n" +
		"Windows 需要 WebView2 Runtime。\n" +
		"也可以改用 mpx ui,在浏览器里打开管理界面。"
	fmt.Fprintln(os.Stderr, hint)
	if runtime.GOOS != "windows" {
		return
	}
	mygo.App.WhenReady(func() {
		mygo.Dialog.Error("MPX 无法启动", hint)
		mygo.App.Quit()
	})
	_ = mygo.App.Run()
}

// shutdownApp 在窗口全部关闭后停掉本地页面服务:先不再接新请求,再给手上的
// 请求几秒钟收尾。
func shutdownApp(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// runAppSmoke 是 MPX_APP_SMOKE 的实现:轮询页面直到主界面或安装向导出现,
// 等页面稳定后截图,打印一行结果并退出。
func runAppSmoke(win *mygo.Window, out string) {
	deadline := time.Now().Add(appSmokeTimeout)
	var last any
	var lastErr error
	for {
		v, err := win.Page().Eval(appSmokeSelector)
		last, lastErr = v, err
		if err == nil {
			if state, ok := v.(string); ok && state != "" {
				if route := os.Getenv(appSmokeRouteEnv); route != "" {
					state = smokeRoute(win, route, deadline)
					if state == "" {
						break
					}
				}
				applySmokeTheme(win)
				time.Sleep(appSmokeSettle)
				if saveAppShot(win, out) {
					saveAppWindowShot(win, out)
					fmt.Printf("APP_SMOKE_OK %s\n", state)
					mygo.App.Exit(0)
					return
				}
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(appSmokeEvery)
	}
	// 超时:仍尽力截一张,方便排查是白屏、登录页还是别的。
	if saveAppShot(win, out) {
		saveAppWindowShot(win, out)
	}
	fmt.Printf("APP_SMOKE_FAIL last=%v err=%v\n", last, lastErr)
	mygo.App.Exit(1)
}

// appSmokeUpdateJS 用示例数据打开更新弹窗(冒烟专用,不连 GitHub):Windows 上要截图
// 确认「可升级」徽章、要点列表这些文字没有掉成宋体。
const appSmokeUpdateJS = `(() => {
  const part = (cur, lat) => ({ current: cur, latest: lat, behind: 1, available: true, release_url: "https://github.com/RovenEverward/my-proxy/releases/tag/" + lat, error: "" });
  UPDATE = { desktop: true, cli: part("v0.9.4", "v0.9.5"), control: part("v0.9.4", "v0.9.5"),
    nodes: [{ name: "jp-1", display: "\u{1F1EF}\u{1F1F5} 东京 A", core: "xray", current: "26.3.27", target: "26.4.1", upgrade: true },
            { name: "us-1", display: "\u{1F1FA}\u{1F1F8} 洛杉矶 A", core: "xray", current: "26.4.1", target: "26.4.1", upgrade: false }],
    notes: "## 更新要点\n\n- 首次安装向导不再问内部的节点名\n- 总览的用时改成中文单位\n- 端口被占用时提前说清楚是哪个程序,并换一个空闲端口\n\n## 我该下哪个\n\n| a | b |\n| --- | --- |\n" };
  openUpdateModal();
  return "";
})()`

// appSmokeFontJS 报出几处中文文字实际用的字体栈,写进冒烟日志。
const appSmokeFontJS = `(() => { const f = s => { const e = document.querySelector(s); return e ? getComputedStyle(e).fontFamily : "-"; };
  return "tagfont=" + f("#modal .tag") + " | bodyfont=" + f("body"); })()`

// smokeRoute 跳到 #route 并等到 appSmokeRouteSelector 成立;超时返回空串。
// route 为 update 时停在总览,再用示例数据打开更新弹窗。
func smokeRoute(win *mygo.Window, route string, deadline time.Time) string {
	hash := route
	if route == "update" {
		hash = "overview"
	}
	b, _ := json.Marshal("#" + hash)
	_, _ = win.Page().Eval("location.hash = " + string(b) + `; ""`)
	for time.Now().Before(deadline) {
		if v, err := win.Page().Eval(appSmokeRouteSelector); err == nil {
			if st, ok := v.(string); ok && st != "" {
				if route != "update" {
					return st
				}
				_, _ = win.Page().Eval(appSmokeUpdateJS)
				for time.Now().Before(deadline) {
					if v, err := win.Page().Eval(`document.querySelector("#modal.open .upd-hl li") ? "1" : ""`); err == nil && v == "1" {
						font, _ := win.Page().Eval(appSmokeFontJS)
						return fmt.Sprintf("%s modal=open %v", st, font)
					}
					time.Sleep(appSmokeEvery)
				}
				return ""
			}
		}
		time.Sleep(appSmokeEvery)
	}
	return ""
}

// applySmokeTheme 在截图前把页面和 mygo 的外观一起切到 MPX_APP_SMOKE_THEME,
// 然后留一秒让页面重绘。没设或值不对时什么也不做。
func applySmokeTheme(win *mygo.Window) {
	theme := os.Getenv(appSmokeThemeEnv)
	if theme != "light" && theme != "dark" {
		return
	}
	q, _ := json.Marshal(theme)
	_, _ = win.Page().Eval(fmt.Sprintf(`(() => { try { localStorage.setItem("mpx-theme", %s); } catch {} document.documentElement.dataset.theme = %s; return ""; })()`, q, q))
	switch theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	}
	time.Sleep(time.Second)
}

// saveAppWindowShot 在 Windows 上额外抓整个窗口的屏幕像素(含 MyGo 自绘的
// 标题栏按钮),写到 out 的 -window.png。CapturePage 只有页面,不含这些按钮。
// 失败只打警告:窗口截图是辅助证据,不影响冒烟结果。
func saveAppWindowShot(win *mygo.Window, out string) {
	if runtime.GOOS != "windows" {
		return
	}
	// CI 的 Windows 桌面上 runner 的控制台窗口常盖在前面,窗口也不一定在屏幕
	// 左上角(位置与缩放换算会错开)。先挪到 (0,0)、临时置顶并聚焦再抓,抓完取消置顶。
	win.SetPosition(0, 0)
	win.SetAlwaysOnTop(true)
	win.Focus()
	defer win.SetAlwaysOnTop(false)
	time.Sleep(800 * time.Millisecond)
	b := win.Bounds()
	if b.Width <= 0 || b.Height <= 0 {
		fmt.Fprintln(os.Stderr, "mpx app: 跳过窗口截图:窗口大小无效", b)
		return
	}
	scale := 1.0
	for _, d := range mygo.Screen.Displays() {
		wa := d.WorkArea
		cx, cy := b.X+b.Width/2, b.Y+b.Height/2
		if cx >= wa.X && cx < wa.X+wa.Width && cy >= wa.Y && cy < wa.Y+wa.Height {
			if d.ScaleFactor > 0 {
				scale = d.ScaleFactor
			}
			break
		}
	}
	px := func(v int) int { return int(float64(v)*scale + 0.5) }
	x, y, w, h := px(b.X), px(b.Y), px(b.Width), px(b.Height)
	path := strings.TrimSuffix(out, ".png") + "-window.png"
	script := fmt.Sprintf(`Add-Type -AssemblyName System.Drawing
$bmp = [System.Drawing.Bitmap]::new(%d, %d)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen(%d, %d, 0, 0, $bmp.Size)
$bmp.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose()
$bmp.Dispose()`, w, h, x, y, strings.ReplaceAll(path, "'", "''"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if msg, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "mpx app: 窗口截图失败(不影响冒烟): %v %s\n", err, strings.TrimSpace(string(msg)))
	}
}

// saveAppShot 截当前窗口并写到 out,报告是否成功。
func saveAppShot(win *mygo.Window, out string) bool {
	png, err := win.CapturePage()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mpx app: 截图失败:", err)
		return false
	}
	if err := os.WriteFile(out, png, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "mpx app: 写截图失败:", err)
		return false
	}
	return true
}

func init() {
	appCmd.Flags().StringVar(&appFlags.addr, "addr", "127.0.0.1:0", "页面监听的回环地址;端口写 0 表示随便挑一个")
	rootCmd.AddCommand(appCmd)
}
