package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPICompression(t *testing.T) {
	for _, tc := range []struct {
		name, accept, contentType string
		code, size                int
		compressed                bool
	}{
		{"json", "gzip", "application/json", 200, 4096, true},
		{"error", "gzip", "application/json", 503, 4096, true},
		{"old-client", "", "application/json", 200, 4096, false},
		{"explicit-zero", "gzip;q=0, *;q=1", "application/json", 200, 4096, false},
		{"zero", "gzip;q=0", "application/json", 200, 4096, false},
		{"wildcard", "*;q=0.5", "application/json", 200, 4096, true},
		{"small", "gzip", "application/json", 200, 20, false},
		{"empty", "gzip", "application/json", 204, 0, false},
		{"backup", "gzip", "application/gzip", 200, 4096, false},
		{"template", "gzip", "text/plain", 200, 4096, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(strings.Repeat("x", tc.size))
			req := httptest.NewRequest("GET", "/api/test", nil)
			req.Header.Set("Accept-Encoding", tc.accept)
			rec := httptest.NewRecorder()
			apiCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.code)
				_, _ = w.Write(body)
			})).ServeHTTP(rec, req)
			if rec.Code != tc.code || (rec.Header().Get("Content-Encoding") == "gzip") != tc.compressed {
				t.Fatalf("status=%d, encoding=%q", rec.Code, rec.Header().Get("Content-Encoding"))
			}
			got := rec.Body.Bytes()
			if tc.compressed {
				zr, err := gzip.NewReader(bytes.NewReader(got))
				if err != nil {
					t.Fatal(err)
				}
				got, err = io.ReadAll(zr)
				if err != nil {
					t.Fatal(err)
				}
				_ = zr.Close()
				if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
					t.Fatal("missing Vary")
				}
			}
			if !bytes.Equal(got, body) {
				t.Fatal("decoded bytes changed")
			}
		})
	}
}

func TestAPICompressionRealHandler(t *testing.T) {
	e := newEnv(t)
	request := func(accept string) *http.Response {
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/rule-catalog", nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Accept-Encoding", accept)
		client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
		t.Cleanup(func() { client.CloseIdleConnections() })
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	plain := request("identity")
	want, _ := io.ReadAll(plain.Body)
	_ = plain.Body.Close()
	packed := request("gzip")
	defer packed.Body.Close()
	if packed.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("API route did not negotiate gzip")
	}
	zr, err := gzip.NewReader(packed.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	_ = zr.Close()
	if !bytes.Equal(got, want) {
		t.Fatal("API JSON bytes changed after decoding")
	}
}
