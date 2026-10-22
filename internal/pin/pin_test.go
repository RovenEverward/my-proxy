package pin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	raw := strings.Repeat("A", 43)
	want := Prefix + raw
	for _, in := range []string{want, raw, "sha256:" + raw, "  " + want + "=  "} {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "SHA256:abc", "SHA256:" + strings.Repeat("!", 43), "MD5:" + raw} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) 应当报错", bad)
		}
	}
}

// 页面一次并行读取多个资源；下一次切页应能复用整批连接，而非只留下两个。
func TestTransportRetainsParallelConnections(t *testing.T) {
	gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
	arrived := make(chan struct{}, 8)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batch, _ := strconv.Atoi(r.Header.Get("Batch"))
		arrived <- struct{}{}
		<-gates[batch]
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	tr := Transport(Fingerprint(srv.Certificate()))
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	for batch := 0; batch < 2; batch++ {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, _ := http.NewRequest("GET", srv.URL, nil)
				req.Header.Set("Batch", strconv.Itoa(batch))
				req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
					if batch == 1 && !info.Reused {
						t.Error("切页重新建立了连接")
					}
				}}))
				res, err := client.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
			}()
		}
		for i := 0; i < 8; i++ {
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				close(gates[batch])
				wg.Wait()
				t.Fatal("并行请求没有到达")
			}
		}
		close(gates[batch])
		wg.Wait()
	}
}
