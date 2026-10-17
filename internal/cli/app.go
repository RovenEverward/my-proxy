package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/spf13/cobra"
)

// appSmokeEnv 让 mpx app 在打开窗口后自动检查页面、截图并退出,给 CI 和本机
// 穿刺用。值是截图的输出路径。
const appSmokeEnv = "MPX_APP_SMOKE"

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

它和 mpx ui 用同一份配置档、同一个本地转发层、同一条一次性登录链接,区别只是
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
		l, err := startUI(appFlags.addr, false)
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
			win := mygo.NewWindow(mygo.WindowOptions{
				Title:  "MPX",
				URL:    link,
				Width:  1280,
				Height: 820,
			})
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
				time.Sleep(appSmokeSettle)
				if saveAppShot(win, out) {
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
	saveAppShot(win, out)
	fmt.Printf("APP_SMOKE_FAIL last=%v err=%v\n", last, lastErr)
	mygo.App.Exit(1)
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
