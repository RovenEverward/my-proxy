package control

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// alertCooldown 是同一个 Key 两次上报之间至少隔多久。
//
// 30 分钟是"够久"和"够快"之间的折中:巡检每 5 分钟一轮,一个持续存在的故障如果
// 完全不冷却,一小时会打 12 条同样的日志,没人会去看第 12 条 —— 日志里真正
// 有用的信号会被自己的重复冲掉。反过来,冷却太长(比如 24 小时)会让一个下午
// 修好又坏掉的节点在日志里只留一条,看不出它反复过。
//
// 注意冷却只压重复,不压"恢复":Resolve 任何时候都会打,因为"好了"是一条新
// 信息。冷却也不影响落库——状态每次检查都写,受影响的只有日志。
const alertCooldown = 30 * time.Minute

// Alerter 是告警的出口。
//
// 定义成接口而不是直接调 LogAlerter,是因为"判断什么该喊"和"喊到哪儿去"是
// 两件事:前者是巡检的逻辑(已经完整),后者这一批只做到日志。批 4 加 webhook
// 时只需要再写一个实现,判断逻辑一行都不用改。
//
// 实现必须自己处理重复:Alert 会被每轮巡检重复调用,同一个 Key 报多少次由
// 实现决定(LogAlerter 的冷却就是干这个的),调用方不做去重。
type Alerter interface {
	// Alert 报出一件事。
	Alert(a model.Alert)
	// Resolve 声明某个 Key 指的那件事已经消失,可以打一条"已恢复"。
	Resolve(key string, at time.Time)
}

// LogAlerter 是默认的告警实现:把告警落进日志。
//
// 这一批只做落日志。webhook 留给批 4,但接口先留好 —— 告警的难点不在"怎么发
// 出去",而在"判断什么该发、多久发一次",那部分现在已经完整了,换一个实现只
// 需要再写一个 Alert/Resolve。要加 webhook 时照这个类型抄一份,把 logger 换成
// HTTP 客户端即可;冷却与去重的逻辑可以直接复用这里的状态机。
type LogAlerter struct {
	logger *slog.Logger

	mu   sync.Mutex
	last map[string]time.Time // Key -> 上次真的报出去的时刻
}

// NewLogAlerter 造一个落日志的告警器。
func NewLogAlerter(logger *slog.Logger) *LogAlerter {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogAlerter{logger: logger, last: map[string]time.Time{}}
}

// Alert 报出一件事;同一个 Key 在冷却期内只报一次。
//
// 判定用的是"上次报出去的这一刻",不是"上次看到这件事"。差别在于:如果一台
// 节点每 5 分钟被检查一次、每次都不通,那么冷却期内它被看到 6 次、只报 1 次,
// 但冷却一过就会再报 —— 故障一直没修,日志里会一直有,只是不再刷屏。
func (a *LogAlerter) Alert(al model.Alert) {
	a.mu.Lock()
	if last, ok := a.last[al.Key]; ok && al.At.Sub(last) < alertCooldown {
		a.mu.Unlock()
		return
	}
	a.last[al.Key] = al.At
	a.mu.Unlock()

	attrs := []any{"key", al.Key, "kind", string(al.Kind)}
	if al.NodeID != 0 {
		attrs = append(attrs, "node_id", al.NodeID, "node", al.NodeName)
	}
	a.logger.Warn("节点告警: "+al.Message, attrs...)
}

// Resolve 说"这个 Key 指的那件事已经没了",并清掉它的冷却状态。
//
// 清冷却不是清理而是语义:条件消失之后再出现,是一次**新的**故障,该立刻报出来。
// 留着旧的时间戳会让它被上一轮的冷却摁住,而那正是"刚修好又坏了"最需要知道的
// 时刻。没有正在报的 Key 上来 Resolve 时什么都不做 —— 只有真的报过才谈得上恢复。
func (a *LogAlerter) Resolve(key string, at time.Time) {
	a.mu.Lock()
	_, was := a.last[key]
	delete(a.last, key)
	a.mu.Unlock()

	if !was {
		return
	}
	a.logger.Info("节点告警已恢复", "key", key)
}

// alertKey 拼出一个告警的 Key。
//
// Key 必须**稳定**:同样的故障在两次巡检里要拼出同一个字符串,否则去重失效,
// 冷却形同虚设。所以它只由"是哪台节点、哪一类问题、哪张证书"拼成 ——
// 绝不能把时间戳、剩余天数、错误原文放进来,那些每轮都在变。
func alertKey(kind model.Kind, nodeID int64, extra string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", kind, nodeID, extra)))
	return fmt.Sprintf("%s-%d-%s", kind, nodeID, hex.EncodeToString(h[:4]))
}
