package acme

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// renewBefore 是到期前多久开始续签。LE 证书 90 天,留 30 天余量 —
// 按指数退避连试几个月都来得及,同时离「过期」这条线足够远。
const renewBefore = 30 * 24 * time.Hour

// Manager 管 ACME 的运行期状态:库里那张 acme_state、内存里的证书、
// 单飞签发任务、到期续期循环。serve 把它的 GetCertificate 交给 TLS listener。
type Manager struct {
	store  *store.Store
	logger *slog.Logger

	// selfSigned 是兜底证书(tls_identity):SNI 没命中域名、或还没签下
	// ACME 证书时的默认身份,永远非 nil。
	selfSigned *tls.Certificate
	// acmeCert 是 ACME 证书;atomic.Pointer 让 GetCertificate 的热替换是
	// 无锁的 —— 握手回调在每个连接上都跑,不能为换证书去抢互斥锁。
	acmeCert atomic.Pointer[tls.Certificate]
	// domain 是 acmeCert 应答的 SNI 名;跟证书一体换。
	domain atomic.Value // string

	// issuer 由 NewIssuer 提供;测试换成假实现。
	newIssuer func(domain string, staging bool, accountKeyPEM []byte) *Issuer

	// issueMu 包住签发任务状态;running 保证单飞 —— 同一时刻只有一轮签发。
	issueMu sync.Mutex
	running bool
	job     int64
	state   string
	seq     int64
	events  []model.ACMEIssueEvent
}

// NewManager 建一个 Manager。selfSigned 是兜底身份(tls_identity);domain 是
// 当前生效的 ACME SNI 域名(settings.acme_domain);acme 是库里已签的证书,
// 可能为空。
//
// domain 与证书里的 Domain 对不上时,证书不装:那张证书属于旧域名,发出去
// 只会让新域名的访客看到证书名不符 —— 宁可回自签,让「要重签」这件事看得见。
func NewManager(st *store.Store, selfSigned tls.Certificate, domain string, acme *store.AcmeState, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{store: st, selfSigned: &selfSigned, logger: logger, state: model.ACMEIssueIdle}
	m.newIssuer = func(dom string, staging bool, accountKeyPEM []byte) *Issuer {
		dir := xacmeDirectory(staging)
		return &Issuer{
			Directory:     dir,
			AccountKeyPEM: accountKeyPEM,
			DNS:           m.dnsProvider(),
			Logger:        logger,
		}
	}
	m.installCert(domain, acme)
	return m
}

// xacmeDirectory 按 staging 标志选 CA 目录。只有 Let's Encrypt 一家;
// 留成函数是给测试换目录(mock CA)留的口子。
func xacmeDirectory(staging bool) string {
	if staging {
		return "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
	return "https://acme-v02.api.letsencrypt.org/directory"
}

// dnsProvider 从 settings 取 CF token 造 Cloudflare 客户端。每次新建而不是
// 缓存:token 可以热改,缓存就要有人负责失效。
func (m *Manager) dnsProvider() DNSProvider {
	st, err := m.store.GetSettings()
	if err != nil || st == nil {
		return &Cloudflare{Token: ""}
	}
	return &Cloudflare{Token: st.ACMECFToken}
}

// SetIssuerFactory 换掉真实签发路径(测试用)。必须在任何签发发起前调用。
func (m *Manager) SetIssuerFactory(f func(domain string, staging bool, accountKeyPEM []byte) *Issuer) {
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	m.newIssuer = f
}

// installCert 按「现在的域名 + 库里的证书」重算 SNI 选择。
//
// 证书必须同时满足:非空、Domain 与生效域名一致 —— 两者任一不满足就不装
// (atomic 存 nil,GetCertificate 回自签)。
func (m *Manager) installCert(domain string, acme *store.AcmeState) {
	if acme != nil && len(acme.CertPEM) > 0 && len(acme.KeyPEM) > 0 && acme.Domain == domain && domain != "" {
		cert, err := tls.X509KeyPair(acme.CertPEM, acme.KeyPEM)
		if err == nil {
			leaf, err := x509.ParseCertificate(cert.Certificate[0])
			if err == nil {
				cert.Leaf = leaf
				m.acmeCert.Store(&cert)
				m.domain.Store(domain)
				return
			}
		}
		if m.logger != nil {
			m.logger.Warn("库里的 ACME 证书解析失败,按没证书处理", "domain", domain)
		}
	}
	m.acmeCert.Store(nil)
	m.domain.Store(domain)
}

// GetCertificate 是 tls.Config 的回调:SNI 命中 ACME 域名且有证书就出 ACME
// 证书,其余一切(无 SNI、IP、别的域名)回自签。它是握手热路径,只读 atomic。
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if dom, _ := m.domain.Load().(string); dom != "" && hello.ServerName == dom {
		if c := m.acmeCert.Load(); c != nil {
			return c, nil
		}
	}
	return m.selfSigned, nil
}

