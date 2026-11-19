package model

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// ProbePoint 是一台境内常驻探测点(UC-12):操作者自有的境内主机,跑 mpx probe run
// 主动拉探测目标、推回结果。它独立于代理节点:没有 SSH、没有核心、不进入下发
// 与订阅,只为触达判定提供境内一侧来源。
//
// 凭据是两段式的:一次性 JoinToken 换正式 Token,库里都只存 SHA-256。
type ProbePoint struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	LastIP     string     `json:"last_ip,omitempty"`
	// Online 由 LastSeenAt 推导,读时现算,不落库。
	Online bool `json:"online"`
	// JoinToken 只在创建响应里出现一次,库中不存明文。
	JoinToken string `json:"join_token,omitempty"`
	// TokenHash / JoinTokenHash 只用于存储层,不序列化给 API。
	TokenHash     string `json:"-"`
	JoinTokenHash string `json:"-"`
}

// ProbeSubjectKey 是一个探测对象在触达存档里的稳定身份。
// 自建节点是 "node:<节点ID>";外部订阅端点沿用 render.EndpointKey
// ("s:<源ID>:<指纹>")。身份一旦写入存档不再改,上游改名不影响判定归属。
type ProbeSubjectKey string

// NodeSubjectKey 返回自建节点的对象身份。
func NodeSubjectKey(id int64) ProbeSubjectKey {
	return ProbeSubjectKey("node:" + strconv.FormatInt(id, 10))
}

// HostSubjectKey 返回一个有独立对外地址的入口(Host)的对象身份 ——
// ISS-15:CDN 类入口不随节点判定连坐,自己有自己的探测对象。
func HostSubjectKey(id int64) ProbeSubjectKey {
	return ProbeSubjectKey("host:" + strconv.FormatInt(id, 10))
}

// ParseHostSubjectKey 认出 "host:<id>" 形式的对象身份,返回 Host ID。
func ParseHostSubjectKey(k ProbeSubjectKey) (int64, bool) {
	s := string(k)
	id, err := strconv.ParseInt(strings.TrimPrefix(s, "host:"), 10, 64)
	if !strings.HasPrefix(s, "host:") || err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ParseNodeSubjectKey 认出 "node:<id>" 形式的对象身份,返回节点 ID。
func ParseNodeSubjectKey(k ProbeSubjectKey) (int64, bool) {
	s := string(k)
	id, err := strconv.ParseInt(strings.TrimPrefix(s, "node:"), 10, 64)
	if !strings.HasPrefix(s, "node:") || err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ReachSubjectCheck 是一个探测对象的触达存档(UC-12):节点级判定 + 明细 JSON。
// 与旧版 reach_checks 的区别是主体从 node_id 换成统一身份,报告形状由
// internal/reach 定义,模型层不重复声明。
type ReachSubjectCheck struct {
	SubjectKey ProbeSubjectKey `json:"subject_key"`
	CheckedAt  time.Time       `json:"checked_at"`
	Verdict    string          `json:"verdict"`
	Report     json.RawMessage `json:"report"`
}

// ReachSourceCheck 是(对象 × 来源)的原始探测结果存档(UC-12)。Summary 是
// reach.SourceSummary 的 JSON;Results 是 reach 明细(自建按入站键的 map、
// 外部端点是单一结果)的 JSON,模型层不解析。
type ReachSourceCheck struct {
	SubjectKey ProbeSubjectKey `json:"subject_key"`
	SourceID   string          `json:"source_id"`
	Scope      string          `json:"scope"` // domestic / cp
	CheckedAt  time.Time       `json:"checked_at"`
	Summary    json.RawMessage `json:"summary"`
	Results    json.RawMessage `json:"results,omitempty"`
}
