package source

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"gopkg.in/yaml.v3"
)

// parse_value.go 是三种容器共用的取值与校验助手。
//
// 原则:认不得又影响连接的字段一律让整条节点进 Issue,而不是丢掉字段继续
// 输出;原因只写字段名与枚举值,不带上游值(可能是凭据)。

// extraKeys 返回 m 里不在 allowed 中的键名(排序后),用于「未知字段」报告。
func extraKeys(m map[string]any, allowed map[string]bool) []string {
	var extra []string
	for k := range m {
		if !allowed[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return extra
}

func sortStrings(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func extraErr(extra []string) error {
	return fmt.Errorf("存在无法完整保留的字段: %s", strings.Join(extra, ", "))
}

// getString 取字符串字段;数字也按字符串收(订阅里端口/密码写成数字很常见)。
func getString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case string:
			return strings.TrimSpace(t)
		case int, int64, float64, uint64:
			return fmt.Sprintf("%v", t)
		}
	}
	return ""
}

// getBool 取布尔字段;字符串 "true"/"false" 也认。
func getBool(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// getInt 取整数字段;字符串数字也认。
func getInt(m map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case int:
			return t, true
		case int64:
			return int(t), true
		case uint64:
			return int(t), true
		case float64:
			return int(t), true
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// getStringList 取字符串列表;单个字符串/逗号串也归一成列表。
func getStringList(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case string:
			return splitCSV(t)
		case []string:
			return t
		case []any:
			out := make([]string, 0, len(t))
			for _, e := range t {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// requireMap 取字段的子对象;字段存在但不是映射时返回 error——上游写了
// 非对象形态的子配置,不能当作「没写」处理。
func requireMap(m map[string]any, key string) (map[string]any, error) {
	v, present := m[key]
	if !present || v == nil {
		return nil, nil
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 不是对象,无法保留", key)
	}
	return sub, nil
}

// getMap 取子对象字段;非对象时返回 nil(用于宽松读取,调用方负责用
// requireMap 校验关键字段)。
func getMap(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			return sub
		}
	}
	return nil
}

// parsePort 校验端口;端口写成字符串、带空格、越界都算坏。
func parsePort(v any) (int, error) {
	switch t := v.(type) {
	case int:
		return checkPort(t)
	case int64:
		return checkPort(int(t))
	case uint64:
		return checkPort(int(t))
	case float64:
		return checkPort(int(t))
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, fmt.Errorf("端口不是数字")
		}
		return checkPort(n)
	}
	return 0, fmt.Errorf("端口缺失")
}

func checkPort(n int) (int, error) {
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("端口 %d 越界", n)
	}
	return n, nil
}

// parseHostPort 从 "host:port" 拆出地址与端口。
func parseHostPort(s string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, fmt.Errorf("地址端口格式无效")
	}
	port, err := checkPort(mustAtoi(portStr))
	return host, port, err
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return -1
	}
	return n
}

// mapNetwork 把外部容器的传输名归一到 model.Network。归不了的返回
// unexpressible=true:调用方必须跳过该节点,不能降级成 tcp。
func mapNetwork(s string) (model.Network, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "tcp":
		return model.NetworkTCP, false
	case "ws", "websocket":
		return model.NetworkWS, false
	case "grpc":
		return model.NetworkGRPC, false
	case "xhttp":
		return model.NetworkXHTTP, false
	case "splithttp":
		return model.NetworkSplitHTTP, false
	case "httpupgrade", "http-upgrade":
		return model.NetworkHTTPUpgrade, false
	}
	return "", true
}

// mapSecurity 把安全层名归一;返回 unexpressible 时调用方必须跳过。
func mapSecurity(s string) (model.Security, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return model.SecurityNone, false
	case "tls":
		return model.SecurityTLS, false
	case "reality":
		return model.SecurityReality, false
	}
	return "", true
}

// yamlMapOf 把任意 YAML 值规整成 map[string]any;不是映射时返回 false。
func yamlMapOf(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	return nil, false
}

// listOfMaps 把 YAML/JSON 里的序列规整成 map 列表;元素不是映射的按
// index 单独报 issue 跳过。
func listOfMaps(v any) ([]map[string]any, []int, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("不是列表")
	}
	out := make([]map[string]any, 0, len(items))
	var bad []int
	for i, item := range items {
		if m, ok := yamlMapOf(item); ok {
			out = append(out, m)
		} else {
			bad = append(bad, i)
			out = append(out, nil)
		}
	}
	return out, bad, nil
}

// yamlScalar 保证 yaml.v3 解码出的 map 键是 string(yaml.Unmarshal 进
// map[string]any 时子层键仍可能是其他类型,这里统一规整)。
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeYAML(e)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[fmt.Sprintf("%v", k)] = normalizeYAML(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = normalizeYAML(e)
		}
		return t
	default:
		return v
	}
}

// decodeYAML 把正文解成规整后的顶层映射。
func decodeYAML(body []byte) (map[string]any, error) {
	var top map[string]any
	if err := yaml.Unmarshal(body, &top); err != nil {
		return nil, err
	}
	if n, ok := normalizeYAML(top).(map[string]any); ok {
		return n, nil
	}
	return top, nil
}

// tuicUUIDCheck 校验 tuic 的 UUID:sing-box 在构造期就解析,坏值 FATAL
// (与 vless/vmess 不同,那里核心容忍)。mihomo 虽容忍,但这类节点必然
// 连不上,按无效字段处理。
func tuicUUIDCheck(uuid string) string {
	if !render.ValidUUID(uuid) {
		return "tuic 的 uuid 不是合法 UUID"
	}
	return ""
}

// ssKeyCheck 校验 2022 method 的密钥形态。非 2022 method 不检查——
// classic method 的密码是任意字符串。
func ssKeyCheck(method, password string) string {
	if !render.ValidSS2022Key(method, password) {
		return "ss-2022 密码须为 base64 密钥且长度匹配 method"
	}
	return ""
}

// realityFieldCheck 校验 REALITY 的公钥与 short id:核心对这两种字段做
// 解码校验,sing-box 对过长 short id 直接 panic。
func realityFieldCheck(ep *render.Endpoint) string {
	if !render.ValidRealityPubKey(ep.RealityPub) {
		return "reality 公钥不是合法的 32 字节 base64"
	}
	if !render.ValidRealityShortID(ep.RealitySID) {
		return "reality short id 须为不超过 8 字节的 hex"
	}
	return ""
}

// realityProtocolCheck 校验 REALITY 只出现在 vless/vmess 上;其他协议没有
// 这个组合,放过去只会渲染出核心不认的配置。
func realityProtocolCheck(ep *render.Endpoint) string {
	if ep.Security == model.SecurityReality &&
		ep.Protocol != model.ProtocolVLESS && ep.Protocol != model.ProtocolVMess {
		return fmt.Sprintf("协议 %q 不支持 reality", ep.Protocol)
	}
	return ""
}
