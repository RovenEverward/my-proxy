// Package acme 是 UC-26 的 ACME 自动证书层:DNS-01(Cloudflare)签发、
// 到期续签、双证书 SNI 选择。
//
// 包内三块:
//   - Cloudflare:把 _acme-challenge TXT 摆上 DNS 的实现(DNSProvider 接口)。
//   - Issuer:一条完整的「下单 → 挑战 → 取证书」状态机,依赖只有 ACMEClient
//     与 DNSProvider 两个接口,测试全走内存替身。
//   - Manager:管 acme_state 落库、SNI 证书热替换、续期后台循环与单飞签发任务。
package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	xacme "golang.org/x/crypto/acme"
)

// ACMEClient 是 Issuer 对 x/crypto/acme 客户端的全部依赖,抽成接口让测试能
// 把整个 CA 换成内存替身;真实实现就是 *xacme.Client。
type ACMEClient interface {
	Register(ctx context.Context, acct *xacme.Account, prompt func(tosURL string) bool) (*xacme.Account, error)
	AuthorizeOrder(ctx context.Context, id []xacme.AuthzID, opt ...xacme.OrderOption) (*xacme.Order, error)
	GetAuthorization(ctx context.Context, url string) (*xacme.Authorization, error)
	Accept(ctx context.Context, chal *xacme.Challenge) (*xacme.Challenge, error)
	WaitAuthorization(ctx context.Context, url string) (*xacme.Authorization, error)
	WaitOrder(ctx context.Context, url string) (*xacme.Order, error)
	CreateCertFromOrder(ctx context.Context, order *xacme.Order, csr []byte, bundle bool) (der [][]byte, certURL string, err error)
	FetchCert(ctx context.Context, url string, bundle bool) ([][]byte, error)
	DNS01ChallengeRecord(token string) (string, error)
}

// realACMENew 造一个真的 ACME 客户端;account key 是 PEM 编码的 ECDSA 私钥,
// 空串时现场生成一个(返回生成出的 PEM,调用方负责落库)。
func newACMEClient(directory string, accountKeyPEM []byte) (ACMEClient, []byte, error) {
	keyPEM := accountKeyPEM
	var key *ecdsa.PrivateKey
	if len(keyPEM) == 0 {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, fmt.Errorf("生成 ACME 账户私钥: %w", err)
		}
		key = k
		der, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			return nil, nil, err
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	} else {
		block, _ := pem.Decode(keyPEM)
		if block == nil {
			return nil, nil, errors.New("ACME 账户私钥不是合法 PEM")
		}
		k, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("解析 ACME 账户私钥: %w", err)
		}
		key = k
	}
	return &xacme.Client{
		Key:          key,
		DirectoryURL: directory,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		UserAgent:    "my-proxy/" + "acme",
	}, keyPEM, nil
}

// Issuer 是一条签发流水线。字段都可注入:测试里 Directory 指向 mock ACME
// server、DNS 是内存假实现、LookupTXT 也换成假实现。
type Issuer struct {
	Directory     string      // ACME 目录 URL(LE 正式或 staging)
	AccountKeyPEM []byte      // 复用的账户私钥;空表示新建并回传
	DNS           DNSProvider // 摆 TXT 的实现
	// LookupTXT 查公网里 _acme-challenge 有没有就位;nil 用真 DNS。
	// 注入点:测试不等真解析。
	LookupTXT func(ctx context.Context, fqdn string) ([]string, error)
	// Step 是进度回调:每到一个有名字的阶段报一次,给签发任务的事件流。
	Step      func(msg string)
	NewClient func(directory string, accountKeyPEM []byte) (ACMEClient, []byte, error)
	// IssueBudget 是一轮签发的总时限;CA 端轮询都吃同一个 ctx。
	IssueBudget time.Duration
	// DNSWaitBudget 是等 TXT 在公网可见的时限。CF 写入到全球可见一般秒级,
	// 给它一分钟宽限;没等到就照走 Accept —— CA 自己会重试验证。
	DNSWaitBudget time.Duration
	Logger        *slog.Logger
}

const (
	defaultIssueBudget   = 5 * time.Minute
	defaultDNSWaitBudget = 60 * time.Second
)

func (i *Issuer) step(msg string) {
	if i.Step != nil {
		i.Step(msg)
	}
	if i.Logger != nil {
		i.Logger.Info("acme 签发", "step", msg)
	}
}

// newClient 是 NewClient 的默认实现。
func (i *Issuer) acmeClientNew(directory string, accountKeyPEM []byte) (ACMEClient, []byte, error) {
	if i.NewClient != nil {
		return i.NewClient(directory, accountKeyPEM)
	}
	return newACMEClient(directory, accountKeyPEM)
}

func (i *Issuer) budget() time.Duration {
	if i.IssueBudget > 0 {
		return i.IssueBudget
	}
	return defaultIssueBudget
}

func (i *Issuer) dnsBudget() time.Duration {
	if i.DNSWaitBudget > 0 {
		return i.DNSWaitBudget
	}
	return defaultDNSWaitBudget
}

// IssueResult 是一轮签发的产物。
type IssueResult struct {
	CertPEM       []byte    // 证书链(叶在前)
	KeyPEM        []byte    // 域名私钥(每轮签发新生成)
	AccountKeyPEM []byte    // 实际使用的账户私钥(新建时非空 → 落库)
	NotAfter      time.Time // 证书到期时刻(取叶子证书)
}

