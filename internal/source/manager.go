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
	"github.com/RovenEverward/my-proxy/internal/store"
)

const maxSourceBody = 10 << 20

// Manager is the only refresh executor. Requests and the scheduler join the same job.
type Manager struct {
	store   *store.Store
	client  *http.Client
	changed func()
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	jobs    map[int64]chan struct{}
	closed  bool
	wg      sync.WaitGroup
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
				if err == nil && m.changed != nil {
					m.changed()
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
