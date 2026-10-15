package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "管理 xray 节点",
	Long: `管理 xray 节点 —— 真的跑 xray 的那台机器。

节点是四类对象里最靠下的一个:入站挂在节点上,对外入口挂在入站上,用户则被单独
授权到某些入站。所以顺序是 节点 → 入站 → 入口 → 用户,反过来建没有东西可挂。

新机器的常规流程:node add 把它登记进来(此时只是数据库里一行,机器上什么都没装)
→ node install 在上面装好核心并启动 → 之后 node sync 把配置推下去。

节点上的 xray 由 systemd 管,单元名就是节点的"服务名"字段。node status 会 ssh
过去现场查一次,node online 则读节点上每个用户此刻的在线 IP 数。`,
}

func init() {
	nodeCmd.AddCommand(nodeListCmd, nodeShowCmd, nodeStatusCmd, nodeAddCmd, nodeEditCmd, nodeRmCmd, nodeConfigCmd, nodeSyncCmd, nodeInstallCmd,
		nodeDrainCmd, nodeUndrainCmd, nodeOnlineCmd, nodeCoreCmd)

	nodeAddCmd.Flags().StringVar(&nodeAddFlags.address, "address", "", "节点对外地址(IP 或域名),必填")
	nodeAddCmd.Flags().IntVar(&nodeAddFlags.sshPort, "ssh-port", 0, "SSH 端口,0 表示 22")
	nodeAddCmd.Flags().StringVar(&nodeAddFlags.sshUser, "ssh-user", "", "SSH 用户,空表示 root")
	nodeAddCmd.Flags().IntVar(&nodeAddFlags.apiPort, "api-port", 0, "xray API 入站端口,0 表示 10085")
	nodeAddCmd.Flags().StringVar(&nodeAddFlags.configPath, "config-path", "", "节点上的 config.json 路径,空表示 /etc/mpx/xray/config.json")
	nodeAddCmd.Flags().StringVar(&nodeAddFlags.serviceName, "service-name", "", "systemd 服务名,空表示 mpx-xray")
	// 密码报到:非空时控制面先拿它登进去装公钥,装不上就当这次建节点没发生。
	// 空表示节点上已经信任控制面的密钥(装过了 / 手工放过),直接落库。
	nodeAddCmd.Flags().BoolVar(&nodeAddFlags.sshPassword, "ssh-password", false,
		"提示输一次 SSH 密码,用它把控制面的公钥装上节点(之后全程用密钥)")

	nodeInstallCmd.Flags().StringVar(&nodeInstallFlags.core, "core", "xray", "核心:xray / sing-box")
	nodeInstallCmd.Flags().StringVar(&nodeInstallFlags.version, "version", "", "核心版本,空表示最新版")
	nodeInstallCmd.Flags().StringVar(&nodeInstallFlags.xrayVersion, "xray-version", "", "装哪个版本的 xray,空表示最新版")

	nodeEditCmd.Flags().StringVar(&nodeEditFlags.name, "name", "", "新的节点名")
	nodeEditCmd.Flags().StringVar(&nodeEditFlags.address, "address", "", "新的对外地址(IP 或域名)")
	nodeEditCmd.Flags().IntVar(&nodeEditFlags.sshPort, "ssh-port", 0, "新的 SSH 端口,0 表示 22")
	nodeEditCmd.Flags().StringVar(&nodeEditFlags.sshUser, "ssh-user", "", "新的 SSH 用户,空表示 root")
	nodeEditCmd.Flags().IntVar(&nodeEditFlags.apiPort, "api-port", 0, "新的 xray API 端口,0 表示 10085")
	nodeEditCmd.Flags().StringVar(&nodeEditFlags.configPath, "config-path", "", "节点上新的 config.json 路径")
	nodeEditCmd.Flags().StringVar(&nodeEditFlags.serviceName, "service-name", "", "新的 systemd 服务名")
}

