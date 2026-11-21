package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/acme"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// UC-26:ACME 配置接口。签发流水线本身在 internal/acme 有测试,这里只管
// HTTP 边界:字段语义、敏感值不出明文、settings PUT 不动 acme 键、
// 换域名/换 CA 会把旧证书作废。

func TestACMEConfigPutGetDelete(t *testing.T) {
	e := newEnv(t)

	// 初始:status 全空。
	code, data := e.api("GET", "/api/acme/status", nil)
	if code != http.StatusOK {
		t.Fatalf("status: %d %s", code, data)
	}
	var st0 model.ACMEStatus
	json.Unmarshal(data, &st0)
	if st0.Domain != "" || st0.CFTokenSet || st0.CertIssued {
		t.Fatalf("初始该全空: %+v", st0)
	}

	// PUT:设域名+token+staging。
	staging := true
	code, data = e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{
		Domain: "CP.Example.Net", CFToken: "tok-1", Staging: &staging})
	if code != http.StatusOK {
		t.Fatalf("PUT: %d %s", code, data)
	}
	var cfg model.ACMEConfig
	json.Unmarshal(data, &cfg)
	if cfg.Domain != "cp.example.net" || !cfg.CFTokenSet || !cfg.Staging {
		t.Fatalf("配置回显不符(域名该归一到小写): %+v", cfg)
	}

	// 库里:token 明文落库(敏感值),但 settings 的 GET 不回它。
	st, err := e.st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if st.ACMECFToken != "tok-1" || st.ACMEDomain != "cp.example.net" || !st.ACMEStaging {
		t.Fatalf("库里配置不符: %+v", st)
	}
	code, data = e.api("GET", "/api/settings", nil)
	var stg model.Settings
	json.Unmarshal(data, &stg)
	if stg.ACMECFToken != "" || !stg.ACMECFTokenSet {
		t.Fatalf("GET /api/settings 不该回 token 明文,该回 token_set: %+v", stg)
	}
	if stg.ACMEDomain != "cp.example.net" || !stg.ACMEStaging {
		t.Fatalf("GET /api/settings 该回 acme 键: %+v", stg)
	}

	// 部分 PUT:只换 token,域名与 staging 不动。
	code, _ = e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{CFToken: "tok-2"})
	if code != http.StatusOK {
		t.Fatalf("部分 PUT: %d", code)
	}
	st, _ = e.st.GetSettings()
	if st.ACMECFToken != "tok-2" || st.ACMEDomain != "cp.example.net" || !st.ACMEStaging {
		t.Fatalf("部分 PUT 动了没给的字段: %+v", st)
	}

	// DELETE:清配置。
	code, data = e.api("DELETE", "/api/acme/config", nil)
	if code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", code, data)
	}
	st, _ = e.st.GetSettings()
	if st.ACMEDomain != "" || st.ACMECFToken != "" || st.ACMEStaging {
		t.Fatalf("DELETE 没清干净: %+v", st)
	}
}

func TestACMEConfigValidation(t *testing.T) {
	e := newEnv(t)
	for _, bad := range []string{"", ".", "-bad.com", "bad_domain.com", "a..b.com", "toolong" + string(make([]byte, 260)) + ".com"} {
		if bad == "" {
			continue // 空串是「不改」,不是非法
		}
		code, data := e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{Domain: bad})
		if code != http.StatusBadRequest {
			t.Fatalf("域名 %q 该 400,得到 %d %s", bad, code, data)
		}
	}
	// 单标签域名不行。
	code, _ := e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{Domain: "localhost"})
	if code != http.StatusBadRequest {
		t.Fatalf("单标签域名该 400: %d", code)
	}
}

// PUT /api/settings 不能改 acme_* —— 那里没有「换域名废证书」的副作用,
// 绕过它会留下域名与证书不符的状态。
func TestSettingsPutPreservesACMEKeys(t *testing.T) {
	e := newEnv(t)
	staging := false
	code, _ := e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{
		Domain: "cp.example.net", CFToken: "tok", Staging: &staging})
	if code != http.StatusOK {
		t.Fatalf("先配 acme: %d", code)
	}

	// 正常走一遍 settings PUT:读出来、改个别的、写回 —— 拿到的 GET 里
	// token 是空的(被掩),如果 PUT 不保留 acme 键,token 就被清掉了。
	code, data := e.api("GET", "/api/settings", nil)
	if code != http.StatusOK {
		t.Fatal(code)
	}
	var st model.Settings
	json.Unmarshal(data, &st)
	st.ProfileTitle = "换了个标题"
	st.ACMEDomain = "evil.example.net" // 客户端塞了也不该生效
	st.ACMECFToken = "evil-token"
	st.ACMEStaging = true
	code, data = e.api("PUT", "/api/settings", st)
	if code != http.StatusOK {
		t.Fatalf("PUT settings: %d %s", code, data)
	}
	got, _ := e.st.GetSettings()
	if got.ACMEDomain != "cp.example.net" || got.ACMECFToken != "tok" || got.ACMEStaging {
		t.Fatalf("settings PUT 动了 acme 键: %+v", got)
	}
}

