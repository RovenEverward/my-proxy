package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "管理入站对外暴露的方式",
	Long: `管理入站对外暴露的方式 —— 客户端实际连的那个地址。

入站描述的是"节点上怎么听",入口描述的是"外面怎么写"。两者可以不一样:入站
监听 443,入口可以告诉客户端去连 cdn.example.com:8443;入口没填的字段一律回落到
入站自己的值(地址回落到节点地址,端口回落到入站端口,SNI 回落到入站 SNI)。

一个入站可以挂多个入口 —— 同一个入站既可以直连 IP,也可以走域名;反过来,一个
入站一个入口都不建也能用,订阅会拿入站自己的地址与端口。所以"订阅里有一堆重复
条目"通常是给同一个入站建了多个入口,而不是建多了入站。

入口只影响订阅里写什么,不改节点上 xray 的监听——改完不需要 node sync。`,
}

var hostHeaders = []string{"ID", "入站", "备注", "地址", "端口", "SNI", "Host 头", "路径", "ALPN"}

func init() {
	hostCmd.AddCommand(hostListCmd, hostAddCmd, hostEditCmd, hostRmCmd)

	hostListCmd.Flags().Int64Var(&hostListInbound, "inbound", 0, "所属入站 id,必填")

	hostAddCmd.Flags().Int64Var(&hostAddFlags.inbound, "inbound", 0, "所属入站 id,必填")
	hostAddCmd.Flags().StringVar(&hostAddFlags.remark, "remark", "", "客户端看到的节点名。变量:{SERVER_IP} {NODE_NAME} {PROTOCOL} {TRANSPORT};{USERNAME} 在这里被抹成空串")
	hostAddCmd.Flags().StringVar(&hostAddFlags.address, "address", "", "对外地址,空表示用节点地址")
	hostAddCmd.Flags().IntVar(&hostAddFlags.port, "port", 0, "对外端口,0 表示用入站端口")
	hostAddCmd.Flags().StringVar(&hostAddFlags.sni, "sni", "", "TLS SNI")
	hostAddCmd.Flags().StringVar(&hostAddFlags.host, "host", "", "ws / grpc 的 Host 头")
	hostAddCmd.Flags().StringVar(&hostAddFlags.path, "path", "", "ws 路径")
	hostAddCmd.Flags().StringVar(&hostAddFlags.alpn, "alpn", "", "ALPN,逗号分隔")

	hostEditCmd.Flags().Int64Var(&hostEditFlags.inbound, "inbound", 0, "所属入站 id,必填")
	hostEditCmd.Flags().StringVar(&hostEditFlags.remark, "remark", "", "客户端看到的节点名")
	hostEditCmd.Flags().StringVar(&hostEditFlags.address, "address", "", "对外地址,给空串表示回落成节点地址")
	hostEditCmd.Flags().IntVar(&hostEditFlags.port, "port", 0, "对外端口,给 0 表示回落成入站端口")
	hostEditCmd.Flags().StringVar(&hostEditFlags.sni, "sni", "", "TLS SNI")
	hostEditCmd.Flags().StringVar(&hostEditFlags.host, "host", "", "ws / grpc 的 Host 头")
	hostEditCmd.Flags().StringVar(&hostEditFlags.path, "path", "", "ws 路径")
	hostEditCmd.Flags().StringVar(&hostEditFlags.alpn, "alpn", "", "ALPN,逗号分隔;给空串表示清空")
}

var hostListInbound int64

var hostAddFlags struct {
	inbound int64
	remark  string
	address string
	port    int
	sni     string
	host    string
	path    string
	alpn    string
}

// hostRow 显示的是生效值,不是存进去的值:入口上留空的字段会回落到节点或入站,
// 光看自己填的那几个空,看不出这个入口最终会把客户端连到哪里。回落由服务端算
// (见 model.Exposure),CLI 不重复一遍。
//
// 生效值里仍然可能带 {VAR} 或 * —— 那些跟具体用户有关,得看某个用户的订阅才
// 知道,表格里原样显示。
func hostRow(h model.HostView) []string {
	port := ""
	if h.Effective.Port != 0 {
		port = strconv.Itoa(h.Effective.Port)
	}
	return []string{
		strconv.FormatInt(h.ID, 10),
		strconv.FormatInt(h.InboundID, 10),
		dash(h.Remark),
		dash(h.Effective.Address),
		dash(port),
		dash(h.Effective.SNI),
		dash(h.Effective.Host),
		dash(h.Effective.Path),
		dash(strings.Join(h.Effective.ALPN, ",")),
	}
}

var hostListCmd = &cobra.Command{
	Use:   "list --inbound ID",
	Short: "列出一个入站的对外入口",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if hostListInbound == 0 {
			return errors.New("必须给 --inbound")
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", inboundPath(hostListInbound)+"/hosts", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		hosts, err := decode[[]model.HostView](raw)
		if err != nil {
			return err
		}
		rows := make([][]string, 0, len(hosts))
		for _, h := range hosts {
			rows = append(rows, hostRow(h))
		}
		return writeTable(hostHeaders, rows)
	},
}