// nodeHeaders 是节点表格的表头。列表与 show 共用同一套列。
//
// 后五列是批 3b 加的状态:它们全部来自库里最近一次检查的缓存,**不发 ssh**
// —— 列表是要能随手敲的,一次列出十几台节点如果每台都连一次,它就变成一条
// 要等半分钟的命令了。想现场问一次用 node status。
var nodeHeaders = []string{
	"名称", "内部名", "地址", "SSH 端口", "SSH 用户", "API 端口", "配置路径", "服务名",
	"状态", "版本", "漂移", "最后检查", "证书最短", "能力", "ID",
}

// nodeRow 按表格要展示的顺序取字段;留空的一律显示成生效的默认值,
// 使用者看到的就是这个节点实际会用到的值。
func nodeRow(v model.NodeView, now time.Time) []string {
	n := v.Node
	// 名字为主、id 放最后(docs/internal/node-naming-rfc.md §8):「名称」是展示名,
	// 还没有展示名时就是内部名;「内部名」是 CLI 里敲的那个。
	display := nodeDisplay(n)
	if display == "" {
		display = n.Name
	}
	return []string{
		display,
		n.Name,
		n.Address,
		strconv.Itoa(n.SSHPortOrDefault()),
		n.SSHUserOrDefault(),
		strconv.Itoa(n.APIPortOrDefault()),
		n.ConfigPathOrDefault(),
		n.ServiceNameOrDefault(),
		statusCell(v),
		dash(versionOf(v)),
		driftCell(v),
		checkedCell(v, now),
		certCell(v),
		capabilitySummary(v.Capabilities),
		strconv.FormatInt(n.ID, 10),
	}
}

// statusCell 把一份状态说成一个词。
//
// 没查过、和查过但连不上是两件事,要分开:"未知"是还没问过,"不可达"是问了
// 没答。合成一个"异常"会让人以为节点坏了,而其实可能只是从来没人查过。
func statusCell(v model.NodeView) string {
	if v.Status == nil {
		return "未知"
	}
	if !v.Status.Reachable {
		return "不可达"
	}
	if v.Status.Active {
		return "active"
	}
	return "inactive"
}

// versionOf 取核心版本;节点连不上时没有版本可报。
func versionOf(v model.NodeView) string {
	if v.Status == nil {
		return ""
	}
	return v.Status.Version
}

// driftCell 说漂移这件事的三种答案:是 / 否 / 未下发。
//
// "未下发"必须和"否"分开:一个从没推过配置的节点没有基准可比,报"否"会让人觉得
// 它的配置是对的,而其实根本不知道上面是什么。
func driftCell(v model.NodeView) string {
	switch {
	case !v.Node.Deployed():
		return "未下发"
	case v.Status == nil || !v.Status.Reachable:
		// 问不到就没法比,这不是"没漂移",是"不知道"。
		return "未知"
	case v.Node.Drift(v.Status):
		return "是"
	default:
		return "否"
	}
}

// checkedCell 说"最后一次检查是多久以前"。
func checkedCell(v model.NodeView, now time.Time) string {
	if v.Status == nil || v.Status.CheckedAt.IsZero() {
		return "从未"
	}
	return humanDuration(now.Sub(v.Status.CheckedAt)) + "前"
}

// certCell 报最短的证书剩余天数。
//
// 报最短的那张而不是全部:列表只要让人一眼看出"有没有快过期的"。读不到到期
// 时间时显示"读不到",不显示 0 —— 0 天看起来像"今天就到期",而实际是不知道。
func certCell(v model.NodeView) string {
	if v.Status == nil || !v.Status.Reachable {
		return "-"
	}
	if len(v.Status.Certificates) == 0 {
		return "-"
	}
	// 任一张读不到到期时间就直接说"读不到":它是这一列里最该被看见的状态,
	// 不能因为同台节点上还有一张能读的证书就被一个天数盖过去。
	best := 0
	for i, c := range v.Status.Certificates {
		if c.NotAfter == nil {
			return "读不到"
		}
		if i == 0 || c.DaysLeft < best {
			best = c.DaysLeft
		}
	}
	return strconv.Itoa(best) + " 天"
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
		views, err := decode[[]model.NodeView](raw)
		if err != nil {
			return err
		}
		now := time.Now()
		rows := make([][]string, 0, len(views))
		for _, v := range views {
			rows = append(rows, nodeRow(v, now))
		}
		return writeTable(nodeHeaders, rows)
	},
}

