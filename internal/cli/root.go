package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/pin"
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
	Short: "xray 多节点管理",
	Long: `mpx 是自建 xray 多节点管理工具的命令行客户端。

它通过控制面的 /api 管理节点、入站、对外入口、用户与订阅设置。控制面地址由
--server、MPX_SERVER、配置文件依次决定,三者都没有时用不了。

第一次用:在你自己的电脑上跑 mpx init --host <VPS>,它经 SSH 把那台 VPS 装成控制面
和节点,设好设备密码,并把这台电脑接上去。之后再也不用登录 VPS。
别的设备:mpx connect <控制面地址>,输入设备密码即可接入。

不知道下一步该敲什么,就先跑 mpx status 与 mpx doctor —— 这两条只读,不改动任何
东西,它们会告诉你哪里不对。每个命令组(mpx node/inbound/host/user/settings)自己
的 --help 里写了这类对象的含义与常见坑,具体子命令的 --help 里带真实可跑的例子,
拿不准就去翻。`,
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
	rootCmd.PersistentFlags().StringVar(&opts.profile, "profile", "", "用哪个配置档,覆盖 MPX_PROFILE 与文件里的 current")
	rootCmd.PersistentFlags().StringVarP(&opts.output, "output", "o", outputTable, "输出格式:table 或 json")

	rootCmd.AddCommand(loginCmd, serveCmd, statusCmd, updateCmd, doctorCmd, nodeCmd, inboundCmd, hostCmd, userCmd, planCmd, settingsCmd, backupCmd, restoreCmd, initCmd, bootstrapCmd)

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
func Execute() int {
	wireCompletion(rootCmd)
	if err := rootCmd.Execute(); err != nil {
		// doctor 发现故障时要给一个能被脚本认出来的码。它走的是"RunE 返回
		// 错误"这条路(cobra 只认这一种失败方式),但它的输出已经打完了、
		// 也不该再往 stderr 上补一行,所以单独放行,把它翻译成退出码。
		if errors.Is(err, errDoctorFailed) {
			return doctorFailExit
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// loginCmd 把一个控制面的地址与 token 记进某个配置档,省得每次敲 flag。
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "保存控制面地址与 token(可存成不同的配置档);不带参数时显示当前生效的值",
	Long: `保存控制面地址与 token。

--profile 指定存到哪个配置档(默认 default),存完就把当前档切过去。配置文件里
能同时放多套:临时起一个控制面试命令时用另一个档名,原来的连接原封不动,切回来
是一次本地操作 —— token 只在服务器上,丢了要跑一趟机器。

不带 --server/--token 时只显示当前生效的值,不写文件。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		changedServer := cmd.Flags().Changed("server")
		changedToken := cmd.Flags().Changed("token")
		changedFP := cmd.InheritedFlags().Changed("fp")
		// 三个 flag 都是根命令上的 persistent flag,所以要用 InheritedFlags 查 ——
		// cmd.Flags() 只包含本命令自己注册的,查不到继承来的(恒为 false)。
		changedProfile := cmd.InheritedFlags().Changed("profile")

		// 什么都没给 = 看。给了 --profile 而没给地址/token 是"切换",不是"看":
		// 切档本来就该只需要一个档名(令牌只在服务器上,切回来不该逼人去把它
		// 翻出来),所以它不能落进"查看"这一支。
		if !changedServer && !changedToken && !changedProfile && !changedFP {
			return showLogin(cfg)
		}

		name := opts.profile
		if name == "" {
			name = defaultProfile
		}
		old, hadOld := cfg.Profiles[name]

		// 切档要求这个档已经存在:否则一个拼错的档名会静静地建出一个空档,
		// 之后每条命令都因为"没有控制面地址"失败,却看不出是拼错了。
		if changedProfile && !changedServer && !changedToken && !hadOld {
			return fmt.Errorf("没有名为 %q 的配置档,%s", name, cfg.profileHint())
		}

		// 只覆盖这次给了的项,没给的沿用该档里已有的值。
		p := old
		if changedServer {
			p.Server = opts.server
		}
		if changedToken {
			p.Token = opts.token
		}
		// 换了地址却没给指纹:旧指纹属于旧地址,留着只会在下次连接时报"不符"。
		if changedServer && old.Server != p.Server {
			p.Fingerprint = ""
		}
		if changedFP {
			fp, err := pin.Normalize(opts.fingerprint)
			if err != nil {
				return err
			}
			p.Fingerprint = fp
		}
		if strings.HasPrefix(strings.ToLower(p.Server), "https://") && p.Fingerprint == "" {
			return errors.New("https 地址必须带 --fp <证书指纹>;推荐直接用 mpx connect,它会显示指纹让你确认")
		}
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]Profile{}
		}
		cfg.Profiles[name] = p
		cfg.Current = name

		if err := saveConfig(cfg); err != nil {
			return err
		}
		path, err := configFile()
		if err != nil {
			return err
		}
		// 回显"从什么变成了什么"。这不是确认步骤(正常的换控制面不该被拦),
		// 只是让覆盖旧地址这件事在屏幕上留一行,出事时一眼看得见。
		if hadOld && old.Server != p.Server {
			fmt.Printf("配置档 %s 的地址:%s → %s\n", name, dash(old.Server), dash(p.Server))
		}
		if hadOld && old.Fingerprint != p.Fingerprint {
			fmt.Printf("配置档 %s 的证书指纹:%s → %s\n", name, dash(old.Fingerprint), dash(p.Fingerprint))
		}
		if hadOld && old.Token != p.Token {
			fmt.Printf("配置档 %s 的 token:%s → %s\n", name, maskToken(old.Token), maskToken(p.Token))
		}
		// 纯切换时上面两行都不打,得说清楚切到哪了。
		if old == p {
			fmt.Printf("已切到配置档 %s(server: %s)\n", name, dash(p.Server))
		} else {
			fmt.Printf("已写入 %s(当前配置档 %s)\n", path, name)
		}
		return nil
	},
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

// showLogin 打印当前生效的控制面与 token,以及存在哪些配置档。
func showLogin(cfg Config) error {
	server, token, err := resolve()
	if err != nil {
		return err
	}
	fmt.Printf("server: %s\n", dash(server))
	fmt.Printf("token:  %s\n", maskToken(token))
	if fp, err := resolveFingerprint(server); err == nil && fp != "" {
		fmt.Printf("指纹:   %s\n", fp)
	}

	names := cfg.names()
	if len(names) == 0 {
		return nil
	}
	// 生效档和文件里的 current 可能不同(--profile / MPX_PROFILE 会盖掉它),
	// 所以标记跟着 resolve 的口径走,不能直接读 cfg.Current —— 否则打了
	// "--profile scratch" 却把 * 标在 default 上。
	current := firstNonEmpty(opts.profile, os.Getenv("MPX_PROFILE"), cfg.Current)
	fmt.Printf("配置档: ")
	for i, name := range names {
		if i > 0 {
			fmt.Printf("  ")
		}
		mark := " "
		if name == current {
			mark = "*"
		}
		fmt.Printf("%s%s", mark, name)
	}
	fmt.Println()
	return nil
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
