// links.go 把端点渲染成分享链接(vless / vmess / trojan)。
//
// 参数顺序与 v2share 参考实现一致:security、type、flow、传输参数、最后是安全层
// 参数。顺序固定,同一批端点每次渲染得到完全相同的链接文本。
package render

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// Links 返回每个端点的分享链接,一行一条,顺序与输入一致。
//
// 只支持 vless / vmess / trojan;其余协议跳过该条并继续,不因为一个坏端点让
// 整份订阅失败。
func Links(eps []Endpoint) []string {
	var out []string
	for _, ep := range eps {
		link, ok := shareLink(ep)
		if !ok {
			continue
		}
		out = append(out, link)
	}
	return out
}

// LinksBase64 返回整体 base64 后的分享链接(内容是内含换行的多行文本)。
func LinksBase64(eps []Endpoint) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(Links(eps), "\n")))
}

// shareLink 渲染单条分享链接;协议不受支持时返回 false。
func shareLink(ep Endpoint) (string, bool) {
	switch ep.Protocol {
	case model.ProtocolVLESS, model.ProtocolVMess, model.ProtocolTrojan:
	default:
		return "", false
	}
	if ep.Protocol == model.ProtocolVMess {
		return vmessLink(ep), true
	}
	return urlLink(ep), true
}

// effectiveNetwork 把留空的传输当作 tcp。
//
// Endpoint 正常来自 Resolve,不会留空;这里兜底是为了让零值 Endpoint 也能渲染
// 出带 type 的链接,而不是吐出一个缺参数的分享链接。
func effectiveNetwork(ep Endpoint) model.Network {
	if ep.Network == "" {
		return model.NetworkTCP
	}
	return ep.Network
}

// effectiveSecurity 把留空的安全层当作 none,理由同 effectiveNetwork。
func effectiveSecurity(ep Endpoint) model.Security {
	if ep.Security == "" {
		return model.SecurityNone
	}
	return ep.Security
}

// urlLink 渲染 vless / trojan 的 URI 形式分享链接。
func urlLink(ep Endpoint) string {
	sec := effectiveSecurity(ep)
	transport := effectiveNetwork(ep)

	q := &linkQuery{}
	q.add("security", string(sec))
	q.add("type", string(transport))
	// flow 只属于 VLESS;v2share 也在任何传输下都照原样带出。
	if ep.Protocol == model.ProtocolVLESS {
		q.add("flow", ep.Flow)
	}

	switch transport {
	case model.NetworkGRPC:
		// grpc 的 serviceName 取 Path,authority 取 Host,mode 固定 gun。
		q.add("serviceName", ep.Path)
		q.add("authority", ep.Host)
		q.add("mode", "gun")
	case model.NetworkWS:
		q.add("path", ep.Path)
		q.add("host", ep.Host)
	}
	// tcp 没有可表达的参数,不带 path / host。

	if sec == model.SecurityTLS || sec == model.SecurityReality {
		q.add("sni", ep.SNI)
		q.add("fp", ep.Fingerprint)
		if ep.AllowInsecure {
			q.add("allowInsecure", "1")
		}
	}
	switch sec {
	case model.SecurityTLS:
		q.add("alpn", strings.Join(ep.ALPN, ","))
	case model.SecurityReality:
		q.add("pbk", ep.RealityPub)
		q.add("sid", ep.RealitySID)
	}

	passphrase := ep.UUID
	if ep.Protocol == model.ProtocolTrojan {
		passphrase = escapeUserinfo(ep.Password)
	}
	return fmt.Sprintf("%s://%s@%s:%d?%s#%s",
		ep.Protocol, passphrase, ep.Address, ep.Port, q.String(), escapeFragment(ep.Remark))
}

// vmessLink 渲染 vmess 分享链接:vmess:// 加 base64 后的 JSON。
//
// 字段与 v2share 一致;type 固定 none(本项目没有 header 伪装类型),v2share 在
// 没有 headerType 时会把该字段整个省略。JSON 由就是 map 的 json.Marshal 产出,
// 键按字典序排列,因此输出稳定。
func vmessLink(ep Endpoint) string {
	sec := effectiveSecurity(ep)

	payload := map[string]any{
		"add":  ep.Address,
		"aid":  "0",
		"host": ep.Host,
		"id":   ep.UUID,
		"net":  string(effectiveNetwork(ep)),
		"path": ep.Path,
		"port": ep.Port,
		"ps":   ep.Remark,
		"scy":  "auto",
		"tls":  string(sec),
		"type": "none",
		"v":    "2",
	}
	if sec == model.SecurityTLS || sec == model.SecurityReality {
		payload["sni"] = ep.SNI
		payload["fp"] = ep.Fingerprint
	}
	switch sec {
	case model.SecurityTLS:
		payload["alpn"] = strings.Join(ep.ALPN, ",")
	case model.SecurityReality:
		payload["pbk"] = ep.RealityPub
		payload["sid"] = ep.RealitySID
	}

	// 空字符串一律不输出:vmess 客户端把缺字段当默认值,空值反而可能被解析成
	// 空地址、空 path。
	for k, v := range payload {
		if s, ok := v.(string); ok && s == "" {
			delete(payload, k)
		}
	}

	// 值只有字符串与整数,序列化不会失败。
	raw, _ := json.Marshal(payload)
	return "vmess://" + base64.StdEncoding.EncodeToString(raw)
}

// linkQuery 按加入顺序拼 query,保证顺序固定且空值不输出。
//
// 不用 url.Values:它会按键排序,而参考实现的顺序是固定的插入顺序。
type linkQuery struct {
	pairs [][2]string
}

func (q *linkQuery) add(key, value string) {
	if value == "" {
		return
	}
	q.pairs = append(q.pairs, [2]string{key, value})
}

func (q *linkQuery) String() string {
	parts := make([]string, 0, len(q.pairs))
	for _, p := range q.pairs {
		parts = append(parts, url.QueryEscape(p[0])+"="+url.QueryEscape(p[1]))
	}
	return strings.Join(parts, "&")
}

// escapeFragment 编码 # 后面的备注:空格要编成 %20 而不是 +。
//
// url.QueryEscape 按 form 编码,空格变 +;落在 fragment 里 + 是普通加号,所以
// 换回 %20。
func escapeFragment(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// escapeUserinfo 编码 trojan 密码:等价于 url.QueryEscape,但保留 : 不转义。
func escapeUserinfo(s string) string {
	s = url.QueryEscape(s)
	s = strings.ReplaceAll(s, "%3A", ":")
	return strings.ReplaceAll(s, "+", "%20")
}