var nodeShowCmd = &cobra.Command{
	Use:   "show <节点>",
	Short: "显示一个节点",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], false)
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
		v, err := decode[model.NodeView](raw)
		if err != nil {
			return err
		}
		headers := nodeHeaders
		values := nodeRow(v, time.Now())
		pairs := make([][2]string, 0, len(headers))
		for i, h := range headers {
			pairs = append(pairs, [2]string{h, values[i]})
		}
		return writeKV(pairs)
	},
}

// nodeStatusCmd 现场问一次节点状态。
//
// 这是 node 子命令里唯一会真的 ssh 出去问的一条(install 与 sync 也会连,但那是
// 下发)。所以它明确说"现场",而 list 里的状态列全部来自缓存 —— 一条命令该不该
// 有副作用、该不该慢,使用者要能一眼看出来。
var nodeStatusCmd = &cobra.Command{
	Use:   "status [节点]",
	Short: "现场查一次节点的状态(会 ssh 到节点上)",
	Long: `现场查一次节点的状态:服务在不在跑、核心什么版本、配置有没有被改过、每张证书还有几天到期。

不给参数就查全部节点。查询结果会立刻落库,所以查完再看 mpx node list,那几列
就是刚才看到的值。

这和 list 里的状态列是两回事:list 读的是缓存(不发 ssh、随手就能敲),这条是
真的连过去问一次。漂移要看的是"现在机器上是什么",所以想看准的就用这条。

节点连不上不是错误:会打出"不可达"和 ssh 给的原话。这正是要查的事情之一。`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}

		nodes, err := resolveNodeArgs(c, args)
		if err != nil {
			return err
		}

		now := time.Now()
		// 一台一条请求:fresh 是"这一次读要不要绕开缓存",而每台节点各有各的
		// 缓存,合成一条请求没有意义 —— 服务端也没法一次 ssh 到十几台机器上。
		views := make([]model.NodeView, 0, len(nodes))
		for _, n := range nodes {
			raw, err := c.doWithin(nodeStatusTimeout, "GET", nodePath(n.ID)+"?fresh=1", nil)
			if err != nil {
				return err
			}
			v, err := decode[model.NodeView](raw)
			if err != nil {
				return err
			}
			if v.Status == nil {
				// fresh=1 之后一定有一份状态;真没有就是服务端那边出了问题,
				// 说出来比在表里显示一片空白强。
				return fmt.Errorf("节点 %s 没返回状态", n.Name)
			}
			views = append(views, v)
		}

		if opts.output == outputJSON {
			// JSON 模式下把整批一次打出去,而不是循环里各打一次 —— 一条命令的
			// 输出要是一整个 JSON 文档,才能直接喂给 jq。
			body, err := json.Marshal(views)
			if err != nil {
				return err
			}
			return emitJSON(body)
		}

		for i, v := range views {
			if i > 0 {
				fmt.Println()
			}
			if err := printNodeStatus(v.Node, *v.Status, now); err != nil {
				return err
			}
		}
		return nil
	},
}

