package model

import (
	"regexp"
	"time"
)

// SubscriptionRule 把 User-Agent 正则映射到一种订阅输出格式。
type SubscriptionRule struct {
	Pattern string       `json:"pattern"`
	Result  ClientFormat `json:"result"`
	// Label 是展示名,只给界面用,不影响匹配;服务端按内置表派生,不落库。
	Label string `json:"label,omitempty"`
}

// SameMatch 报告两条规则的匹配语义是否相同(Pattern 与 Result 一致)。
//
// 它有意不看 Label:Label 只是展示名,不落库、不参与匹配,新旧服务端之间
// 比对规则表时它不该算差异(见 cli 的 rulesAreBuiltin)。
func (r SubscriptionRule) SameMatch(o SubscriptionRule) bool {
	return r.Pattern == o.Pattern && r.Result == o.Result
}

// WithBuiltinLabels 返回一份新切片:Label 为空、且 Pattern+Result 与某条内置规则
// 相同的规则补上内置的 Label;已有 Label 或对不上内置表的原样保留。
//
// 它只影响响应与界面展示,不写回库、不改匹配;不修改传入的切片。
func WithBuiltinLabels(rules []SubscriptionRule) []SubscriptionRule {
	if rules == nil {
		return nil
	}
	builtin := BuiltinRules()
	out := make([]SubscriptionRule, len(rules))
	copy(out, rules)
	for i := range out {
		if out[i].Label != "" {
			continue
		}
		for _, b := range builtin {
			if out[i].SameMatch(b) {
				out[i].Label = b.Label
				break
			}
		}
	}
	return out
}

// Settings 是控制面的全局设置。
type Settings struct {
	// SubURLPrefix 是订阅链接前缀,CLI 用它拼出完整订阅地址。
	SubURLPrefix string `json:"sub_url_prefix"`
	// ProfileTitle 是客户端里显示的订阅名,支持 {USERNAME} 等变量。
	ProfileTitle string `json:"profile_title"`
	SupportLink  string `json:"support_link"`
	// UpdateInterval 是客户端自动更新间隔,单位小时。
	UpdateInterval int `json:"update_interval"`

	// WebhookURL 是通知的投递地址,POST 一个 JSON 事件过去;为空表示不通知。
	WebhookURL string `json:"webhook_url"`
	// NotifyDaysLeft 是用户到期前多少天开始提醒(用户侧通知)。
	NotifyDaysLeft int `json:"notify_days_left"`
	// NotifyUsagePercent 是用户用量达到多少百分比开始提醒(用户侧通知)。
	NotifyUsagePercent int `json:"notify_usage_percent"`

	Rules []SubscriptionRule `json:"rules"`

	// Templates 是各格式的客户端模板,键是 ClientFormat 的字符串
	// (clash-meta / sing-box / xray)。缺键或空串表示用内置默认模板。
	Templates map[string]string `json:"templates"`

	// RulesetMode 决定订阅里引用的上游规则集从哪下载:"" / "upstream" 由
	// 客户端直连上游,"privatized" 由控制面镜像、客户端改从控制面取。
	RulesetMode string `json:"ruleset_mode"`
}

// RulesetModePrivatized 是设置里「规则集由控制面镜像」的取值。
const RulesetModePrivatized = "privatized"

// RulesetModeValid 校验 ruleset_mode 的合法值:空串即上游直连(零值),
// 其余只认 upstream / privatized。
func RulesetModeValid(v string) bool {
	return v == "" || v == "upstream" || v == RulesetModePrivatized
}

// 用户侧通知的默认阈值,写入设置里的缺省值。0 有特殊含义(见 DaysLeft 与
// UsagePercent),所以默认值用正数,和"关掉某项通知"的写法分开。
const (
	DefaultNotifyDaysLeft     = 3
	DefaultNotifyUsagePercent = 80
)

