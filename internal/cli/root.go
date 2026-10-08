package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// 输出格式的取值。
const (
	outputTable = "table"
	outputJSON  = "json"
)

// opts 是三个全局 flag 的落点。
var opts struct {
	server string
	token  string
	output string
}

// rootCmd 定义在包级:另一个文件里的 serveCmd 要挂到它上面。
var rootCmd = &cobra.Command{
	Use:   "mpx",
	Short: "xray 多节点管理",
	Long: `mpx 是自建 xray 多节点管理工具的命令行客户端。

它通过控制面的 /api 管理节点、入站、对外入口、用户与订阅设置。控制面地址由
--server、MPX_SERVER、配置文件依次决定,三者都没有时用不了。`,
	// 错误统一由 Execute 打印一次;cobra 默认还会补一份 usage,那对使用者是噪音。
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if opts.output != outputTable && opts.output != outputJSON {
			return fmt.Errorf("--output 只能是 %s 或 %s,收到 %q", outputTable, outputJSON, opts.output)
		}
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&opts.server, "server", "", "控制面地址,覆盖 MPX_SERVER 与配置文件")
	rootCmd.PersistentFlags().StringVar(&opts.token, "token", "", "管理 token,覆盖 MPX_TOKEN 与配置文件")
	rootCmd.PersistentFlags().StringVarP(&opts.output, "output", "o", outputTable, "输出格式:table 或 json")

	rootCmd.AddCommand(loginCmd, serveCmd, nodeCmd, inboundCmd, hostCmd, userCmd, settingsCmd, backupCmd, restoreCmd)
}

// Execute 运行命令行,把错误打到 stderr,并返回进程退出码。
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// loginCmd 把控制面地址与 token 记进配置文件,省得每次敲 flag。
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "保存控制面地址与 token;不带参数时显示当前生效的值",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		changedServer := cmd.Flags().Changed("server")
		changedToken := cmd.Flags().Changed("token")

		// 一个都没给就是查看:显示当前生效值,而不是配置文件里那半份。
		if !changedServer && !changedToken {
			server, token, err := resolve()
			if err != nil {
				return err
			}
			fmt.Printf("server: %s\n", dash(server))
			fmt.Printf("token:  %s\n", maskToken(token))
			return nil
		}

		// 只覆盖这次给了的项,没给的沿用文件里已有的值。
		if changedServer {
			cfg.Server = opts.server
		}
		if changedToken {
			cfg.Token = opts.token
		}
		if err := saveConfig(cfg); err != nil {
			return err
		}
		path, err := configFile()
		if err != nil {
			return err
		}
		fmt.Printf("已写入 %s\n", path)
		return nil
	},
}

// idArg 先把位置参数校验成本地能看懂的错,免得为一个手误跑一趟网络。
func idArg(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("id 不是整数: %q", s)
	}
	return id, nil
}

// splitCSV 把逗号分隔的 flag 值切成切片,顺手去掉每段两侧的空白;
// 全是空白时得到 nil,表示"没给"。
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