// resolveNodeArgs 把"给名字/给 ID/什么都不给"统一成一组节点 ID。
//
// 支持按名字找是因为节点名是给人用的,而 ID 是数据库给的;让人为了查一台节点先去
// list 里翻 ID,是把内部的编号泄漏到了使用方式上。
func resolveNodeArgs(c *Client, args []string) ([]model.Node, error) {
	all, err := listNodes(c)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		if len(all) == 0 {
			return nil, errors.New("还没有任何节点")
		}
		out := make([]model.Node, 0, len(all))
		for _, v := range all {
			out = append(out, v.Node)
		}
		return out, nil
	}

	// id、名字、展示名、唯一前缀都认,规则见 resolve.go。
	x, _, err := pick("节点", args[0], nodeCandidates(all))
	if err != nil {
		return nil, err
	}
	for _, v := range all {
		if v.Node.ID == x.ID {
			return []model.Node{v.Node}, nil
		}
	}
	return nil, fmt.Errorf("没有 id 为 %d 的节点", x.ID)
}

// listNodes 取节点列表。现场查状态前要先拿到节点本身,因为"有没有漂移"要靠
// 节点记录里的下发哈希去比 —— 单独一份 NodeStatus 里没有基准。
func listNodes(c *Client) ([]model.NodeView, error) {
	raw, err := c.do("GET", "/api/nodes", nil)
	if err != nil {
		return nil, err
	}
	return decode[[]model.NodeView](raw)
}

// printNodeStatus 打一台节点的现场状态。
//
// 漂移这一行是这条命令的重点之一:用户手改过节点上的 config.json 时,这里会
// 明确写"是",并给出两个哈希 —— 光说一句"漂移了"没法让人确认是不是自己改的,
// 而给出哈希他就能去机器上 sha256sum 一下对。当前哈希报不出来时也要说清楚
// (节点上没有这个文件、或者读不到),不能显示成"否"。
func printNodeStatus(n model.Node, st model.NodeStatus, now time.Time) error {
	if err := writeKV([][2]string{
		{"节点", fmt.Sprintf("%s(ID %d)", n.Name, n.ID)},
		{"可达", yesNo(st.Reachable)},
		{"服务", serviceCell(st)},
		{"版本", dash(st.Version)},
		{"配置漂移", driftText(n, st)},
		{"检查时间", st.CheckedAt.Format(time.RFC3339) + "(" + humanDuration(now.Sub(st.CheckedAt)) + "前)"},
	}); err != nil {
		return err
	}
	if st.Error != "" {
		fmt.Println()
		fmt.Println("ssh 说:" + st.Error)
	}
	if len(st.Certificates) == 0 {
		return nil
	}
	fmt.Println()
	rows := make([][]string, 0, len(st.Certificates))
	for _, c := range st.Certificates {
		rows = append(rows, certRow(c))
	}
	return writeTable([]string{"入站", "证书路径", "到期", "剩余", "说明"}, rows)
}

// certRow 是一张证书一行。读不到的时候说清楚为什么,不留空白 ——
// 空白会被读成"没问题"。
func certRow(c model.CertificateInfo) []string {
	name := c.InboundName
	if name == "" {
		name = strconv.FormatInt(c.InboundID, 10)
	}
	expire, left, note := "-", "-", ""
	switch {
	case c.NotAfter != nil:
		expire = c.NotAfter.Format("2006-01-02")
		left = strconv.Itoa(c.DaysLeft) + " 天"
	case c.Error != "":
		note = c.Error
	}
	if c.Path == "" {
		note = strings.TrimSpace("路径读不到 " + note)
	}
	return []string{name, dash(c.Path), expire, left, note}
}

// serviceCell 说服务在不在跑。"问不到"要和"没在跑"分开。
func serviceCell(st model.NodeStatus) string {
	if !st.Reachable {
		return "未知(节点不可达)"
	}
	if st.Active {
		return "active"
	}
	return "inactive"
}

// driftText 说清楚漂移这四种情况,而不是一个布尔值。
func driftText(n model.Node, st model.NodeStatus) string {
	switch {
	case !n.Deployed():
		return "未下发过(没有可比对的基准)"
	case !st.Reachable:
		return "不知道(节点不可达,比不了)"
	case st.ObservedHash == "":
		return "不知道(节点上读不到配置文件的哈希)"
	case st.ObservedHash == n.DeployedHash:
		return "否(和下发的一致)"
	default:
		return fmt.Sprintf("是(下发 %s,节点上 %s)", shortHash(n.DeployedHash), shortHash(st.ObservedHash))
	}
}

