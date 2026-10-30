// Package render 把领域对象渲染成三类产物:
//
//   - Endpoint:一个用户可连接节点入口的完整描述,即各客户端格式的公共输入
//   - 由 Endpoint 派生的客户端配置:分享链接、Clash、Clash.Meta、sing-box、xray
//   - 节点侧 xray 的 config.json
//
// 本包全是纯函数:不读文件、不联网、不看系统时间。同一个输入永远得到同一个
// 输出,渲染结果因此可以直接用表驱动测试比对。
package render

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/netip"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// DedupeRemarks 让一份订阅里的节点名互不相同,原地改 Remark 并返回同一个切片。
//
// Clash 拿 name 当主键、sing-box 拿 tag 当主键,重名会让节点在客户端里互相顶掉;
// 而两个节点上的入口取同一个 Remark 是件很自然的事(都会叫「香港」)。
// 它按输入顺序给后来者加序号,所以同一个输入永远得到同一个输出。
func DedupeRemarks(eps []Endpoint) []Endpoint {
	taken := make(map[string]bool, len(eps))
	for i := range eps {
		base := eps[i].Remark
		if base == "" {
			base = "节点"
		}
		name := base
		// 不能只判断「base 出现过」:base 加序号之后可能正好撞上另一个 base。
		for n := 2; taken[name]; n++ {
			name = base + " " + strconv.Itoa(n)
		}
		taken[name] = true
		eps[i].Remark = name
	}
	return eps
}

// uniqueTag 取一个不与 used 中任何名字冲突的名字,并把它记进 used。
//
// sing-box 的 outbound 与 xray 的 outbound 都拿名字当主键,重名会让客户端直接
// 起不来;而两个节点上的入口取同一个 Remark 是件很自然的事(都会叫「香港」)。
// base 为空时依次回落到地址与 "node",免得生成一个没有名字的出口。
func uniqueTag(base, fallback string, used map[string]struct{}) string {
	if base == "" {
		base = fallback
	}
	if base == "" {
		base = "node"
	}
	tag := base
	for i := 2; ; i++ {
		if _, ok := used[tag]; !ok {
			break
		}
		tag = base + "-" + strconv.Itoa(i)
	}
	used[tag] = struct{}{}
	return tag
}

// Endpoint 是一个用户可连接节点的完整描述。
//
// 它是渲染的中间表示:Resolve 负责产出它,各客户端格式只负责把它序列化。
// 新增一种客户端格式只需读它,不需要再碰领域模型。
type Endpoint struct {
	NodeID    int64
	InboundID int64
	HostID    int64 // zero identifies the default inbound exposure
	Remark    string
	// AutoRemark 表示 Remark 是默认生成的(host 没写 remark),可以加协议后缀。
	AutoRemark bool
	Address    string
	Port       int

	Protocol          model.Protocol
	UUID              string // vless / vmess / tuic
	Password          string // trojan / shadowsocks-2022 / hysteria2 / tuic 的用户密钥
	CongestionControl string // TUIC:空表示 bbr
	Flow              string // vless

	// Wireguard 的字段单独放:它是 L3,不需要传输/安全层,复用 Path/Host 会让
	// 渲染器分不清「该写哪个键」。
	WGPrivateKey string // 客户端自己的私钥(标准 base64 带填充)
	WGPublicKey  string // 服务端公钥
	WGAddress    string // 客户端隧道地址,如 10.0.0.2/32

	Network model.Network
	// Path 是 ws 的 path、grpc 的 serviceName,或 http 系传输的 path。
	Path string
	// Host 是 ws 的 Host 头、grpc 的 authority,或 http 系传输的 host。
	Host string
	// Mode 只对 xhttp 有意义:auto / packet-up / stream-up / stream-one。
	// 空表示 auto。
	Mode string

	Security      model.Security
	SNI           string
	ALPN          []string
	Fingerprint   string
	RealityPub    string // REALITY 公钥
	RealitySID    string // REALITY shortId
	AllowInsecure bool

	// 客户端侧特性:只写进订阅,不改变节点配置。
	Mux      bool // xray / sing-box 认识,mihomo 忽略
	Fragment bool // 只有 xray 客户端渲染
}

