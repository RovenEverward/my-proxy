package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var inboundCmd = &cobra.Command{
	Use:   "inbound",
	Short: "管理节点上的 xray 入站",
}

var inboundHeaders = []string{"ID", "节点", "TAG", "协议", "传输", "安全", "监听", "端口", "参数"}

func init() {
	inboundCmd.AddCommand(inboundListCmd, inboundShowCmd, inboundAddCmd, inboundRmCmd)

	inboundListCmd.Flags().Int64Var(&inboundListNode, "node", 0, "只看这个节点上的入站")

	inboundAddCmd.Flags().Int64Var(&inboundAddFlags.node, "node", 0, "所属节点 id,必填")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.tag, "tag", "", "入站 tag,空表示按协议与端口生成")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.protocol, "protocol", "", "协议:vless / vmess / trojan / shadowsocks / wireguard,必填")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.listen, "listen", "", "监听地址,空表示 0.0.0.0")
	inboundAddCmd.Flags().IntVar(&inboundAddFlags.port, "port", 0, "监听端口,必填")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.network, "network", string(model.NetworkTCP), "传输方式:tcp / ws / grpc / xhttp / splithttp / httpupgrade")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.security, "security", string(model.SecurityNone), "安全层:none / tls / reality")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.wsPath, "ws-path", "", "ws 传输的路径")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.grpcService, "grpc-service", "", "gRPC 传输的 serviceName")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.sni, "sni", "", "TLS / REALITY 的 SNI")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.alpn, "alpn", "", "ALPN,逗号分隔")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.fingerprint, "fingerprint", "", "客户端 uTLS 指纹,空表示 chrome")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.certFile, "cert-file", "", "TLS 证书在节点上的路径")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.keyFile, "key-file", "", "TLS 私钥在节点上的路径")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.realityDest, "reality-dest", "", "REALITY 回落目标,如 www.microsoft.com:443")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.shortIDs, "short-ids", "", "REALITY shortId,逗号分隔")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.flow, "flow", "", "VLESS flow,如 xtls-rprx-vision")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.xhttpPath, "xhttp-path", "", "xhttp / splithttp / httpupgrade 的路径")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.xhttpHost, "xhttp-host", "", "xhttp / splithttp / httpupgrade 的 Host")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.xhttpMode, "xhttp-mode", "", "xhttp mode:auto / packet-up / stream-up / stream-one")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.wgAddress, "wg-address", "", "wireguard 服务端隧道地址,如 10.0.0.1/24")
	inboundAddCmd.Flags().IntVar(&inboundAddFlags.wgMTU, "wg-mtu", 1420, "wireguard MTU")
	inboundAddCmd.Flags().BoolVar(&inboundAddFlags.mux, "mux", false, "订阅里给客户端开 mux")
	inboundAddCmd.Flags().BoolVar(&inboundAddFlags.fragment, "fragment", false, "订阅里给客户端开分片(仅 xray)")
}

var inboundListNode int64

var inboundAddFlags struct {
	node        int64
	tag         string
	protocol    string
	listen      string
	port        int
	network     string
	security    string
	wsPath      string
	grpcService string
	sni         string
	alpn        string
	fingerprint string
	certFile    string
	keyFile     string
	realityDest string
	shortIDs    string
	flow        string
	xhttpPath   string
	xhttpHost   string
	xhttpMode   string
	wgAddress   string
	wgMTU       int
	mux         bool
	fragment    bool
}

// inboundDetail 把传输与安全层的几个可选参数压成一列,免得表格宽到看不完。
func inboundDetail(in model.Inbound) string {
	var parts []string
	if in.WSPath != "" {
		parts = append(parts, "path="+in.WSPath)
	}
	if in.GRPCService != "" {
		parts = append(parts, "service="+in.GRPCService)
	}
	if in.XHTTPPath != "" {
		parts = append(parts, "xpath="+in.XHTTPPath)
	}
	if in.XHTTPHost != "" {
		parts = append(parts, "xhost="+in.XHTTPHost)
	}
	if in.XHTTPMode != "" {
		parts = append(parts, "xmode="+in.XHTTPMode)
	}
	if in.WGAddress != "" {
		parts = append(parts, "wgaddr="+in.WGAddress)
	}
	if in.SNI != "" {
		parts = append(parts, "sni="+in.SNI)
	}
	if len(in.ALPN) > 0 {
		parts = append(parts, "alpn="+strings.Join(in.ALPN, ","))
	}
	if in.Flow != "" {
		parts = append(parts, "flow="+in.Flow)
	}
	return strings.Join(parts, " ")
}

func inboundRow(in model.Inbound) []string {
	return []string{
		strconv.FormatInt(in.ID, 10),
		strconv.FormatInt(in.NodeID, 10),
		in.TagOrDefault(),
		string(in.Protocol),
		string(in.Network),
		string(in.Security),
		in.ListenOrDefault(),
		strconv.Itoa(in.Port),
		inboundDetail(in),
	}
}

var inboundListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有入站",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		path := "/api/inbounds"
		if inboundListNode != 0 {
			path += "?node_id=" + strconv.FormatInt(inboundListNode, 10)
		}
		raw, err := c.do("GET", path, nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		inbounds, err := decode[[]model.Inbound](raw)
		if err != nil {
			return err
		}
		rows := make([][]string, 0, len(inbounds))
		for _, in := range inbounds {
			rows = append(rows, inboundRow(in))
		}
		return writeTable(inboundHeaders, rows)
	},
}

var inboundShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "显示一个入站的全部字段",
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
		raw, err := c.do("GET", inboundPath(id), nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		in, err := decode[model.Inbound](raw)
		if err != nil {
			return err
		}
		return writeKV([][2]string{
			{"ID", strconv.FormatInt(in.ID, 10)},
			{"节点", strconv.FormatInt(in.NodeID, 10)},
			{"TAG", in.TagOrDefault()},
			{"协议", string(in.Protocol)},
			{"监听", in.ListenOrDefault()},
			{"端口", strconv.Itoa(in.Port)},
			{"传输", string(in.Network)},
			{"安全层", string(in.Security)},
			{"ws 路径", in.WSPath},
			{"gRPC serviceName", in.GRPCService},
			{"SNI", in.SNI},
			{"ALPN", strings.Join(in.ALPN, ",")},
			{"uTLS 指纹", in.Fingerprint},
			{"证书路径", in.CertFile},
			{"私钥路径", in.KeyFile},
			{"REALITY 回落目标", in.RealityDest},
			{"REALITY 私钥", in.RealityPriv},
			{"REALITY 公钥", in.RealityPub},
			{"REALITY shortId", strings.Join(in.ShortIDs, ",")},
			{"flow", in.Flow},
			{"xhttp 路径", in.XHTTPPath},
			{"xhttp Host", in.XHTTPHost},
			{"xhttp mode", in.XHTTPMode},
			{"wg 私钥", in.WGSecretKey},
			{"wg 隧道地址", in.WGAddress},
			{"wg MTU", strconv.Itoa(in.WGMTU)},
			{"mux", strconv.FormatBool(in.Mux)},
			{"fragment", strconv.FormatBool(in.Fragment)},
		})
	},
}

var inboundAddCmd = &cobra.Command{
	Use:   "add",
	Short: "新增一个入站",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// 服务端也会校验,这里先拦一道是为了省一次往返,并给出更具体的提示。
		if inboundAddFlags.node == 0 {
			return errors.New("必须给 --node")
		}
		if inboundAddFlags.protocol == "" {
			return errors.New("必须给 --protocol")
		}
		if inboundAddFlags.port < 1 || inboundAddFlags.port > 65535 {
			return fmt.Errorf("--port 必须在 1-65535 之间,收到 %d", inboundAddFlags.port)
		}
		in := model.Inbound{
			NodeID:      inboundAddFlags.node,
			Tag:         inboundAddFlags.tag,
			Protocol:    model.Protocol(inboundAddFlags.protocol),
			Listen:      inboundAddFlags.listen,
			Port:        inboundAddFlags.port,
			Network:     model.Network(inboundAddFlags.network),
			Security:    model.Security(inboundAddFlags.security),
			WSPath:      inboundAddFlags.wsPath,
			GRPCService: inboundAddFlags.grpcService,
			SNI:         inboundAddFlags.sni,
			ALPN:        splitCSV(inboundAddFlags.alpn),
			Fingerprint: inboundAddFlags.fingerprint,
			CertFile:    inboundAddFlags.certFile,
			KeyFile:     inboundAddFlags.keyFile,
			RealityDest: inboundAddFlags.realityDest,
			ShortIDs:    splitCSV(inboundAddFlags.shortIDs),
			Flow:        inboundAddFlags.flow,
			XHTTPPath:   inboundAddFlags.xhttpPath,
			XHTTPHost:   inboundAddFlags.xhttpHost,
			XHTTPMode:   inboundAddFlags.xhttpMode,
			WGAddress:   inboundAddFlags.wgAddress,
			WGMTU:       inboundAddFlags.wgMTU,
			Mux:         inboundAddFlags.mux,
			Fragment:    inboundAddFlags.fragment,
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", "/api/inbounds", in)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		created, err := decode[model.Inbound](raw)
		if err != nil {
			return err
		}
		return report(raw, "已创建入站 %d(%s)", created.ID, created.TagOrDefault())
	},
}

var inboundRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "删除一个入站",
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
		raw, err := c.do("DELETE", inboundPath(id), nil)
		if err != nil {
			return err
		}
		return report(raw, "已删除入站 %d", id)
	},
}

// inboundPath 拼出入站资源的路径。
func inboundPath(id int64) string {
	return "/api/inbounds/" + strconv.FormatInt(id, 10)
}
