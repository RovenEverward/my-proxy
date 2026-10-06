package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "管理入站对外暴露的方式",
}

var hostHeaders = []string{"ID", "入站", "备注", "地址", "端口", "SNI", "Host 头", "路径", "ALPN"}

func init() {
	hostCmd.AddCommand(hostListCmd, hostAddCmd, hostRmCmd)

	hostListCmd.Flags().Int64Var(&hostListInbound, "inbound", 0, "所属入站 id,必填")

	hostAddCmd.Flags().Int64Var(&hostAddFlags.inbound, "inbound", 0, "所属入站 id,必填")
	hostAddCmd.Flags().StringVar(&hostAddFlags.remark, "remark", "", "客户端看到的节点名,支持 {USERNAME} 等变量")
	hostAddCmd.Flags().StringVar(&hostAddFlags.address, "address", "", "对外地址,空表示用节点地址")
	hostAddCmd.Flags().IntVar(&hostAddFlags.port, "port", 0, "对外端口,0 表示用入站端口")
	hostAddCmd.Flags().StringVar(&hostAddFlags.sni, "sni", "", "TLS SNI")
	hostAddCmd.Flags().StringVar(&hostAddFlags.host, "host", "", "ws / grpc 的 Host 头")
	hostAddCmd.Flags().StringVar(&hostAddFlags.path, "path", "", "ws 路径")
	hostAddCmd.Flags().StringVar(&hostAddFlags.alpn, "alpn", "", "ALPN,逗号分隔")
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

// hostRow 里留空的项按"-"显示:这些字段本来就允许空,空着代表回落到
// 入站或节点上的值,表格里没必要替换成别的数字误导使用者。
func hostRow(h model.Host) []string {
	port := ""
	if h.Port != 0 {
		port = strconv.Itoa(h.Port)
	}
	return []string{
		strconv.FormatInt(h.ID, 10),
		strconv.FormatInt(h.InboundID, 10),
		h.Remark,
		h.Address,
		port,
		h.SNI,
		h.Host,
		h.Path,
		strings.Join(h.ALPN, ","),
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
		hosts, err := decode[[]model.Host](raw)
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
	Args:  cobra.NoArgs,
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
		created, err := decode[model.Host](raw)
		if err != nil {
			return err
		}
		return report(raw, "已创建对外入口 %d", created.ID)
	},
}

var hostRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "删除一个对外入口",
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
		raw, err := c.do("DELETE", "/api/hosts/"+strconv.FormatInt(id, 10), nil)
		if err != nil {
			return err
		}
		return report(raw, "已删除对外入口 %d", id)
	},
}
