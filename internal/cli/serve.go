package cli

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/server"
)

// serveFlags 是启动控制面时能调的三件事;token 复用全局的 --token。
var serveFlags struct {
	addr     string
	db       string
	interval time.Duration
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动控制面(HTTP + 后台任务)",
	Long: `启动控制面:对外提供 /api 管理接口与 /sub 订阅接口,并在后台收敛各节点的人员名单。

管理 token 走 --token / MPX_TOKEN / 配置文件,和 CLI 用的是同一个;token 为空
时不校验鉴权,所以默认只监听 127.0.0.1 —— 要对外提供订阅就得给出 token 并显式
指定 --addr。

收到 Ctrl-C 或 SIGTERM 时先停 HTTP(等手上的请求做完),再停后台任务,最后关库。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// 只用 token,不要 server:这条命令就是控制面本身,--server 在这里没有意义。
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		token := opts.token
		if token == "" {
			token = os.Getenv("MPX_TOKEN")
		}
		if token == "" {
			token = cfg.Token
		}

		addr := serveFlags.addr
		if token == "" && !isLoopback(addr) {
			return fmt.Errorf("监听 %s 却不校验鉴权:要么设 --token(或 MPX_TOKEN),要么用 --addr 127.0.0.1:8080", addr)
		}

		db, err := filepath.Abs(serveFlags.db)
		if err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		fmt.Printf("控制面监听 %s,数据库 %s,鉴权 %v\n", addr, db, token != "")
		return server.Serve(ctx, server.ServeOptions{
			Addr:     addr,
			DBPath:   db,
			Token:    token,
			Interval: serveFlags.interval,
		})
	},
}

// defaultDBPath 把库放在与配置文件同一个目录里:单机部署时权限(0700)已经
// 是对的,零参数就能跑起来;正式部署用 --db 指到数据盘上。
func defaultDBPath() string {
	path, err := configFile()
	if err != nil {
		return "mpx.db"
	}
	return filepath.Join(filepath.Dir(path), "mpx.db")
}

// isLoopback 判断监听地址是否只对本机开放。空 host(如 ":8080")算对外。
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func init() {
	serveCmd.Flags().StringVar(&serveFlags.addr, "addr", "127.0.0.1:8080", "HTTP 监听地址")
	serveCmd.Flags().StringVar(&serveFlags.db, "db", defaultDBPath(), "SQLite 数据库路径")
	serveCmd.Flags().DurationVar(&serveFlags.interval, "interval", 30*time.Second, "后台收敛人员名单与采集流量的间隔")
}
