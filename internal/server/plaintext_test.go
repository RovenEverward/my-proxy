package server

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlaintextAPIWarning(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.plaintextAPIWarning(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cases := []struct {
		name, remote, path string
		tls                bool
		want               bool
	}{
		{"公网明文管理", "203.0.113.5:4000", "/api/status", false, true},
		{"公网 HTTPS 管理", "203.0.113.5:4000", "/api/status", true, false},
		{"回环明文管理", "127.0.0.1:4000", "/api/status", false, false},
		{"公网明文订阅", "203.0.113.5:4000", "/sub/abc", false, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", c.path, nil)
		r.RemoteAddr = c.remote
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get(plaintextWarningHeader) != ""; got != c.want {
			t.Errorf("%s: 有提示=%v,want %v", c.name, got, c.want)
		}
	}
}