// DaysLeft 返回用户到期的剩余天数;不过期返回 0 与 false。
func (u *User) DaysLeft(now time.Time) (int, bool) {
	if u.ExpireAt == nil {
		return 0, false
	}
	days := int(u.ExpireAt.Sub(now).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return days, true
}

// UsagePercent 返回本周期已用量的百分比;不限量返回 0。
func (u *User) UsagePercent() int {
	if u.DataLimit <= 0 {
		return 0
	}
	pct := int(u.UsedTraffic * 100 / u.DataLimit)
	if pct > 100 {
		pct = 100
	}
	return pct
}

// TemplateFor 返回该格式的模板文本;没存过或存了空串时返回空串,
// 调用方据此回落内置默认模板。
func (s *Settings) TemplateFor(format ClientFormat) string {
	if s == nil || s.Templates == nil {
		return ""
	}
	return s.Templates[string(format)]
}

// BuiltinRules 是开箱即用的 UA 识别规则,按顺序匹配。
//
// 它的存在是「订阅链接只有一条、客户端各有各的格式」这件事的另一半:光有
// 三大格式的模板,没有规则把请求认到对应模板上,Clash 客户端拿到的还是纯
// 分享链接。所以规则表不是可选配置,而是默认体验的一部分 —— 用户把订阅
// 地址填进客户端就该拿到对的配置,不该先来读文档写正则。
//
// 顺序即优先级,短的/具体的放前面:clash 系要先认 mihomo 再认 clash,否则
// 「clash-verge/v1.7.0 mihomo/v1.18.0」这种同时报多个名字的 UA 会被宽的那条
// 先接住,拿到原版 Clash 的最小集合而不是完整的 meta 底稿。
//
// 返回新的切片,调用方改动不会波及其他调用者。
func BuiltinRules() []SubscriptionRule {
	return []SubscriptionRule{
		// Karing 会同时声称自己是好几种客户端,排在最前,免得更宽的那条抢走它。
		{Pattern: `(?i)karing`, Result: FormatClashMeta, Label: "Karing"},
		// mihomo 系(Clash Verge Rev / Mihomo Party / Stash / FlClash)。
		{Pattern: `(?i)mihomo|clash-verge|clash\.meta|clashmeta|flclash|stash`, Result: FormatClashMeta, Label: "Clash Verge、Mihomo Party、FlClash、Stash"},
		// 原版 Clash 家族排在 meta 之后:它们的 UA 里通常只有 "clash"。
		{Pattern: `(?i)clash`, Result: FormatClash, Label: "其他 Clash 客户端"},
		{Pattern: `(?i)sing-box|sfi/|sfa/|sfm/|hiddify|nekobox`, Result: FormatSingBox, Label: "sing-box、Hiddify、NekoBox"},
		{Pattern: `(?i)xray`, Result: FormatXray, Label: "Xray"},
		// 这几个都只认 base64 链接列表,给不了 YAML/JSON。
		{Pattern: `(?i)v2rayn|v2rayng`, Result: FormatBase64, Label: "v2rayN、v2rayNG"},
		{Pattern: `(?i)shadowrocket`, Result: FormatBase64, Label: "Shadowrocket"},
		{Pattern: `(?i)quantumult`, Result: FormatBase64, Label: "Quantumult"},
		{Pattern: `(?i)surge|loon`, Result: FormatBase64, Label: "Surge、Loon"},
	}
}

// RuleList 返回生效的规则表。没配过规则(空表)时回落内置默认。
//
// 空表算「没配过」而不是「用户特意关掉了分流」:后者可以自己写一条
// `(?i).*` → links 的规则来表达,而前者是绝大多数人的真实状态 —— 他们
// 装完控制面就直接把订阅填进客户端了。老库升级上来时 rules 列是 "[]",
// 走的也是这条路,不然修好的默认规则对新装的库生效、对已有的库不生效,
// 同一版本两种行为。
func (s *Settings) RuleList() []SubscriptionRule {
	if s == nil || len(s.Rules) == 0 {
		return BuiltinRules()
	}
	return s.Rules
}

// MatchFormat 返回第一条匹配该 User-Agent 的规则结果与是否命中;没有命中时
// 返回 FormatLinks 与 false。
//
// 第二个返回值区分「规则真的命中了」和「谁都没匹配上、落的默认值」:同一个
// FormatLinks,前者是用户在规则里点名要的,后者只是没人认领。订阅入口靠它决定
// 浏览器要不要拿订阅页 —— 只有走到「没人认领的默认」这一步,页面才顶上来。
//
// 规则只有个位数,每次请求重新编译的开销可以忽略,因此不缓存编译结果。
// 写入口负责拒绝编译不过的正则;这里遇到坏正则跳过而不是报错,免得一条坏
// 规则让整个订阅不可用。
func (s *Settings) MatchFormat(userAgent string) (ClientFormat, bool) {
	for _, r := range s.RuleList() {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			continue
		}
		if re.MatchString(userAgent) {
			return r.Result, true
		}
	}
	return FormatLinks, false
}
