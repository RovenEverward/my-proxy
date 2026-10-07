package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "管理 xray 节点",
}

func init() {
	nodeCmd.AddCommand(nodeListCmd, nodeShowCmd, nodeAddCmd, nodeRmCmd, nodeConfigCmd, nodeSyncCmd, nodeInstallCmd)

	nodeAddCmd.Flags().StringVar(&nodeAddFlags.address, "address", "", "节点对外地址(IP 或域名),必填")
	nodeAddCmd.Flags().IntVar(&nodeAddFlags.sshPort, "ssh-port", 0, "SSH 端口,0 表示 22")
	nodeAddCmd.Flags().StringVar(&nodeAddFlags.sshUser, "ssh-user", "", "SSH 用户,空表示 root")
	nodeAddCmd.Flags().IntVar(&nodeAddFlags.apiPort, "api-port", 0, "xray API 入站端口,0 表示 10085")
	nodeAddCmd.Flags().StringVar(&nodeAddFlags.configPath, "config-path", "", "节点上的 config.json 路径,空表示 /etc/mpx/xray/config.json")
	nodeAddCmd.Flags().StringVar(&nodeAddFlags.serviceName, "service-name", "", "systemd 服务名,空表示 mpx-xray")

	nodeInstallCmd.Flags().StringVar(&nodeInstallFlags.xrayVersion, "xray-version", "", "装哪个版本的 xray,空表示最新版")
}

// nodeHeaders 是节点表格的表头。列表与其它命令共用同一套列。
var nodeHeaders = []string{"ID", "名称", "地址", "SSH 端口", "SSH 用户", "API 端口", "配置路径", "服务名"}

// nodeRow 按表格要展示的顺序取字段;留空的一律显示成生效的默认值,
// 使用者看到的就是这个节点实际会用到的值。
func nodeRow(n model.Node) []string {
	return []string{
		strconv.FormatInt(n.ID, 10),
		n.Name,
		n.Address,
		strconv.Itoa(n.SSHPortOrDefault()),
		n.SSHUserOrDefault(),
		strconv.Itoa(n.APIPortOrDefault()),
		n.ConfigPathOrDefault(),
		n.ServiceNameOrDefault(),
	}
}

var nodeListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有节点",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/nodes", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		nodes, err := decode[[]model.Node](raw)
		if err != nil {
			return err
		}
		rows := make([][]string, 0, len(nodes))
		for _, n := range nodes {
			rows = append(rows, nodeRow(n))
		}
		return writeTable(nodeHeaders, rows)
	},
}

var nodeShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "显示一个节点",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := idArg(args[0])
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", nodePath(id), nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		n, err := decode[model.Node](raw)
		if err != nil {
			return err
		}
		headers := nodeHeaders
		values := nodeRow(n)
		pairs := make([][2]string, 0, len(headers))
		for i, h := range headers {
			pairs = append(pairs, [2]string{h, values[i]})
		}
		return writeKV(pairs)
	},
}

var nodeAddFlags struct {
	address     string
	sshPort     int
	sshUser     string
	apiPort     int
	configPath  string
	serviceName string
}

var nodeAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "新增一个节点",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if nodeAddFlags.address == "" {
			return errors.New("必须给 --address")
		}
		n := model.Node{
			Name:        args[0],
			Address:     nodeAddFlags.address,
			SSHPort:     nodeAddFlags.sshPort,
			SSHUser:     nodeAddFlags.sshUser,
			APIPort:     nodeAddFlags.apiPort,
			ConfigPath:  nodeAddFlags.configPath,
			ServiceName: nodeAddFlags.serviceName,
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", "/api/nodes", n)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		created, err := decode[model.Node](raw)
		if err != nil {
			return err
		}
		return report(raw, "已创建节点 %d(%s)", created.ID, created.Name)
	},
}

var nodeRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "删除一个节点",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := idArg(args[0])
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("DELETE", nodePath(id), nil)
		if err != nil {
			return err
		}
		return report(raw, "已删除节点 %d", id)
	},
}

var nodeConfigCmd = &cobra.Command{
	Use:   "config <id>",
	Short: "输出该节点完整的 xray config.json",
	Long: `输出该节点完整的 xray config.json。

内容是服务端渲染好的原始 JSON,原样写到 stdout,-o 不影响它:这条命令的
用途就是重定向到文件或交给别的工具,重新排版只会让字节数和原来不一样。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := idArg(args[0])
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", nodePath(id)+"/config", nil)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(raw); err != nil {
			return err
		}
		// 服务端渲染的 JSON 不带结尾换行,这里补一个,免得终端提示符黏在最后一行上。
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		return nil
	},
}

var nodeSyncCmd = &cobra.Command{
	Use:   "sync <id>",
	Short: "把当前配置下发到节点并重启它的 xray",
	Long: `把当前配置下发到节点并重启它的 xray。

配置由控制面渲染并送达(它才是能 SSH 到节点的那一方),这条命令只负责说"该
下发了"。这是唯一会重启节点 xray 的动作,该节点上所有人的连接会断一次,所以
它不会自动发生:新增入站、改端口、换证书这类结构改动只有跑过它才生效。

日常的加人、删人、到期、超限不走这里 —— 控制面每 30 秒自动收敛一次人员名单,
不需要重启。下发前想看看到底会推什么,用 mpx node config。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := idArg(args[0])
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", nodePath(id)+"/sync", nil)
		if err != nil {
			return err
		}
		return report(raw, "已下发节点 %d,它的 xray 已重启", id)
	},
}

var nodeInstallFlags struct {
	xrayVersion string
}

var nodeInstallCmd = &cobra.Command{
	Use:   "install <id>",
	Short: "在节点上装好 xray 并启动它",
	Long: `在节点上装好 xray 并启动它。

发布包由控制面下载一次并缓存在它自己那边,再由它把核心和 geodata 推过 ssh 送到
节点上 —— 所以节点不必能连 GitHub,也不需要 curl、unzip。核心落在一个独占的位
置 /usr/local/lib/mpx/xray,配套的 systemd 单元叫 mpx-xray.service,两处都不占用
机器上可能已经有的别人的 xray。

全新机器跑这一条就够:装核心、写单元、下发配置、启动,一步到 "能用了"。换核心
走同一条命令,默认取最新版,--xray-version 可以指定或退回某个版本;替换之前会先
拿节点上现有的配置试跑新核心,通不过就不换。

核心已装好、配置也通过了校验,但服务没起来时,会回滚到上一份配置并把节点日志的
末尾带回来。下载和推送加起来几十兆,慢链路下要几分钟,所以这条命令的上限比其它
命令长得多。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := idArg(args[0])
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		path := nodePath(id) + "/install"
		if nodeInstallFlags.xrayVersion != "" {
			path += "?version=" + url.QueryEscape(nodeInstallFlags.xrayVersion)
		}
		raw, err := c.doWithin(installTimeout, "POST", path, nil)
		if err != nil {
			return err
		}
		res, err := decode[map[string]string](raw)
		if err != nil {
			return err
		}
		return report(raw, "已在节点 %d 上装好并启动 xray %s", id, res["version"])
	},
}

// nodePath 拼出节点资源的路径。
func nodePath(id int64) string {
	return "/api/nodes/" + strconv.FormatInt(id, 10)
}
