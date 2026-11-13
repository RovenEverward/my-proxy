// Package ruleset 是 UC-05 私有化分发里「把上游规则集镜像进控制面」的那半边。
//
// 分两块:Files / FilesForRules / FilesByGroup 是纯函数,回答「现在被引用的
// 上游规则集文件有哪些」;Manager 是唯一拉取执行者,定时与手动刷新共用同一份
// 单飞作业表,结果落在 ruleset_snapshots(见 internal/store/ruleset.go)。
//
// 镜像的粒度是「文件」而不是「规则组」:key 形如 "clash/<name>" 或
// "singbox/<tag>",渲染层按同一个 key 拼镜像地址。
package ruleset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// maxBody 是单个规则集文件的上限。mihomo/sing-box 的规则集都是几 MB 以内的
// 文本/二进制,16 MiB 留足余量;超出的多半是拉错了东西,宁可失败也不入库。
const maxBody = 16 << 20

// refreshInterval 是「多久没试过就该重试」的间隔,与客户端默认的 rule-provider
// 刷新周期(86400s)对齐。
const refreshInterval = 24 * time.Hour

// File 是一个待镜像的上游规则集文件。Key 全局唯一,与快照表、镜像 URL 共用。
type File struct {
	Fmt      string // "clash" 或 "singbox"
	Name     string // clash 的 provider 名 / sing-box 的 tag
	Key      string // "clash/<name>" 或 "singbox/<name>"
	Upstream string // 上游下载地址
}

// Files 返回内置目录(model.RuleCatalog)里全部组的全部文件,按 Key 去重。
func Files() []File {
	out := []File{}
	seen := map[string]bool{}
	for _, rs := range model.RuleCatalog() {
		out = appendSetFiles(out, seen, rs)
	}
	return out
}

