package model

import "time"

// UpdateInfo 是「有没有比现在更新的发布」这个问题的答案。
//
// 它只带消息,不带动作:升级永远是一次显式的人工决定。控制面换掉的正是自己,
// 自动换出一个起不来的二进制,连"查一下现在什么状态"这条路都断了。
//
// 单独定义一份而不是直接复用 updatecheck.Result,是为了让 API 的形状不跟着那个
// 包的内部实现走 —— 它多存了几个字段(比如未经截断的正文),没必要都发出去。
type UpdateInfo struct {
	// Current 是当前版本;Latest 是上游最新发布的版本。
	Current string `json:"current,omitempty"`
	Latest  string `json:"latest,omitempty"`
	// Behind 是落后了几次发布;0 表示已是最新。
	Behind int `json:"behind"`
	// URL 是最新发布的网页地址,去看具体改了什么。
	URL string `json:"url,omitempty"`
	// Notes 是最新发布正文的开头一段。
	Notes string `json:"notes,omitempty"`
	// PublishedAt 是最新发布的发布时间。
	PublishedAt *time.Time `json:"published_at,omitempty"`
	// CheckedAt 是这份结论是什么时候问出来的。
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	// Error 非空表示这次没问成,其余字段是上一次的结论。
	Error string `json:"error,omitempty"`
}

// Newer 报告确实有一份更新的发布。
//
// 当前版本或最新版本缺一个时一律返回 false:不知道自己在哪一版,就不能说别人
// 更新。这比猜一个要诚实,也免得从源码编出来的开发版天天提示"有新版本"。
func (u UpdateInfo) Newer() bool {
	return u.Latest != "" && u.Current != "" && u.Behind > 0
}

// Counts 是各类对象的数量,给状态页一眼看规模用。
type Counts struct {
	Nodes    int `json:"nodes"`
	Inbounds int `json:"inbounds"`
	Hosts    int `json:"hosts"`
	Users    int `json:"users"`
}

// NodeBackoff 是一台正处于失败退避中的节点。
type NodeBackoff struct {
	NodeID   int64  `json:"node_id"`
	NodeName string `json:"node_name"`
	// Failures 是连续失败次数,成功一次即清零。
	Failures int `json:"failures"`
	// SkipUntil 是这个窗口的结束时间,到了就重新试。
	SkipUntil time.Time `json:"skip_until"`
}