// shortHash 只显示哈希前 12 位:完整的够长、又要在终端里能一眼对上,
// 而完整值在 -o json 里有。
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

var nodeAddFlags struct {
	address     string
	sshPort     int
	sshUser     string
	apiPort     int
	configPath  string
	serviceName string
	sshPassword bool
}

var nodeAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "新增一个节点",
	Example: `  # 登记一台机器,地址就是对外 IP
  mpx node add hk --address 203.0.113.10

  # 非 22 端口、非 root 用户,并且当场把控制面的公钥装上去(会提示输密码)
  mpx node add us --address 198.51.100.7 --ssh-port 2222 --ssh-user ubuntu --ssh-password

  # 登记完在它上面装核心并启动
  mpx node install 1`,
	Args: cobra.ExactArgs(1),
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

		var password string
		if nodeAddFlags.sshPassword {
			// 密码走 TTY 提示而不是 flag 值:--password xxx 会留在 shell 历史和
			// ps 里,提示输入不会。
			pw, err := promptPassword(fmt.Sprintf("节点 %s 的 SSH 密码: ", n.Address))
			if err != nil {
				return err
			}
			password = pw
		}

		c, err := newClient()
		if err != nil {
			return err
		}
		// 请求体在 Node 之外多个 ssh_password:它只是一次性的钥匙,不进 model.Node。
		body := struct {
			model.Node
			SSHPassword string `json:"ssh_password,omitempty"`
		}{Node: n, SSHPassword: password}
		raw, err := c.do("POST", "/api/nodes", body)
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

// promptPassword 在 TTY 上问一次密码,不回显。
//
// 用 term.ReadPassword 而不是自己读 stdin:回显开关是终端自己的事,读错模式会把
// 密码原样打出来。stdin 不是 TTY(管道、重定向)时退回读一行 —— 脚本里没法交互,
// 只能走 stdin 喂。
func promptPassword(prompt string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		var pw string
		if _, err := fmt.Fscanln(os.Stdin, &pw); err != nil {
			return "", fmt.Errorf("从 stdin 读密码: %w", err)
		}
		if pw == "" {
			return "", errors.New("密码不能为空")
		}
		return pw, nil
	}
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // ReadPassword 不吃回车,补个换行让提示符回行首
	if err != nil {
		return "", fmt.Errorf("读密码: %w", err)
	}
	if len(pw) == 0 {
		return "", errors.New("密码不能为空")
	}
	return string(pw), nil
}

