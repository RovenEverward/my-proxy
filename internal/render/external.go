// external.go 外部(非托管)端点的身份与格式取舍。
//
// 外部端点来自订阅源解析:没有 node/inbound/host 三元 ID,身份用来源 ID 与连接
// 参数指纹(SourceKey)。Compatible 只为外部节点回答「这个格式能不能完整表达
// 这条连接」;托管路径的错误策略不变,仍由各渲染函数自行报错。
package render

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// EndpointKey 是一个端点在一份订阅里的稳定身份。
//
// 托管端点沿用三个 ID;外部端点用来源 ID + SourceKey。把它当 map 键用,
// 分流组靠它把成员端点映射回渲染出来的 tag。
type EndpointKey string

// Key 返回端点身份。SourceID>0 表示外部端点。
func (ep Endpoint) Key() EndpointKey {
	if ep.SourceID > 0 {
		return EndpointKey("s:" + strconv.FormatInt(ep.SourceID, 10) + ":" + ep.SourceKey)
	}
	return EndpointKey(fmt.Sprintf("m:%d:%d:%d", ep.NodeID, ep.InboundID, ep.HostID))
}

// ssMethod 返回端点的 SS 加密方式;空值回落自建默认(ss-2022)。
func ssMethod(ep Endpoint) string {
	if ep.SSMethod != "" {
		return ep.SSMethod
	}
	return "2022-blake3-aes-128-gcm"
}

// vmessSecurity 返回 vmess 的用户加密;空值回落 auto。
func vmessSecurity(ep Endpoint) string {
	if ep.VMessSecurity != "" {
		return ep.VMessSecurity
	}
	return "auto"
}

// classicSSMethods 是原版 Clash 认得的 AEAD method(2022-blake3 不在其中)。
var classicSSMethods = map[string]bool{
	"aes-128-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
}

// singBoxSSMethods 是 sing-box / mihomo 认得的 method 集合。
var singBoxSSMethods = map[string]bool{
	"aes-128-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true,
}

// vmessCiphers 是 xray/mihomo/sing-box 用户级加密可接受的取值。
var vmessCiphers = map[string]bool{
	"auto": true, "aes-128-gcm": true, "chacha20-poly1305": true,
	"none": true, "zero": true,
}

// 下面的校验只拦截核心已验证会拒绝或必然失效的值(sing-box 1.14.3 与
// mihomo 1.19.33 实测,Xray 按构造期校验惯例):ss-2022 密钥、REALITY
// 公钥/short_id、tuic 的 UUID 都属此类;sing-box 对过长 short_id 直接
// panic,更必须拦在进入配置之前。

// ValidUUID 判断是否为 UUID 形态;接受带连字符的规范形与 32 位纯 hex
// 写法,sing-box/xray 两种都认。
func ValidUUID(s string) bool {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' {
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
		n++
	}
	return n == 32
}

// ss2022KeyLength 是各 ss-2022 method 要求的密钥字节数。
var ss2022KeyLength = map[string]int{
	"2022-blake3-aes-128-gcm":       16,
	"2022-blake3-aes-256-gcm":       32,
	"2022-blake3-chacha20-poly1305": 32,
}

// ValidSS2022Key 校验 2022 method 的密码:核心在构造期按 base64 解码并核对
// 长度,非法值让整份配置加载失败。四种 base64 编码都接受。
func ValidSS2022Key(method, key string) bool {
	want, ok := ss2022KeyLength[method]
	if !ok {
		return true
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err := enc.DecodeString(key); err == nil && len(raw) == want {
			return true
		}
	}
	return false
}

// ValidRealityPubKey 校验 REALITY 公钥:base64 解码后必须是 32 字节。
func ValidRealityPubKey(key string) bool {
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.StdEncoding, base64.RawStdEncoding} {
		if raw, err := enc.DecodeString(key); err == nil && len(raw) == 32 {
			return true
		}
	}
	return false
}

