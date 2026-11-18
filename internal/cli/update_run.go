package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/pin"
	"github.com/RovenEverward/my-proxy/internal/selfupdate"
	"github.com/RovenEverward/my-proxy/internal/server"
)

// updateRunHealthTimeout 是健康检查单次请求的超时。控制面就在本机,5 秒足够;
// 超了说明服务还没起来,交给 ControlRunner 去轮询。
const updateRunHealthTimeout = 5 * time.Second

// updateRunFlags 是 mpx update run control 的三个入口参数。
//
// 默认值就是部署时的样子:控制面监听回环、库在 /var/lib/mpx、token 在
// /etc/mpx/env。单元文件里不用写任何参数就能跑。
var updateRunFlags struct {
	addr    string
	db      string
	envFile string
}

var updateRunCmd = &cobra.Command{
	Use:   "run <control>",
	Short: "由 mpx-update.service 调用，执行待处理的控制面升级",
	Long: `读取控制面接口落下的升级/回退请求并执行,是 root 侧的一次性动作。

控制面以普通用户运行,换不了 /usr/local/bin/mpx,所以由 mpx-update.service
调用这条命令。没有待处理请求时什么都不做,直接成功退出。

不做鉴权、不连节点:它只处理本机 update 目录下的请求文件,因此只该由 root
的单元调用,不要手工在别的场合跑。`,
	Hidden: true,
	// 部件写成位置参数:Use 里的第二个词 cobra 不认,写成 NoArgs 会让
	// 「mpx update run control」直接报 unknown command。
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || args[0] != "control" {
			return errors.New("用法:mpx update run control(目前只支持 control)")
		}
		return nil
	},
	RunE: runUpdateRunControl,
}

func init() {
	f := updateRunCmd.Flags()
	f.StringVar(&updateRunFlags.addr, "addr", "127.0.0.1:8080", "控制面监听地址(健康检查用)")
	f.StringVar(&updateRunFlags.db, "db", "/var/lib/mpx/mpx.db", "控制面数据库路径")
	f.StringVar(&updateRunFlags.envFile, "env-file", "/etc/mpx/env", "控制面的环境文件(里面有 MPX_TOKEN)")
}

// runUpdateRunControl 组装 root 侧执行器并跑一轮。
func runUpdateRunControl(cmd *cobra.Command, args []string) error {
	db := updateRunFlags.db
	dir := filepath.Join(filepath.Dir(db), "update")
	paths := selfupdate.DefaultControlPaths()
	// 只换数据库与 update 目录,发布目录与符号链接沿用部署默认值。
	paths.DB = db
	paths.Request = filepath.Join(dir, "request")
	paths.State = filepath.Join(dir, "state.json")
	paths.Snapshot = filepath.Join(dir, "pre.db")

	source := selfupdate.Source{Repo: buildinfo.Repo()}
	runner := &selfupdate.ControlRunner{
		Paths: paths,
		Fetch: func(ctx context.Context, version string) ([]byte, bool, error) {
			return source.Fetch(ctx, version, runtime.GOOS, runtime.GOARCH)
		},
		Latest: source.Latest,
		Health: controlHealth(updateRunFlags.addr, updateRunFlags.envFile, db),
	}
	return runner.Run(cmd.Context())
}

// controlHealth 返回一个健康检查实现:GET 控制面的 /api/status,读出它自报的版本。
//
// 控制面是自签证书,只按指纹认人:指纹从库旁边的 tls-fingerprint 读,token 从
// env 文件读(控制面自己写不了命令行)。做成独立函数是为了测试能直接喂假的
// 控制面地址与文件。
func controlHealth(addr, envFile, db string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		token, err := readEnvToken(envFile)
		if err != nil {
			return "", err
		}
		fp, err := readFingerprint(server.FingerprintPath(db))
		if err != nil {
			return "", err
		}

		url := "https://" + addr + "/api/status"
		reqCtx, cancel := context.WithTimeout(ctx, updateRunHealthTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			return "", fmt.Errorf("构造健康检查请求失败: %w", err)
		}
		// 与 CLI 的 Client.doOnce 用同一个头名,别在两处各写一套。
		req.Header.Set("Authorization", "Bearer "+token)

		client := &http.Client{Transport: pin.Transport(fp)}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("连接控制面 %s 失败: %w", url, err)
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("读取控制面状态失败: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("控制面 %s 返回 HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(data)))
		}

		var body struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return "", fmt.Errorf("解析控制面状态失败: %w", err)
		}
		if body.Version == "" {
			return "", errors.New("控制面没有报告版本")
		}
		return body.Version, nil
	}
}

// readEnvToken 从环境文件里读 MPX_TOKEN,与 bootstrap 的读法保持一致。
func readEnvToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读 %s 失败: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "MPX_TOKEN="); ok {
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if v != "" {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("%s 里没有 MPX_TOKEN", path)
}

// readFingerprint 读库旁边的证书指纹文件并规范化。
func readFingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读证书指纹 %s 失败(控制面装好了吗?): %w", path, err)
	}
	fp, err := pin.Normalize(strings.TrimSpace(string(data)))
	if err != nil {
		return "", fmt.Errorf("证书指纹 %s 不合法: %w", path, err)
	}
	return fp, nil
}
