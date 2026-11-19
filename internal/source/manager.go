package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

const maxSourceBody = 10 << 20

// Manager is the only refresh executor. Requests and the scheduler join the same job.
type Manager struct {
	store   *store.Store
	client  *http.Client
	changed func()
	// geoProbe 是给「备注解析不出、也没默认地区」的新端点兜底查地区用的:
	// 控制面直连第三方(JMS 之类主机名备注靠它自救)。返回空串表示查不到。
	// 由 server 注入;为空时这条兜底整个不跑。
	geoProbe func(ctx context.Context, address string) (region, city string)
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	jobs     map[int64]chan struct{}
	closed   bool
	wg       sync.WaitGroup
}

// SetGeoProbe 注入刷新兜底用的降级探针;nil 表示关掉这条路径。
func (m *Manager) SetGeoProbe(fn func(ctx context.Context, address string) (string, string)) {
	m.geoProbe = fn
}

type RefreshResult struct {
	SourceID  int64  `json:"source_id"`
	LastError string `json:"last_error,omitempty"`
}

func NewManager(st *store.Store, changed func()) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &Manager{store: st, changed: changed, ctx: ctx, cancel: cancel, jobs: map[int64]chan struct{}{}, client: &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("订阅重定向次数过多")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("订阅重定向协议不受支持")
		}
		return nil
	}}}
}

func (m *Manager) Running(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.jobs[id]
	return ok
}

func (m *Manager) start(id int64) (<-chan struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("来源刷新服务已停止")
	}
	if done, ok := m.jobs[id]; ok {
		return done, nil
	}
	src, err := m.store.GetSource(id)
	if err != nil {
		return nil, err
	}
	if !src.Enabled {
		return nil, errors.New("来源已停用，请先恢复")
	}
	done := make(chan struct{})
	m.jobs[id] = done
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() { m.mu.Lock(); delete(m.jobs, id); close(done); m.mu.Unlock() }()
		for {
			result, snapshot, failure := m.fetch(src)
			var report []byte
			if result.Format != "" || len(result.Issues) > 0 {
				report, _ = json.Marshal(result)
			}
			if m.ctx.Err() != nil {
				return
			}
			err := m.store.CompleteSourceRefresh(src.ID, src.Revision, time.Now(), snapshot, report, failure)
			if !errors.Is(err, store.ErrVersionConflict) {
				if err == nil {
					m.syncExternalMetadata(src.ID, src.Region, snapshot)
					if m.changed != nil {
						m.changed()
					}
				}
				return
			}
			// A changed URL/configuration cannot commit the old response. Retry the current one.
			src, err = m.store.GetSource(id)
			if err != nil || !src.Enabled || m.ctx.Err() != nil {
				return
			}
		}
	}()
	return done, nil
}

func (m *Manager) StartRefresh(id int64) error { _, err := m.start(id); return err }

// syncExternalMetadata 在一次成功快照后对齐外部端点清洗层(UC-17):
// 消失的端点记录删掉,新端点能从原名解析出地区就自动建一条 auto 记录;
// 解析不出时回退到来源配置的默认地区——JMS 这类纯主机名命名的上游靠它兜底。
// 快照没换(失败或不落快照的成功)时不跑;同步失败只记日志,不回滚快照。
func (m *Manager) syncExternalMetadata(sourceID int64, defaultRegion string, snapshot []byte) {
	if len(snapshot) == 0 {
		return
	}
	var eps []render.Endpoint
	if err := json.Unmarshal(snapshot, &eps); err != nil {
		return
	}
	keys := make([]string, 0, len(eps))
	regions := map[string]string{}
	for _, ep := range eps {
		keys = append(keys, ep.SourceKey)
		regions[ep.SourceKey] = model.RegionFromRemark(ep.Remark)
		if regions[ep.SourceKey] == "" {
			regions[ep.SourceKey] = defaultRegion
		}
	}
	existingAll, err := m.store.ListExternalMetadata(sourceID)
	if err != nil {
		return
	}
	// ListExternalMetadata 的键带来源前缀("id\x00key");AssignExternalLetters
	// 按裸指纹查 existing,先重键成 SourceKey。不转换的话 existing 永远查不中:
	// graded 记录会被回填重排、旧字母又被占住,每刷一次字母往后挪一位。
	existing := map[string]model.ExternalMetadata{}
	for _, meta := range existingAll {
		existing[meta.SourceKey] = meta
	}
	assigned := model.AssignExternalLetters(sourceID, keys, regions, existing)
	// 只把「新增或变化」的记录写回去:已存在的 graded/auto 记录按原样
	// 也在 assigned 里,重复写一遍无害,但只对快照键集合内的写。
	upserts := make([]model.ExternalMetadata, 0, len(assigned))
	for _, key := range keys {
		if meta, ok := assigned[key]; ok {
			prev, had := existing[key]
			if had && prev.Kind == "graded" {
				continue // 探测/重排写过的记录回填不动它
			}
			if had && prev.Region == meta.Region && prev.City == meta.City && prev.Letter == meta.Letter {
				continue // 没变化不写,省一次事务
			}
			upserts = append(upserts, meta)
		}
	}
	_ = m.store.SyncExternalMetadata(sourceID, keys, upserts)

	// 解析不出地区也没默认地区的新端点,用降级探针按地址真查——JMS 换
	// 主机名后新端点靠这条路径自己长出干净名,不等人手工 grade。
	if m.geoProbe != nil {
		var orphan []render.Endpoint
		for _, ep := range eps {
			if _, ok := existing[ep.SourceKey]; ok {
				continue // 已有记录(含 graded)不碰
			}
			if regions[ep.SourceKey] == "" {
				orphan = append(orphan, ep)
			}
		}
		m.backfillOrphanGeo(sourceID, orphan)
	}
}