// Issue 走一遍完整签发:建/复用账户 → 下单 → 逐 authorization 过 dns-01
// 挑战 → finalize → 取证书。
//
// 任何一步失败都直接返回 error,不在内部吞结果 —— 调用方(Manager)负责
// 把错误落到 acme_state,并保证失败不动已有效的证书。
func (i *Issuer) Issue(ctx context.Context, domain string) (*IssueResult, error) {
	ctx, cancel := context.WithTimeout(ctx, i.budget())
	defer cancel()

	client, accountKeyPEM, err := i.acmeClientNew(i.Directory, i.AccountKeyPEM)
	if err != nil {
		return nil, err
	}
	i.step("注册/复用 ACME 账户")
	if _, err := client.Register(ctx, &xacme.Account{}, func(tosURL string) bool { return true }); err != nil &&
		!errors.Is(err, xacme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("注册 ACME 账户: %w", err)
	}
	i.step(fmt.Sprintf("下单: %s", domain))
	order, err := client.AuthorizeOrder(ctx, []xacme.AuthzID{{Type: "dns", Value: domain}})
	if err != nil {
		return nil, fmt.Errorf("创建订单: %w", err)
	}

	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return nil, fmt.Errorf("取授权 %s: %w", authzURL, err)
		}
		if authz.Status == xacme.StatusValid {
			continue // 复用历史授权,不重复过挑战
		}
		if authz.Status != xacme.StatusPending {
			return nil, fmt.Errorf("授权 %s 状态不是 pending: %s", authzURL, authz.Status)
		}
		var chal *xacme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "dns-01" {
				chal = c
				break
			}
		}
		if chal == nil {
			return nil, fmt.Errorf("授权 %s 不提供 dns-01 挑战(只有 %s)", authzURL, challengeTypes(authz.Challenges))
		}
		txtValue, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return nil, fmt.Errorf("计算 dns-01 应答值: %w", err)
		}
		i.step(fmt.Sprintf("摆 DNS 挑战记录: _acme-challenge.%s", domain))
		cleanup, err := i.DNS.SetTXT(ctx, domain, txtValue)
		if err != nil {
			return nil, fmt.Errorf("摆 DNS 挑战记录: %w", err)
		}
		// TXT 只活到挑战验完。延迟删除放在这里而不是整轮结束:授权可能不止
		// 一个,留着一堆旧记录没意义;而验失败时清理也照跑。
		if err := i.waitTXT(ctx, "_acme-challenge."+domain, txtValue); err != nil {
			// 等不到公网可见不直接判死:CA 自己会验证,我们只是少等一拍。
			if i.Logger != nil {
				i.Logger.Warn("等 _acme-challenge 在公网可见超时,继续让 CA 验证", "error", err)
			}
		}
		i.step("让 CA 来验 DNS-01")
		if _, err := client.Accept(ctx, chal); err != nil {
			_ = cleanup(context.Background())
			return nil, fmt.Errorf("接受挑战: %w", err)
		}
		waitErr := func() error {
			_, err := client.WaitAuthorization(ctx, authz.URI)
			return err
		}()
		if cerr := cleanup(context.Background()); cerr != nil && i.Logger != nil {
			i.Logger.Warn("清理 _acme-challenge 记录失败", "error", cerr)
		}
		if waitErr != nil {
			return nil, fmt.Errorf("dns-01 验证未通过: %w", waitErr)
		}
	}

	i.step("提交 CSR 等签发")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成域名私钥: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domain},
		DNSNames: []string{domain},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("生成 CSR: %w", err)
	}
	der, certURL, err := client.CreateCertFromOrder(ctx, order, csrDER, false)
	if err != nil {
		return nil, fmt.Errorf("finalize 订单: %w", err)
	}
	i.step("取回证书链")
	// bundle=false 只给叶;控制面要完整链才好用(GetCertificate 会原样送出),
	// 用 certURL 再取一次带中间证书的版本。
	chain, err := client.FetchCert(ctx, certURL, true)
	if err != nil {
		// 拿不到链就用 finalize 那份叶子顶着:服务能跑,浏览器可能认不全 ——
		// 但那是次优降级,不是失败。
		chain = der
	}
	if len(chain) == 0 {
		return nil, errors.New("CA 返回的证书链是空的")
	}
	var pemBuf strings.Builder
	for _, d := range chain {
		if err := pem.Encode(&pemBuf, &pem.Block{Type: "CERTIFICATE", Bytes: d}); err != nil {
			return nil, fmt.Errorf("编码证书链: %w", err)
		}
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return nil, fmt.Errorf("解析 CA 给的叶子证书: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &IssueResult{
		CertPEM:       []byte(pemBuf.String()),
		KeyPEM:        pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		AccountKeyPEM: accountKeyPEM,
		NotAfter:      leaf.NotAfter,
	}, nil
}

func challengeTypes(chals []*xacme.Challenge) string {
	out := make([]string, 0, len(chals))
	for _, c := range chals {
		out = append(out, c.Type)
	}
	return strings.Join(out, ",")
}

// waitTXT 轮询公网 DNS 直到 _acme-challenge.<domain> 出现目标值。
// 它不是一个硬门 —— 超时也继续走 Accept(见 Issue);存在只是为了把
// 「记录刚摆上、CA 立刻读不到」这个最常见的假失败挡掉。
func (i *Issuer) waitTXT(ctx context.Context, fqdn, want string) error {
	lookup := i.LookupTXT
	if lookup == nil {
		lookup = lookupTXTDefault
	}
	deadline := time.Now().Add(i.dnsBudget())
	for {
		got, _ := lookup(ctx, fqdn)
		for _, v := range got {
			if v == want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s 在 %s 内未在公网可见", fqdn, i.dnsBudget())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// lookupTXTDefault 默认向 Cloudflare 的公共解析(1.1.1.1)问 TXT:
// 它和我们写入的权威是同一家,记录一提交就可见,绕过了递归缓存那一层等待。
func lookupTXTDefault(ctx context.Context, fqdn string) ([]string, error) {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	return r.LookupTXT(ctx, fqdn)
}
