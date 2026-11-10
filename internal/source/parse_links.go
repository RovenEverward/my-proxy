package source

// parse_links.go 解分享链接列表:每行一条 ss:// vmess:// trojan://
// vless:// hysteria2/hy2:// tuic://。不带 "://" 的行当注释静默跳过;
// 带 "://" 但解不开的进 Issue。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// parseLinks 解明(或 base64 解出来的)链接列表。
func parseLinks(body []byte) (Result, error) {
	res := Result{Format: FormatLinks}
	lines := strings.Split(string(body), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.Contains(line, "://") {
			continue // 注释、订阅信息等普通文本,不是节点。
		}
		ep, reason, name := parseShareLink(line)
		if reason != "" {
			res.Issues = append(res.Issues, Issue{Index: i + 1, Name: name, Reason: reason})
			continue
		}
		res.Endpoints = append(res.Endpoints, ep)
	}
	if len(res.Endpoints) == 0 && len(res.Issues) == 0 {
		return res, fmt.Errorf("%w: 没有可识别的内容", ErrUnrecognized)
	}
	return res, nil
}

// parseShareLink 解一条链接。返回失败原因时 name 尽力取自 fragment。
func parseShareLink(line string) (render.Endpoint, string, string) {
	ep := render.Endpoint{}
	scheme, rest, ok := strings.Cut(line, "://")
	if !ok {
		return ep, "不是可识别的分享链接", ""
	}
	rest = strings.TrimSpace(rest)
	scheme = strings.ToLower(strings.TrimSpace(scheme))

	if scheme == "vmess" {
		return parseVMessLine(rest)
	}
	if scheme == "ss" {
		return parseSSLine(rest)
	}

	u, err := url.Parse(line)
	if err != nil {
		return ep, "链接无法解析", ""
	}
	name := u.Fragment
	ep.Remark = name
	host, portStr, err := splitHostPort(u.Host)
	if err != nil {
		return ep, "地址或端口无法解析", name
	}
	port, err := portFromString(portStr)
	if err != nil {
		return ep, "地址或端口无法解析", name
	}
	ep.Address, ep.Port = host, port
	q := u.Query()

	switch scheme {
	case "vless":
		return finishURLLink(u, q, &ep, name, func() string {
			ep.Protocol = model.ProtocolVLESS
			ep.UUID = u.User.Username()
			if ep.UUID == "" {
				return "缺少 uuid"
			}
			if enc := q.Get("encryption"); enc != "" && enc != "none" {
				return "vless encryption 无法保留"
			}
			ep.Flow = q.Get("flow")
			return urlTransportSecurity(q, &ep)
		})
	case "trojan":
		return finishURLLink(u, q, &ep, name, func() string {
			ep.Protocol = model.ProtocolTrojan
			ep.Password = joinUserInfo(u)
			if ep.Password == "" {
				return "缺少密码"
			}
			if reason := urlTransportSecurity(q, &ep); reason != "" {
				return reason
			}
			// trojan 链接不写 security 也是 TLS 握手;显式 none 才保留 none。
			if q.Get("security") == "" {
				ep.Security = model.SecurityTLS
			}
			return ""
		})
	case "hysteria2", "hy2":
		return finishURLLink(u, q, &ep, name, func() string {
			ep.Protocol = model.ProtocolHysteria2
			ep.Password = joinUserInfo(u)
			if ep.Password == "" {
				return "缺少密码"
			}
			for _, bad := range []string{"obfs", "obfs-password", "mport", "ports", "pinSHA256", "fp"} {
				if v := q.Get(bad); v != "" {
					return bad + " 无法保留"
				}
			}
			ep.Network = model.NetworkUDP
			ep.Security = model.SecurityTLS
			ep.SNI = q.Get("sni")
			if ep.SNI == "" {
				ep.SNI = q.Get("peer") // peer 是部分客户端写法的 SNI 别名。
			}
			ep.ALPN = splitCSV(q.Get("alpn"))
			ep.AllowInsecure = q.Get("insecure") == "1" || q.Get("insecure") == "true"
			// auth 等影响连接的参数白名单外,直接跳过。
			if extra := queryExtras(q, "sni", "peer", "alpn", "insecure"); len(extra) > 0 {
				return extraErr(extra).Error()
			}
			return ""
		})
	case "tuic":
		return finishURLLink(u, q, &ep, name, func() string {
			ep.Protocol = model.ProtocolTUIC
			ep.UUID = u.User.Username()
			pw, _ := u.User.Password()
			if pw == "" {
				pw = q.Get("password")
			}
			ep.Password = pw
			if ep.UUID == "" || ep.Password == "" {
				return "缺少 uuid 或密码"
			}
			if reason := tuicUUIDCheck(ep.UUID); reason != "" {
				return reason
			}
			for _, bad := range []string{"udp_relay_mode", "udp_over_stream", "disable_sni", "version"} {
				switch bad {
				case "udp_relay_mode":
					if v := q.Get(bad); v != "" && v != "native" {
						return bad + " 无法保留"
					}
				default:
					if v := q.Get(bad); v != "" {
						return bad + " 无法保留"
					}
				}
			}
			ep.CongestionControl = q.Get("congestion_control")
			if ep.CongestionControl != "" {
				switch ep.CongestionControl {
				case "bbr", "cubic", "new_reno":
				default:
					return "tuic 拥塞控制无法保留"
				}
			}
			ep.Network = model.NetworkUDP
			ep.Security = model.SecurityTLS
			ep.SNI = q.Get("sni")
			ep.ALPN = splitCSV(q.Get("alpn"))
			ep.AllowInsecure = q.Get("allow_insecure") == "1" || q.Get("allow_insecure") == "true"
			if extra := queryExtras(q, "sni", "alpn", "allow_insecure", "congestion_control",
				"udp_relay_mode", "udp_over_stream", "disable_sni", "version", "password"); len(extra) > 0 {
				return extraErr(extra).Error()
			}
			return ""
		})
	default:
		return ep, fmt.Sprintf("协议 %q 不在支持范围", scheme), name
	}
}