// ValidRealityShortID 校验 REALITY short id:hex 且解码后不超过 8 字节;
// 空值表示不带 sid,合法。
func ValidRealityShortID(sid string) bool {
	if sid == "" {
		return true
	}
	raw, err := hex.DecodeString(sid)
	return err == nil && len(raw) <= 8
}

// Compatible 报告外部端点能否在指定输出格式里完整表达;不能时返回只含
// 协议/传输/参数名的安全原因,不泄漏凭据。托管路径不调用它。
func Compatible(ep Endpoint, format model.ClientFormat) error {
	switch format {
	case model.FormatLinks, model.FormatBase64:
		return linksCompatible(ep)
	case model.FormatClash:
		return clashCompatible(ep, false)
	case model.FormatClashMeta:
		return clashCompatible(ep, true)
	case model.FormatSingBox:
		return singboxCompatible(ep)
	case model.FormatXray:
		return xrayCompatible(ep)
	default:
		// block / 未识别格式:不是节点输出格式,由调用方先拦。
		return nil
	}
}

// endpointShape 做各格式共用的基础校验:协议、传输、安全层得是已知值。
func endpointShape(ep Endpoint) error {
	if ep.Protocol.QUIC() {
		return quicEndpoint(ep)
	}
	if !ep.Protocol.Valid() || ep.Protocol == model.ProtocolWireguard {
		return fmt.Errorf("协议 %q 不在支持范围", ep.Protocol)
	}
	transport := effectiveNetwork(ep)
	if !transport.Valid() {
		return fmt.Errorf("传输 %q 不受支持", transport)
	}
	if transport == model.NetworkUDP {
		return fmt.Errorf("传输 udp 只适用 QUIC 协议")
	}
	sec := effectiveSecurity(ep)
	if !sec.Valid() {
		return fmt.Errorf("安全层 %q 不受支持", sec)
	}
	return nil
}

// ssCompatible 校验 ss 的 method 与传输:classic/2022 method 集合按客户端分,
// ss 一律不能带传输层。
func ssCompatible(ep Endpoint, methods map[string]bool) error {
	if effectiveNetwork(ep) != model.NetworkTCP {
		return fmt.Errorf("shadowsocks 不支持传输 %q", effectiveNetwork(ep))
	}
	if effectiveSecurity(ep) != model.SecurityNone {
		return fmt.Errorf("shadowsocks 不支持安全层 %q", effectiveSecurity(ep))
	}
	m := ssMethod(ep)
	if !methods[m] {
		return fmt.Errorf("shadowsocks method %q 不受支持", m)
	}
	if ep.Password == "" {
		return fmt.Errorf("shadowsocks 缺密码")
	}
	return nil
}

// vmessCompatible 校验 vmess 的用户加密与 alterId。alterId>0 在新核心(含
// mihomo/sing-box/xray 新版)无法表达,只有原版 Clash 与分享链接接受。
func vmessCompatible(ep Endpoint, alterID bool) error {
	if ep.VMessAlterID > 0 && !alterID {
		return fmt.Errorf("vmess alterId>0 无法表达")
	}
	if s := vmessSecurity(ep); !vmessCiphers[s] {
		return fmt.Errorf("vmess 加密 %q 不受支持", s)
	}
	return nil
}

// clashCompatible 对应 clashNode 的取舍(meta 与否由 meta 区分)。
func clashCompatible(ep Endpoint, meta bool) error {
	if ep.Protocol.QUIC() {
		if !meta {
			return fmt.Errorf("原版 Clash 不支持 QUIC 协议 %q", ep.Protocol)
		}
		return quicEndpoint(ep)
	}
	if err := endpointShape(ep); err != nil {
		return err
	}
	transport := effectiveNetwork(ep)
	if transport == model.NetworkSplitHTTP {
		return fmt.Errorf("mihomo 没有 splithttp 传输")
	}
	if !meta && (transport == model.NetworkXHTTP || transport == model.NetworkHTTPUpgrade) {
		return fmt.Errorf("原版 Clash 不支持传输 %q", transport)
	}
	if !meta && effectiveSecurity(ep) == model.SecurityReality {
		return fmt.Errorf("原版 Clash 不支持 REALITY")
	}
	switch ep.Protocol {
	case model.ProtocolShadowsocks:
		methods := singBoxSSMethods
		if !meta {
			methods = classicSSMethods
		}
		return ssCompatible(ep, methods)
	case model.ProtocolVMess:
		return vmessCompatible(ep, !meta)
	}
	return nil
}