// backfillOrphanGeo 串行慢查没进清洗层的新端点:地址解析 + ipwho.is,
// 查到地区就按同组补一条 auto 记录。失败端点跳过,不影响刷新结果。
func (m *Manager) backfillOrphanGeo(sourceID int64, eps []render.Endpoint) {
	if len(eps) == 0 {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for _, ep := range eps {
			if m.ctx.Err() != nil {
				return
			}
			ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
			region, city := m.geoProbe(ctx, ep.Address)
			cancel()
			if region == "" {
				continue
			}
			existingAll, err := m.store.ListExternalMetadata(sourceID)
			if err != nil {
				return
			}
			existing := map[string]model.ExternalMetadata{}
			for _, meta := range existingAll {
				existing[meta.SourceKey] = meta
			}
			if _, ok := existing[ep.SourceKey]; ok {
				continue // 并发期间有人写过了
			}
			assigned := model.AssignExternalLetters(sourceID, []string{ep.SourceKey},
				map[string]string{ep.SourceKey: region}, existing)
			meta, ok := assigned[ep.SourceKey]
			if !ok {
				continue
			}
			meta.City = city
			_ = m.store.SaveExternalMetadataBatch([]model.ExternalMetadata{meta})
		}
		if m.changed != nil {
			m.changed()
		}
	}()
}

func (m *Manager) Refresh(ctx context.Context, id int64) (RefreshResult, error) {
	done, err := m.start(id)
	if err != nil {
		return RefreshResult{}, err
	}
	select {
	case <-done:
	case <-ctx.Done():
		return RefreshResult{SourceID: id}, ctx.Err()
	}
	src, err := m.store.GetSource(id)
	if err != nil {
		return RefreshResult{SourceID: id}, err
	}
	result := RefreshResult{SourceID: id, LastError: src.LastError}
	if src.LastError != "" {
		return result, errors.New(src.LastError)
	}
	return result, nil
}

func (m *Manager) fetch(src *model.SubscriptionSource) (Result, []byte, string) {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return Result{}, nil, "订阅地址无效"
	}
	req.Header.Set("User-Agent", "Clash.Meta")
	resp, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, nil, "拉取超时或已中断"
		}
		return Result{}, nil, "无法连接上游订阅，请检查地址、网络或证书"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, nil, fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceBody+1))
	if err != nil {
		return Result{}, nil, "读取上游订阅失败"
	}
	if len(body) > maxSourceBody {
		return Result{}, nil, "上游订阅超过 10 MiB，已保留上次结果"
	}
	result, err := Parse(body)
	if errors.Is(err, ErrUnrecognized) {
		return result, nil, "无法识别上游订阅格式，请检查地址或改用专用格式地址"
	}
	if err != nil || len(result.Endpoints) == 0 {
		return result, nil, "上游订阅没有可导入的有效节点，请查看兼容报告"
	}
	snapshot, err := json.Marshal(result.Endpoints)
	if err != nil {
		return result, nil, "无法保存节点快照"
	}
	return result, snapshot, ""
}

// Run schedules sources independently from managed-node traffic and reconciliation.
func (m *Manager) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	m.RefreshDue(time.Now())
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

func (m *Manager) RefreshDue(now time.Time) {
	sources, err := m.store.ListSources()
	if err != nil {
		return
	}
	for _, s := range sources {
		if !s.Enabled || s.IntervalSeconds == 0 {
			continue
		}
		if s.LastAttemptAt == nil || now.Sub(*s.LastAttemptAt) >= time.Duration(s.IntervalSeconds)*time.Second {
			_ = m.StartRefresh(s.ID)
		}
	}
}

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
