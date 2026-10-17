// 本文件实现桌面端「后台预下载新版本」:发现 CLI 有新版本后,在后台把发布件
// 取到暂存目录并校验;用户点「重启以更新」时本机一步直接用暂存件。
//
// 它只影响弹窗状态,失败不打扰正常使用;控制面/节点仍然在点击后执行。
package ui

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/RovenEverward/my-proxy/internal/selfupdate"
)

// 预下载的几种状态,与设计稿一致。
const (
	prefetchIdle        = "idle"
	prefetchDownloading = "downloading"
	prefetchReady       = "ready"
	prefetchFailed      = "failed"
	prefetchDisabled    = "disabled"
)

// prefetchStatus 是 GET /local/update 里可选的 prefetch 字段。
type prefetchStatus struct {
	Version string `json:"version"`
	State   string `json:"state"`
	Bytes   int64  `json:"bytes"`
	Total   int64  `json:"total"`
	Error   string `json:"error,omitempty"`
}

// prefetchBaseDelay 是第一次失败后的等待;之后逐次翻倍,封顶 30 分钟。
const (
	prefetchBaseDelay = 30 * time.Second
	prefetchMaxDelay  = 30 * time.Minute
)

// prefetchBackoff 返回第 attempt 次失败(从 0 数)之后该等多久:
// 30s → 1m → 2m → 4m → 8m → 16m → 30m,之后一直是 30m。
func prefetchBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := prefetchBaseDelay
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= prefetchMaxDelay {
			return prefetchMaxDelay
		}
	}
	return d
}

// prefetchDevVersion 报告版本号是不是源码/开发构建:这种版本不预下载,
// 免得把发布件硬塞给一个来路不明的二进制。
func prefetchDevVersion(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.Contains(v, "dirty") {
		return true
	}
	return strings.HasPrefix(v, "v0.0.0-") || strings.HasPrefix(v, "0.0.0-")
}

// newPrefetchStager 造一个暂存器。做成变量,测试可以把缓存目录指到临时目录。
var newPrefetchStager = func(src selfupdate.Source) *selfupdate.Stager {
	return &selfupdate.Stager{Source: src}
}

// prefetcher 是一次会话里的后台预下载状态。同一时刻只有一个目标版本在下载,
// 目标变化时旧的一轮被取消,新的一轮从零(或已有的 .part)开始。
type prefetcher struct {
	mu     sync.Mutex
	status prefetchStatus
	gen    int
	cancel context.CancelFunc
	stager *selfupdate.Stager
	// sleep 等退避时间;返回 false 表示这一轮已被取消。测试里换成快进。
	sleep func(context.Context, time.Duration) bool
}

// newPrefetcher 按当前的发布来源造一个预下载器。
func newPrefetcher(src selfupdate.Source) *prefetcher {
	return &prefetcher{
		status: prefetchStatus{State: prefetchIdle},
		stager: newPrefetchStager(src),
		sleep:  sleepPrefetch,
	}
}

// sleepPrefetch 是默认的退避等待:ctx 取消时立刻返回 false。
func sleepPrefetch(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (p *prefetcher) snapshot() prefetchStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// start 把目标版本换成 version 并起一轮下载。已经在下载同一版或已经就绪时是
// 空操作;目标变化时取消上一轮。
func (p *prefetcher) start(version string) {
	version = strings.TrimSpace(version)
	if version == "" {
		return
	}
	p.mu.Lock()
	if p.status.Version == version && (p.status.State == prefetchDownloading || p.status.State == prefetchReady) {
		p.mu.Unlock()
		return
	}
	p.gen++
	gen := p.gen
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.status = prefetchStatus{Version: version, State: prefetchDownloading}
	p.mu.Unlock()

	go p.run(ctx, gen, version)
}

// retry 立刻重试当前目标(不等退避)。只在 failed 时有意义。
func (p *prefetcher) retry() {
	p.mu.Lock()
	version := p.status.Version
	if version == "" || p.status.State != prefetchFailed {
		p.mu.Unlock()
		return
	}
	p.gen++
	gen := p.gen
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.status.State = prefetchDownloading
	p.status.Error = ""
	p.status.Bytes = 0
	p.status.Total = 0
	p.mu.Unlock()

	go p.run(ctx, gen, version)
}

// fail 把暂存件标成不可用(比如 Open 时发现被篡改),之后按 failed 处理。
func (p *prefetcher) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.State = prefetchFailed
	p.status.Error = err.Error()
}

