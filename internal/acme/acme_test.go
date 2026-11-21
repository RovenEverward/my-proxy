package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	xacme "golang.org/x/crypto/acme"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// ---- 假件 ----

// fakeDNS 是内存 DNSProvider:记下 SetTXT 的内容,支持注入失败。
type fakeDNS struct {
	mu      sync.Mutex
	records []string
	err     error
}

func (f *fakeDNS) SetTXT(_ context.Context, domain, value string) (func(context.Context) error, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, domain+"="+value)
	return func(context.Context) error { return nil }, nil
}

// fakeClient 是内存 ACMEClient:走完整状态机(下单 → 授权 → 挑战 → finalize → 证书);
// failAt 非空时对应那一步直接报错。
//
// finalize 时真按 CSR 里的公钥签证书 —— 真 CA 就是这么干的;拿一张别的公钥
// 的证书回来,X509KeyPair 配不上,等于把「签错对象」当正确测过去了。
type fakeClient struct {
	failAt  string // "register"/"order"/"authz"/"accept"/"wait"/"cert"/"fetch"
	caKey   *ecdsa.PrivateKey
	lastDER []byte
}

func (f *fakeClient) Register(context.Context, *xacme.Account, func(string) bool) (*xacme.Account, error) {
	if f.failAt == "register" {
		return nil, errors.New("register 失败")
	}
	return &xacme.Account{URI: "acct/1", Status: "valid"}, nil
}
func (f *fakeClient) AuthorizeOrder(context.Context, []xacme.AuthzID, ...xacme.OrderOption) (*xacme.Order, error) {
	if f.failAt == "order" {
		return nil, errors.New("order 失败")
	}
	return &xacme.Order{URI: "order/1", Status: xacme.StatusPending, AuthzURLs: []string{"authz/1"}, FinalizeURL: "final/1"}, nil
}
func (f *fakeClient) GetAuthorization(context.Context, string) (*xacme.Authorization, error) {
	if f.failAt == "authz" {
		return nil, errors.New("authz 失败")
	}
	return &xacme.Authorization{URI: "authz/1", Status: xacme.StatusPending,
		Challenges: []*xacme.Challenge{{Type: "dns-01", URI: "chal/1", Token: "tok1"}}}, nil
}
func (f *fakeClient) Accept(context.Context, *xacme.Challenge) (*xacme.Challenge, error) {
	if f.failAt == "accept" {
		return nil, errors.New("accept 失败")
	}
	return &xacme.Challenge{Type: "dns-01", URI: "chal/1", Token: "tok1"}, nil
}
func (f *fakeClient) WaitAuthorization(context.Context, string) (*xacme.Authorization, error) {
	if f.failAt == "wait" {
		return nil, errors.New("wait 失败")
	}
	return &xacme.Authorization{URI: "authz/1", Status: xacme.StatusValid}, nil
}
func (f *fakeClient) WaitOrder(context.Context, string) (*xacme.Order, error) {
	return &xacme.Order{URI: "order/1", Status: xacme.StatusValid}, nil
}
func (f *fakeClient) CreateCertFromOrder(_ context.Context, _ *xacme.Order, csrDER []byte, _ bool) ([][]byte, string, error) {
	if f.failAt == "cert" {
		return nil, "", errors.New("cert 失败")
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, "", err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: csr.Subject.CommonName},
		DNSNames:     csr.DNSNames,
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if f.caKey == nil {
		f.caKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	caDER, err := x509.CreateCertificate(rand.Reader, tpl, tpl, csr.PublicKey, f.caKey)
	if err != nil {
		return nil, "", err
	}
	f.lastDER = caDER
	return [][]byte{caDER}, "cert/1", nil
}
func (f *fakeClient) FetchCert(context.Context, string, bool) ([][]byte, error) {
	if f.failAt == "fetch" {
		return nil, errors.New("fetch 失败")
	}
	return [][]byte{f.lastDER}, nil
}
func (f *fakeClient) DNS01ChallengeRecord(token string) (string, error) {
	return "txt-" + token, nil
}

// testIssuer 造一个全内存的 Issuer:NewClient 回假 ACME,DNS 是内存实现,
// LookupTXT 直接通过。
func testIssuer(client ACMEClient, dns DNSProvider) *Issuer {
	return &Issuer{
		Directory: "fake://dir",
		DNS:       dns,
		LookupTXT: func(context.Context, string) ([]string, error) { return []string{"txt-tok1"}, nil },
		NewClient: func(string, []byte) (ACMEClient, []byte, error) {
			return client, []byte("account-key"), nil
		},
	}
}

func TestIssuerHappyPath(t *testing.T) {
	dns := &fakeDNS{}
	res, err := testIssuer(&fakeClient{}, dns).Issue(context.Background(), "cp.example.net")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(res.CertPEM) == 0 || len(res.KeyPEM) == 0 || string(res.AccountKeyPEM) != "account-key" {
		t.Fatalf("产物不全")
	}
	if res.NotAfter.IsZero() {
		t.Fatal("没拿到到期时刻")
	}
	if len(dns.records) != 1 || dns.records[0] != "cp.example.net=txt-tok1" {
		t.Fatalf("TXT 记录不符: %v", dns.records)
	}
	// 拿回来的证书真能配成 tls.Certificate。
	if _, err := tls.X509KeyPair(res.CertPEM, res.KeyPEM); err != nil {
		t.Fatalf("证书+私钥配不上: %v", err)
	}
}

func TestIssuerFailureAtStage(t *testing.T) {
	for _, at := range []string{"register", "order", "authz", "accept", "wait", "cert"} {
		_, err := testIssuer(&fakeClient{failAt: at}, &fakeDNS{}).Issue(context.Background(), "cp.example.net")
		if err == nil || !strings.Contains(err.Error(), at) {
			t.Fatalf("failAt=%s 该失败且带环节名,得到 %v", at, err)
		}
	}
}

func TestIssuerDNSFail(t *testing.T) {
	_, err := testIssuer(&fakeClient{}, &fakeDNS{err: errors.New("CF 401")}).Issue(context.Background(), "cp.example.net")
	if err == nil || !strings.Contains(err.Error(), "CF 401") {
		t.Fatalf("该失败: %v", err)
	}
}

// ---- Manager ----

// selfSignedIdentity 造一张与 tls_identity 等价的自签证书(测试用)。
func selfSignedIdentity(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func pemOf(t *testing.T, typ string, der []byte) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// newTestStore 开一个临时库。
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// sniCN 返回 GetCertificate 在 hello.ServerName=sni 下给出的证书 CN。
func sniCN(t *testing.T, m *Manager, sni string) string {
	t.Helper()
	c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: sni})
	if err != nil {
		t.Fatal(err)
	}
	if c.Leaf == nil {
		l, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		return l.Subject.CommonName
	}
	return c.Leaf.Subject.CommonName
}

