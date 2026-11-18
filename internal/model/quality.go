package model

import (
	"encoding/json"
	"strings"
	"time"
)

// NodeQuality 是一台节点最近一次 IP 质量探测的存档:判定 + 加权分 + 明细 JSON。
// 与 reach_checks 同一形态:控制面只校验与透传明细,明细结构由 quality.go 定义。
type NodeQuality struct {
	NodeID    int64           `json:"node_id"`
	CheckedAt time.Time       `json:"checked_at"`
	Verdict   string          `json:"verdict"` // ok / partial / untested
	Score     int             `json:"score"`   // 0–100;untested 时无意义,界面不展示
	Report    json.RawMessage `json:"report"`
}

// NodeQualityVerdicts 是质量判定的合法值:
// ok 探测完整出分;partial 部分来源失败但已尽力出分;untested 没测出出口 IP 不记分。
var NodeQualityVerdicts = map[string]bool{
	"ok": true, "partial": true, "untested": true,
}

// QualityReport 是 node_quality.report 的明细形状,CLI 与界面都按它渲染。
type QualityReport struct {
	EgressIP   string             `json:"egress_ip,omitempty"`
	Region     string             `json:"region,omitempty"`      // ipwho.is 给出的国家码,大写
	CityEn     string             `json:"city_en,omitempty"`     // ipwho.is 原文城市
	City       string             `json:"city,omitempty"`        // 经 CityZh 对照后的中文城市,没有则为空
	ASN        string             `json:"asn,omitempty"`         // ipapi.is,例如 AS13335
	Org        string             `json:"org,omitempty"`         // ipapi.is 组织名
	Flags      map[string]bool    `json:"flags,omitempty"`       // ipapi.is 风险标记:is_datacenter / is_vpn / is_proxy / is_tor / is_abuser / is_bogon
	AbuseScore *int               `json:"abuse_score,omitempty"` // AbuseIPDB 置信分(0–100),只在控制面配了密钥时有
	Score      int                `json:"score"`
	Deductions []QualityDeduction `json:"deductions,omitempty"`
	Steps      []QualityStep      `json:"steps"`           // 每个探测子步骤的结果
	Error      string             `json:"error,omitempty"` // untested 的原因
}

// QualityStep 记录一次探测里每个子步骤的状态。
type QualityStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok / failed / skipped
	Reason string `json:"reason,omitempty"`
}

// QualityDeduction 是一条扣分项,界面可以直接列出来解释分数。
type QualityDeduction struct {
	Flag   string `json:"flag"`
	Points int    `json:"points"`
	Reason string `json:"reason"`
}

// qualityWeights 是初版扣分权重:基准 100,命中标记就扣。
// 改权重等于改分数语义,调过要在文档与 UI 说明里同步。
var qualityWeights = []QualityDeduction{
	{Flag: "is_tor", Points: 50, Reason: "Tor 出口,通用场景基本不可用"},
	{Flag: "is_bogon", Points: 40, Reason: "bogon 地址,出口异常"},
	{Flag: "is_abuser", Points: 30, Reason: "被标记为滥用来源"},
	{Flag: "is_proxy", Points: 25, Reason: "已被识别为代理,易被风控"},
	{Flag: "is_vpn", Points: 25, Reason: "已被识别为 VPN,易被风控"},
	{Flag: "is_datacenter", Points: 10, Reason: "机房 IP;多数 VPS 属此类,轻度扣"},
}

// QualityScore 按风险标记与 AbuseIPDB 置信分算 0–100 的质量分。
// abuse 为 nil 表示没查(未配置密钥);分数下限 0。
func QualityScore(flags map[string]bool, abuse *int) (int, []QualityDeduction) {
	score := 100
	var out []QualityDeduction
	for _, w := range qualityWeights {
		if flags[w.Flag] {
			score -= w.Points
			out = append(out, w)
		}
	}
	if abuse != nil && *abuse > 0 {
		points := *abuse * 3 / 10
		if points > 0 {
			score -= points
			out = append(out, QualityDeduction{Flag: "abuse_score", Points: points, Reason: "AbuseIPDB 举报置信分"})
		}
	}
	if score < 0 {
		score = 0
	}
	return score, out
}

// NormalizeRegion 归一化地区名:stored 为空或不是两位国家码时用探测值替换;
// 两边都是有效国家码但不同时返回 conflict=true,由操作者自己处理,不自动改。
func NormalizeRegion(stored, detected string) (region string, conflict bool) {
	stored = trimUpper(stored)
	detected = trimUpper(detected)
	if RegionFlag(detected) == "" {
		// 探测没给国家码:stored 原样返回,有效无效都不动。
		return stored, false
	}
	if RegionFlag(stored) == "" {
		return detected, false
	}
	if stored != detected {
		return stored, true
	}
	return stored, false
}

func trimUpper(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}
