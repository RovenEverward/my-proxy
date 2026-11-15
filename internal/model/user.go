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

	// InboundIDs 限定该用户可用的入站;为空表示全部入站(没挂套餐时)。
	// 挂了套餐时,它是套餐之外的「额外授权」,与套餐取并集。
	InboundIDs []int64 `json:"inbound_ids,omitempty"`

	// PlanID 是用户挂的套餐 id;nil 表示没挂套餐,完全沿用上面的旧语义。
	// 用指针是为了在整体 PUT 里区分「没提这个字段」和「摘掉套餐」。
	PlanID *int64 `json:"plan_id,omitempty"`

	// planAllNodes / planInbounds 是挂套餐时解析出来的授权,只在内存里,不序列化。
	// 判定入口只有 AllowsInbound 一处,别的地方不再各自解释套餐。
	//
	// planResolved 表示这份授权是否已经按套餐解析过,只有 SetPlanAuth 会置位。
	// 从 JSON 直接解码出来的用户即使带着 plan_id,这份授权也是空的;不把
	// 「已解析」与「plan_id 非空」分开,这种对象会被误判成什么入站都不能用。
	planResolved bool
	planAllNodes bool
	planInbounds map[int64]bool

	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SetPlanAuth 设置解析后的套餐授权(仅内存,不落库),并把这份授权标记为已解析。
// allNodes 为真表示全部入站,此时 inboundIDs 被忽略。存储层读用户时按套餐表解析
// 后调用它;没挂套餐的用户也要调用(传 allNodes=false、inboundIDs=nil),这样
// 「已解析」与「有没有套餐」是两件独立的事实。
func (u *User) SetPlanAuth(allNodes bool, inboundIDs []int64) {
	u.planResolved = true
	u.planAllNodes = allNodes
	if len(inboundIDs) == 0 {
		u.planInbounds = nil
		return
	}
	m := make(map[int64]bool, len(inboundIDs))
	for _, id := range inboundIDs {
		m[id] = true
	}
	u.planInbounds = m
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
//
// 判定只有这一份,订阅、xray 下发、收敛、能力检测、用户筛选都走这里。
//
//   - 套餐授权没解析(从没调用过 SetPlanAuth,例如从 JSON 解码出来的用户):
//     按旧语义判定,InboundIDs 为空表示全部入站 —— 不能因为带着 plan_id 却
//     没有解析结果就判成什么都不能用。
//   - 已解析且没挂套餐(PlanID 为空):同样按旧语义,InboundIDs 为空表示全部。
//   - 已解析且挂了套餐:all_nodes 套餐就是全部;否则可用范围 = 套餐入站 ∪ 用户
//     自己的 InboundIDs(额外授权)。套餐为空且没有额外授权时什么都不能用 ——
//     空套餐不等于全开,这是有意的。
func (u *User) AllowsInbound(inboundID int64) bool {
	if u.planResolved && u.PlanID != nil {
		if u.planAllNodes || u.planInbounds[inboundID] {
			return true
		}
	} else if len(u.InboundIDs) == 0 {
		return true
	}
	for _, id := range u.InboundIDs {
		if id == inboundID {
			return true
		}
	}
	return false
}