var nodeRmCmd = &cobra.Command{
	Use:   "rm <节点>",
	Short: "删除一个节点",
	Long: `删除一个节点 —— 不可逆。

它连同这台节点上的入站、以及挂在那些入站上的对外入口一起删掉,数据库里立刻生效,
没有确认提示。节点本身不会被动:机器上的 xray 还在跑、配置还在,只是控制面再也
不管它了,要清理得自己登上去。

只想让新订阅别再发这台节点,用 node drain(已连上的用户会在下次刷新订阅时自然
漂走),不必删。确认节点 id 用 mpx node list。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], true)
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

// nodeEditFlags 是 node edit 的可选字段。
//
// 全部用 Changed() 判断"这次到底改没改":一个只给了 --name 的调用不该把
// address 悄悄清空。这是"读-改-写",不是"整体替换" —— 后者要求把十几个字段
// 原样抄一遍,抄漏一个就是一次静默的数据损坏。
var nodeEditFlags struct {
	name        string
	address     string
	sshPort     int
	sshUser     string
	apiPort     int
	configPath  string
	serviceName string
}

var nodeEditCmd = &cobra.Command{
	Use:   "edit <节点>",
	Short: "改一个节点的连接参数或名字",
	Long: `改一个节点的名字与连接参数。只改给出来的字段,没给的保持原样。

  mpx node edit 2 --name jp-1
  mpx node edit 4 --ssh-port 2222 --ssh-user deploy

注意几个连带影响:

改 address 会同时改掉订阅里发出去的地址 —— 除非某个对外入口自己写了地址
(host 的 address 字段非空时优先)。所以换 IP 之前先看一眼 mpx host list,
否则用户订阅会跟着变。

改 ssh-port / ssh-user / config-path / service-name 只影响控制面以后怎么连它,
不会立刻碰节点;要真正生效跑一次 mpx node sync。

改完不会自动下发配置:名字与连接参数都不进节点上的 config.json。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], true)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		// 先读回来:控制面那条 PUT 是整体替换,只发这次要改的字段会把其余
		// 字段清空。连接参数抄漏一个,下一次 ssh 就找不到节点了。
		raw, err := c.do("GET", nodePath(id), nil)
		if err != nil {
			return err
		}
		v, err := decode[model.NodeView](raw)
		if err != nil {
			return err
		}
		n := v.Node
		oldAddress := n.Address

		if cmd.Flags().Changed("name") {
			n.Name = nodeEditFlags.name
		}
		if cmd.Flags().Changed("address") {
			n.Address = nodeEditFlags.address
		}
		if cmd.Flags().Changed("ssh-port") {
			n.SSHPort = nodeEditFlags.sshPort
		}
		if cmd.Flags().Changed("ssh-user") {
			n.SSHUser = nodeEditFlags.sshUser
		}
		if cmd.Flags().Changed("api-port") {
			n.APIPort = nodeEditFlags.apiPort
		}
		if cmd.Flags().Changed("config-path") {
			n.ConfigPath = nodeEditFlags.configPath
		}
		if cmd.Flags().Changed("service-name") {
			n.ServiceName = nodeEditFlags.serviceName
		}

		// DeployedHash / DeployedAt 是"我们这边推过什么"的基准,换地址不改变
		// 这个事实,所以原样带回去(上面是整体 Node,本来就在)。
		raw, err = c.do("PUT", nodePath(id), n)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		updated, err := decode[model.Node](raw)
		if err != nil {
			return err
		}
		if err := report(raw, "已更新节点 %d(%s)", updated.ID, updated.Name); err != nil {
			return err
		}
		// 换了地址就提醒一句:订阅里的地址多半跟着变了,这是最容易踩的地方。
		if n.Address != oldAddress {
			fmt.Printf("注意:节点地址由 %s 变成 %s,订阅里没显式写地址的入口会跟着变。\n", oldAddress, n.Address)
			fmt.Println("      想确认哪些入口用了节点地址:mpx host list --inbound <id>")
		}
		return nil
	},
}

var nodeConfigCmd = &cobra.Command{
	Use:   "config <节点>",
	Short: "输出该节点完整的 xray config.json",
	Long: `输出该节点完整的 xray config.json。

内容是服务端渲染好的原始 JSON,原样写到 stdout,-o 不影响它:这条命令的
用途就是重定向到文件或交给别的工具,重新排版只会让字节数和原来不一样。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], false)
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
	Use:   "sync <节点>",
	Short: "把当前配置下发到节点并重启它的 xray",
	Long: `把当前配置下发到节点并重启它的 xray。

配置由控制面渲染并送达(它才是能 SSH 到节点的那一方),这条命令只负责说"该
下发了"。这是唯一会重启节点 xray 的动作,该节点上所有人的连接会断一次,所以
它不会自动发生:新增入站、改端口、换证书这类结构改动只有跑过它才生效。

日常的加人、删人、到期、超限不走这里 —— 控制面每 30 秒自动收敛一次人员名单,
不需要重启。下发前想看看到底会推什么,用 mpx node config。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], true)
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
	core        string
	version     string
	xrayVersion string
}