// finishURLLink 跑协议解析,失败时把端点作废。
func finishURLLink(u *url.URL, q url.Values, ep *render.Endpoint, name string, fill func() string) (render.Endpoint, string, string) {
	if reason := fill(); reason != "" {
		return render.Endpoint{}, reason, name
	}
	return *ep, "", name
}

// splitHostPort 拆 host:port;IPv6 方括号剥掉,非常规端口交给调用方的
// 端口检查报错。
func splitHostPort(hostport string) (string, string, error) {
	if i := strings.LastIndex(hostport, ":"); i < 0 {
		return "", "", fmt.Errorf("缺端口")
	}
	host := hostport[:strings.LastIndex(hostport, ":")]
	port := hostport[strings.LastIndex(hostport, ":")+1:]
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "" || port == "" {
		return "", "", fmt.Errorf("地址或端口为空")
	}
	return host, port, nil
}

// checkPort 的端口参数包装:字符串先转数字。
func portFromString(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	return checkPort(n)
}

func atoi2(s string) int { n, _ := strconv.Atoi(s); return n }

// joinUserInfo 把 userinfo 原样拼回:trojan 密码里可能有编码过的冒号,
// url.User 已经按第一个冒号切好,拼回去即可。
func joinUserInfo(u *url.URL) string {
	user := u.User.Username()
	pw, has := u.User.Password()
	if !has {
		return user
	}
	return user + ":" + pw
}

