package model

import (
	"strconv"
	"time"
)

// ReachSourceSample 是一次活性探测的样本(对象 × 来源),UC-20 境内稳定度的
// 原料。reach_source_checks 只存每个(对象,来源)的最新一行,滚动指标要历史,
// 所以探测回写时把同一摘要追加进样本表。
type ReachSourceSample struct {
	SubjectKey ProbeSubjectKey `json:"subject_key"`
	SourceID   string          `json:"source_id"`
	Scope      string          `json:"scope"`
	CheckedAt  time.Time       `json:"checked_at"`
	Probed     bool            `json:"probed"`
	Reachable  bool            `json:"reachable"`
	Failed     bool            `json:"failed"`
	LatencyMs  int64           `json:"latency_ms"`
}

// StabilityMetrics 是境内稳定度的滚动聚合视图,读时现算不落库。
// Rounds 是窗口内参与统计的探测轮数;样本不足时 Insufficient 为真,
// 其余字段无意义,界面显示「样本不足」而不是给出一个低置信百分比。
type StabilityMetrics struct {
	WindowDays int `json:"window_days"`
	Rounds     int `json:"rounds"`
	// SuccessRate 保留两位小数的比例(0–1)。
	SuccessRate float64 `json:"success_rate"`
	LatencyMs   int64   `json:"latency_ms"` // 可达轮次的中位时延;没有可达轮次为 0
	// Sources 是窗口内贡献样本的境内来源数。
	Sources      int  `json:"sources"`
	Insufficient bool `json:"insufficient"`
}

// StabilityMinRounds 是稳定度出数的最小样本量。
const StabilityMinRounds = 3

// BandwidthSample 是一次带宽测量存档(UC-20)。Status 为 ok 时 BytesPerSec
// 有效;failed / unsupported 记录失败原因,不覆盖展示值——展示层取最近一次
// ok 样本配最新一次状态。
type BandwidthSample struct {
	SubjectKey  ProbeSubjectKey `json:"subject_key"`
	SourceID    string          `json:"source_id"`
	CheckedAt   time.Time       `json:"checked_at"`
	Status      string          `json:"status"` // ok / failed / unsupported
	BytesPerSec int64           `json:"bytes_per_sec,omitempty"`
	DurationMs  int64           `json:"duration_ms,omitempty"`
	Bytes       int64           `json:"bytes,omitempty"`
	TestURL     string          `json:"test_url,omitempty"`
	Error       string          `json:"error,omitempty"`
}

// FormatBps 把字节/秒按订阅名与界面通用的短式排版:
// ≥1Mbps 取整为 Mbps,慢到不足 1Mbps 显 Kbps —— 这种节点值本身就值得警告。
func FormatBps(bps int64) string {
	kbps := bps / 125 // 字节/秒 → Kbps(十进制,订阅名里的惯例口径)
	if kbps >= 1000 {
		return strconv.FormatInt(kbps/1000, 10) + "Mbps"
	}
	return strconv.FormatInt(kbps, 10) + "Kbps"
}

// BandwidthStatuses 是带宽样本状态的合法值。
var BandwidthStatuses = map[string]bool{
	"ok": true, "failed": true, "unsupported": true,
}

// BandwidthView 是带宽指标对外的展示形状:最近一次有效值 + 最近状态。
type BandwidthView struct {
	Status      string     `json:"status"` // untested / ok / failed / unsupported
	BytesPerSec int64      `json:"bytes_per_sec,omitempty"`
	MeasuredAt  *time.Time `json:"measured_at,omitempty"`
	SourceID    string     `json:"source_id,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// SubjectMetrics 是一个探测对象的完整指标视图(UC-20)。
type SubjectMetrics struct {
	SubjectKey ProbeSubjectKey  `json:"subject_key"`
	Stability  StabilityMetrics `json:"stability"`
	Bandwidth  BandwidthView    `json:"bandwidth"`
}
