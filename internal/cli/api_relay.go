package cli

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// UC-29:SSH 通道的服务器端一半。
//
// api-relay 跑在控制面所在的服务器上,由本机 CLI 的
// `ssh <目标> 'mpx api-relay --port <端口>'` 唤起:stdin 读一条 HTTP
// 请求,注入主钥匙 Authorization 后打到 127.0.0.1 的控制面,响应原样
// 写回 stdout。它不做任何自己的鉴权 —— 能 ssh 进来执行它的人本来就能读
// /etc/mpx/env,授权与 SSH 等价;这也是整条通道的信任依据。
//
// 对使用者隐藏:它是「CLI 协议的一个动词」,出现在 ssh 命令行里,不是给
// 人直接敲的(直接敲它只会把自己挂在 stdin 上)。

var apiRelayFlags struct {
	port int
}

var apiRelayCmd = &cobra.Command{
	Use:    "api-relay",
	Short:  "(内部)SSH 通道的远端一端:stdin/stdout 转发到本机控制面",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPIRelay(os.Stdin, os.Stdout)
	},
}

// apiRelayTimeout 是 relay 端转发的兜底上限:慢接口(装核心、备份)由
// 调用方的 ctx deadline 掐时间,这里只管「relay 不能永远挂着」。
const apiRelayTimeout = 20 * time.Minute

// relayEnvFile 是找不到 MPX_TOKEN 环境变量时的回退读法。按行解析 KEY=VALUE,
// 只取第一个 MPX_TOKEN;文件权限(0600 root)保证读它的人本来就有主钥匙。
const relayEnvFile = "/etc/mpx/env"

func runAPIRelay(stdin io.Reader, stdout io.Writer) error {
	token := strings.TrimSpace(os.Getenv("MPX_TOKEN"))
	if token == "" {
		var err error
		if token, err = readEnvToken(relayEnvFile); err != nil {
			return fmt.Errorf("取不到主钥匙(env 与 %s 都没有): %w", relayEnvFile, err)
		}
	}
	port := apiRelayFlags.port
	if port <= 0 {
		port = defaultControlPort
	}

	br := bufio.NewReader(stdin)
	req, err := http.ReadRequest(br)
	if err != nil {
		return fmt.Errorf("读请求报文: %w", err)
	}
	defer req.Body.Close()

	// 重写目标到本机控制面:入站报文的 host 是通道占位符(mpx-relay),
	// relay 的职责就是把它钉回 127.0.0.1:<port>,与发送方声称的地址无关。
	req.URL.Scheme = "http"
	req.URL.Host = net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	req.RequestURI = ""
	req.Header.Set("Authorization", "Bearer "+token)
	// 转发体不该再带客户端的 Host 头 —— Go 会按 URL.Host 自己补。
	req.Host = ""

	tr := &http.Transport{
		DisableCompression: true, // 字节原样过,不在通道里引入二次编解码
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: apiRelayTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("转发到控制面失败: %w", err)
	}
	defer resp.Body.Close()
	return resp.Write(stdout)
}

func init() {
	apiRelayCmd.Flags().IntVar(&apiRelayFlags.port, "port", defaultControlPort, "本机控制面的 HTTP 端口")
	rootCmd.AddCommand(apiRelayCmd)
}

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
