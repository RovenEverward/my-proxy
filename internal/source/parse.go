// Package source 负责外部订阅的拉取、解析与规范化。
//
// Parse 把上游正文(Clash/mihomo YAML 的 proxies、sing-box JSON 的 outbounds、
// 明文分享链接、base64 链接列表)规范成 render.Endpoint 列表。解析是保守的:
// 连接参数表达不全的节点进 Issues 不进 Endpoints——输出一条丢了认证参数的
// 节点比少一条更难排查。错误与原因只含字段名、协议名和位置,不回显 URL 凭据、
// UUID、密码或私钥。
package source

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// ErrUnrecognized 表示正文不是任何认识的订阅容器。
var ErrUnrecognized = errors.New("source: 正文不是认识的订阅格式")

// ErrNoEndpoints 表示容器认识、但没有任何可完整表达的节点。
var ErrNoEndpoints = errors.New("source: 没有可完整表达的节点")

// 容器格式名。Result.Format 只用这几个取值。
const (
	FormatClash       = "clash"
	FormatSingBox     = "sing-box"
	FormatLinks       = "links"
	FormatBase64Links = "base64-links"
)

// Result 是一次解析的产出:能完整表达的节点列表、实际容器格式与跳过报告。
type Result struct {
	Endpoints []render.Endpoint `json:"-"`
	Format    string            `json:"format"`
	Issues    []Issue           `json:"issues"`
}

// Issue 是一条被跳过的条目:位置、显示名与不含凭据的原因。
type Issue struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Parse 解析一份订阅正文。正文为空或不是认识的容器时返回 ErrUnrecognized;
// 容器能认但一个节点都表达不了时返回 ErrNoEndpoints,同时 Result 里带上
// 逐条跳过原因供来源详情展示。
func Parse(body []byte) (Result, error) {
	trimmed := trimSpace(body)
	if len(trimmed) == 0 {
		return Result{}, fmt.Errorf("%w: 空正文", ErrUnrecognized)
	}
	res, err := detect(trimmed)
	if err != nil {
		return res, err
	}
	res.Endpoints = dedupe(res.Endpoints)
	if len(res.Endpoints) == 0 {
		return res, fmt.Errorf("%w: 跳过 %d 条", ErrNoEndpoints, len(res.Issues))
	}
	return res, nil
}

// detect 按正文形状分派容器解析。
//
// 顺序:YAML 容器(JSON 是 YAML 子集,sing-box 配置也走这里)→ base64 → 明文
// 链接。普通文本虽然能被 YAML 读成标量,但顶不上 proxies/outbounds 键,落
// 不到容器分支;它能进链接分支只是因为里面有 "://" 的行。
func detect(body []byte) (Result, error) {
	if top, err := decodeYAML(body); err == nil && len(top) > 0 {
		if v, ok := top["proxies"]; ok {
			return parseClash(v)
		}
		if v, ok := top["outbounds"]; ok {
			return parseSingBox(v)
		}
	}
	if decoded, ok := decodeBase64Text(body); ok {
		res, err := detect(decoded)
		if err != nil {
			return Result{}, err
		}
		if res.Format == FormatLinks {
			res.Format = FormatBase64Links
		}
		return res, nil
	}
	return parseLinks(body)
}

// trimSpace 去 BOM 与首尾空白。
func trimSpace(b []byte) []byte {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return b
}

// decodeBase64Text 尝试把整段正文当 base64 解;解出来得是能当文本看的
// 东西(含 "://" 或像 YAML/JSON 容器),才算数。
func decodeBase64Text(body []byte) ([]byte, bool) {
	s := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		}
		return r
	}, string(body))
	if s == "" {
		return nil, false
	}
	var raw []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		raw, err = enc.DecodeString(s)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, false
	}
	text := strings.TrimSpace(string(raw))
	if !looksLikeText(text) {
		return nil, false
	}
	return []byte(text), true
}

// looksLikeText 报告解码结果像不像订阅文本:得是 UTF-8 可打印,并带
// 链接或容器特征。
func looksLikeText(s string) bool {
	if strings.Contains(s, "://") {
		return true
	}
	if strings.Contains(s, "proxies:") || strings.Contains(s, "outbounds") {
		return true
	}
	return false
}

// dedupe 按连接指纹合并同一来源里的完全重复节点,并给每个端点填
// SourceKey。备注、名字与出现顺序不进指纹:去重保留先到的条目。
func dedupe(eps []render.Endpoint) []render.Endpoint {
	seen := map[string]bool{}
	out := make([]render.Endpoint, 0, len(eps))
	for _, ep := range eps {
		ep.SourceKey = fingerprint(ep)
		if seen[ep.SourceKey] {
			continue
		}
		seen[ep.SourceKey] = true
		out = append(out, ep)
	}
	return out
}

// fingerprint 给端点的连接参数算稳定指纹。涵盖全部影响连接的字段(含
// Mux/Fragment 这类改变拨号行为的客户端侧选项,含 ALPN 原序——顺序是协商
// 优先级),只排除备注、来源名、托管身份与顺序;输出是 sha256 hex,不携带
// 可逆的凭据材料。
func fingerprint(ep render.Endpoint) string {
	var b strings.Builder
	put := func(s string) {
		b.WriteString(s)
		b.WriteByte(0)
	}
	put(string(ep.Protocol))
	put(strings.ToLower(ep.Address))
	put(fmt.Sprintf("%d", ep.Port))
	put(ep.UUID)
	put(ep.Password)
	put(ep.Flow)
	put(ep.CongestionControl)
	put(ep.SSMethod)
	put(ep.VMessSecurity)
	put(fmt.Sprintf("%d", ep.VMessAlterID))
	network := ep.Network
	if network == "" {
		network = model.NetworkTCP
	}
	put(string(network))
	put(ep.Path)
	put(ep.Host)
	put(ep.Mode)
	sec := ep.Security
	if sec == "" {
		sec = model.SecurityNone
	}
	put(string(sec))
	put(ep.SNI)
	// ALPN 原序参与指纹:顺序是客户端宣告的协商优先级,换序会改变握手
	// 结果,不是同一连接。
	put(strings.Join(ep.ALPN, ","))
	put(ep.Fingerprint)
	put(ep.RealityPub)
	put(ep.RealitySID)
	if ep.AllowInsecure {
		put("1")
	} else {
		put("0")
	}
	if ep.Mux {
		put("mux")
	}
	if ep.Fragment {
		put("fragment")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:16])
}