// urlTransportSecurity 读 vless/trojan 链接的 type/security/传输/TLS 参数。
// 参数白名单之外的键无法保留。
func urlTransportSecurity(q url.Values, ep *render.Endpoint) string {
	sec, bad := mapSecurity(q.Get("security"))
	if bad {
		return fmt.Sprintf("安全层 %q 无法保留", q.Get("security"))
	}
	ep.Security = sec

	network, bad := mapNetwork(q.Get("type"))
	if bad {
		return fmt.Sprintf("传输 %q 无法保留", q.Get("type"))
	}
	ep.Network = network

	switch ep.Network {
	case model.NetworkWS, model.NetworkXHTTP, model.NetworkSplitHTTP, model.NetworkHTTPUpgrade:
		ep.Path = q.Get("path")
		ep.Host = q.Get("host")
	case model.NetworkGRPC:
		ep.Path = q.Get("serviceName")
		if ep.Path == "" {
			ep.Path = q.Get("service_name")
		}
		ep.Host = q.Get("authority")
		if m := q.Get("mode"); m != "" && m != "gun" {
			return fmt.Sprintf("grpc mode %q 无法保留", m)
		}
	}
	if ep.Network == model.NetworkXHTTP {
		ep.Mode = q.Get("mode")
	}
	if h := q.Get("headerType"); h != "" && h != "none" {
		return fmt.Sprintf("headerType %q 无法保留", h)
	}

	if ep.Security == model.SecurityTLS || ep.Security == model.SecurityReality {
		ep.SNI = q.Get("sni")
		ep.Fingerprint = q.Get("fp")
		ep.ALPN = splitCSV(q.Get("alpn"))
		ep.AllowInsecure = q.Get("allowInsecure") == "1" || q.Get("allowInsecure") == "true"
	}
	if ep.Security == model.SecurityReality {
		ep.RealityPub = q.Get("pbk")
		ep.RealitySID = q.Get("sid")
		if ep.RealityPub == "" {
			return "reality 缺 pbk"
		}
		if reason := realityFieldCheck(ep); reason != "" {
			return reason
		}
	}
	if reason := realityProtocolCheck(ep); reason != "" {
		return reason
	}

	if extra := queryExtras(q, linkQueryWhitelist...); len(extra) > 0 {
		return extraErr(extra).Error()
	}
	return ""
}

// linkQueryWhitelist 是 vless/trojan 链接可保留或已处理的参数名。
// xhttp 的 extra 与 REALITY 的 spx 都影响连接,不在名单上,出现即跳过。
var linkQueryWhitelist = []string{
	"security", "type", "flow", "encryption",
	"path", "host", "serviceName", "service_name", "authority", "mode",
	"headerType", "sni", "fp", "alpn", "allowInsecure", "insecure",
	"pbk", "sid",
}

// queryExtras 返回白名单外的 query 键;_ 前缀的元数据键忽略。
func queryExtras(q url.Values, allowed ...string) []string {
	set := map[string]bool{}
	for _, a := range allowed {
		set[a] = true
	}
	var extra []string
	for k := range q {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if !set[k] {
			extra = append(extra, k)
		}
	}
	return sortStrings(extra)
}

// parseVMessLine 解 vmess://<base64-JSON>。
func parseVMessLine(payload string) (render.Endpoint, string, string) {
	ep := render.Endpoint{}
	payload = strings.TrimSpace(payload)
	if i := strings.IndexByte(payload, '#'); i >= 0 {
		payload = payload[:i]
	}
	if i := strings.IndexByte(payload, '?'); i >= 0 {
		payload = payload[:i]
	}
	var raw []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		raw, err = enc.DecodeString(payload)
		if err == nil {
			break
		}
	}
	if err != nil {
		return ep, "vmess 内容不是 base64", ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ep, "vmess 内容不是 JSON", ""
	}
	m, _ = normalizeYAML(m).(map[string]any)
	name := getString(m, "ps", "remark", "remarks")
	ep.Remark = name

	if extra := extraKeys(m, vmessJSONKeys); len(extra) > 0 {
		return ep, extraErr(extra).Error(), name
	}
	for _, bad := range []string{"verify_cert", "subid", "source", "clash", "class"} {
		delete(m, bad)
	}
	ep.Protocol = model.ProtocolVMess
	ep.Address = getString(m, "add", "server", "server_address")
	if ep.Address == "" {
		return ep, "缺少 add", name
	}
	port, err := parsePort(m["port"])
	if err != nil {
		return ep, "端口无法解析", name
	}
	ep.Port = port
	ep.UUID = getString(m, "id", "uuid")
	if ep.UUID == "" {
		return ep, "缺少 id", name
	}
	if aid, present := getInt(m, "aid"); present {
		ep.VMessAlterID = aid
	}
	if scy := getString(m, "scy", "security"); scy != "" {
		if !vmessAllowedCiphers[scy] {
			return ep, fmt.Sprintf("vmess 加密 %q 不在支持范围", scy), name
		}
		ep.VMessSecurity = scy
	}
	if h := getString(m, "type"); h != "" && h != "none" {
		return ep, fmt.Sprintf("header 伪装 %q 无法保留", h), name
	}
	network, bad := mapNetwork(getString(m, "net", "network"))
	if bad {
		return ep, fmt.Sprintf("传输 %q 无法保留", getString(m, "net")), name
	}
	ep.Network = network
	ep.Host = getString(m, "host")
	ep.Path = getString(m, "path")

	sec, bad2 := mapSecurity(getString(m, "tls"))
	if bad2 {
		return ep, fmt.Sprintf("安全层 %q 无法保留", getString(m, "tls")), name
	}
	ep.Security = sec
	ep.SNI = getString(m, "sni")
	ep.ALPN = getStringList(m, "alpn")
	ep.Fingerprint = getString(m, "fp")
	if v := getString(m, "insecure", "allowInsecure"); v != "" {
		ep.AllowInsecure = strings.EqualFold(v, "true") || v == "1"
	}
	if ep.Security == model.SecurityReality {
		ep.RealityPub = getString(m, "pbk")
		ep.RealitySID = getString(m, "sid")
		if ep.RealityPub == "" {
			return ep, "reality 缺 pbk", name
		}
		if reason := realityFieldCheck(&ep); reason != "" {
			return ep, reason, name
		}
	}
	return ep, "", name
}

