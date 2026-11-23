package model

import "time"

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
