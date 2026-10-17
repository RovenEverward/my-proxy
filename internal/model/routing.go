package model

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 分流规则(见 docs/internal/routing-rules-rfc.md)。一条规则 = 匹配类型 + 值 + 去向;
// 同一作用域内按 Position 从上往下匹配,第一条命中的生效,都没命中走兜底(RouteFinal)。

// RoutingMode 决定订阅的分流部分从哪来:template 是旧路径(模板原样 + 用途),
// rules 是由规则表生成。迁移成功前一律是 template。
type RoutingMode string

const (
	RoutingTemplate RoutingMode = "template"
	RoutingRules    RoutingMode = "rules"
)

// RuleType 是规则的匹配方式。
type RuleType string

const (
	RuleSuffix  RuleType = "suffix"  // 域名后缀(含该域名本身)
	RuleKeyword RuleType = "keyword" // 域名包含
	RuleSetRef  RuleType = "set"     // 内置规则组(见 RuleCatalog)
	RuleGeoIP   RuleType = "geoip"   // IP 所在国家(两位大写国家码)
	RuleCIDR    RuleType = "cidr"    // IP 段
)

// 去向:直连、拦截,或 "group:<id>" 指向一个策略组。不能指向单个节点(RFC 决策 4)。
const (
	TargetDirect = "DIRECT"
	TargetReject = "REJECT"
	groupPrefix  = "group:"
)

// GroupTarget 返回指向策略组的去向字符串。
func GroupTarget(id int64) string { return groupPrefix + strconv.FormatInt(id, 10) }