// 换域名或换 CA 后,库里的旧证书必须作废(它签给的是旧域名/旧 CA)。
func TestACMEConfigChangeClearsCert(t *testing.T) {
	e := newEnv(t)
	staging := false
	e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{
		Domain: "cp.example.net", CFToken: "tok", Staging: &staging})
	// 伪造一张已签证书(形状与真签发落库的一致:PEM + 域名)。
	if err := e.st.SaveACMEState(testACMEState(t, "cp.example.net")); err != nil {
		t.Fatal(err)
	}

	// 换域名 → 证书作废。
	code, _ := e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{Domain: "new.example.net"})
	if code != http.StatusOK {
		t.Fatalf("换域名: %d", code)
	}
	as2, _ := e.st.GetACMEState()
	if len(as2.CertPEM) != 0 || as2.Domain != "" {
		t.Fatalf("换域名后证书该作废: %+v", as2)
	}

	// 切 staging(不重给域名)→ 同样作废。先补一张新证书。
	staging = true
	e.st.SaveACMEState(testACMEState(t, "new.example.net"))
	code, _ = e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{Staging: &staging})
	if code != http.StatusOK {
		t.Fatalf("切 staging: %d", code)
	}
	as3, _ := e.st.GetACMEState()
	if len(as3.CertPEM) != 0 {
		t.Fatalf("切 CA 后证书该作废: %+v", as3)
	}
}

// 只换 token、或 PUT 同值域名,不该动证书。
func TestACMEConfigSameValueKeepsCert(t *testing.T) {
	e := newEnv(t)
	staging := false
	e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{
		Domain: "cp.example.net", CFToken: "tok", Staging: &staging})
	e.st.SaveACMEState(testACMEState(t, "cp.example.net"))
	code, _ := e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{
		Domain: "cp.example.net", CFToken: "tok-2"})
	if code != http.StatusOK {
		t.Fatalf("PUT 同值域名: %d", code)
	}
	as, _ := e.st.GetACMEState()
	if len(as.CertPEM) == 0 {
		t.Fatal("同值 PUT 不该动证书")
	}
}

// POST issue 没配域名/没配 token/没接 Manager 时的拒绝路径。
func TestACMEIssueRejected(t *testing.T) {
	e := newEnv(t)
	// 没接 Manager(测试 env 没 serve 接线):503。
	code, _ := e.api("POST", "/api/acme/issue", nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("没 Manager 该 503: %d", code)
	}
	// 接了 Manager 但没配域名:400。
	m := acme.NewManager(e.st, testSelfSignedCert(t), "", nil, nil)
	e.s.SetACME(m)
	code, data := e.api("POST", "/api/acme/issue", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("没域名该 400: %d %s", code, data)
	}
	// 配了域名没配 token:400。
	staging := false
	e.api("PUT", "/api/acme/config", model.ACMEConfigRequest{Domain: "cp.example.net", Staging: &staging})
	code, _ = e.api("POST", "/api/acme/issue", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("没 token 该 400: %d", code)
	}
	// 进度接口没跑过任务也能答(idle)。
	code, data = e.api("GET", "/api/acme/issue", nil)
	if code != http.StatusOK {
		t.Fatalf("progress: %d %s", code, data)
	}
	var p model.ACMEIssueProgress
	json.Unmarshal(data, &p)
	if p.State != model.ACMEIssueIdle {
		t.Fatalf("初始该 idle: %s", p.State)
	}
}

// testSelfSignedCert 造一张自签证书,给 Manager 当兜底身份。
func testSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "self-signed"},
		NotBefore:    time.Now(),
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

// testACMEState 造一行「签过证书」的 acme_state(PEM 是测试自签证书,
// 形状与真签发一致)。
func testACMEState(t *testing.T, domain string) *store.AcmeState {
	t.Helper()
	cert := testSelfSignedCert(t)
	keyDER, _ := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	return &store.AcmeState{
		AccountKeyPEM: []byte("account-key-pem"),
		CertPEM:       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}),
		KeyPEM:        pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		Domain:        domain,
		IssuedAt:      time.Now().Add(-24 * time.Hour),
		NotAfter:      time.Now().Add(60 * 24 * time.Hour),
	}
}
