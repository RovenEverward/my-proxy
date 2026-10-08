package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// webhookTimeout 是一条 webhook 最多等多久。
//
// 5 秒的取舍:webhook 是"顺手喊一声",收不收得到都不该拖住控制面的正事。正常的
// 地址(钉钉/Slack/自建)都在一秒内应答;5 秒还没答,多半是地址写错了或者对面
// 挂了,再等下去只是让采样和收敛陪着一起卡。
const webhookTimeout = 5 * time.Second

// 事件类型的取值,写在 JSON 的 event 字段里。
const (
	EventAlert      = "alert"       // 节点出了问题
	EventResolved   = "resolved"    // 节点的问题好了
	EventUserNotice = "user_notice" // 用户快到期 / 快超限
	EventTest       = "test"        // 来自"测试 webhook"按钮,确认地址通不通
)

// WebhookEvent 是发到 webhook 上的一条 JSON。
//
// 不直接把 model.Alert 发出去,是因为这个通道还要送用户通知 —— 用户不是节点,
// 硬塞进 Alert 只会让那个结构长出"有时指节点、有时指人"的字段。这里字段全部
// 可以缺省:节点告警没有 username,用户通知没有 node_id。
type WebhookEvent struct {
	// Event 是事件类型:alert / resolved / user_notice。
	Event string `json:"event"`
	// Kind 是细分类:节点告警的 Kind,或用户通知的 NoticeKind。
	Kind string `json:"kind,omitempty"`
	// Message 是给人看的一句话。
	Message string    `json:"message"`
	At      time.Time `json:"at"`

	// Key 是告警的身份,只有 alert / resolved 有:靠它把"好了"对回"坏了"。
	Key string `json:"key,omitempty"`

	NodeID   int64  `json:"node_id,omitempty"`
	NodeName string `json:"node_name,omitempty"`

	Username string `json:"username,omitempty"`
}

// PostWebhook 把一条事件 POST 到 url。
//
// 单独抽出来是为了让"测试 webhook"和真正的告警走同一条路:设置页里的测试、
// CLI 的 settings test、巡检里的通知,三处各写一份的话,测试通过而告警发不出去
// 是迟早的事。
//
// 判断成功只看 2xx:很多服务端(比如各种机器人)用 200 表示收到了,4xx/5xx 都
// 意味着没送到。响应正文最多留 200 字节做错误提示,免得一个坏地址回一整页 HTML
// 把日志撑满,也免得正文没读完导致连接不能复用。
func PostWebhook(ctx context.Context, url string, ev WebhookEvent, logger *slog.Logger) error {
	if url == "" {
		return fmt.Errorf("没有配置 webhook 地址")
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("序列化 webhook 事件: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造 webhook 请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: webhookTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发不出 webhook: %w", err)
	}
	defer resp.Body.Close()

	tail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(tail))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("webhook 返回 %d: %s", resp.StatusCode, msg)
	}
	if logger != nil {
		logger.Debug("webhook 已投递", "event", ev.Event, "kind", ev.Kind)
	}
	return nil
}

// WebhookAlerter 把节点告警发到设置里的 webhook 地址。
//
// 冷却和去重的规则和 LogAlerter 完全一样(两条出口各自记一份状态),额外多记了
// 每条告警的内容 —— Resolve 只拿到一个 Key,要把"哪台节点的哪件事好了"发出去,
// 就得回头查当初报的是什么。
//
// 投递失败也照样记冷却:节点告警同时走了日志那条出口,人在日志里一定看得到,
// webhook 只是多一个通道;为了一条没送到的 webhook 每轮重试,只会让一个写错的
// 地址每 30 秒刷一行警告。用户通知不一样,那里没有日志兜底,所以那一边发失败
// **不**记账(见 notify.go)。
type WebhookAlerter struct {
	// url 每次现取:设置里的地址运行中能被改,构造时定死的话改完要重启控制面。
	url    func() string
	logger *slog.Logger

	mu     sync.Mutex
	active map[string]model.Alert // Key -> 当初报的那条,给 Resolve 用
	last   map[string]time.Time   // Key -> 上次真的发出去的时刻
}

// NewWebhookAlerter 造一个发 webhook 的告警器。url 为 nil 或返回空串时整条出口
// 静默 —— 没配地址就不发,这不该算错。
func NewWebhookAlerter(url func() string, logger *slog.Logger) *WebhookAlerter {
	if logger == nil {
		logger = slog.Default()
	}
	if url == nil {
		url = func() string { return "" }
	}
	return &WebhookAlerter{
		url:    url,
		logger: logger,
		active: map[string]model.Alert{},
		last:   map[string]time.Time{},
	}
}

// Alert 报出一件事;同一个 Key 在冷却期内只发一次。
func (a *WebhookAlerter) Alert(al model.Alert) {
	a.mu.Lock()
	if last, ok := a.last[al.Key]; ok && al.At.Sub(last) < alertCooldown {
		a.mu.Unlock()
		return
	}
	a.last[al.Key] = al.At
	a.active[al.Key] = al
	a.mu.Unlock()

	url := a.url()
	if url == "" {
		return
	}
	ev := WebhookEvent{
		Event:    EventAlert,
		Kind:     string(al.Kind),
		Message:  al.Message,
		At:       al.At,
		Key:      al.Key,
		NodeID:   al.NodeID,
		NodeName: al.NodeName,
	}
	if err := PostWebhook(context.Background(), url, ev, a.logger); err != nil {
		a.logger.Warn("发告警 webhook 失败", "key", al.Key, "error", err)
	}
}

// Resolve 说"这个 Key 指的那件事已经没了"。
//
// 和 LogAlerter 一样,只有真的报过才发恢复 —— 一条从没发出去的告警再来一条
// "已恢复",只会让收到的人以为错过了什么。
func (a *WebhookAlerter) Resolve(key string, at time.Time) {
	a.mu.Lock()
	al, was := a.active[key]
	delete(a.active, key)
	delete(a.last, key)
	a.mu.Unlock()

	if !was {
		return
	}
	url := a.url()
	if url == "" {
		return
	}
	ev := WebhookEvent{
		Event:    EventResolved,
		Kind:     string(al.Kind),
		Message:  al.Message + "(已恢复)",
		At:       at,
		Key:      key,
		NodeID:   al.NodeID,
		NodeName: al.NodeName,
	}
	if err := PostWebhook(context.Background(), url, ev, a.logger); err != nil {
		a.logger.Warn("发恢复 webhook 失败", "key", key, "error", err)
	}
}

// FanoutAlerter 把一条告警同时送给多个出口。
//
// 存在的理由:webhook 一配上,日志不该就此消失 —— 出问题时最可靠的现场还是控制
// 面的日志。两个出口自己管自己的冷却,不共用状态:它们失败的方式不一样(webhook
// 会超时,日志不会),合成一个状态只会让一方的抖动影响另一方。
type FanoutAlerter struct {
	alerters []Alerter
}

// NewFanoutAlerter 把若干出口合成一个。nil 出口直接丢掉,调用方不用先判空。
func NewFanoutAlerter(alers ...Alerter) *FanoutAlerter {
	out := &FanoutAlerter{}
	for _, a := range alers {
		if a != nil {
			out.alerters = append(out.alerters, a)
		}
	}
	return out
}

// Alert 把一条告警发给每个出口。
func (f *FanoutAlerter) Alert(al model.Alert) {
	for _, a := range f.alerters {
		a.Alert(al)
	}
}

// Resolve 把恢复发给每个出口。
func (f *FanoutAlerter) Resolve(key string, at time.Time) {
	for _, a := range f.alerters {
		a.Resolve(key, at)
	}
}
