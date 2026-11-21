package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 这条测试让 Issuer 走真实的 x/crypto/acme 客户端,对上一个按 RFC 8555
// 应答的 mock CA。它验证的不是状态机(那个 fakeClient 已经验了),而是
// 「我们调的 API 形状和真 CA 的协议对得上」:JWS 请求、nonce、订单、
// authz、dns-01、finalize、取证书。

// mockACME 是一个最小的 RFC 8555 CA:不验签名,按约定状态机应答。
type mockACME struct {
	nonce        atomic.Int64
	chalAccepted atomic.Bool
	caDER        []byte
	caKey        *ecdsa.PrivateKey
	leafDER      []byte // finalize 时签
}

func newMockACME(t *testing.T) *mockACME {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mock CA"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return &mockACME{caDER: der, caKey: k}
}

func (m *mockACME) handler(t *testing.T, base string) http.Handler {
	mux := http.NewServeMux()
	nonce := func(w http.ResponseWriter) {
		w.Header().Set("Replay-Nonce", big.NewInt(m.nonce.Add(1)).String())
	}
	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if v != nil {
			json.NewEncoder(w).Encode(v)
		}
	}
	mux.HandleFunc("GET /dir", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{
			"newNonce":   base + "/new-nonce",
			"newAccount": base + "/new-account",
			"newOrder":   base + "/new-order",
		})
	})
	mux.HandleFunc("HEAD /new-nonce", func(w http.ResponseWriter, r *http.Request) { nonce(w); w.WriteHeader(200) })
	mux.HandleFunc("GET /new-nonce", func(w http.ResponseWriter, r *http.Request) { nonce(w); w.WriteHeader(200) })
	mux.HandleFunc("POST /new-account", func(w http.ResponseWriter, r *http.Request) {
		nonce(w)
		w.Header().Set("Location", base+"/acct/1")
		write(w, 201, map[string]any{"status": "valid"})
	})
	mux.HandleFunc("POST /new-order", func(w http.ResponseWriter, r *http.Request) {
		nonce(w)
		w.Header().Set("Location", base+"/order/1")
		write(w, 201, map[string]any{
			"status":         "pending",
			"authorizations": []string{base + "/authz/1"},
			"finalize":       base + "/finalize/1",
		})
	})
	mux.HandleFunc("POST /authz/1", func(w http.ResponseWriter, r *http.Request) {
		nonce(w)
		// 挑战被 Accept 过(标志位)之后,授权就是 valid —— 用状态位而不是
		// 计数区分「首次读」与「Accept 后回读」。
		if !m.chalAccepted.Load() {
			write(w, 200, map[string]any{
				"status":     "pending",
				"identifier": map[string]string{"type": "dns", "value": "cp.example.net"},
				"challenges": []map[string]any{
					{"type": "dns-01", "url": base + "/chal/1", "token": "tok-abc", "status": "pending"},
				},
			})
			return
		}
		write(w, 200, map[string]any{"status": "valid"})
	})
	mux.HandleFunc("POST /chal/1", func(w http.ResponseWriter, r *http.Request) {
		nonce(w)
		m.chalAccepted.Store(true)
		write(w, 200, map[string]any{"type": "dns-01", "url": base + "/chal/1", "token": "tok-abc", "status": "valid"})
	})
	mux.HandleFunc("POST /finalize/1", func(w http.ResponseWriter, r *http.Request) {
		nonce(w)
		// ACME 请求体是 JWS:{protected, payload, signature},payload 是
		// base64url 编码的 JSON{"csr": "<base64url DER>"} —— 要剥两层。
		var jws struct {
			Payload string `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&jws); err != nil {
			t.Errorf("finalize body 不是 JWS JSON: %v", err)
			write(w, 400, nil)
			return
		}
		payload, err := base64.RawURLEncoding.DecodeString(jws.Payload)
		if err != nil {
			t.Errorf("payload base64 解码失败: %v", err)
			write(w, 400, nil)
			return
		}
		var body struct {
			CSR string `json:"csr"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("payload 不是 JSON: %v", err)
			write(w, 400, nil)
			return
		}
		csrDER, err := base64.RawURLEncoding.DecodeString(body.CSR)
		if err != nil {
			t.Errorf("csr base64 解码失败: %v", err)
			write(w, 400, nil)
			return
		}
		csr, err := x509.ParseCertificateRequest(csrDER)
		if err != nil {
			t.Errorf("CSR 解析失败: %v", err)
			write(w, 400, map[string]any{"type": "urn:ietf:params:acme:error:badCSR"})
			return
		}
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: csr.Subject.CommonName},
			DNSNames: csr.DNSNames, NotBefore: time.Now(), NotAfter: time.Now().Add(90 * 24 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		caCert, _ := x509.ParseCertificate(m.caDER)
		leaf, err := x509.CreateCertificate(rand.Reader, tpl, caCert, csr.PublicKey, m.caKey)
		if err != nil {
			t.Errorf("签发失败: %v", err)
			return
		}
		m.leafDER = leaf
		w.Header().Set("Location", base+"/order/1")
		write(w, 200, map[string]any{
			"status":         "ready",
			"finalize":       base + "/finalize/1",
			"authorizations": []string{base + "/authz/1"},
		})
	})
	mux.HandleFunc("POST /order/1", func(w http.ResponseWriter, r *http.Request) {
		nonce(w)
		write(w, 200, map[string]any{
			"status":         "valid",
			"authorizations": []string{base + "/authz/1"},
			"finalize":       base + "/finalize/1",
			"certificate":    base + "/cert/1",
		})
	})
	mux.HandleFunc("POST /cert/1", func(w http.ResponseWriter, r *http.Request) {
		nonce(w)
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		w.WriteHeader(200)
		w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.leafDER}))
		w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.caDER}))
	})
	return mux
}

func b64decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// tlsX509 是 tls.X509KeyPair 的短写。
func tlsX509(certPEM, keyPEM []byte) (tls.Certificate, error) {
	return tls.X509KeyPair(certPEM, keyPEM)
}

func TestIssuerAgainstMockACME(t *testing.T) {
	ca := newMockACME(t)
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ca.handler(t, base).ServeHTTP(w, r)
	}))
	defer srv.Close()
	base = srv.URL

	dns := &fakeDNS{}
	issuer := &Issuer{
		Directory: srv.URL + "/dir",
		DNS:       dns,
		// 等 TXT 在公网可见那步:真实现查 1.1.1.1,测试里让「刚摆上 DNS
		// 的值」立即可见 —— fakeDNS 记下的最后一条就是要找的值。
		LookupTXT: func(context.Context, string) ([]string, error) {
			dns.mu.Lock()
			defer dns.mu.Unlock()
			if len(dns.records) == 0 {
				return nil, nil
			}
			parts := strings.SplitN(dns.records[len(dns.records)-1], "=", 2)
			return []string{parts[1]}, nil
		},
	}
	res, err := issuer.Issue(context.Background(), "cp.example.net")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(res.CertPEM) == 0 {
		t.Fatal("没拿到证书")
	}
	// 证书能配进 tls、CN 对、链带了两张(叶 + CA)。
	cert, err := tlsX509(res.CertPEM, res.KeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if leaf.Subject.CommonName != "cp.example.net" {
		t.Fatalf("证书 CN 不符: %s", leaf.Subject.CommonName)
	}
	if len(cert.Certificate) < 2 {
		t.Fatalf("该带完整链(叶+CA),得到 %d 张", len(cert.Certificate))
	}
	if len(dns.records) != 1 || dns.records[0] == "" {
		t.Fatalf("该摆过一条 TXT: %v", dns.records)
	}
}
