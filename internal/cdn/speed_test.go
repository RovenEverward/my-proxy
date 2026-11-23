package cdn

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// MeasureSpeed 对着本地 TLS 服务打:服务端给 4MiB 定长体,断言速度
// 是正的且 CF-RAY 头被带出来。
func TestMeasureSpeed(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("CF-RAY", "8f0000000000-TST")
		w.WriteHeader(200)
		// 4 MiB 定长体:本地回环读完不到一秒,断言量足够。
		buf := make([]byte, 4*1024*1024)
		for i := range buf {
			buf[i] = byte(i)
		}
		_, _ = w.Write(buf)
	}))
	srv.TLS = &tls.Config{}
	srv.StartTLS()
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	u, _ := url.Parse("https://speed.example.com/__down")

	mbps, colo, err := measureSpeed(context.Background(), host, port, u, 10*time.Second)
	if err != nil {
		t.Fatalf("测速失败: %v", err)
	}
	if mbps <= 0 {
		t.Fatalf("速度应为正,实际 %f", mbps)
	}
	if colo != "TST" {
		t.Fatalf("CF-RAY 机房码该带出来,实际 %q", colo)
	}
}

// 地址端回非 200 时按失败处理 —— 探测端靠连续失败判「地址挂了」。
func TestMeasureSpeedBadStatus(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	u, _ := url.Parse("https://speed.example.com/__down")
	if _, _, err := measureSpeed(context.Background(), host, port, u, 5*time.Second); err == nil {
		t.Fatal("非 200 该报错")
	}
}

// 限速服务验证折算口径:每秒 1MiB,3 秒时限约得 ~1MB/s。
func TestMeasureSpeedThrottle(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 64*1024)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			for i := 0; i < 16; i++ { // 1MiB per 秒
				if _, err := w.Write(buf); err != nil {
					return
				}
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(time.Second)
		}
	}))
	srv.TLS = &tls.Config{}
	srv.StartTLS()
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	u, _ := url.Parse("https://speed.example.com/__down")
	mbps, _, err := measureSpeed(context.Background(), host, port, u, 2500*time.Millisecond)
	if err != nil {
		t.Fatalf("测速失败: %v", err)
	}
	// 1MiB/s 供流,约 2 秒多窗口,期望 0.5~1.5 MB/s 区间。
	if mbps < 0.4 || mbps > 2.0 {
		t.Fatalf("折算速度离谱: %f MB/s", mbps)
	}
}