var nodeInstallCmd = &cobra.Command{
	Use:   "install <节点>",
	Short: "在节点上装好指定核心并启动它",
	Long: `在节点上装好指定核心并启动它。

--core 默认 xray;hysteria2 / tuic 使用 sing-box。发布包由控制面下载和缓存,
再通过 SSH 送到节点。--version 指定版本,留空使用最新版。

两个核心各自使用独立的配置文件、API 端口和 systemd 服务。需要定制 sing-box
部署信息时,先运行 mpx node core edit,再首次安装;部署后不能直接修改这些位置。

安装会下发当前配置并启动所选核心。下载和推送在慢链路下需要几分钟。`,
	Example: `  # 全新节点,装最新版
  mpx node install 1

  # 装指定的核心版本(也可以用来退回旧版)
  mpx node install 1 --version v25.1.1

  # 安装 hysteria2 / tuic 使用的 sing-box
  mpx node install 1 --core sing-box --version 1.12.0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], true)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		core := model.Core(nodeInstallFlags.core)
		if !core.Valid() {
			return errors.New("--core 只能是 xray / sing-box")
		}
		version := nodeInstallFlags.version
		if cmd.Flags().Changed("xray-version") {
			if core != model.CoreXray || version != "" {
				return errors.New("--xray-version 只支持 Xray,且不能与 --version 同用")
			}
			version = nodeInstallFlags.xrayVersion
		}
		path := nodePath(id) + "/install?core=" + url.QueryEscape(string(core))
		if version != "" {
			path += "&version=" + url.QueryEscape(version)
		}
		raw, err := c.doWithin(installTimeout, "POST", path, nil)
		if err != nil {
			return err
		}
		res, err := decode[map[string]string](raw)
		if err != nil {
			return err
		}
		return report(raw, "已在节点 %d 上装好并启动 %s %s", id, core, res["version"])
	},
}

var nodeDrainCmd = &cobra.Command{
	Use:   "drain <节点>",
	Short: "开始排水:新订阅不再发这台节点的入口",
	Long: `开始排水:新订阅不再发这台节点的入口。

排水是订阅层的开关:已连上的用户不会立刻断线,会在下一次订阅刷新时自然漂
走。想立刻掐线,请 node rm 或手动摘除 xray 上的用户。

取消排水用 node undrain。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], true)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", nodePath(id)+"/drain", nil)
		if err != nil {
			return err
		}
		return report(raw, "节点 %d 已开始排水", id)
	},
}

var nodeUndrainCmd = &cobra.Command{
	Use:   "undrain <节点>",
	Short: "取消排水",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], true)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", nodePath(id)+"/undrain", nil)
		if err != nil {
			return err
		}
		return report(raw, "节点 %d 已取消排水", id)
	},
}

// nodeOnlineCmd 现场问节点此刻每个用户连着几个 IP。
//
// 这是除了 node status 之外唯一会真发 ssh 的 node 子命令。它不落库、不缓存 —
// 在线名单下一秒就过期,存它没意义。要趋势请自己轮询,或者看 mpx status。
var nodeOnlineCmd = &cobra.Command{
	Use:   "online <节点>",
	Short: "现场读出节点上每个用户此刻的在线 IP 数",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], false)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", nodePath(id)+"/online", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		res, err := decode[struct {
			Node   string `json:"node"`
			Online []struct {
				User  string   `json:"user"`
				Count int      `json:"count"`
				IPs   []string `json:"ips"`
			} `json:"online"`
		}](raw)
		if err != nil {
			return err
		}
		if len(res.Online) == 0 {
			fmt.Println("节点 " + res.Node + " 上没有在线用户")
			return nil
		}
		fmt.Printf("节点 %s 在线 %d 个用户\n", res.Node, len(res.Online))
		for _, e := range res.Online {
			fmt.Printf("  %s  %d IP  %s\n", e.User, e.Count, strings.Join(e.IPs, ", "))
		}
		return nil
	},
}

// nodePath 拼出节点资源的路径。
func nodePath(id int64) string {
	return "/api/nodes/" + strconv.FormatInt(id, 10)
}