// ReloadDomain 域名或 CA 标志变了后重算证书选择:读库、按新域名装或卸。
// 配置接口(PUT/DELETE /api/acme/config)在写库后调用它。
func (m *Manager) ReloadDomain(domain string) {
	acme, err := m.store.GetACMEState()
	if err != nil {
		m.logger.Error("读 ACME 状态失败", "error", err)
	}
	m.installCert(domain, acme)
}

// ---- 签发任务 ----

// StartIssue 起一轮签发;已有一轮在跑返回 false。
//
// 任务在后台 goroutine 跑,返回 job id 给进度接口用。ctx 是调用方的 —
// 传 context.Background() 才对,HTTP handler 的 ctx 在响应返回后就被
// 取消了,会杀掉后台任务。
func (m *Manager) StartIssue(ctx context.Context, domain string, staging bool) (int64, bool) {
	m.issueMu.Lock()
	if m.running {
		m.issueMu.Unlock()
		return 0, false
	}
	m.running = true
	m.job++
	m.state = model.ACMEIssueRunning
	m.seq = 0
	m.events = nil
	job := m.job
	m.issueMu.Unlock()
	go m.runIssue(ctx, job, domain, staging)
	return job, true
}

func (m *Manager) addEvent(msg string) {
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	m.seq++
	m.events = append(m.events, model.ACMEIssueEvent{Seq: m.seq, Msg: msg})
}

func (m *Manager) finishIssue(state string) {
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	m.state = state
	m.running = false
}

// IssueProgress 回答签发任务此刻的进度;same since 语义与节点升级一致。
func (m *Manager) IssueProgress(since int64) model.ACMEIssueProgress {
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	out := model.ACMEIssueProgress{
		Job:    m.job,
		State:  m.state,
		Seq:    m.seq,
		Events: []model.ACMEIssueEvent{},
	}
	for _, ev := range m.events {
		if ev.Seq > since {
			out.Events = append(out.Events, ev)
		}
	}
	return out
}

// IssueRunning 报告此刻有没有签发在跑(status 出参用)。
func (m *Manager) IssueRunning() bool {
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	return m.running
}

// runIssue 是一轮签发的任务体。
//
// 写库节奏:每一步的产物先落库再进下一步 —— 进程死在中间不丢已拿到的
// 东西(account key 复用是续期的关键)。失败路径只写错误,不动证书;
// 成功路径先把证书写进库、再热替换进 listener —— 库里有的才算数。
func (m *Manager) runIssue(ctx context.Context, job int64, domain string, staging bool) {
	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		m.addEvent("失败: " + msg)
		m.recordFailure(msg)
		m.finishIssue(model.ACMEIssueFailed)
	}

	st, err := m.store.GetACMEState()
	if err != nil {
		fail("读 ACME 状态: %v", err)
		return
	}
	issuer := m.newIssuer(domain, staging, st.AccountKeyPEM)
	issuer.Step = m.addEvent

	res, err := issuer.Issue(ctx, domain)
	if err != nil {
		fail("%v", err)
		return
	}

	// 成功:先把整份状态写进库(account key + 新证书),再让 listener 换证书。
	// 顺序反过来会先换证书后写库 —— 写库失败时进程持有的证书与库不一致,
	// 重启后又回到旧证书。
	now := time.Now()
	st.AccountKeyPEM = res.AccountKeyPEM
	st.CertPEM = res.CertPEM
	st.KeyPEM = res.KeyPEM
	st.Domain = domain
	st.Staging = staging
	st.IssuedAt = now
	st.NotAfter = res.NotAfter
	st.LastError = ""
	st.LastAttemptAt = now
	st.Failures = 0
	st.NextRetryAt = time.Time{}
	if err := m.store.SaveACMEState(st); err != nil {
		fail("签发成功但写库失败: %v", err)
		return
	}
	m.installCert(domain, st)
	m.addEvent(fmt.Sprintf("签发完成,证书 %s 到期", res.NotAfter.Format("2006-01-02")))
	m.finishIssue(model.ACMEIssueDone)
}

