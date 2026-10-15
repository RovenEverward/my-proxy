package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
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
	// NoAutoLocate 关掉「新建节点后自动识别地区」(mpx serve --auto-locate=false 或 MPX_AUTO_LOCATE=0)。
	NoAutoLocate bool
}

// Serve 打开数据库、拉起后台任务、监听 HTTP,直到 ctx 结束或监听失败。
//
// 启动时**不**向节点下发配置。下发意味着重启 xray,而控制面重启是常事(升级、
// 改配置、机器重启),不该因此把每个节点上所有人的连接断一次。结构性变更由
// 使用者显式触发(CLI 的 node sync);启动后自动做的只有人员名单的收敛,那是不
// 重启就能完成的。
func Serve(ctx context.Context, opt ServeOptions) error {
	ctx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	logger := opt.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// 先把暂存的恢复换入,再打开数据库:换入必须在 Open 之前做,否则运行中的
	// 进程正拿着旧库文件,改名换文件就乱了。这里还库没开,是唯一安全的时机。
	// 没有暂存文件时这步什么也不做。
	if staged, preRestore, err := applyStagedRestore(opt.DBPath); err != nil {
		return err
	} else if staged != "" {
		logger.Info("已换入暂存的恢复", "staged", staged, "preRestore", preRestore,
			"旧库保留在", preRestore)
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

	srv := New(st, opt.Token, logger, opt.DBPath)
	// 控制面连节点的密钥放在数据目录下:systemd 的 StateDirectory=mpx 保证它有、
	// 权限对,而且备份/迁移时跟着库一起走,不会留在某个 home 里找不到。
	srv.sshKeyPath = filepath.Join(filepath.Dir(opt.DBPath), ".ssh", "id_ed25519")
	// 下发通道接在控制面进程里:能 SSH 到节点的是它,不是操作者手边那台笔记本。
	srv.SetNodeOps(ctrl)
	srv.autoLocate = !opt.NoAutoLocate
	srv.SetPurposeFilter(srv.filterPurposeCapabilities)
	srv.AutoMigrateRouting()
	// 心跳也接上:GET /api/status 靠它回答「上一轮什么时候跑的、跑了多久」。
	srv.SetStatusSource(task)

	// 自签证书:库里有就用,没有就生成。指纹另外落一份纯文本在库旁边,给
	// mpx init 经 SSH 读回去打印接入命令用 —— 那一侧不该为了一个字符串去开库。
	cert, fp, err := loadOrCreateIdentity(st)
	if err != nil {
		return err
	}
	srv.tlsFingerprint = fp
	if err := writeFingerprintFile(FingerprintPath(opt.DBPath), fp); err != nil {
		logger.Warn("写证书指纹文件失败(不影响服务)", "err", err)
	}

	httpSrv := &http.Server{
		Handler:           srv.plaintextAPIWarning(srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		// 不设 WriteTimeout:订阅响应要渲染几百个节点,给一个够用的上限比让它
		// 在中途被切断好。读写超时留给反向代理那一层去定。
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	capabilityDone := make(chan struct{})
	go func() { defer close(capabilityDone); ctrl.RunCapabilityChecks(ctx) }()
	defer func() { stopWorkers(); <-capabilityDone }()

	taskDone := make(chan struct{})
	go func() {
		defer close(taskDone)
		task.Run(ctx)
	}()
	defer func() { stopWorkers(); <-taskDone }()

	errCh := make(chan error, 1)
	go func() {
		ln, err := net.Listen("tcp", opt.Addr)
		if err != nil {
			errCh <- fmt.Errorf("监听 %s: %w", opt.Addr, err)
			return
		}
		logger.Info("控制面已启动", "addr", opt.Addr, "db", opt.DBPath,
			"auth", opt.Token != "", "fingerprint", fp)
		tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"},
			MinVersion: tls.VersionTLS12}
		if err := httpSrv.Serve(newSniffListener(ln, tlsCfg)); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// applyStagedRestore 把 <db>.restore 里暂存的库换入为正式库,必须在打开数据库
// 之前调用。
//
// 换入是三步:
//  1. 现有库改名成 <db>.pre-restore-<UTC 时间戳>,连同它的 -wal/-shm 一起
//     (旧 sidecar 不删、跟着旧库走,回退时三件套一起改回去就是完整旧状态);
//  2. 暂存文件改名成 <db>;
//  3. 旧的 -wal/-shm 随旧库一起挪走了,名字下不再有残留,新库不会读到它们。
//
// 返回 (staged, preRestore, err):没有暂存文件时 staged 为空、err 为 nil,调用
// 方什么都不用做;有暂存文件时 staged/preRestore 是两个参与改名的路径,方便
// 记日志。
//
// 为什么换入在打开之前:开库之后进程就持有文件句柄,再改名换文件会出现"半个
// 库"——有的连接看到旧库、有的看到新库。在 Open 之前一步做完,要么整个换掉、
// 要么整个不动。旧库留着是为了出错能回退;也因此"没有控制面也能恢复":手动
// 把 mpx.db 改名成 mpx.db.restore 再启动,就走同一条路。
func applyStagedRestore(dbPath string) (staged string, preRestore string, err error) {
	staged = dbPath + ".restore"
	if _, err := os.Stat(staged); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("检查暂存恢复文件: %w", err)
	}

	preName := dbPath + ".pre-restore-" + time.Now().UTC().Format("20060102T150405Z")
	moved := false
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", "", fmt.Errorf("检查旧库文件 %s: %w", p, err)
		}
		suffix := ""
		if p != dbPath {
			suffix = p[len(dbPath):] // "-wal" 或 "-shm"
		}
		if err := os.Rename(p, preName+suffix); err != nil {
			return "", "", fmt.Errorf("把旧库 %s 改名到 %s: %w", p, preName+suffix, err)
		}
		moved = true
	}

	if err := os.Rename(staged, dbPath); err != nil {
		return "", "", fmt.Errorf("把暂存库 %s 换入为 %s: %w", staged, dbPath, err)
	}
	if moved {
		preRestore = preName
	}

	return staged, preRestore, nil
}
