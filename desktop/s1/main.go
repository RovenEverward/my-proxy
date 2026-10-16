// Command s1 是桌面端的 S1 冒烟:用 mygo 开一个 WebView 载入 mpx ui 的登录链接,
// 等页面进入登录后的主界面,截一张 PNG,然后退出。
//
//	s1 <url> <截图输出.png>
//
// 退出码:0 = 截到图;1 = 60 秒内没看到主界面(仍尽力截图);2 = 用法错或系统
// WebView 不可用。
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/egoist/mygo"
)

const (
	// probeTimeout 是等主界面出现的上限。
	probeTimeout = 60 * time.Second
	// settleDelay 是看到主界面后、截图前留的渲染时间。
	settleDelay = 2 * time.Second
	// probeEvery 是两次探测之间的间隔。
	probeEvery = time.Second
)

// mainSelector 只有登录后的主界面才有:#root 里渲染出的 .side(侧边栏)。
// 登录页(internal/ui/ui.go 的 loginPage)与安装向导都没有它。拿到非空结果
// 说明主界面已经渲染,document.title 就是标题。
const mainSelector = `document.querySelector("#nav, nav, .side") ? document.title : ""`

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "用法: s1 <url> <截图输出.png>")
		os.Exit(2)
	}
	if !mygo.WebViewAvailable() {
		fmt.Fprintln(os.Stderr, "s1: 系统 WebView 不可用(WKWebView / WebKitGTK / WebView2 Runtime 缺失),无法渲染页面")
		os.Exit(2)
	}
	url, out := os.Args[1], os.Args[2]

	// smoke goroutine 在退出前把退出码发到这里;App.Run 返回后 main 取用。
	done := make(chan int, 1)
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:  "MPX S1",
			URL:    url,
			Width:  1280,
			Height: 800,
		})
		go smoke(win, out, done)
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "s1: 事件循环退出:", err)
		os.Exit(1)
	}
	select {
	case code := <-done:
		os.Exit(code)
	default:
		os.Exit(1)
	}
}

// smoke 轮询页面直到主界面出现,截图写 png,把退出码发给 done,然后让应用退出。
func smoke(win *mygo.Window, out string, done chan<- int) {
	deadline := time.Now().Add(probeTimeout)
	var last any
	var lastErr error
	for {
		v, err := win.Page().Eval(mainSelector)
		last, lastErr = v, err
		if err == nil {
			if title, ok := v.(string); ok && title != "" {
				time.Sleep(settleDelay)
				if saveShot(win, out) {
					fmt.Printf("S1_OK title=%s url=%s\n", title, win.Page().URL())
					done <- 0
					mygo.App.Quit()
					return
				}
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(probeEvery)
	}
	// 超时:仍尽力截一张,方便排查。
	saveShot(win, out)
	fmt.Printf("S1_FAIL last=%v err=%v\n", last, lastErr)
	done <- 1
	mygo.App.Quit()
}

// saveShot 截当前页面并写到 out,报告是否成功。
func saveShot(win *mygo.Window, out string) bool {
	png, err := win.CapturePage()
	if err != nil {
		fmt.Fprintln(os.Stderr, "s1: 截图失败:", err)
		return false
	}
	if err := os.WriteFile(out, png, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "s1: 写截图失败:", err)
		return false
	}
	return true
}