// Resolve 把节点上的入站展开成该用户可以连接的端点列表。
//
// 入站先按用户授权过滤;每个入站再展开成它的全部 Host,hosts 里没有该入站时
// 按入站自身与节点地址生成一个默认入口。Host 上留空的字段回落到入站或节点。
func Resolve(node model.Node, inbounds []model.Inbound, hosts map[int64][]model.Host, user *model.User) []Endpoint {
	return ResolveNamed(node, "", inbounds, hosts, user)
}

// ResolveNamed 同 Resolve,另带节点显示名(docs/internal/node-naming-rfc.md §5):
// host 没写 remark 时用它当默认名字,空时退回 node.Name;也供 {NODE_DISPLAY} 展开。
func ResolveNamed(node model.Node, display string, inbounds []model.Inbound, hosts map[int64][]model.Host, user *model.User) []Endpoint {
	uuid := model.DeriveUUID(user.Key)
	password := model.DerivePassword(user.Key)
	salt := saltOf(user)

	var out []Endpoint
	for _, in := range inbounds {
		if !user.AllowsInbound(in.ID) {
			continue
		}
		exposures := hosts[in.ID]
		if len(exposures) == 0 {
			exposures = []model.Host{{}}
		}
		for _, h := range exposures {
			if ep, ok := endpoint(node, display, in, h, user, uuid, password, salt); ok {
				out = append(out, ep)
			}
		}
	}
	return out
}

// endpoint 把 (节点, 入站, 对外入口) 合成一个 Endpoint,并展开其中的变量。
// 返回 ok=false 表示该端点的关键字段无法生成(如密钥派生失败),跳过而不是发一个坏配置。
func endpoint(node model.Node, display string, in model.Inbound, h model.Host, user *model.User, uuid, password, salt string) (Endpoint, bool) {
	if display == "" {
		display = node.Name
	}
	vars := map[string]string{
		"USERNAME":     "",
		"SERVER_IP":    node.Address,
		"NODE_NAME":    node.Name,
		"NODE_DISPLAY": display,
		"PROTOCOL":     string(in.Protocol),
		"TRANSPORT":    string(in.Network),
	}

	ex := ResolveExposure(node, in, h)

	remark := expand(h.Remark, vars, salt)
	auto := remark == ""
	if auto {
		remark = expand(display, vars, salt)
	}

	ep := Endpoint{
		NodeID: node.ID, InboundID: in.ID, HostID: h.ID,
		Remark:     remark,
		AutoRemark: auto,
		Address:    expand(ex.Address, vars, salt),
		Port:       ex.Port,

		Protocol: in.Protocol,
		UUID:     uuid,
		Password: password,
		Flow:     in.Flow,

		Network: in.Network,
		Path:    ex.Path,
		Host:    expand(ex.Host, vars, salt),
		Mode:    in.XHTTPMode,

		Security:    in.Security,
		SNI:         expand(ex.SNI, vars, salt),
		ALPN:        ex.ALPN,
		Fingerprint: firstNonEmpty(in.Fingerprint, "chrome"),
		RealityPub:  in.RealityPub,
		RealitySID:  firstShortID(in.ShortIDs),

		Mux:      in.Mux,
		Fragment: in.Fragment,
	}

	switch in.Protocol {
	case model.ProtocolHysteria2, model.ProtocolTUIC:
		ep.Network = model.NetworkUDP
		ep.Security = model.SecurityTLS
		ep.Fingerprint = ""
		ep.Mux, ep.Fragment = false, false
		ep.CongestionControl = in.CongestionControl
		// mpx 为 hy2/tuic 签发自签证书，客户端必须跳过校验，否则 Clash Meta 测速 503。
		ep.AllowInsecure = true
		if in.Protocol == model.ProtocolHysteria2 {
			ep.UUID = ""
		}
	case model.ProtocolShadowsocks:
		ep.Password = model.DeriveSS2022Key(user.Key)
	case model.ProtocolWireguard:
		priv, _, err := model.DeriveWGKeypair(user.Key)
		if err != nil {
			// SHA-256 输出永远是合法的 X25519 种子,这里 err 不可能非空;
			// 但万一出现,跳过一个端点比让整份订阅崩掉好——坏端点只会少一个
			// 入口,坏订阅会让客户端整份不加载。
			return Endpoint{}, false
		}
		ep.WGPrivateKey = priv
		ep.WGPublicKey = wgServerPub(in)
		ep.WGAddress = wgClientAddr(in.WGAddress, user.Key)
	}

	return ep, true
}

