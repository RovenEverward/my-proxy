package model

import "time"

// Kind 是告警的种类,一个 Kind 对应一类可恢复的状况。
type Kind string

const (
	KindNodeUnreachable Kind = "node_unreachable"
	KindNodeInactive    Kind = "node_inactive"
	KindNodeDrift       Kind = "node_drift"
	KindCertExpiring    Kind = "cert_expiring"
)

// 用户侧通知的种类,用于 user_notices 表记"哪一类提醒已经发过"。
const (
	NoticeKindExpiring  = "expiring"   // 快到期
	NoticeKindOverUsage = "over_usage" // 快超限
)

// UserNotice 是"这一条用户通知已经发过了"的记录。
//
// 它存在的意义是让用户侧提醒精确地只发一次:巡检每轮都会看一遍用户,如果只看
// 窗口(比如"三天内到期")而不记账,一轮漏跑或窗口横跨多轮就会重复发。记下
// (user, kind) 之后,同一用户同一类提醒只在条件消失(续费/重置)后才会再发。
type UserNotice struct {
	UserID int64     `json:"user_id"`
	Kind   string    `json:"kind"`
	SentAt time.Time `json:"sent_at"`
}

// SubPull 是一个用户最近一次订阅拉取的记录。
//
// 首次拉到的时间只在真正"第一次"时写,之后只更新最近一次的时间与客户端信息;
// 吊销订阅 token 后会清掉,新链接重新从第一次开始等。
type SubPull struct {
	Username      string    `json:"username"`
	FirstPulledAt time.Time `json:"first_pulled_at"`
	LastPulledAt  time.Time `json:"last_pulled_at"`
	Format        string    `json:"format"`
	Client        string    `json:"client"`
}

// Alert 是一条告警。
//
// Key 是这条告警的身份:同一个 Key 在冷却期内只报一次,条件消失时用同一个 Key
// 去 Resolve。所以 Key 必须由「哪台节点的哪一类问题」拼出来(NodeID + Kind,
// 证书再加路径),不能带上时间戳或剩余天数 —— 那样每次算出来都是新的 Key,
// 去重就失效了。
type Alert struct {
	Key      string    `json:"key"`
	Kind     Kind      `json:"kind"`
	NodeID   int64     `json:"node_id"`
	NodeName string    `json:"node_name"`
	Message  string    `json:"message"`
	At       time.Time `json:"at"`
}

// DoctorCheck 是自检的一项结论。
type DoctorCheck struct {
	// Name 是这一项的名字,给人看也用来定位(如 "节点 hk-1")。
	Name string `json:"name"`
	// Status 取 ok / warn / fail。
	Status string `json:"status"`
	// Detail 是结论的依据,带具体数字。
	Detail string `json:"detail"`
	// Hint 是"接下来该做什么",没有可做的就为空。
	Hint string `json:"hint,omitempty"`
	// UIHint 是同一个"接下来该做什么"的界面版:界面里该怎么做,只描述界面
	// 操作、不出现命令;为空时界面退回 Hint。
	UIHint string `json:"ui_hint,omitempty"`
	// NodeID 是这一项检查对应的节点;不是节点检查项的为空。
	NodeID int64 `json:"node_id,omitempty"`
	// UIAction 是界面可以对这个检查项直接做的动作。目前只有 "apply" = 界面可以
	// 提供「应用到节点」按钮(NodeID 指明是哪台);为空表示没有可做的行内动作。
	UIAction string `json:"ui_action,omitempty"`
}

// DoctorReport 是 GET /api/doctor 的回答,也是 mpx doctor 打印的东西。
//
// 与 ControlStatus / NodeView 同样的理由:服务端与 CLI 共用一份形状。
type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
}

// 自检结论的三个取值。
const (
	DoctorOK   = "ok"
	DoctorWarn = "warn"
	DoctorFail = "fail"
)

// HasFailure 报告这份报告里有没有 fail。CLI 拿它决定退出码:有一项 fail 就是
// 非零,脚本可以直接拿 doctor 当健康检查。
func (r DoctorReport) HasFailure() bool {
	for _, c := range r.Checks {
		if c.Status == DoctorFail {
			return true
		}
	}
	return false
}

// ControlStatus 是控制面自报的「现在怎么样」,GET /api/status 与 mpx status 共用。
//
// 与 HostView 同样的理由:服务端和 CLI 看的是同一份形状,分叉只会让两边各自
// 漂移。这些字段全在内存里,控制面一重启就从零再算 —— 库里的账不受影响。
type ControlStatus struct {
	// StartedAt 是控制面进程启动的时间。
	StartedAt time.Time `json:"started_at"`
	// UptimeSeconds 是到这次查询为止已运行的秒数。
	UptimeSeconds float64 `json:"uptime_seconds"`
	// IntervalSeconds 是后台任务的周期。
	IntervalSeconds float64 `json:"interval_seconds"`
	// Ticks 是已跑完的后台轮数。
	Ticks uint64 `json:"ticks"`
	// LastTickAt 是上一轮开始的时间;一次都没跑过时为 nil。
	LastTickAt *time.Time `json:"last_tick_at"`
	// LastTickSeconds 是上一轮花掉的秒数。
	LastTickSeconds float64 `json:"last_tick_seconds"`

	// Version 是这份控制面的版本。从源码直接编出来、又没打 tag 时为空。
	Version string `json:"version,omitempty"`
	// Update 是更新检查的结论。没查过、或者查不成时字段基本是空的。
	Update UpdateInfo `json:"update"`
	// TLSFingerprint 是控制面自签证书的公钥指纹(SHA256:...),CLI 按它认人。
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
	// Devices 是接入的设备数;DevicePasswordSet 表示设备密码接入是否开着。
	Devices           int  `json:"devices"`
	DevicePasswordSet bool `json:"device_password_set"`
	// ConnectLockedUntil 非空表示设备密码连续输错、接入锁定到这个时刻。
	ConnectLockedUntil *time.Time `json:"connect_locked_until,omitempty"`

	Counts   Counts        `json:"counts"`
	Backoffs []NodeBackoff `json:"backoffs"`
}
