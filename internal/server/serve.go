package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// ServeOptions 是跑起一次控制面需要的全部输入。
type ServeOptions struct {
	Addr     string        // HTTP 监听地址,例如 127.0.0.1:8080
	DBPath   string        // SQLite 文件路径
	Token    string        // 管理 token;为空表示不校验,只适合本机开发
	Interval time.Duration // 后台任务间隔,0 用 control.DefaultInterval
	Logger   *slog.Logger
}

// Serve 打开数据库、拉起后台任务、监听 HTTP,直到 ctx 结束或监听失败。
//
// 启动时**不**向节点下发配置。下发意味着重启 xray,而控制面重启是常事(升级、
// 改配置、机器重启),不该因此把每个节点上所有人的连接断一次。结构性变更由
// 使用者显式触发(CLI 的 node sync);启动后自动做的只有人员名单的收敛,那是不
// 重启就能完成的。
func Serve(ctx context.Context, opt ServeOptions) error {
	logger := opt.Logger
	if logger == nil {
		logger = slog.Default()
	}

	st, err := store.Open(opt.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// 核心缓存放在数据库旁边:服务单元的 ProtectSystem=strict 只给控制面留了
	// /var/lib/mpx 一处可写,而缓存要能长期留存、重启之后还在。
	ctrl := control.New(st, node.SSH{}, logger, filepath.Join(filepath.Dir(opt.DBPath), "xray"))
	task := control.NewTask(ctrl, st, logger, opt.Interval)

	srv := New(st, opt.Token, logger)
	// 下发通道接在控制面进程里:能 SSH 到节点的是它,不是操作者手边那台笔记本。
	srv.SetNodeOps(ctrl)

	httpSrv := &http.Server{
		Addr:              opt.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// 不设 WriteTimeout:订阅响应要渲染几百个节点,给一个够用的上限比让它
		// 在中途被切断好。读写超时留给反向代理那一层去定。
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	taskDone := make(chan struct{})
	go func() {
		defer close(taskDone)
		task.Run(ctx)
	}()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("控制面已启动", "addr", opt.Addr, "db", opt.DBPath,
			"auth", opt.Token != "")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("监听 %s: %w", opt.Addr, err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// 收摊:先停 HTTP(它可能正在写库),等它把手上的请求做完,再停后台任务并
	// 关掉数据库。顺序反了的话,任务可能拿着已经关掉的库继续写。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("关闭 HTTP 服务超时", "error", err)
	}
	<-taskDone
	logger.Info("控制面已停止")
	return nil
}