// wgClientAddr 给客户端算隧道地址,由服务端网段与用户 Key 派生。
//
// wireguard 靠 allowedIPs 做路由:两个 peer 拿了同一个地址,后到的会把先到
// 的流量挤掉。地址因此必须每用户唯一,又不能在控制面存一份分配表——这里把
// Key 的散列映进网段的主机位,同一用户永远拿到同一个地址。
// 主机位里跳过三个值:网络地址(.0)、服务端自己(.1)、最后一个(广播位)。
func wgClientAddr(serverAddr, key string) string {
	prefix, err := netip.ParsePrefix(serverAddr)
	if err != nil || !prefix.Addr().Is4() {
		return serverAddr
	}
	hostBits := 32 - prefix.Bits()
	if hostBits < 2 {
		// 网段小到放不下第三个地址,直接让客户端拿服务端地址,连不上也比
		// 算出一个网段外的地址好排查。
		return serverAddr
	}
	usable := uint64(1)<<hostBits - 3
	sum := sha256.Sum256([]byte("wgaddr:" + key))
	host := 2 + binary.BigEndian.Uint64(sum[:8])%usable

	base := binary.BigEndian.Uint32(prefix.Masked().Addr().AsSlice())
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, base+uint32(host))
	return netip.AddrFrom4([4]byte{b[0], b[1], b[2], b[3]}).String() + "/32"
}

