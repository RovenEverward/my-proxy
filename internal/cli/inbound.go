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
	Short: "管理节点上的入站",
	Long: `管理节点上的入站 —— 客户端连上来的那一种协议。

一个入站 = 节点上的一段监听(协议 + 传输 + 安全层 + 端口)。它自己就带着连接
参数,所以"入站建好了"不等于"订阅里出现了":入站没有配对外入口时,订阅会直接
用入站自己的地址与端口(见 mpx host --help)。

几个约束是服务端强制、本地看不出来的:REALITY 只支持 tcp 传输且必须填 SNI 与
回落目标;TLS 必须给证书与私钥在节点上的路径;flow 只有 VLESS 支持;gRPC 传输
必须给 serviceName。REALITY 的密钥对由控制面自己生成,不用你填。

建完入站记得 mpx node sync,否则改动只落在库里、没进节点上的 config.json。`,
}

var inboundHeaders = []string{"ID", "节点", "TAG", "协议", "传输", "安全", "监听", "端口", "上限", "参数"}

func init() {
	inboundCmd.AddCommand(inboundListCmd, inboundShowCmd, inboundAddCmd, inboundEditCmd, inboundRmCmd)

	inboundListCmd.Flags().Var(nodeFlag(&inboundListNode), "node", "只看这个节点上的入站(id、名字或唯一前缀)")

	inboundAddCmd.Flags().Var(nodeFlag(&inboundAddFlags.node), "node", "所属节点,必填(id、名字或唯一前缀)")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.tag, "tag", "", "入站 tag,空表示按协议与端口生成")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.protocol, "protocol", "", "协议:vless / vmess / trojan / shadowsocks / wireguard / hysteria2 / tuic,必填")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.listen, "listen", "", "监听地址,空表示 0.0.0.0")
	inboundAddCmd.Flags().IntVar(&inboundAddFlags.port, "port", 0, "监听端口,必填")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.network, "network", string(model.NetworkTCP), "传输方式:tcp / ws / grpc / xhttp / splithttp / httpupgrade / udp;QUIC 默认 udp")
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.security, "security", string(model.SecurityNone), "安全层:none / tls / reality;QUIC 默认 tls")
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
	inboundAddCmd.Flags().StringVar(&inboundAddFlags.congestion, "congestion-control", "", "TUIC 拥塞控制:bbr / cubic / new_reno,空表示 bbr")
	inboundAddCmd.Flags().IntVar(&inboundAddFlags.maxUsers, "max-users", 0, "该入站能容纳的用户数上限,0 表示不限")

	inboundEditCmd.Flags().StringVar(&inboundEditFlags.tag, "tag", "", "新的入站 tag")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.protocol, "protocol", "", "新的协议:vless / vmess / trojan / shadowsocks / wireguard / hysteria2 / tuic")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.listen, "listen", "", "新的监听地址,空表示 0.0.0.0")
	inboundEditCmd.Flags().IntVar(&inboundEditFlags.port, "port", 0, "新的监听端口")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.network, "network", "", "新的传输方式:tcp / ws / grpc / xhttp / splithttp / httpupgrade / udp")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.security, "security", "", "新的安全层:none / tls / reality")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.wsPath, "ws-path", "", "ws 传输的路径")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.grpcService, "grpc-service", "", "gRPC 传输的 serviceName")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.sni, "sni", "", "TLS / REALITY 的 SNI")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.alpn, "alpn", "", "ALPN,逗号分隔;给空串表示清空")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.fingerprint, "fingerprint", "", "客户端 uTLS 指纹")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.certFile, "cert-file", "", "TLS 证书在节点上的路径")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.keyFile, "key-file", "", "TLS 私钥在节点上的路径")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.realityDest, "reality-dest", "", "REALITY 回落目标,如 www.microsoft.com:443")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.shortIDs, "short-ids", "", "REALITY shortId,逗号分隔;给空串表示清空")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.flow, "flow", "", "VLESS flow,如 xtls-rprx-vision")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.xhttpPath, "xhttp-path", "", "xhttp / splithttp / httpupgrade 的路径")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.xhttpHost, "xhttp-host", "", "xhttp / splithttp / httpupgrade 的 Host")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.xhttpMode, "xhttp-mode", "", "xhttp mode:auto / packet-up / stream-up / stream-one")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.wgAddress, "wg-address", "", "wireguard 服务端隧道地址,如 10.0.0.1/24")
	inboundEditCmd.Flags().IntVar(&inboundEditFlags.wgMTU, "wg-mtu", 0, "wireguard MTU")
	inboundEditCmd.Flags().BoolVar(&inboundEditFlags.mux, "mux", false, "订阅里给客户端开 mux")
	inboundEditCmd.Flags().BoolVar(&inboundEditFlags.fragment, "fragment", false, "订阅里给客户端开分片(仅 xray)")
	inboundEditCmd.Flags().StringVar(&inboundEditFlags.congestion, "congestion-control", "", "TUIC 拥塞控制:bbr / cubic / new_reno")
	inboundEditCmd.Flags().IntVar(&inboundEditFlags.maxUsers, "max-users", 0, "该入站能容纳的用户数上限,0 表示不限")
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
	maxUsers    int
	congestion  string
}