// FilesForRules 返回这批规则引用到的上游文件:type=set 展开成该组的全部
// clash + singbox 文件;type=geoip 生成 sing-box 的 geoip-<国家码> 规则集
// (clash 的 GEOIP 规则是内核内置的,没有上游文件)。按 Key 去重,规则顺序保持。
func FilesForRules(rules []model.RouteRule) []File {
	out := []File{}
	seen := map[string]bool{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		switch r.Type {
		case model.RuleSetRef:
			rs, ok := model.RuleSetByKey(r.Value)
			if !ok {
				continue
			}
			out = appendSetFiles(out, seen, rs)
		case model.RuleGeoIP:
			f := geoipFile(r.Value)
			if f.Key != "" && !seen[f.Key] {
				seen[f.Key] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// FilesByGroup 按组聚合 FilesForRules 的结果:map key 是目录组 key;geoip 规则
// 不属于任何目录组,统一放进合成组 GeoIPGroupKey 下。组内文件按组与 key 去重。
func FilesByGroup(rules []model.RouteRule) map[string][]File {
	out := map[string][]File{}
	groupSeen := map[string]map[string]bool{}
	add := func(group string, f File) {
		if groupSeen[group] == nil {
			groupSeen[group] = map[string]bool{}
		}
		if groupSeen[group][f.Key] {
			return
		}
		groupSeen[group][f.Key] = true
		out[group] = append(out[group], f)
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		switch r.Type {
		case model.RuleSetRef:
			rs, ok := model.RuleSetByKey(r.Value)
			if !ok {
				continue
			}
			for _, c := range rs.Clash {
				add(rs.Key, File{Fmt: "clash", Name: c.Name, Key: "clash/" + c.Name, Upstream: c.URL})
			}
			for _, sb := range rs.SingBox {
				add(rs.Key, File{Fmt: "singbox", Name: sb.Tag, Key: "singbox/" + sb.Tag, Upstream: sb.URL})
			}
		case model.RuleGeoIP:
			if f := geoipFile(r.Value); f.Key != "" {
				add(GeoIPGroupKey, f)
			}
		}
	}
	return out
}

// GeoIPGroupKey 是 geoip 规则集文件在按组视图里的合成组名:它们没有目录组,
// 但状态接口按组给界面聚合,需要一个落点。
const GeoIPGroupKey = "geoip"

// geoipFile 展开一条 geoip 规则引用到的 sing-box 规则集。国家码非法时返回空
// File —— 校验在写入口做过,这里是防御,不是第二道校验。
func geoipFile(country string) File {
	cc := strings.ToLower(country)
	if len(cc) != 2 || cc[0] < 'a' || cc[0] > 'z' || cc[1] < 'a' || cc[1] > 'z' {
		return File{}
	}
	name := "geoip-" + cc
	return File{
		Fmt:      "singbox",
		Name:     name,
		Key:      "singbox/" + name,
		Upstream: "https://cdn.jsdelivr.net/gh/SagerNet/sing-geoip@rule-set/" + name + ".srs",
	}
}

// appendSetFiles 把一个目录组的两种格式文件展开进 out(按 Key 去重)。
func appendSetFiles(out []File, seen map[string]bool, rs model.RuleSet) []File {
	for _, c := range rs.Clash {
		f := File{Fmt: "clash", Name: c.Name, Key: "clash/" + c.Name, Upstream: c.URL}
		if !seen[f.Key] {
			seen[f.Key] = true
			out = append(out, f)
		}
	}
	for _, sb := range rs.SingBox {
		f := File{Fmt: "singbox", Name: sb.Tag, Key: "singbox/" + sb.Tag, Upstream: sb.URL}
		if !seen[f.Key] {
			seen[f.Key] = true
			out = append(out, f)
		}
	}
	return out
}

// FileByKey 按 key 找一个已知的文件:全目录的 clash/singbox 文件加上规则集
// geoip 命名规律的文件。不在引用列表里也能解出来 —— StartRefresh 只负责「这个
// key 认不认识」,该不该拉由调用方(RefreshDue/接口层)决定。
func FileByKey(key string) (File, bool) {
	for _, f := range Files() {
		if f.Key == key {
			return f, true
		}
	}
	if strings.HasPrefix(key, "singbox/geoip-") {
		if f := geoipFile(strings.ToUpper(strings.TrimPrefix(key, "singbox/geoip-"))); f.Key == key {
			return f, true
		}
	}
	return File{}, false
}

// Store 是 Manager 需要的持久化面,由 *store.Store 实现(测试里用替身)。
type Store interface {
	RulesetSnapshotBody(key string) ([]byte, string, error)
	CompleteRulesetRefresh(key, url string, at time.Time, body []byte, contentType, failure string) (becameReady bool, err error)
	ListRulesetSnapshots() (map[string]*store.RulesetSnapshot, error)
}

// Manager 是唯一的规则集拉取执行者:定时刷新(RefreshDue)与手动刷新
// (RefreshAll / StartRefresh)共用同一份单飞作业表,请求彼此合并、不重复拉取。
//
// 引用集合不在包里读库,由 refs 回调注入(控制面填 ListEnabledRules):
// 「哪些文件该镜像」是订阅渲染侧的决定,拉取器只执行。
type Manager struct {
	store   Store
	client  *http.Client
	changed func(becameReady bool)
	refs    func() ([]model.RouteRule, error)

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	jobs   map[string]chan struct{}
	closed bool
	wg     sync.WaitGroup
}

// New 组装一个 Manager。client 为空时自建:不走代理(镜像就是为了让客户端
// 不直连上游,控制面自己也别经节点绕一圈)、30s 超时、重定向限 http/https。
// changed 在一个文件「首次就绪」时被调,供控制面失效订阅缓存。
func New(st Store, client *http.Client, changed func(becameReady bool)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	if client == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		client = &http.Client{
			Transport: tr,
			Timeout:   30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("规则集重定向次数过多")
				}
				if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
					return errors.New("规则集重定向协议不受支持")
				}
				return nil
			},
		}
	}
	return &Manager{store: st, client: client, changed: changed,
		ctx: ctx, cancel: cancel, jobs: map[string]chan struct{}{}}
}

// SetRefs 注入「当前启用中的全部规则」。必须在 Run/RefreshDue/RefreshAll 之前
// 调用;没接时这几个方法什么都不做 —— 宁可不拉,也不把整个目录都拖回来。
func (m *Manager) SetRefs(refs func() ([]model.RouteRule, error)) { m.refs = refs }