func acmeStateFor(t *testing.T, cert tls.Certificate, domain string) *store.AcmeState {
	t.Helper()
	return &store.AcmeState{
		CertPEM:  pemOf(t, "CERTIFICATE", cert.Certificate[0]),
		KeyPEM:   pemOf(t, "EC PRIVATE KEY", mustKeyDER(t, cert)),
		Domain:   domain,
		NotAfter: time.Now().Add(60 * 24 * time.Hour),
	}
}

func TestManagerSNI(t *testing.T) {
	st := newTestStore(t)
	selfSigned := selfSignedIdentity(t, "self-signed-fallback")
	acmeCert := selfSignedIdentity(t, "cp.example.net")

	m := NewManager(st, selfSigned, "cp.example.net", acmeStateFor(t, acmeCert, "cp.example.net"), nil)

	if got := sniCN(t, m, "cp.example.net"); got != "cp.example.net" {
		t.Fatalf("SNI 命中该出 ACME 证书,得到 %q", got)
	}
	for _, sni := range []string{"", "other.example.net", "1.2.3.4"} {
		if got := sniCN(t, m, sni); got != "self-signed-fallback" {
			t.Fatalf("SNI=%q 该回自签,得到 %q", sni, got)
		}
	}
}

// TestManagerNoCertFallsBack:有域名没证书,SNI 命中也回自签。
func TestManagerNoCertFallsBack(t *testing.T) {
	st := newTestStore(t)
	selfSigned := selfSignedIdentity(t, "self-signed-fallback")
	m := NewManager(st, selfSigned, "cp.example.net", &store.AcmeState{}, nil)
	if got := sniCN(t, m, "cp.example.net"); got != "self-signed-fallback" {
		t.Fatalf("没证书该回自签,得到 %q", got)
	}
}

