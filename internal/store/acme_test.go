package store

import (
	"testing"
	"time"
)

func TestACMEStateRoundtrip(t *testing.T) {
	s := newTestStore(t)

	// 没写过:零值,不算错误。
	st, err := s.GetACMEState()
	if err != nil {
		t.Fatalf("GetACMEState(空): %v", err)
	}
	if st.Domain != "" || len(st.CertPEM) != 0 || st.LastError != "" || !st.IssuedAt.IsZero() {
		t.Fatalf("空库应回零值,得到 %+v", st)
	}

	want := AcmeState{
		AccountKeyPEM: []byte("-----BEGIN EC PRIVATE KEY-----\nAAA\n-----END EC PRIVATE KEY-----\n"),
		CertPEM:       []byte("-----BEGIN CERTIFICATE-----\nCCC\n-----END CERTIFICATE-----\n"),
		KeyPEM:        []byte("-----BEGIN EC PRIVATE KEY-----\nKKK\n-----END EC PRIVATE KEY-----\n"),
		Domain:        "cp.example.net",
		Staging:       true,
		IssuedAt:      time.Now().Add(-time.Hour).Truncate(time.Second),
		NotAfter:      time.Now().Add(90 * 24 * time.Hour).Truncate(time.Second),
		LastError:     "上一轮超时",
		LastAttemptAt: time.Now().Add(-time.Minute).Truncate(time.Second),
		Failures:      2,
		NextRetryAt:   time.Now().Add(2 * time.Minute).Truncate(time.Second),
	}
	if err := s.SaveACMEState(&want); err != nil {
		t.Fatalf("SaveACMEState: %v", err)
	}
	got, err := s.GetACMEState()
	if err != nil {
		t.Fatalf("GetACMEState: %v", err)
	}
	if string(got.AccountKeyPEM) != string(want.AccountKeyPEM) ||
		string(got.CertPEM) != string(want.CertPEM) ||
		string(got.KeyPEM) != string(want.KeyPEM) ||
		got.Domain != want.Domain || got.Staging != want.Staging ||
		!got.IssuedAt.Equal(want.IssuedAt) || !got.NotAfter.Equal(want.NotAfter) ||
		got.LastError != want.LastError || !got.LastAttemptAt.Equal(want.LastAttemptAt) ||
		got.Failures != want.Failures || !got.NextRetryAt.Equal(want.NextRetryAt) {
		t.Fatalf("往返不符: %+v", got)
	}

	// ClearACMECert 只清证书域:account key 与错误保留。
	if err := s.ClearACMECert(); err != nil {
		t.Fatalf("ClearACMECert: %v", err)
	}
	got, err = s.GetACMEState()
	if err != nil {
		t.Fatalf("GetACMEState(清后): %v", err)
	}
	if len(got.CertPEM) != 0 || len(got.KeyPEM) != 0 || got.Domain != "" || !got.NotAfter.IsZero() || !got.IssuedAt.IsZero() {
		t.Fatalf("证书域该清空: %+v", got)
	}
	if string(got.AccountKeyPEM) != string(want.AccountKeyPEM) || got.LastError != want.LastError {
		t.Fatalf("账户与错误不该动: %+v", got)
	}
}

func TestSettingsACMERoundtrip(t *testing.T) {
	s := newTestStore(t)
	st, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if st.ACMEDomain != "" || st.ACMECFToken != "" || st.ACMEStaging {
		t.Fatalf("acme 键默认该为空: %+v", st)
	}
	st.ACMEDomain = "cp.example.net"
	st.ACMECFToken = "tok"
	st.ACMEStaging = true
	if err := s.SaveSettings(st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.ACMEDomain != "cp.example.net" || got.ACMECFToken != "tok" || !got.ACMEStaging {
		t.Fatalf("acme 键往返不符: %+v", got)
	}
}
