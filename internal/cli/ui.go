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
	// 远程升级控制面(mpx update apply control 的对应接口)
	"POST /api/update/control",
	// 用户:add / edit / enable / disable / renew(都是 PUT 整体回写)/ rm /
	// reset-traffic / revoke-sub
	"POST /api/users",
	"PUT /api/users",
	"DELETE /api/users",
	// 套餐:add(POST /api/plans)/ edit(整体 PUT)/ rm(有用户时带 ?reassign=<id>)
	"POST /api/plans",
	"PUT /api/plans",
	"DELETE /api/plans",
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
	// 设备:device revoke;device password / --disable(设置页「设备密码」一节)
	"DELETE /api/devices",
	"PUT /api/device-password",
	"DELETE /api/device-password",
	// 分流规则:rule add / move / toggle / rm(整表 PUT 与 PATCH)/ final / test / migrate;
	// group add / edit / rm / preview
	"PUT /api/rules",
	"PATCH /api/rules",
	"POST /api/rules/test",
	"POST /api/proxy-groups",
	"PUT /api/proxy-groups",
	"DELETE /api/proxy-groups",
	"POST /api/routing/migrate",
}

var uiFlags struct {
	addr   string
	noOpen bool
	setup  bool
}

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "在本机浏览器里打开管理界面",
	Long: `在本机起一个只听回环地址的页面服务,用浏览器看控制面。

页面是 CLI 的另一种界面:它和其他命令用同一份配置(--server / --token /
--profile),请求经本进程带上 token 转给控制面的 /api。token 不会交给浏览器。

打印出来的链接只能用一次:浏览器用它换一个会话 cookie,之后链接作废。
关掉这个命令,页面随之失效。要在别的设备上看,用 SSH 隧道转发这个端口。`,
	Example: `  mpx ui
  mpx ui --profile prod --addr 127.0.0.1:7480 --no-open`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		l, err := startUI(uiFlags.addr, uiFlags.setup)
		if err != nil {
			return err
		}
		if l.restarted {
			// 页面还开着,老会话继续用:不再发一次性链接、不再开新标签。
			fmt.Printf("mpx ui 已用新版本重启: http://%s/\n", l.listener.Addr().String())
		} else {
			link := "http://" + l.listener.Addr().String() + "/?k=" + l.server.Key()
			switch {
			case uiFlags.setup:
				fmt.Println("首次安装模式")
			case l.setupMode:
				fmt.Println("还没有配置控制面:在打开的页面里完成首次安装")
			default:
				fmt.Printf("mpx ui 已启动,连的是 %s\n", l.pageOpts.Upstream.Redacted())
			}
			fmt.Println(link)
			fmt.Println("这条链接只能打开一次;Ctrl-C 结束。")
			if !uiFlags.noOpen {
				openBrowser(link)
			}
		}
		return serveUI(cmd.Context(), l)
	},
}

// uiLaunch 是 mpx ui 与 mpx app 共用的启动结果:页面服务、绑好的监听、页面
// 选项,以及启动时是从哪儿接续来的。
type uiLaunch struct {
	server    *ui.Server
	listener  net.Listener
	pageOpts  ui.Options
	setupMode bool
	restarted bool
}

// startUI 完成两种界面共用的启动:读重启交接、解析控制面、绑回环端口、建页面
// 服务。它不打印、不开浏览器,也不决定谁来 Serve —— mpx ui 交给浏览器,mpx app
// 交给窗口,各自的收尾不同。
func startUI(addr string, forceSetup bool) (*uiLaunch, error) {
	// 一键更新的重启会把监听地址与会话放进交接文件,用 MPX_UI_RESTART 指过来:
	// 接着上次的地址与会话跑,浏览器里已打开的页面不用重新登录。
	session, restarted := "", false
	if p := os.Getenv(ui.RestartEnv); p != "" {
		st, rerr := ui.ReadRestartState(p)
		if rerr != nil {
			return nil, fmt.Errorf("接续升级前的 mpx ui 失败: %w", rerr)
		}
		_ = os.Unsetenv(ui.RestartEnv)
		addr, session, restarted = st.Addr, st.Session, true
	}
	server, token, err := resolve()
	if err != nil {
		// 没配置控制面是正常状态(首次安装),不是错误;只有配置存在却读不出
		// 该用哪个 profile 时才把错误抛出去。
		cfg, cerr := loadConfig()
		if cerr != nil || len(cfg.Profiles) > 0 {
			return nil, err
		}
		server, token = "", ""
	}
	if !isLoopback(addr) {
		return nil, fmt.Errorf("mpx ui 只监听回环地址,%s 不是;要在别的设备上看,用 SSH 隧道", addr)
	}

	setupMode := server == "" || forceSetup
	pageOpts := ui.Options{Allow: uiAllow, Setup: setupRunner{}, Connect: connectRunner{},
		Profiles: profilesRunner{}, Relabel: relabelRunner{}, Session: session,
		// 页面里的本机升级走 ui 包的同一套实现,但 ui 不能引用 cli;把结果
		// 交回来落 update.json,mpx update show 才看得到这次升级。
		CLIUpdated: saveCLIStateFromUI}
	if !setupMode {
		up, err := url.Parse(server)
		if err != nil || (up.Scheme != "http" && up.Scheme != "https") || up.Host == "" {
			return nil, fmt.Errorf("控制面地址 %q 不是 http(s) URL", server)
		}
		profile := firstNonEmpty(opts.profile, os.Getenv("MPX_PROFILE"))
		if profile == "" {
			if cfg, err := loadConfig(); err == nil {
				profile = cfg.Current
			}
		}
		tr, err := transportFor(server)
		if err != nil {
			return nil, err
		}
		pageOpts.Upstream = up
		pageOpts.Token = token
		pageOpts.Profile = profile
		pageOpts.Transport = tr
	}

	ln, err := listenUI(addr, restarted)
	if err != nil {
		return nil, err
	}
	s, err := ui.New(ln.Addr().String(), pageOpts)
	if err != nil {
		ln.Close()
		return nil, err
	}
	return &uiLaunch{server: s, listener: ln, pageOpts: pageOpts,
		setupMode: setupMode, restarted: restarted}, nil
}

// serveUI 用浏览器的方式跑 mpx ui:收到 Ctrl-C / SIGTERM 时先停 HTTP,等手上的
// 请求做完再退出。
func serveUI(parent context.Context, l *uiLaunch) error {
	srv := &http.Server{Handler: l.server, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	if err := srv.Serve(l.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// listenUI 绑定页面端口。重启接续时多试几秒:Windows 上是先起新进程再退出旧
// 进程,旧进程放端口有个小空档,立刻绑会失败;正常启动一次不成直接报错。
func listenUI(addr string, retry bool) (net.Listener, error) {
	if !retry {
		return net.Listen("tcp", addr)
	}
	var lastErr error
	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return nil, lastErr
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
	uiCmd.Flags().BoolVar(&uiFlags.setup, "setup", false, "进入首次安装向导,即使已经配置了控制面")
	rootCmd.AddCommand(uiCmd)
}
