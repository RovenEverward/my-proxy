package model

import "time"

// ResetStrategy 是流量周期重置策略。
type ResetStrategy string

const (
	ResetNone    ResetStrategy = "none"
	ResetDaily   ResetStrategy = "daily"
	ResetWeekly  ResetStrategy = "weekly"
	ResetMonthly ResetStrategy = "monthly"
)

// Status 是用户对外的可用状态。
type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusExpired  Status = "expired"
	StatusLimited  Status = "limited" // 流量已超限
)

// UserStatuses 按 User.Status 的判定优先级列出全部状态。
//
// 判定逻辑只有 Status 方法那一份,这里只列取值 —— 校验查询参数、拼错误提示、
// 写测试都从这一处取,新加状态时不会漏掉某个列表。
var UserStatuses = []Status{StatusDisabled, StatusExpired, StatusLimited, StatusActive}

// Valid 报告取值是不是一个已知状态。空串不算:它在这里表示"不限",由调用方处理。
func (s Status) Valid() bool {
	for _, v := range UserStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// User 是一个订阅主体。
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	// Key 是派生 UUID / 密码的种子,不直接下发。
	Key string `json:"key,omitempty"`
	// SubToken 是订阅链接里那段不可猜的路径。与 Key 分开是为了让订阅链接泄
	// 露后可以单独换掉,而不用连带换掉用户在各个协议上的凭证。
	SubToken string `json:"sub_token,omitempty"`

	Enabled bool `json:"enabled"`

	DataLimit int64 `json:"data_limit"` // 字节;0 表示不限
	// UsedTraffic 是本周期内已用的字节数;周期重置会把它归零。
	UsedTraffic int64 `json:"used_traffic"`
	// LifetimeUsedTraffic 是从创建至今的累计用量,周期重置不动它。
	LifetimeUsedTraffic int64      `json:"lifetime_used_traffic"`
	ExpireAt            *time.Time `json:"expire_at,omitempty"` // nil 表示不过期

	ResetStrategy ResetStrategy `json:"reset_strategy"`
	// TrafficResetAt 是上一次周期重置的时刻,也是周期的起算点;nil 表示从未
	// 重置过(以 CreatedAt 起算)。只存这一个事实,下次重置时间由它现算。
	TrafficResetAt *time.Time `json:"traffic_reset_at,omitempty"`

	// InboundIDs 限定该用户可用的入站;为空表示全部入站。
	InboundIDs []int64 `json:"inbound_ids,omitempty"`

	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Status 按固定优先级判定用户状态:显式停用 > 过期 > 超限 > 可用。
func (u *User) Status(now time.Time) Status {
	switch {
	case !u.Enabled:
		return StatusDisabled
	case u.ExpireAt != nil && now.After(*u.ExpireAt):
		return StatusExpired
	case u.DataLimit > 0 && u.UsedTraffic >= u.DataLimit:
		return StatusLimited
	default:
		return StatusActive
	}
}

// AllowsInbound 报告该用户是否被授权使用某个入站。
// InboundIDs 为空表示全部入站。
func (u *User) AllowsInbound(inboundID int64) bool {
	if len(u.InboundIDs) == 0 {
		return true
	}
	for _, id := range u.InboundIDs {
		if id == inboundID {
			return true
		}
	}
	return false
}
