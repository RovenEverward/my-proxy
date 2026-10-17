package model

import "time"

// UpdatePart 是「某一部分的版本」的统一形状,GET /api/update、/api/status 与
// mpx update 共用。
//
// 单独定义一份而不是直接复用 UpdateInfo:那个是 /api/status 里嵌套的形状,键名
// 叫 url;这里按 RFC 用 release_url。两边各用各的名字,免得为了复用把已经发出去
// 的键名改掉。
type UpdatePart struct {
	// Current 是这一部分现在跑的版本。从源码直接编出来时为空。
	Current string `json:"current"`
	// Latest 是上游最新发布的版本;控制面还没查到时为空。
	Latest string `json:"latest"`
	// Behind 是落后了几次发布;0 表示已是最新或数不出来。
	Behind int `json:"behind"`
	// Available 表示确实有一份更新可装。当前版本未知时一律为 false。
	Available bool `json:"available"`
	// ReleaseURL 是最新发布在网页上的地址,去看具体改了什么。
	ReleaseURL string `json:"release_url"`
	// PublishedAt 是最新发布的发布时间;没问到时为 null。
	PublishedAt *time.Time `json:"published_at"`
	// CheckedAt 是这份结论是什么时候问出来的;没问过时为 null。
	CheckedAt *time.Time `json:"checked_at"`
	// Error 非空表示最近一次没问成,其余字段是上一次的结论。
	Error string `json:"error"`
	// Last 是控制面最近一次执行升级/回退的状态;从没执行过时为 nil。
	//
	// model 不能反过来依赖 selfupdate,所以这里单独定义一份同形状的 UpdateRun,
	// 由 server 在拼响应时从 selfupdate.State 转过来。
	Last *UpdateRun `json:"last,omitempty"`
}

// UpdateRun 是控制面一次升级/回退的执行状态,字段与 selfupdate.State 一一对应。
//
// 单独定义而不是复用 selfupdate.State:model 是被接口与 CLI 共同依赖的最底层,
// 不能为了一个 JSON 形状就把它和改动系统二进制的 selfupdate 绑在一起。
type UpdateRun struct {
	Component  string    `json:"component"`
	State      string    `json:"state"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Result     string    `json:"result"`
	Trigger    string    `json:"trigger"`
	Error      string    `json:"error"`
}

// UpdateNodeCore 是一台节点上某个核心的版本行。
//
// 一个节点装了两种核心就出两行。Current 是观察到的值(可能为空:还没查过、或者
// 查的时候核心没起来);Target 是控制面给这个核心钉的死版本 —— xray 是
// xrayrelease.RecommendedVersion,sing-box 是 singboxbuild.DefaultVersion。
type UpdateNodeCore struct {
	NodeID int64  `json:"id"`
	Name   string `json:"name"`
	// Display 是只读显示名(旗帜 + 城市/国家 + 字母);没有地区时为空。
	Display string `json:"display"`
	Core    Core   `json:"core"`
	Current string `json:"current"`
	Target  string `json:"target"`
}

// UpdateNodeEvent 是节点核心升级任务里的一条事件。
//
// 一次升级按「每台节点的每个核心」产生事件:开始时一条 start,结束再按结果补一条
// ok / skip / rolled_back / failed。Seq 全局递增,客户端靠 since 增量拉取。
type UpdateNodeEvent struct {
	Seq    int64  `json:"seq"`
	Node   string `json:"node"`
	Core   Core   `json:"core"`
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status"`
	Msg    string `json:"msg"`
}

// UpdateNodeEventStatuses 是一次升级任务里允许出现的事件状态。
var UpdateNodeEventStatuses = []string{"start", "ok", "skip", "rolled_back", "failed"}

// UpdateNodesProgress 是 GET /api/update/nodes 的形状。
//
// 它是节点核心升级任务的进度快照:State 是任务状态(见 UpdateNodesStates),
// Seq 是本次响应带回来的最新游标(没有新事件时等于请求里的 since),Events 是
// since 之后最早的一批事件(服务端会截断,客户端拿 Seq 当下一次 since 即可)。
type UpdateNodesProgress struct {
	Job    int64             `json:"job"`
	State  string            `json:"state"`
	Seq    int64             `json:"seq"`
	Events []UpdateNodeEvent `json:"events"`
}

// 节点核心升级任务的状态取值。
const (
	UpdateNodesIdle    = "idle"
	UpdateNodesRunning = "running"
	UpdateNodesDone    = "done"
	UpdateNodesFailed  = "failed"
)

// UpdateSnapshot 是 GET /api/update 的形状:控制面自己那一部分,加上各节点核心。
//
// 本机 CLI 的那一行不在里面 —— 控制面不认识调用它的那份 CLI,mpx update 拿回
// 这份之后在自己这边补上 CLI 行。
type UpdateSnapshot struct {
	Control UpdatePart       `json:"control"`
	Nodes   []UpdateNodeCore `json:"nodes"`
}

// UpdateReport 是 mpx update -o json 的完整形状,比控制面多一行本机 CLI。
type UpdateReport struct {
	CLI     UpdatePart       `json:"cli"`
	Control UpdatePart       `json:"control"`
	Nodes   []UpdateNodeCore `json:"nodes"`
}

// UpdatePartFrom 把 /api/status 里的更新结论翻成独立接口的形状。
//
// 「有没有更新」沿用 UpdateInfo.Newer() 的判据,和 /api/status 保持同一套说法:
// 当前版本未知时不能说别人更新。抽成一个函数,是为了让 /api/update 与控制面
// 内部那条路径共用同一份翻译,不会各算各的。
func UpdatePartFrom(info UpdateInfo) UpdatePart {
	return UpdatePart{
		Current:     info.Current,
		Latest:      info.Latest,
		Behind:      info.Behind,
		Available:   info.Newer(),
		ReleaseURL:  info.URL,
		PublishedAt: info.PublishedAt,
		CheckedAt:   info.CheckedAt,
		Error:       info.Error,
	}
}

// ToUpdatePart 是控制面状态里那一份版本的独立形状,CLI 回退到 /api/status 时用。
//
// 当前版本优先取 status.version:RFC §5 说的就是"CLI 读取 /api/status.version",
// 而嵌套的 update.current 可能因为更新源没接上而空着。两者本该一致,不一致时
// 以显式的那一个为准。
func (st ControlStatus) ToUpdatePart() UpdatePart {
	part := UpdatePartFrom(st.Update)
	if st.Version != "" {
		part.Current = st.Version
	}
	return part
}
