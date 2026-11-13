package model

import (
	"encoding/json"
	"time"
)

// ReachCheck 是一 台节点最近一次触达检查的存档:判定 + 明细 JSON。
// 明细用 json.RawMessage 存,控制面只做校验与透传,不在 model 里再展开一份
// (明细形状由 internal/reach 定义,模型层不重复声明同一份结构)。
type ReachCheck struct {
	NodeID    int64           `json:"node_id"`
	CheckedAt time.Time       `json:"checked_at"`
	Verdict   string          `json:"verdict"`
	Report    json.RawMessage `json:"report"`
}

// ReachVerdicts 是节点级判定的合法值,跟 internal/reach 的常量对应;
// 放在 model 里是因为这是 API 与存储的契约词。
var ReachVerdicts = map[string]bool{
	"ok": true, "blocked": true, "down": true, "unknown": true,
}