// Running 报告某个文件 key 此刻有没有刷新作业在跑(状态接口用它标 refreshing)。
func (m *Manager) Running(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.jobs[key]
	return ok
}

// referencedFiles 算出当前引用集合的文件清单。refs 没接或读库失败时返回空
// 切片:拉取器没有自己的「该拉什么」的判断,拿不到引用就不动。
func (m *Manager) referencedFiles() []File {
	if m.refs == nil {
		return nil
	}
	rules, err := m.refs()
	if err != nil {
		return nil
	}
	return FilesForRules(rules)
}

// StartRefresh 给某个文件 key 起一个刷新作业。已在跑的作业会合并进去而不是
// 重开一个 —— 定时器与手动刷新撞上是常态,上游不该为同一份文件挨两遍打。
// key 不认识或 Manager 已关闭时返回错误,作业本身照常异步执行(不等结果)。
func (m *Manager) StartRefresh(key string) error {
	f, ok := FileByKey(key)
	if !ok {
		return fmt.Errorf("未知的规则集文件 %q", key)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("规则集刷新服务已停止")
	}
	if _, running := m.jobs[key]; running {
		m.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	m.jobs[key] = done
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.wg.Done()
		defer func() { m.mu.Lock(); delete(m.jobs, key); close(done); m.mu.Unlock() }()
		body, ct, failure := m.fetch(f.Upstream)
		if m.ctx.Err() != nil {
			return
		}
		becameReady, err := m.store.CompleteRulesetRefresh(key, f.Upstream,
			time.Now(), body, ct, failure)
		if err == nil && becameReady && m.changed != nil {
			m.changed(true)
		}
	}()
	return nil
}

// RefreshDue 由定时循环驱动:引用集合里「没快照」或「距上次尝试超过 24h」的
// 文件起一次刷新。读库失败直接放弃这一轮 —— 下次 tick 再来,不放大故障。
func (m *Manager) RefreshDue(now time.Time) {
	files := m.referencedFiles()
	if len(files) == 0 {
		return
	}
	snaps, err := m.store.ListRulesetSnapshots()
	if err != nil {
		return
	}
	for _, f := range files {
		snap := snaps[f.Key]
		if snap == nil || snap.LastAttemptAt <= 0 ||
			now.Sub(time.UnixMilli(snap.LastAttemptAt)) >= refreshInterval {
			_ = m.StartRefresh(f.Key)
		}
	}
}

// RefreshAll 对引用集合里全部文件各起一次刷新(异步,不等结果)。
// 返回实际发起的数量,重复的 key 由 StartRefresh 的单飞合并掉。
func (m *Manager) RefreshAll() int {
	n := 0
	for _, f := range m.referencedFiles() {
		if err := m.StartRefresh(f.Key); err == nil {
			n++
		}
	}
	return n
}

// Run 是刷新循环:启动先补一轮到期的,之后每 30s 扫一遍引用集合。
func (m *Manager) Run(ctx context.Context) {
	m.RefreshDue(time.Now())
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.ctx.Done():
			return
		case now := <-tick.C:
			m.RefreshDue(now)
		}
	}
}

// fetch 从上游地址拉一个文件,返回正文、Content-Type 与失败原因(中文短句,
// 直接进 last_error 给界面看;空串表示成功)。
func (m *Manager) fetch(url string) (body []byte, contentType string, failure string) {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", "规则集地址无效"
	}
	// UA 用 mihomo:上游(Loyalsoldier 等)按 UA 区别服务,报告它拿到了自己
	// 本来要给的那类客户端。
	req.Header.Set("User-Agent", "mihomo")
	resp, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", "拉取超时或已中断"
		}
		return nil, "", "无法连接上游规则集"
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, "", "读取上游规则集失败"
	}
	if len(data) > maxBody {
		return nil, "", "上游规则集超过 16 MiB"
	}
	if len(data) == 0 {
		return nil, "", "上游规则集为空"
	}
	return data, resp.Header.Get("Content-Type"), ""
}

// Close 停掉刷新循环并等在跑作业结束。幂等。
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
	if tr, ok := m.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}