// recordFailure 把一次签发失败写进库:last_error/last_attempt_at 必写,
// failures 递增并按它算出下一次允许重试的时刻(指数退避)。
//
// 退避阶梯:1m → 2m → 4m → …封顶 12h。起步短是为了让「改完配置立刻重试」
// 的感受好;封顶 12h 是因为证书有几十天寿命,频繁重试没有意义。
func (m *Manager) recordFailure(msg string) {
	st, err := m.store.GetACMEState()
	if err != nil {
		m.logger.Error("读 ACME 状态失败", "error", err)
		return
	}
	st.LastError = msg
	st.LastAttemptAt = time.Now()
	st.Failures++
	backoff := time.Minute << min(st.Failures-1, 10) // 1m..~17h,再夹到 12h
	if backoff > 12*time.Hour {
		backoff = 12 * time.Hour
	}
	st.NextRetryAt = st.LastAttemptAt.Add(backoff)
	if err := m.store.SaveACMEState(st); err != nil {
		m.logger.Error("写 ACME 失败记录失败", "error", err)
	}
}

// ---- 续期循环 ----

// Run 是续期后台循环,serve 在 ctx 里跑它。
//
// 每小时看一眼:证书到期前 30 天、或上次失败后退避窗口到了,就开一轮
// 签发。没配域名 / 没证书且域名空着时什么都不做 —— 域名是 settings,
// 状态查询全走库,不写内存副本,所以配置随时改随时生效。
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	m.tick(ctx) // 启动先跑一轮:进程重启可能正好落在续期窗口里
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tick(ctx)
		}
	}
}

// tick 是一轮续期检查:决定要不要签,然后借 StartIssue 的单飞去签。
func (m *Manager) tick(ctx context.Context) {
	st, err := m.store.GetSettings()
	if err != nil {
		m.logger.Error("读设置失败,跳过 ACME 续期检查", "error", err)
		return
	}
	domain := st.ACMEDomain
	if domain == "" {
		return // 没配域名:整个功能关闭,自签兜底
	}
	as, err := m.store.GetACMEState()
	if err != nil {
		m.logger.Error("读 ACME 状态失败,跳过续期检查", "error", err)
		return
	}
	now := time.Now()
	switch {
	case len(as.CertPEM) == 0:
		// 没签过 / 被作废(域名或 CA 刚换):立刻补签,但上次失败还在退避
		// 里时等窗口到 —— 否则配置错了的域名每小时猛打 CA。
		if !as.NextRetryAt.IsZero() && now.Before(as.NextRetryAt) {
			return
		}
	case as.Domain != domain:
		// 证书属于旧域名:已被 installCert 卸下来了,这里补签一张新的。
		if !as.NextRetryAt.IsZero() && now.Before(as.NextRetryAt) {
			return
		}
	case now.After(as.NotAfter.Add(-renewBefore)):
		// 到期前 30 天:正常续期,不查退避 —— 到窗口了就该试,退避只用在
		// 「签失败之后别立刻再来」的场景(没证书/域名变了那条)。
		if !as.NextRetryAt.IsZero() && now.Before(as.NextRetryAt) {
			return
		}
	default:
		return // 还早
	}
	m.logger.Info("ACME 续期/补签开始", "domain", domain, "staging", st.ACMEStaging)
	m.StartIssue(ctx, domain, st.ACMEStaging)
}

// Status 组装 /api/acme/status 的出参:配置来自 settings,证书与错误来自
// acme_state,任务状态来自内存。
func (m *Manager) Status() (*model.ACMEStatus, error) {
	st, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	as, err := m.store.GetACMEState()
	if err != nil {
		return nil, err
	}
	out := &model.ACMEStatus{
		ACMEConfig: model.ACMEConfig{
			Domain:     st.ACMEDomain,
			CFTokenSet: st.ACMECFToken != "",
			Staging:    st.ACMEStaging,
		},
		LastError: as.LastError,
	}
	out.CertIssued = len(as.CertPEM) > 0
	if out.CertIssued {
		t := as.NotAfter
		out.CertNotAfter = &t
	}
	if !as.IssuedAt.IsZero() {
		t := as.IssuedAt
		out.CertIssuedAt = &t
	}
	if !as.LastAttemptAt.IsZero() {
		t := as.LastAttemptAt
		out.LastAttemptAt = &t
	}
	if !as.NextRetryAt.IsZero() {
		t := as.NextRetryAt
		out.NextRetryAt = &t
	}
	out.IssueRunning = m.IssueRunning()
	m.issueMu.Lock()
	out.IssueState = m.state
	m.issueMu.Unlock()
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