var hostAddCmd = &cobra.Command{
	Use:   "add --inbound ID",
	Short: "给一个入站加一种对外暴露方式",
	Example: `  # 让入站 1 对外走域名与 8443 端口(节点前面可能有反代)
  mpx host add --inbound 1 --address cdn.example.com --port 8443 --sni cdn.example.com

  # 给同一个入站再加一个直连 IP 的入口 —— 一个入站可以有多个入口
  mpx host add --inbound 1 --remark 直连

  # 看看这个入站现在有哪些入口
  mpx host list --inbound 1`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if hostAddFlags.inbound == 0 {
			return errors.New("必须给 --inbound")
		}
		h := model.Host{
			InboundID: hostAddFlags.inbound,
			Remark:    hostAddFlags.remark,
			Address:   hostAddFlags.address,
			Port:      hostAddFlags.port,
			SNI:       hostAddFlags.sni,
			Host:      hostAddFlags.host,
			Path:      hostAddFlags.path,
			ALPN:      splitCSV(hostAddFlags.alpn),
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", inboundPath(h.InboundID)+"/hosts", h)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		created, err := decode[model.HostView](raw)
		if err != nil {
			return err
		}
		return report(raw, "已创建对外入口 %d", created.ID)
	},
}

// hostEditFlags 是 host edit 的可选字段。
//
// 只认 Changed() 过的 flag。入口上"空"是有含义的(回落到节点/入站),所以
// 需要一个办法把字段显式改回空 —— 那就是 --address "" 这种写法,Changed()
// 一样认得出来。
var hostEditFlags struct {
	inbound int64
	remark  string
	address string
	port    int
	sni     string
	host    string
	path    string
	alpn    string
}

var hostEditCmd = &cobra.Command{
	Use:   "edit <id> --inbound ID",
	Short: "改一个对外入口",
	Long: `改一个对外入口。只改给出来的字段,没给的保持原样。

  mpx host edit 13 --inbound 2 --remark "东京 WS 备用"
  mpx host edit 13 --inbound 2 --address jp.example.com --port 443

--inbound 是必填的:控制面没有"按 id 读一个入口"的接口,要先把那个入站的
入口列出来才能找到它,所以得知道它属于哪个入站。

把某个字段显式改回空(回落成节点/入站的值)就传空串:
  mpx host edit 13 --inbound 2 --address ""

改地址或端口会改变订阅里发出去的入口,用户下次刷新订阅就跟着变;入站本身
不受影响,不用下发配置。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := idArg(args[0])
		if err != nil {
			return err
		}
		if hostEditFlags.inbound == 0 {
			return errors.New("必须给 --inbound(入口属于哪个入站)")
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		// 没有 GET /api/hosts/{id} 这个端点,只能从所属入站的列表里找出来。
		raw, err := c.do("GET", inboundPath(hostEditFlags.inbound)+"/hosts", nil)
		if err != nil {
			return err
		}
		hosts, err := decode[[]model.HostView](raw)
		if err != nil {
			return err
		}
		var found *model.Host
		for i := range hosts {
			if hosts[i].ID == id {
				h := hosts[i].Host
				found = &h
				break
			}
		}
		if found == nil {
			return fmt.Errorf("入站 %d 上没有 id 为 %d 的对外入口", hostEditFlags.inbound, id)
		}

		h := *found
		changed := func(name string) bool { return cmd.Flags().Changed(name) }
		if changed("remark") {
			h.Remark = hostEditFlags.remark
		}
		if changed("address") {
			h.Address = hostEditFlags.address
		}
		if changed("port") {
			h.Port = hostEditFlags.port
		}
		if changed("sni") {
			h.SNI = hostEditFlags.sni
		}
		if changed("host") {
			h.Host = hostEditFlags.host
		}
		if changed("path") {
			h.Path = hostEditFlags.path
		}
		if changed("alpn") {
			h.ALPN = splitCSV(hostEditFlags.alpn)
		}

		raw, err = c.do("PUT", "/api/hosts/"+strconv.FormatInt(id, 10), h)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		return report(raw, "已更新对外入口 %d", id)
	},
}

var hostRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "删除一个对外入口",
	Long: `删除一个对外入口 —— 不可逆,但没有确认提示。

它只影响订阅里写什么:客户端下次刷新订阅后这个地址就没了,节点上的 xray 与已经
连着的用户都不受影响。删掉最后一个入口也不会出事,订阅会回落到入站自己的地址与
端口。

确认入口 id 用 mpx host list --inbound <id>。`,
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
		raw, err := c.do("DELETE", "/api/hosts/"+strconv.FormatInt(id, 10), nil)
		if err != nil {
			return err
		}
		return report(raw, "已删除对外入口 %d", id)
	},
}
