package bwtest

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestBoxMixedInboundDirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(make([]byte, 1<<20)) // 1 MiB
	}))
	defer target.Close()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	cfg := []byte(`{
	  "log": {"level": "error"},
	  "dns": {"servers": [{"type": "local", "tag": "dns-local"}], "final": "dns-local"},
	  "inbounds": [{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": ` + strconv.Itoa(port) + `}],
	  "outbounds": [{"type": "direct", "tag": "out"}],
	  "route": {"final": "out"}
	}`)
	b, err := startBox(cfg)
	if err != nil {
		t.Fatalf("startBox: %v", err)
	}
	defer b.Close()
	time.Sleep(300 * time.Millisecond)

	tr := &http.Transport{Proxy: http.ProxyURL(mustURL(t, "socks5://127.0.0.1:"+strconv.Itoa(port)))}
	c := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	resp, err := c.Get(target.URL)
	if err != nil {
		t.Fatalf("request via socks: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 1<<20 {
		t.Fatalf("got %d bytes, want 1 MiB", len(body))
	}
	t.Logf("downloaded %d bytes via embedded sing-box", len(body))
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
