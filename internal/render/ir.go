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
	"crypto/sha256"
	"encoding/base64"
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
	Remark  string
	Address string
	Port    int

	Protocol model.Protocol
	UUID     string // vless / vmess
	Password string // trojan
	Flow     string // vless

	Network model.Network
	// Path 是 ws 的 path,或 grpc 的 serviceName。
	Path string
	// Host 是 ws 的 Host 头,或 grpc 的 authority。
	Host string

	Security      model.Security
	SNI           string
	ALPN          []string
	Fingerprint   string
	RealityPub    string // REALITY 公钥
	RealitySID    string // REALITY shortId
	AllowInsecure bool
}

// Resolve 把节点上的入站展开成该用户可以连接的端点列表。
//
// 入站先按用户授权过滤;每个入站再展开成它的全部 Host,hosts 里没有该入站时
// 按入站自身与节点地址生成一个默认入口。Host 上留空的字段回落到入站或节点。
func Resolve(node model.Node, inbounds []model.Inbound, hosts map[int64][]model.Host, user *model.User) []Endpoint {
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
			out = append(out, endpoint(node, in, h, uuid, password, salt))
		}
	}
	return out
}

// endpoint 把 (节点, 入站, 对外入口) 合成一个 Endpoint,并展开其中的变量。
func endpoint(node model.Node, in model.Inbound, h model.Host, uuid, password, salt string) Endpoint {
	vars := map[string]string{
		"USERNAME":  "",
		"SERVER_IP": node.Address,
		"NODE_NAME": node.Name,
		"PROTOCOL":  string(in.Protocol),
		"TRANSPORT": string(in.Network),
	}

	address := firstNonEmpty(h.Address, node.Address)
	port := in.Port
	if h.Port != 0 {
		port = h.Port
	}
	path, host := in.WSPath, ""
	if in.Network == model.NetworkGRPC {
		path = in.GRPCService
	}
	if h.Host != "" {
		host = h.Host
	}
	if h.Path != "" {
		path = h.Path
	}
	sni := firstNonEmpty(h.SNI, in.SNI)
	alpn := h.ALPN
	if len(alpn) == 0 {
		alpn = in.ALPN
	}
	fp := firstNonEmpty(in.Fingerprint, "chrome")

	remark := expand(h.Remark, vars, salt)
	if remark == "" {
		remark = expand(node.Name, vars, salt)
	}

	return Endpoint{
		Remark:  remark,
		Address: expand(address, vars, salt),
		Port:    port,

		Protocol: in.Protocol,
		UUID:     uuid,
		Password: password,
		Flow:     in.Flow,

		Network: in.Network,
		Path:    path,
		Host:    expand(host, vars, salt),

		Security:    in.Security,
		SNI:         expand(sni, vars, salt),
		ALPN:        alpn,
		Fingerprint: fp,
		RealityPub:  in.RealityPub,
		RealitySID:  firstShortID(in.ShortIDs),
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