// TargetGroupID 解析去向;是策略组时返回 id 与 true。
func TargetGroupID(t string) (int64, bool) {
	if !strings.HasPrefix(t, groupPrefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(t[len(groupPrefix):], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ValidTarget 只检查形状;组是否存在由写入口对照存储检查。
func ValidTarget(t string) bool {
	if t == TargetDirect || t == TargetReject {
		return true
	}
	_, ok := TargetGroupID(t)
	return ok
}

// RouteRule 是一条分流规则。UserID 为 0 表示全局规则。
type RouteRule struct {
	ID       int64    `json:"id"`
	UserID   int64    `json:"user_id,omitempty"`
	Position int      `json:"position"`
	Type     RuleType `json:"type"`
	Value    string   `json:"value"`
	Target   string   `json:"target"`
	Enabled  bool     `json:"enabled"`
	Note     string   `json:"note,omitempty"`
}

var (
	ruleDomain  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
	ruleKeyword = regexp.MustCompile(`^[a-z0-9.-]{1,64}$`)
	ruleCountry = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Normalize 把值整理成存储形式:域名、关键词小写,国家码大写,IP 段取规范写法。
func (r *RouteRule) Normalize() {
	r.Value = strings.TrimSpace(r.Value)
	r.Note = strings.TrimSpace(r.Note)
	switch r.Type {
	case RuleSuffix, RuleKeyword:
		r.Value = strings.TrimPrefix(strings.ToLower(r.Value), ".")
	case RuleGeoIP:
		r.Value = strings.ToUpper(r.Value)
	case RuleCIDR:
		if p, err := netip.ParsePrefix(r.Value); err == nil {
			r.Value = p.Masked().String()
		}
	}
}

// Validate 只做不依赖存储的检查。值里不允许逗号与换行:Clash 规则是逗号分隔的
// 一行文本,放进去就能拼出别的规则。
func (r RouteRule) Validate() error {
	if strings.ContainsAny(r.Value, ",\r\n") {
		return fmt.Errorf("规则值不能包含逗号或换行")
	}
	switch r.Type {
	case RuleSuffix:
		if len(r.Value) > 253 || !ruleDomain.MatchString(r.Value) {
			return fmt.Errorf("域名后缀 %q 不合法,应像 example.com", r.Value)
		}
	case RuleKeyword:
		if !ruleKeyword.MatchString(r.Value) {
			return fmt.Errorf("关键词 %q 只能是 1–64 位小写字母、数字、点或连字符", r.Value)
		}
	case RuleSetRef:
		if _, ok := RuleSetByKey(r.Value); !ok {
			return fmt.Errorf("没有内置规则组 %q(mpx rule catalog 查看)", r.Value)
		}
	case RuleGeoIP:
		if !ruleCountry.MatchString(r.Value) {
			return fmt.Errorf("国家码 %q 应为两位大写字母,如 CN", r.Value)
		}
	case RuleCIDR:
		if _, err := netip.ParsePrefix(r.Value); err != nil {
			return fmt.Errorf("IP 段 %q 不合法,应像 91.108.4.0/22", r.Value)
		}
	default:
		return fmt.Errorf("规则类型只能是 suffix / keyword / set / geoip / cidr")
	}
	if !ValidTarget(r.Target) {
		return fmt.Errorf("去向只能是策略组、DIRECT 或 REJECT")
	}
	if utf8.RuneCountInString(r.Note) > 80 {
		return fmt.Errorf("备注最多 80 个字")
	}
	return nil
}

// MaxRulesPerScope 限制每个作用域的规则数,防止误操作把订阅撑爆。
const MaxRulesPerScope = 1000

// GroupMode 是策略组在客户端里怎么选节点。
type GroupMode string

const (
	GroupAuto   GroupMode = "auto"   // 按延迟自动选
	GroupManual GroupMode = "manual" // 用户在客户端里手选
)

// ProxyGroup 是按条件自动收入口的策略组;成员在渲染时按当前用户的端点计算。
type ProxyGroup struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Icon         string    `json:"icon,omitempty"`
	Mode         GroupMode `json:"mode"`
	Regions      []string  `json:"regions,omitempty"`      // 国家码,任一
	Protocols    []string  `json:"protocols,omitempty"`    // 协议短名(与订阅名后缀一致,如 Reality、Hy2),任一
	Capabilities []string  `json:"capabilities,omitempty"` // 能力检测,全部通过
	Tags         []string  `json:"tags,omitempty"`         // 节点/入口标签,任一
	TestURL      string    `json:"test_url,omitempty"`
	Interval     int       `json:"interval,omitempty"` // 秒
}

// 策略组名不能和这些保留名或内置规则组生成的组名撞上(渲染时会出同名组)。
var reservedGroupNames = map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "GLOBAL": true, "direct": true, "block": true, "其他全部": true}

func (g *ProxyGroup) Normalize() {
	g.Name = strings.TrimSpace(g.Name)
	g.Icon = strings.TrimSpace(g.Icon)
	if g.Mode == "" {
		g.Mode = GroupAuto
	}
	if g.TestURL == "" {
		g.TestURL = "https://www.gstatic.com/generate_204"
	}
	if g.Interval == 0 {
		g.Interval = 300
	}
	for i, r := range g.Regions {
		g.Regions[i] = strings.ToUpper(strings.TrimSpace(r))
	}
}

func (g ProxyGroup) Validate() error {
	if g.Name == "" || utf8.RuneCountInString(g.Name) > 32 || strings.ContainsAny(g.Name, ",\r\n") {
		return fmt.Errorf("策略组名不能为空、最多 32 个字,且不能含逗号或换行")
	}
	if reservedGroupNames[g.Name] {
		return fmt.Errorf("%q 是保留名", g.Name)
	}
	for _, rs := range ruleCatalog {
		if rs.Name == g.Name {
			return fmt.Errorf("%q 和内置规则组重名,换一个名字", g.Name)
		}
	}
	if utf8.RuneCountInString(g.Icon) > 4 {
		return fmt.Errorf("图标最多 4 个字符")
	}
	if g.Mode != GroupAuto && g.Mode != GroupManual {
		return fmt.Errorf("选择方式只能是 auto / manual")
	}
	for _, r := range g.Regions {
		if !ruleCountry.MatchString(r) {
			return fmt.Errorf("地区 %q 应为两位大写国家码", r)
		}
	}
	for _, p := range g.Protocols {
		if !containsString(ProtocolShortNames, p) {
			return fmt.Errorf("未知协议 %q(可选 %s)", p, strings.Join(ProtocolShortNames, " / "))
		}
	}
	for _, c := range g.Capabilities {
		if !ValidCapabilityTarget(c) {
			return fmt.Errorf("能力条件只能是 egress / youtube / netflix")
		}
	}
	for _, t := range g.Tags {
		if !tagSlug.MatchString(t) {
			return fmt.Errorf("无效标签 %q", t)
		}
	}
	if !strings.HasPrefix(g.TestURL, "http://") && !strings.HasPrefix(g.TestURL, "https://") {
		return fmt.Errorf("测速地址必须是 HTTP(S)")
	}
	if g.Interval < 30 || g.Interval > 86400 {
		return fmt.Errorf("测速间隔必须在 30–86400 秒之间")
	}
	return nil
}

// ProtocolShortNames 是 render.ProtocolShort 可能给出的全部短名,策略组按它筛协议。
var ProtocolShortNames = []string{"Reality", "Hy2", "TUIC", "SS", "WG", "WS", "gRPC", "XHTTP", "SplitHTTP", "HTTPUpgrade", "VLESS", "VMess", "Trojan"}

// GroupCandidate 是判断策略组成员需要的入口属性。
type GroupCandidate struct {
	Region       string
	Protocol     string // 协议短名,见 render.ProtocolShort
	Tags         []string
	Capabilities map[string]bool // 已稳定判定为可用的能力
}

// Admits 判断一个入口是否进组:各类条件之间同时满足,类内任一(能力为全部)。
func (g ProxyGroup) Admits(c GroupCandidate) bool {
	if len(g.Regions) > 0 && !containsString(g.Regions, c.Region) {
		return false
	}
	if len(g.Protocols) > 0 && !containsString(g.Protocols, c.Protocol) {
		return false
	}
	for _, cap := range g.Capabilities {
		if !c.Capabilities[cap] {
			return false
		}
	}
	if len(g.Tags) == 0 {
		return true
	}
	for _, t := range c.Tags {
		if containsString(g.Tags, t) {
			return true
		}
	}
	return false
}

// RuleSet 是一个内置规则组:每种客户端格式各自的上游规则集,内容由客户端下载更新。
type RuleSet struct {
	Key       string           `json:"key"`
	Name      string           `json:"name"`
	Desc      string           `json:"desc"`
	Default   string           `json:"default"` // DIRECT / REJECT / PROXY(PROXY 表示第一个策略组)
	PrivateIP bool             `json:"private_ip,omitempty"`
	Clash     []ClashRuleSet   `json:"clash"`
	SingBox   []SingBoxRuleSet `json:"sing_box"`
}

type ClashRuleSet struct {
	Name      string `json:"name"`
	Behavior  string `json:"behavior"`
	URL       string `json:"url"`
	NoResolve bool   `json:"no_resolve,omitempty"`
}

type SingBoxRuleSet struct {
	Tag string `json:"tag"`
	URL string `json:"url"`
}

// RuleCatalog 返回内置规则组目录(副本)。
func RuleCatalog() []RuleSet { return append([]RuleSet(nil), ruleCatalog...) }

func RuleSetByKey(k string) (RuleSet, bool) {
	for _, rs := range ruleCatalog {
		if rs.Key == k {
			return rs, true
		}
	}
	return RuleSet{}, false
}
