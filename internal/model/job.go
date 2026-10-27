package model

import (
	"encoding/json"
	"time"
)

// 后台任务的种类与状态。Job.Kind / Job.Status 只取这里的值。
const (
	JobKindDiagnose = "diagnose" // 节点体检
	JobKindInstall  = "install"  // 节点核心安装
)

const (
	JobStatusRunning     = "running"     // 正在跑
	JobStatusDone        = "done"        // 跑完了,同步接口会回的 HTTP 状态码是 200
	JobStatusFailed      = "failed"      // 跑完了但出错,Code 是同步接口会回的状态码
	JobStatusInterrupted = "interrupted" // 控制面在它没跑完时重启了
)

// Job:控制面在后台替节点做的一件长事(体检、安装)。关掉页面也会做完。
type Job struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"` // diagnose | install
	NodeID     int64           `json:"node_id"`
	Status     string          `json:"status"` // running | done | failed | interrupted
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at"`
	Code       int             `json:"code,omitempty"` // 同步接口会回的 HTTP 状态码
	Error      string          `json:"error,omitempty"`
	UIHint     string          `json:"ui_hint,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"` // 同步接口会回的 JSON 正文
}
