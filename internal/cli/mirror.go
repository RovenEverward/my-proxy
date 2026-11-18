package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 订阅镜像(UC-10):在名单内一台节点上开放 /sub 入口。节点侧只是核心自带的
// TCP 转发(dokodemo-door / direct),不装 MPX 组件;订阅内容与鉴权以控制面
// 为唯一真相。加/删/改上游都会下发到节点并重启核心 —— 结构性变更不走周期
// 收敛(同 node sync 的规矩)。

var mirrorCmd = &cobra.Command{
	Use:   "mirror",
	Short: "管理订阅镜像(在节点上开 /sub 入口)",
	Long: `管理订阅镜像 —— 让 /sub 不只有控制面一个入口。

镜像把订阅地址挂到名单内一台节点的公开端口上,客户端拿到
http://<节点地址>:<端口>/sub/<token>,内容与其他入口一致。节点上
不装 MPX 组件:用的是核心自带的 TCP 转发。

加、删、改上游都会把配置下发到节点并重启核心(同 node sync)。
想先看再手动下发,加 --sync=false 只登记,之后跑 mpx node sync <节点>。

控制面迁移后,把既有镜像的 upstream 指到新控制面:
  mpx mirror set <id> --upstream <新控制面>:8081`,
}

var mirrorAddFlags struct {
	port     int
	upstream string
	remark   string
	sync     bool
}

var mirrorSetFlags struct {
	upstream string
	remark   string
	sync     bool
}

var mirrorRmFlags struct {
	sync bool
}

var mirrorCmdList = &cobra.Command{
	Use:     "list",
	Short:   "列出全部订阅镜像",
	Example: "  mpx mirror list",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/mirrors", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		type view struct {
			model.SubMirror
			Node     string `json:"node"`
			Endpoint string `json:"endpoint"`
		}
		list, err := decode[[]view](raw)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("还没有订阅镜像。用 mpx mirror add <节点> 开一个。")
			return nil
		}
		rows := make([][]string, 0, len(list))
		for _, m := range list {
			name := m.Node
			if m.Remark != "" {
				name += "(" + m.Remark + ")"
			}
			rows = append(rows, []string{strconv.FormatInt(m.ID, 10), name,
				strconv.Itoa(m.Port), m.Upstream, m.Endpoint})
		}
		fmt.Println(tableText([]string{"ID", "节点", "端口", "上游", "入口"}, rows))
		return nil
	},
}

var mirrorCmdAdd = &cobra.Command{
	Use:   "add <节点>",
	Short: "在一台节点上开订阅入口",
	Long: `在一台节点上开订阅入口。端口默认 18080,上游默认用订阅前缀推出来
(没有的话用本次请求的控制面地址)。加完默认下发到节点并重启核心。`,
	Example: `  # 在 jp-1 上开 18080
  mpx mirror add jp-1

  # 换端口、备注、先不下发
  mpx mirror add jp-1 --port 18081 --remark 东京 --sync=false

  # 手动指定上游(比如控制面换了机器)
  mpx mirror add jp-1 --upstream 177.5.75.195:8081`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := resolveNodeID(args[0], true)
		if err != nil {
			return err
		}
		if mirrorAddFlags.port < 1 || mirrorAddFlags.port > 65535 {
			return fmt.Errorf("--port 必须在 1-65535 之间,收到 %d", mirrorAddFlags.port)
		}
		raw, err := c.do("POST", "/api/mirrors", map[string]any{
			"node_id": id, "port": mirrorAddFlags.port,
			"upstream": mirrorAddFlags.upstream, "remark": mirrorAddFlags.remark,
			"sync": mirrorAddFlags.sync,
		})
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		resp, err := decode[struct {
			ID       int64  `json:"id"`
			Node     string `json:"node"`
			Port     int    `json:"port"`
			Upstream string `json:"upstream"`
			Endpoint string `json:"endpoint"`
			Synced   bool   `json:"synced"`
			Warn     string `json:"warn"`
		}](raw)
		if err != nil {
			return err
		}
		if resp.Warn != "" {
			fmt.Println("警告: " + resp.Warn)
		}
		fmt.Printf("已在 %s 上开订阅入口 %s(上游 %s)%s\n",
			resp.Node, resp.Endpoint, resp.Upstream, syncNote(resp.Synced))
		fmt.Println("用户订阅地址: " + resp.Endpoint + "/sub/<token>")
		return nil
	},
}

var mirrorCmdSet = &cobra.Command{
	Use:   "set <id>",
	Short: "改镜像的上游或备注(控制面迁移后用)",
	Example: `  # 控制面搬到新机器后,把镜像指过去
  mpx mirror set 1 --upstream 177.5.75.195:8081

  # 只改备注,不下发
  mpx mirror set 1 --remark 东京 --sync=false`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("镜像 id 必须是数字,收到 %q", args[0])
		}
		body := map[string]any{"sync": mirrorSetFlags.sync}
		if mirrorSetFlags.upstream != "" {
			body["upstream"] = mirrorSetFlags.upstream
		}
		if mirrorSetFlags.remark != "" {
			body["remark"] = mirrorSetFlags.remark
		}
		raw, err := c.do("PUT", "/api/mirrors/"+strconv.FormatInt(id, 10), body)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		fmt.Printf("镜像 %d 已更新%s\n", id, syncNote(mirrorSetFlags.sync))
		return nil
	},
}

var mirrorCmdRm = &cobra.Command{
	Use:   "rm <id>",
	Short: "删一个订阅镜像",
	Example: `  mpx mirror rm 1
  mpx mirror rm 1 --sync=false   # 先删登记,稍后跑 mpx node sync`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("镜像 id 必须是数字,收到 %q", args[0])
		}
		path := "/api/mirrors/" + strconv.FormatInt(id, 10)
		if !mirrorRmFlags.sync {
			path += "?sync=0"
		}
		raw, err := c.do("DELETE", path, nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		fmt.Printf("镜像 %d 已删除\n", id)
		return nil
	},
}

func syncNote(synced bool) string {
	if synced {
		return ""
	}
	return "(未下发,跑 mpx node sync <节点> 生效)"
}

func init() {
	rootCmd.AddCommand(mirrorCmd)
	mirrorCmd.AddCommand(mirrorCmdList, mirrorCmdAdd, mirrorCmdSet, mirrorCmdRm)
	mirrorCmdAdd.Flags().IntVarP(&mirrorAddFlags.port, "port", "p", 18080, "节点对外监听的端口")
	mirrorCmdAdd.Flags().StringVar(&mirrorAddFlags.upstream, "upstream", "", "转发目标 host:port(默认按订阅前缀/控制面推)")
	mirrorCmdAdd.Flags().StringVar(&mirrorAddFlags.remark, "remark", "", "入口备注(展示在订阅页)")
	mirrorCmdAdd.Flags().BoolVar(&mirrorAddFlags.sync, "sync", true, "建完立即下发到节点(重启核心)")
	mirrorCmdSet.Flags().StringVar(&mirrorSetFlags.upstream, "upstream", "", "新的转发目标 host:port")
	mirrorCmdSet.Flags().StringVar(&mirrorSetFlags.remark, "remark", "", "入口备注")
	mirrorCmdSet.Flags().BoolVar(&mirrorSetFlags.sync, "sync", true, "改完立即下发")
	mirrorCmdRm.Flags().BoolVar(&mirrorRmFlags.sync, "sync", true, "删完立即下发到节点(重启核心)")
}