// singboxCompatible 对应 singboxNode 的取舍。
func singboxCompatible(ep Endpoint) error {
	if err := endpointShape(ep); err != nil {
		return err
	}
	if effectiveNetwork(ep) == model.NetworkSplitHTTP {
		return fmt.Errorf("sing-box 没有 splithttp 传输")
	}
	switch ep.Protocol {
	case model.ProtocolShadowsocks:
		return ssCompatible(ep, singBoxSSMethods)
	case model.ProtocolVMess:
		return vmessCompatible(ep, false)
	case model.ProtocolTrojan:
		// singboxTLS 对 trojan 强制开 TLS;上游明说不带 TLS 的 trojan
		// 在这里无法忠实输出。
		if effectiveSecurity(ep) == model.SecurityNone {
			return fmt.Errorf("trojan 不带 TLS 无法忠实输出")
		}
	}
	// sing-box 实测:reality 客户端缺 utls 指纹直接 FATAL。渲染器会兜底
	// chrome,但那改变上游声明,这里如实拒绝。
	if effectiveSecurity(ep) == model.SecurityReality && ep.Fingerprint == "" {
		return fmt.Errorf("sing-box 的 reality 必须带 uTLS 指纹")
	}
	return nil
}

// xrayCompatible 对应 xrayNode 的取舍。
func xrayCompatible(ep Endpoint) error {
	if ep.Protocol.QUIC() {
		return fmt.Errorf("xray 不支持 QUIC 协议 %q", ep.Protocol)
	}
	if err := endpointShape(ep); err != nil {
		return err
	}
	switch ep.Protocol {
	case model.ProtocolShadowsocks:
		return ssCompatible(ep, singBoxSSMethods)
	case model.ProtocolVMess:
		return vmessCompatible(ep, false)
	}
	switch ep.Protocol {
	case model.ProtocolVLESS, model.ProtocolVMess:
		// Xray 官方文档:vless/vmess 的 id 是「合法 UUID 或 < 30 字节的
		// 任意字符串」,自定义 id 合法。只有超过长度又不是 UUID 的值才会
		// 构造失败。
		if !ValidUUID(ep.UUID) && len(ep.UUID) >= 30 {
			return fmt.Errorf("xray 的 id 须为 UUID 或少于 30 字节的字符串")
		}
	}
	return nil
}

// linksCompatible 对应 shareLink 的取舍:能出的就是 vless/vmess/trojan、
// 外部 classic ss 与 QUIC 链接。
func linksCompatible(ep Endpoint) error {
	if ep.Protocol.QUIC() {
		return quicEndpoint(ep)
	}
	switch ep.Protocol {
	case model.ProtocolShadowsocks:
		// 只有外部 classic ss(SSMethod 非空)出 ss://;ss 的 URI 不能带传输层。
		if ep.SSMethod == "" || effectiveNetwork(ep) != model.NetworkTCP {
			return fmt.Errorf("shadowsocks 无可用分享链接")
		}
		if ep.Password == "" {
			return fmt.Errorf("shadowsocks 缺密码")
		}
		return nil
	case model.ProtocolVLESS, model.ProtocolTrojan:
		if err := endpointShape(ep); err != nil {
			return err
		}
		return nil
	case model.ProtocolVMess:
		if err := endpointShape(ep); err != nil {
			return err
		}
		// vmess 链接是 JSON,alterId 任何值都能写。
		if s := vmessSecurity(ep); !vmessCiphers[s] {
			return fmt.Errorf("vmess 加密 %q 不受支持", s)
		}
		return nil
	default:
		return fmt.Errorf("协议 %q 无分享链接", ep.Protocol)
	}
}
