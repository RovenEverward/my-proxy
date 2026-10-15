package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/ui"
)

// uiAllow 是 mpx ui 的转发层放行的请求。页面能做的事以此为界:每一条写操作都
// 对应一条已有的 CLI 命令(见 internal/ui/e2e 里逐条对照的测试)。
var uiAllow = []string{
	"GET /api",
	// 用户:add / edit / enable / disable / renew(都是 PUT 整体回写)/ rm /
	// reset-traffic / revoke-sub
	"POST /api/users",
	"PUT /api/users",
	"DELETE /api/users",
	// 节点:add / edit / rm / sync / install / drain / undrain / diagnose / locate;metadata set
	"POST /api/nodes",
	"PUT /api/nodes",
	"DELETE /api/nodes",
	// 入站与入口:inbound add / edit / rm,host add(POST /api/inbounds/{id}/hosts)/ edit / rm
	"POST /api/inbounds",
	"PUT /api/inbounds",
	"DELETE /api/inbounds",
	"PUT /api/hosts",
	"DELETE /api/hosts",
	// 设置:settings set / rules add|rm|move|reset(都是整体 PUT)、template set / del、test
	"PUT /api/settings",
	"DELETE /api/settings/templates",
	"POST /api/settings/test-webhook",
	// 用途:purpose create / set / del;metadata set 走上面的 PUT /api/nodes、/api/hosts
	"POST /api/purposes",
	"PUT /api/purposes",
	"DELETE /api/purposes",
}

var uiFlags struct {
	addr   string
	noOpen bool
}

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "在本机浏览器里打开管理界面",
	Long: `在本机起一个只听回环地址的页面服务,用浏览器看控制面。

页面是 CLI 的另一种界面:它和其他命令用同一个配置档(--server / --token /
--profile),请求经本进程带上 token 转给控制面的 /api。token 不会交给浏览器。

打印出来的链接只能用一次:浏览器用它换一个会话 cookie,之后链接作废。
关掉这个命令,页面随之失效。要在别的设备上看,用 SSH 隧道转发这个端口。`,
	Example: `  mpx ui
  mpx ui --profile prod --addr 127.0.0.1:7480 --no-open`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		server, token, err := resolve()
		if err != nil {
			return err
		}
		if server == "" {
			return errors.New("没配置控制面地址:先跑 mpx connect <控制面地址>")
		}
		up, err := url.Parse(server)
		if err != nil || (up.Scheme != "http" && up.Scheme != "https") || up.Host == "" {
			return fmt.Errorf("控制面地址 %q 不是 http(s) URL", server)
		}
		if !isLoopback(uiFlags.addr) {
			return fmt.Errorf("mpx ui 只监听回环地址,%s 不是;要在别的设备上看,用 SSH 隧道", uiFlags.addr)
		}
		ln, err := net.Listen("tcp", uiFlags.addr)
		if err != nil {
			return err
		}
		profile := firstNonEmpty(opts.profile, os.Getenv("MPX_PROFILE"))
		if profile == "" {
			if cfg, err := loadConfig(); err == nil {
				profile = cfg.Current
			}
		}
		tr, err := transportFor(server)
		if err != nil {
			ln.Close()
			return err
		}
		s, err := ui.New(ln.Addr().String(), ui.Options{Upstream: up, Token: token, Allow: uiAllow, Profile: profile, Transport: tr})
		if err != nil {
			ln.Close()
			return err
		}
		link := "http://" + ln.Addr().String() + "/?k=" + s.Key()
		fmt.Printf("mpx ui 已启动,连的是 %s\n", up.Redacted())
		fmt.Println(link)
		fmt.Println("这条链接只能打开一次;Ctrl-C 结束。")
		if !uiFlags.noOpen {
			openBrowser(link)
		}

		srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = srv.Shutdown(shut)
		}()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	},
}

// openBrowser 尽力在系统浏览器里打开 link;打不开就算了,链接已经打印出来。
func openBrowser(link string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", link)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		c = exec.Command("xdg-open", link)
	}
	_ = c.Start()
}

func init() {
	uiCmd.Flags().StringVar(&uiFlags.addr, "addr", "127.0.0.1:7480", "页面监听的回环地址;端口写 0 表示随便挑一个")
	uiCmd.Flags().BoolVar(&uiFlags.noOpen, "no-open", false, "只打印链接,不自动打开浏览器")
	rootCmd.AddCommand(uiCmd)
}