// current 报告 gen 这一轮是否仍是当前目标。
func (p *prefetcher) current(gen int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gen == gen
}

func (p *prefetcher) progress(gen int, written, total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen != gen || p.status.State != prefetchDownloading {
		return
	}
	p.status.Bytes = written
	p.status.Total = total
}

// run 是后台下载循环:成功一次就结束;失败按指数退避重试,目标被换掉或页面
// 退出时取消。
func (p *prefetcher) run(ctx context.Context, gen int, version string) {
	for attempt := 0; ; attempt++ {
		if !p.current(gen) {
			return
		}
		// 每一轮用一份自己的 Stager 副本,进度回调绑定到这一轮的 gen。
		stager := *p.stager
		stager.Progress = func(written, total int64) { p.progress(gen, written, total) }

		st, err := stager.Ensure(ctx, version)
		if err == nil {
			if !p.current(gen) {
				return
			}
			_ = stager.Prune(version)
			p.mu.Lock()
			if p.gen == gen {
				p.status.State = prefetchReady
				p.status.Bytes = st.Size
				p.status.Total = st.Size
				p.status.Error = ""
			}
			p.mu.Unlock()
			return
		}
		if ctx.Err() != nil || !p.current(gen) {
			return
		}

		p.mu.Lock()
		if p.gen != gen {
			p.mu.Unlock()
			return
		}
		p.status.State = prefetchFailed
		p.status.Error = err.Error()
		// 暂停期间已经下到多少就报多少,面板不会因为重试把进度归零。
		p.mu.Unlock()

		if !p.sleep(ctx, prefetchBackoff(attempt)) {
			return
		}
		if ctx.Err() != nil || !p.current(gen) {
			return
		}
		p.mu.Lock()
		if p.gen == gen {
			p.status.State = prefetchDownloading
			p.status.Error = ""
		}
		p.mu.Unlock()
	}
}

// prefetchEnabled 报告这台进程该不该在后台预下载:桌面构建、非开发版、
// 且没有用 MPX_NO_PREFETCH 关掉。
func prefetchEnabled() bool {
	if !updateDesktop() {
		return false
	}
	if strings.TrimSpace(os.Getenv("MPX_NO_PREFETCH")) != "" {
		return false
	}
	return !prefetchDevVersion(updateLocalVersion())
}

// getPrefetcher 取当前会话的预下载器;没有时按当前发布来源造一个。
func (s *Server) getPrefetcher() *prefetcher {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prefetch == nil {
		s.prefetch = newPrefetcher(updateSource())
	}
	return s.prefetch
}

// startPrefetch 在发现新版本时调用:桌面构建下把目标交给后台,非桌面构建什么
// 也不做。重复调用同一版是空操作。
func (s *Server) startPrefetch(version string) {
	if !prefetchEnabled() {
		return
	}
	s.getPrefetcher().start(version)
}

// prefetchView 组装 GET /local/update 的 prefetch 字段。非桌面构建返回 nil,
// 老页面照旧;桌面构建即使关了预下载也返回 disabled,让弹窗知道有这回事。
func (s *Server) prefetchView() *prefetchStatus {
	if !updateDesktop() {
		return nil
	}
	if !prefetchEnabled() {
		return &prefetchStatus{State: prefetchDisabled}
	}
	st := s.getPrefetcher().snapshot()
	return &st
}

// stagedFor 返回目标版本的暂存可执行文件;没有合格暂存时 ok=false,调用方
// 回退到现下现装。
func (s *Server) stagedFor(version string) (bin []byte, ok bool) {
	if !prefetchEnabled() {
		return nil, false
	}
	pf := s.getPrefetcher()
	st := pf.snapshot()
	if st.State != prefetchReady || !sameUpdateVersion(st.Version, version) {
		return nil, false
	}
	pf.mu.Lock()
	stager := pf.stager
	pf.mu.Unlock()
	if stager == nil {
		return nil, false
	}
	bin, err := stager.Open(st.Version)
	if err != nil {
		// 暂存件坏了(被篡改、被删):标成失败,回退到现下现装;下一轮会比较
		// 哈希后重下。
		pf.fail(err)
		return nil, false
	}
	return bin, true
}

// sameUpdateVersion 比较两个版本号,忽略打头的 v 与空白。
func sameUpdateVersion(a, b string) bool {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	return a != "" && a == b
}
