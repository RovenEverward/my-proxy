package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
)

// 输出格式的取值。
const (
	outputTable = "table"
	outputJSON  = "json"
)

// opts 是全局 flag 的落点。
var opts struct {
	server      string
	token       string
	fingerprint string
	profile     string
	output      string
}

// rootCmd 定义在包级:另一个文件里的 serveCmd 要挂到它上面。
var rootCmd = &cobra.Command{
	Use:   "mpx",
	Short: "Agent-Native 极简代理编排核心（xray / sing-box 多节点控制面与 CLI）",
	Long: `mpx 是面向极客与 AI Agent 的极简多节点代理编排核心（xray / sing-box 控制面与 CLI）。

它通过控制面的 /api 管理节点、入站、对外入口、用户、套餐、分流规则与多格式订阅。
所有查询与写操作均支持 --output json / -o json 结构化输出，适合脚本与 Coding Agent 编排。

第一次用:在你自己的电脑上跑 mpx init --host <VPS>,它经 SSH 把那台 VPS 装成控制面
和节点,建好管理员账号,并把这台电脑接上去。
别的设备:mpx connect <控制面地址> --ssh <ssh别名>（推荐,免密走 SSH 通道）,或
mpx connect <控制面地址>（账号密码登录）。

不知道下一步该敲什么,先跑只读自检:mpx status 与 mpx doctor。
配置变更后用 mpx node sync --dry-run <节点> 预检,确认无误再 mpx node sync <节点> 下发。`,
	// 错误统一由 Execute 打印一次;cobra 默认还会补一份 usage,那对使用者是噪音。
	SilenceUsage:  true,
	SilenceErrors: true,
	// 有了它 cobra 自动提供 --version。版本号来自编译期注入(见 buildinfo)。
	//
	// 空版本必须换成一个非空的说法: cobra 见到 Version == "" 就干脆不注册
	// --version 这个 flag,于是"从源码编的"这份连问都问不了,比答"不知道"更糟。
	Version: cliVersion(),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if opts.output != outputTable && opts.output != outputJSON {
			return fmt.Errorf("--output 只能是 %s 或 %s,收到 %q", outputTable, outputJSON, opts.output)
		}
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&opts.fingerprint, "fp", "", "控制面证书指纹(SHA256:...),覆盖 MPX_FINGERPRINT 与配置文件")
	rootCmd.PersistentFlags().StringVar(&opts.server, "server", "", "控制面地址,覆盖 MPX_SERVER 与配置文件")
	rootCmd.PersistentFlags().StringVar(&opts.token, "token", "", "管理 token,覆盖 MPX_TOKEN 与配置文件")
	// --profile 只注册这一次,而且必须是 persistent 的:login 也要用它,而根命令上
	// 的 persistent flag 对子命令同样可见。别在 loginCmd.Flags() 上再注册一个同名的
	// —— 那是个被忽略的重复注册,还会让 cmd.Flags().Changed("profile") 永远为 false
	// (它只看本命令自己的 flag,看不到继承来的)。
	rootCmd.PersistentFlags().StringVar(&opts.profile, "profile", "", "用哪个控制面(按保存时的名称),覆盖 MPX_PROFILE 与文件里的 current")
	rootCmd.PersistentFlags().StringVarP(&opts.output, "output", "o", outputTable, "输出格式:table 或 json")

	rootCmd.AddCommand(serveCmd, statusCmd, updateCmd, doctorCmd, nodeCmd, inboundCmd, hostCmd, userCmd, planCmd, settingsCmd, backupCmd, restoreCmd, initCmd, bootstrapCmd)

	// cobra 默认打 "mpx version v0.2.0"。"version" 这个英文单词夹在中间没有信息量,
	// 而这一行最该说清的恰恰是版本号本身是从哪来的。
	rootCmd.SetVersionTemplate("mpx {{.Version}}\n")
	// update 的子命令在 update.go 里定义,这里挂上去,让 root.go 保留"命令组
	// 一览"这一个入口。
	updateCmd.AddCommand(updateShowCmd)
	updateCmd.AddCommand(updateApplyCmd)
	updateCmd.AddCommand(updateRollbackCmd)
	// root 侧的一次性执行器,由 mpx-update.service 调用,不出现在帮助里。
	updateCmd.AddCommand(updateRunCmd)
}

// Execute 运行命令行,把错误打到 stderr,并返回进程退出码。
//
// args 是进程参数(不含程序名);不传时 cobra 照旧自己读 os.Args[1:],测试与
// 其他调用方两种用法都能走。
func Execute(args ...string) int {
	wireCompletion(rootCmd)
	if len(args) > 0 {
		rootCmd.SetArgs(args)
	}
	if err := rootCmd.Execute(); err != nil {
		// doctor 发现故障时要给一个能被脚本认出来的码。它走的是"RunE 返回
		// 错误"这条路(cobra 只认这一种失败方式),但它的输出已经打完了、
		// 也不该再往 stderr 上补一行,所以单独放行,把它翻译成退出码。
		if errors.Is(err, errDoctorFailed) {
			return doctorFailExit
		}
		writeCLIError(os.Stderr, err)
		return 1
	}
	return 0
}

// writeCLIError 输出命令行错误:在 -o json 模式下向 stderr 输出结构化 JSON
// 错误对象,让脚本与 AI Agent 能稳定解析错误码与原因,同时保持 stdout 干净。
func writeCLIError(w io.Writer, err error) {
	if opts.output == outputJSON {
		payload := struct {
			Error      string `json:"error"`
			StatusCode int    `json:"status_code,omitempty"`
		}{Error: err.Error()}
		var ae *APIError
		if errors.As(err, &ae) && ae.StatusCode > 0 {
			payload.StatusCode = ae.StatusCode
		}
		if b, merr := json.Marshal(payload); merr == nil {
			fmt.Fprintln(w, string(b))
			return
		}
	}
	fmt.Fprintln(w, err)
}

// cliVersion 是 --version 打出来的那一行。
//
// 从源码直接编出来时 buildinfo 说不出自己是谁,这里如实说不知道,而不是报一个
// 空串 —— 空串会让 cobra 连 --version 这个 flag 都不注册。
func cliVersion() string {
	if v := buildinfo.Version(); v != "" {
		return v
	}
	return "未知(从源码直接编译,没带版本号;发布件会带)"
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