// maxUsersCell 把入站的用户数上限说成一个词:0 表示不限,不打印 0 让人误读成
// 「这个入站装不下任何人」。
func maxUsersCell(max int) string {
	if max <= 0 {
		return "不限"
	}
	return strconv.Itoa(max)
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
	if in.CongestionControl != "" {
		parts = append(parts, "congestion="+in.CongestionControl)
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
		maxUsersCell(in.MaxUsers),
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
	Use:   "show <入站>",
	Short: "显示一个入站的全部字段",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveInboundID(args[0], false)
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
			{"拥塞控制", in.CongestionControl},
			{"xhttp 路径", in.XHTTPPath},
			{"xhttp Host", in.XHTTPHost},
			{"xhttp mode", in.XHTTPMode},
			{"wg 私钥", in.WGSecretKey},
			{"wg 隧道地址", in.WGAddress},
			{"wg MTU", strconv.Itoa(in.WGMTU)},
			{"mux", strconv.FormatBool(in.Mux)},
			{"fragment", strconv.FormatBool(in.Fragment)},
			{"用户上限", maxUsersCell(in.MaxUsers)},
		})
	},
}

var inboundAddCmd = &cobra.Command{
	Use:   "add",
	Short: "新增一个入站",
	Example: `  # 最常用的一种:VLESS + TCP + REALITY,监听 443
  mpx inbound add --node 1 --protocol vless --port 443 \
      --security reality --sni www.cloudflare.com --reality-dest www.cloudflare.com:443 \
      --flow xtls-rprx-vision

  # 套 CDN 就用 ws:入站监听本地端口,入口再写对外域名与路径
  mpx inbound add --node 1 --protocol vless --port 8080 \
      --network ws --ws-path /ws

  # 建完把配置推到节点上,否则只在库里
  mpx node sync 1`,
	Args: cobra.NoArgs,
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
			NodeID:            inboundAddFlags.node,
			Tag:               inboundAddFlags.tag,
			Protocol:          model.Protocol(inboundAddFlags.protocol),
			Listen:            inboundAddFlags.listen,
			Port:              inboundAddFlags.port,
			Network:           model.Network(inboundAddFlags.network),
			Security:          model.Security(inboundAddFlags.security),
			WSPath:            inboundAddFlags.wsPath,
			GRPCService:       inboundAddFlags.grpcService,
			SNI:               inboundAddFlags.sni,
			ALPN:              splitCSV(inboundAddFlags.alpn),
			Fingerprint:       inboundAddFlags.fingerprint,
			CertFile:          inboundAddFlags.certFile,
			KeyFile:           inboundAddFlags.keyFile,
			RealityDest:       inboundAddFlags.realityDest,
			ShortIDs:          splitCSV(inboundAddFlags.shortIDs),
			Flow:              inboundAddFlags.flow,
			XHTTPPath:         inboundAddFlags.xhttpPath,
			XHTTPHost:         inboundAddFlags.xhttpHost,
			XHTTPMode:         inboundAddFlags.xhttpMode,
			WGAddress:         inboundAddFlags.wgAddress,
			WGMTU:             inboundAddFlags.wgMTU,
			Mux:               inboundAddFlags.mux,
			Fragment:          inboundAddFlags.fragment,
			MaxUsers:          inboundAddFlags.maxUsers,
			CongestionControl: inboundAddFlags.congestion,
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		if in.Protocol.QUIC() {
			if !cmd.Flags().Changed("network") {
				in.Network = model.NetworkUDP
			}
			if !cmd.Flags().Changed("security") {
				in.Security = model.SecurityTLS
			}
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
	Use:   "rm <入站>",
	Short: "删除一个入站",
	Long: `删除一个入站 —— 不可逆。

挂在它上面的对外入口会一起删掉,没有确认提示。这个入站的协议参数随之消失,
用它连着的客户端在下次订阅刷新后就没了入口 —— 除非你同时 node sync 把节点上的
config.json 重写一遍,否则节点上的 xray 还会照旧监听一段时间。

确认入站 id 用 mpx inbound list。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveInboundID(args[0], true)
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

// inboundEditFlags 是 inbound edit 的可选字段。
//
// 和 node edit 一样,只认 Changed() 过的 flag:入站有二十多个字段,要求调用方
// 把没改的抄一遍既啰嗦又容易抄错。
var inboundEditFlags struct {
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
	maxUsers    int
	congestion  string
}

var inboundEditCmd = &cobra.Command{
	Use:   "edit <入站>",
	Short: "改一个入站",
	Long: `改一个入站。只改给出来的字段,没给的保持原样。

  mpx inbound edit 7 --port 8443
  mpx inbound edit 7 --max-users 100 --sni example.com

改结构(端口、协议、传输、安全层、证书)之后节点上还是旧的,要跑一次
mpx node sync 才生效,并且会重启 xray 断开现有连接。只改 --max-users 不用
下发:它只在订阅层生效。

改协议或安全层时,服务端自己那侧的密钥(REALITY 密钥对、wireguard 私钥)
在原本就是同一协议的情况下会原样留住;换过协议再换回来则是新的一对,客户端
要用新配置。

--alpn / --short-ids 给空串表示清空这个列表,不给则是保持原样。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveInboundID(args[0], true)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		// 读-改-写:服务端那条 PUT 是整体替换,只发改动的字段会把别的清掉。
		// 入站字段多,这一点比节点更要紧。
		raw, err := c.do("GET", inboundPath(id), nil)
		if err != nil {
			return err
		}
		in, err := decode[model.Inbound](raw)
		if err != nil {
			return err
		}
		oldPort := in.Port
		oldNetwork := in.Network
		oldSecurity := in.Security

		changed := func(name string) bool { return cmd.Flags().Changed(name) }
		if changed("tag") {
			in.Tag = inboundEditFlags.tag
		}
		if changed("protocol") {
			in.Protocol = model.Protocol(inboundEditFlags.protocol)
			if in.Protocol.QUIC() {
				if !changed("network") {
					in.Network = model.NetworkUDP
				}
				if !changed("security") {
					in.Security = model.SecurityTLS
				}
			}
		}
		if changed("listen") {
			in.Listen = inboundEditFlags.listen
		}
		if changed("port") {
			in.Port = inboundEditFlags.port
		}
		if changed("network") {
			in.Network = model.Network(inboundEditFlags.network)
		}
		if changed("security") {
			in.Security = model.Security(inboundEditFlags.security)
		}
		if changed("ws-path") {
			in.WSPath = inboundEditFlags.wsPath
		}
		if changed("grpc-service") {
			in.GRPCService = inboundEditFlags.grpcService
		}
		if changed("sni") {
			in.SNI = inboundEditFlags.sni
		}
		if changed("alpn") {
			in.ALPN = splitCSV(inboundEditFlags.alpn)
		}
		if changed("fingerprint") {
			in.Fingerprint = inboundEditFlags.fingerprint
		}
		if changed("cert-file") {
			in.CertFile = inboundEditFlags.certFile
		}
		if changed("key-file") {
			in.KeyFile = inboundEditFlags.keyFile
		}
		if changed("reality-dest") {
			in.RealityDest = inboundEditFlags.realityDest
		}
		if changed("short-ids") {
			in.ShortIDs = splitCSV(inboundEditFlags.shortIDs)
		}
		if changed("flow") {
			in.Flow = inboundEditFlags.flow
		}
		if changed("xhttp-path") {
			in.XHTTPPath = inboundEditFlags.xhttpPath
		}
		if changed("xhttp-host") {
			in.XHTTPHost = inboundEditFlags.xhttpHost
		}
		if changed("xhttp-mode") {
			in.XHTTPMode = inboundEditFlags.xhttpMode
		}
		if changed("wg-address") {
			in.WGAddress = inboundEditFlags.wgAddress
		}
		if changed("wg-mtu") {
			in.WGMTU = inboundEditFlags.wgMTU
		}
		if changed("mux") {
			in.Mux = inboundEditFlags.mux
		}
		if changed("fragment") {
			in.Fragment = inboundEditFlags.fragment
		}
		if changed("congestion-control") {
			in.CongestionControl = inboundEditFlags.congestion
		}
		if changed("max-users") {
			in.MaxUsers = inboundEditFlags.maxUsers
		}

		raw, err = c.do("PUT", inboundPath(id), in)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		updated, err := decode[model.Inbound](raw)
		if err != nil {
			return err
		}
		if err := report(raw, "已更新入站 %d(%s)", updated.ID, updated.TagOrDefault()); err != nil {
			return err
		}
		// 改了端口 / 传输 / 安全层 = 节点上那份 config.json 已经对不上了。
		if in.Port != oldPort || in.Network != oldNetwork || in.Security != oldSecurity {
			fmt.Println("注意:改的是节点配置,节点上还是旧的;跑 mpx node sync " +
				strconv.FormatInt(updated.NodeID, 10) + " 才生效(会重启对应核心)")
		}
		return nil
	},
}

// inboundPath 拼出入站资源的路径。
func inboundPath(id int64) string {
	return "/api/inbounds/" + strconv.FormatInt(id, 10)
}