// TestManagerDomainMismatchUnloads:库里的证书属于旧域名时按没证书处理。
func TestManagerDomainMismatchUnloads(t *testing.T) {
	st := newTestStore(t)
	selfSigned := selfSignedIdentity(t, "self-signed-fallback")
	acmeCert := selfSignedIdentity(t, "old.example.net")
	m := NewManager(st, selfSigned, "new.example.net", acmeStateFor(t, acmeCert, "old.example.net"), nil)
	for _, sni := range []string{"old.example.net", "new.example.net"} {
		if got := sniCN(t, m, sni); got != "self-signed-fallback" {
			t.Fatalf("域名不符的证书不该装,SNI=%q 得到 %q", sni, got)
		}
	}
}

func mustKeyDER(t *testing.T, cert tls.Certificate) []byte {
	t.Helper()
	k, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatal("不是 ECDSA 私钥")
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// issueIssuerFactory 给 Manager 塞假签发器:client 可配失败点。
func issueIssuerFactory(t *testing.T, client ACMEClient, dns DNSProvider) func(string, bool, []byte) *Issuer {
	return func(string, bool, []byte) *Issuer {
		return testIssuer(client, dns)
	}
}

func waitState(t *testing.T, m *Manager, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.IssueProgress(0).State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等状态 %s 超时,现在 %s", want, m.IssueProgress(0).State)
}

func TestManagerIssueSuccess(t *testing.T) {
	st := newTestStore(t)
	selfSigned := selfSignedIdentity(t, "self-signed-fallback")
	m := NewManager(st, selfSigned, "cp.example.net", &store.AcmeState{}, nil)
	m.SetIssuerFactory(issueIssuerFactory(t, &fakeClient{}, &fakeDNS{}))

	if _, ok := m.StartIssue(context.Background(), "cp.example.net", false); !ok {
		t.Fatal("该启动成功")
	}
	waitState(t, m, model.ACMEIssueDone)
	p := m.IssueProgress(0)
	if len(p.Events) == 0 {
		t.Fatal("该有进度事件")
	}
	as, err := st.GetACMEState()
	if err != nil {
		t.Fatal(err)
	}
	if len(as.CertPEM) == 0 || as.Domain != "cp.example.net" {
		t.Fatalf("证书没落库: %+v", as)
	}
	if string(as.AccountKeyPEM) != "account-key" {
		t.Fatal("账户 key 没落库")
	}
	if got := sniCN(t, m, "cp.example.net"); got != "cp.example.net" {
		t.Fatalf("签发后 SNI 该出 ACME 证书,得到 %q", got)
	}
}

func TestManagerIssueSingleFlight(t *testing.T) {
	st := newTestStore(t)
	m := NewManager(st, selfSignedIdentity(t, "s"), "d.example.net", &store.AcmeState{}, nil)
	release := make(chan struct{})
	m.SetIssuerFactory(func(string, bool, []byte) *Issuer {
		is := testIssuer(&blockingClient{fakeClient{}, release}, &fakeDNS{})
		return is
	})
	if _, ok := m.StartIssue(context.Background(), "d.example.net", false); !ok {
		t.Fatal("第一个该起")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !m.IssueRunning() {
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := m.StartIssue(context.Background(), "d.example.net", false); ok {
		t.Fatal("第二个该被拒(单飞)")
	}
	close(release)
	waitState(t, m, model.ACMEIssueDone)
}

// blockingClient 把 WaitAuthorization 卡住,用于单飞测试。
type blockingClient struct {
	fakeClient
	release chan struct{}
}

func (b *blockingClient) WaitAuthorization(ctx context.Context, u string) (*xacme.Authorization, error) {
	select {
	case <-b.release:
		return b.fakeClient.WaitAuthorization(ctx, u)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestManagerIssueFailureKeepsCert(t *testing.T) {
	st := newTestStore(t)
	selfSigned := selfSignedIdentity(t, "self-signed-fallback")
	acmeCert := selfSignedIdentity(t, "cp.example.net")
	as0 := acmeStateFor(t, acmeCert, "cp.example.net")
	as0.AccountKeyPEM = []byte("ak")
	if err := st.SaveACMEState(as0); err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, selfSigned, "cp.example.net", as0, nil)
	m.SetIssuerFactory(issueIssuerFactory(t, &fakeClient{failAt: "wait"}, &fakeDNS{}))

	if _, ok := m.StartIssue(context.Background(), "cp.example.net", false); !ok {
		t.Fatal("该启动")
	}
	waitState(t, m, model.ACMEIssueFailed)
	as, _ := st.GetACMEState()
	if len(as.CertPEM) == 0 {
		t.Fatal("失败把已有证书清掉了")
	}
	if as.LastError == "" || as.Failures != 1 || as.NextRetryAt.IsZero() {
		t.Fatalf("失败记录不全: %+v", as)
	}
	if got := sniCN(t, m, "cp.example.net"); got != "cp.example.net" {
		t.Fatalf("已有证书该继续服役,得到 %q", got)
	}
}

// ---- Cloudflare ----

func TestCloudflareSetTXT(t *testing.T) {
	var (
		mu       sync.Mutex
		gotPosts []map[string]any
		deleted  []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("token 没在 Authorization 里")
		}
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones") && !strings.Contains(r.URL.Path, "/dns_records"):
			if r.URL.Query().Get("name") == "example.net" {
				json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]any{{"id": "z1", "name": "example.net"}}})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]any{}})
			}
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dns_records"):
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dns_records"):
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			gotPosts = append(gotPosts, body)
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"id": "rec1"}})
		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
		default:
			t.Errorf("意外的请求 %s %s", r.Method, r.URL)
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": nil})
		}
	}))
	defer srv.Close()

	cf := &Cloudflare{Token: "tok", APIBase: srv.URL}
	cleanup, err := cf.SetTXT(context.Background(), "cp.example.net", "v1")
	if err != nil {
		t.Fatalf("SetTXT: %v", err)
	}
	if len(gotPosts) != 1 {
		t.Fatalf("该建一条记录,得到 %v", gotPosts)
	}
	if gotPosts[0]["name"] != "_acme-challenge.cp.example.net" || gotPosts[0]["content"] != "v1" || gotPosts[0]["type"] != "TXT" {
		t.Fatalf("记录形状不符: %v", gotPosts[0])
	}
	if err := cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(deleted) != 1 || !strings.Contains(deleted[0], "rec1") {
		t.Fatalf("cleanup 该删 rec1: %v", deleted)
	}
}

func TestCloudflareNoZone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]any{}})
	}))
	defer srv.Close()
	cf := &Cloudflare{Token: "t", APIBase: srv.URL}
	_, err := cf.SetTXT(context.Background(), "nowhere.example", "v")
	if err == nil || !strings.Contains(err.Error(), "zone") {
		t.Fatalf("该报找不到 zone: %v", err)
	}
}

func TestCloudflareAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"success": false,
			"errors": []map[string]any{{"code": 9109, "message": "invalid token"}}})
	}))
	defer srv.Close()
	cf := &Cloudflare{Token: "bad", APIBase: srv.URL}
	_, err := cf.SetTXT(context.Background(), "x.example", "v")
	if err == nil || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("该报 CF 错误: %v", err)
	}
}