// vmessJSONKeys 是 vmess 分享 JSON 里认得或明确忽略(客户端元数据)的字段。
var vmessJSONKeys = map[string]bool{
	"v": true, "ps": true, "add": true, "server": true, "server_address": true,
	"port": true, "id": true, "uuid": true, "aid": true, "scy": true,
	"security": true, "net": true, "network": true, "type": true,
	"host": true, "path": true, "tls": true, "sni": true, "alpn": true,
	"fp": true, "insecure": true, "allowInsecure": true, "pbk": true, "sid": true,
	"verify_cert": true, "subid": true, "source": true, "clash": true,
	"class": true, "remark": true, "remarks": true,
}

// parseSSLine 解 ss:// 链接。userinfo 可能是 base64(method:password),
// 也可能是明文 method:password;plugin 等 query 表达不了,整条跳过。
func parseSSLine(rest string) (render.Endpoint, string, string) {
	ep := render.Endpoint{Protocol: model.ProtocolShadowsocks, Network: model.NetworkTCP}
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		frag, err := url.QueryUnescape(rest[i+1:])
		if err == nil {
			ep.Remark = frag
		}
		rest = rest[:i]
	}
	name := ep.Remark
	var query string
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		query = rest[i+1:]
		rest = rest[:i]
	}
	if query != "" {
		q, err := url.ParseQuery(query)
		if err != nil {
			return ep, "ss query 无法解析", name
		}
		// ss 链接只认得 plugin 系列参数;它们都表达不了。
		if extra := queryExtras(q); len(extra) > 0 {
			return ep, extraErr(extra).Error(), name
		}
	}
	// userinfo 可能含 @ 的编码或明文,取最后一个 @ 前当凭据。
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return ep, "ss 链接缺 @", name
	}
	userinfo, hostport := rest[:at], rest[at+1:]
	host, portStr, err := splitHostPort(hostport)
	if err != nil {
		return ep, "地址或端口无法解析", name
	}
	port, err := portFromString(portStr)
	if err != nil {
		return ep, "地址或端口无法解析", name
	}
	ep.Address, ep.Port = host, port

	cred := userinfo
	if dec, ok := decodeSSUserinfo(userinfo); !ok {
		// 明文 userinfo 可能带 percent 编码。
		if un, err := url.PathUnescape(userinfo); err == nil {
			cred = un
		}
	} else {
		cred = dec
	}
	method, password, ok := strings.Cut(cred, ":")
	if !ok {
		return ep, "ss userinfo 缺 method:password", name
	}
	if !ssAllowedMethods[method] {
		return ep, fmt.Sprintf("ss method %q 不在支持范围", method), name
	}
	ep.SSMethod = method
	ep.Password = password
	if ep.Password == "" {
		return ep, "缺少密码", name
	}
	if reason := ssKeyCheck(method, ep.Password); reason != "" {
		return ep, reason, name
	}
	return ep, "", name
}

// decodeSSUserinfo 尝试把 userinfo 当 SIP002 base64 解;解出来必须含 ":"
// 且形似 method:password 才算数,否则按明文处理。
func decodeSSUserinfo(s string) (string, bool) {
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		raw, err := enc.DecodeString(s)
		if err != nil {
			continue
		}
		text := string(raw)
		if strings.Contains(text, ":") && !strings.ContainsAny(text, "\x00\xff") {
			return text, true
		}
	}
	return "", false
}