// wgServerPub 由服务端私钥推出服务端公钥。
//
// 渲染端点时不直接存公钥——公钥跟着私钥走,存一份只会多一份要同步的东西。
func wgServerPub(in model.Inbound) string {
	if in.WGSecretKey == "" {
		return ""
	}
	// WGSecretKey 存的是 RawURLEncoding(与 xray 一致);DeriveWGKeypair 返回的
	// 也是 RawURLEncoding,直接复用它的解码/编码逻辑会绕,这里只做 base64 换算。
	raw, err := base64.RawURLEncoding.DecodeString(in.WGSecretKey)
	if err != nil {
		return ""
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
}

// ResolveExposure 算出 (节点, 入站, 对外入口) 合成之后的对外形态。回落规则只有
// 这一份:订阅渲染和 API 都从这里取,否则「列表上看到的」和「实际发出去的」迟早
// 对不上。
//
// 这里只合并,不展开 {VAR} 和 * —— 那两个跟具体用户有关,得等渲染时才知道。
func ResolveExposure(node model.Node, in model.Inbound, h model.Host) model.Exposure {
	// 路径:grpc 用 serviceName,http 系传输用自己的 path,其余用 ws 的 path。
	path, host := in.WSPath, ""
	switch in.Network {
	case model.NetworkGRPC:
		path = in.GRPCService
	case model.NetworkXHTTP, model.NetworkSplitHTTP, model.NetworkHTTPUpgrade:
		path = in.XHTTPPath
	}
	// Host 同理:grpc 没有 host 概念,http 系用自己的 host,其余用 ws host(入站
	// 上没有,只能由 Host 覆盖)。
	if in.Network == model.NetworkXHTTP || in.Network == model.NetworkSplitHTTP ||
		in.Network == model.NetworkHTTPUpgrade {
		host = in.XHTTPHost
	}
	if h.Host != "" {
		host = h.Host
	}
	if h.Path != "" {
		path = h.Path
	}
	alpn := h.ALPN
	if len(alpn) == 0 {
		alpn = in.ALPN
	}
	port := in.Port
	if h.Port != 0 {
		port = h.Port
	}

	return model.Exposure{
		Address: firstNonEmpty(h.Address, node.Address),
		Port:    port,
		SNI:     firstNonEmpty(h.SNI, in.SNI),
		// Host 头没有可回落的东西:入站上没有这个概念,空就是不发。
		Host: host,
		Path: path,
		ALPN: alpn,
	}
}

// expand 展开 Host 上的 {VAR} 变量,并把 * 换成该用户专属的随机子域。
//
// 变量表由调用方给定;未知变量原样保留,便于用户自己发现拼写错误。
func expand(s string, vars map[string]string, salt string) string {
	if s == "" {
		return ""
	}
	if strings.Contains(s, "*") {
		s = strings.ReplaceAll(s, "*", salt)
	}
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// paddedStdB64 把 RawURLEncoding 的 base64 换成带填充的标准 base64。
//
// DeriveWGKeypair 产出的密钥是 RawURLEncoding(xray 认这种),而 sing-box
// 与 mihomo 只认标准带填充形式;不转换它们会直接拒收。换字母表之前先解码
// 再编码,避免只补 padding 不换 `-`/`_` 的半成品。
func paddedStdB64(s string) string {
	if s == "" {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		// 解码失败说明输入本来就不是 RawURL 形式,原样透传让下游报错。
		return s
	}
	return base64.StdEncoding.EncodeToString(b)
}

// saltOf 为每个用户生成稳定的随机子域片段。
//
// 用用户名派生而不是随机数:同一用户每次拿到的地址一致,否则客户端每次更新
// 订阅都会看到新地址。
func saltOf(user *model.User) string {
	sum := sha256.Sum256([]byte("salt:" + user.Username))
	return strings.ToLower(base64.RawURLEncoding.EncodeToString(sum[:6]))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstShortID(ids []string) string {
	for _, id := range ids {
		if id != "" {
			return id
		}
	}
	return ""
}

// AddProtocolSuffix 给同一节点在这份订阅里的多个默认名入口加「 · 协议短名」,
// 例如「🇺🇸 洛杉矶 A · Reality」「🇺🇸 洛杉矶 A · Hy2」。host 上手写的 remark 不动;
// 加完仍重名的交给 DedupeRemarks 加序号。原地修改并返回同一个切片。
func AddProtocolSuffix(eps []Endpoint) []Endpoint {
	count := map[int64]int{}
	for _, ep := range eps {
		if ep.AutoRemark {
			count[ep.NodeID]++
		}
	}
	for i := range eps {
		if eps[i].AutoRemark && count[eps[i].NodeID] > 1 {
			eps[i].Remark += " · " + ProtocolShort(eps[i])
		}
	}
	return eps
}

// ProtocolShort 是端点的协议短名:优先协议本身有辨识度的(Hy2、TUIC、SS、WG),
// 其次 Reality,再次传输层(WS、gRPC、XHTTP…),最后是协议名。
func ProtocolShort(ep Endpoint) string {
	switch ep.Protocol {
	case model.ProtocolHysteria2:
		return "Hy2"
	case model.ProtocolTUIC:
		return "TUIC"
	case model.ProtocolShadowsocks:
		return "SS"
	case model.ProtocolWireguard:
		return "WG"
	}
	if ep.Security == model.SecurityReality {
		return "Reality"
	}
	switch ep.Network {
	case model.NetworkWS:
		return "WS"
	case model.NetworkGRPC:
		return "gRPC"
	case model.NetworkXHTTP:
		return "XHTTP"
	case model.NetworkSplitHTTP:
		return "SplitHTTP"
	case model.NetworkHTTPUpgrade:
		return "HTTPUpgrade"
	}
	switch ep.Protocol {
	case model.ProtocolVLESS:
		return "VLESS"
	case model.ProtocolVMess:
		return "VMess"
	case model.ProtocolTrojan:
		return "Trojan"
	}
	return strings.ToUpper(string(ep.Protocol))
}
