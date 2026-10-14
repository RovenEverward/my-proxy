package server

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/pin"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// startSniff 在随机端口上起一个真实的单端口 HTTP/HTTPS 服务。
func startSniff(t *testing.T, h http.Handler) (addr, fp string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cert, fp, err := loadOrCreateIdentity(st)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := newSniffListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(sl)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), fp
}

func schemeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			io.WriteString(w, "tls "+r.URL.Path)
			return
		}
		io.WriteString(w, "plain "+r.URL.Path)
	})
}

func get(t *testing.T, c *http.Client, url string) (string, error) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func TestSniffServesPlainAndTLSOnOnePort(t *testing.T) {
	addr, fp := startSniff(t, schemeHandler())

	plain := &http.Client{Timeout: 5 * time.Second}
	tlsc := &http.Client{Timeout: 5 * time.Second, Transport: pin.Transport(fp)}

	// 交错多轮,同时验证长连接复用不会串台。
	for i := 0; i < 3; i++ {
		if got, err := get(t, plain, "http://"+addr+"/sub/x"); err != nil || got != "plain /sub/x" {
			t.Fatalf("明文: %q %v", got, err)
		}
		if got, err := get(t, tlsc, "https://"+addr+"/api/status"); err != nil || got != "tls /api/status" {
			t.Fatalf("TLS: %q %v", got, err)
		}
	}
}

func TestSniffRejectsWrongFingerprint(t *testing.T) {
	addr, _ := startSniff(t, schemeHandler())
	wrong := "SHA256:" + strings.Repeat("A", 43)
	c := &http.Client{Timeout: 5 * time.Second, Transport: pin.Transport(wrong)}
	_, err := get(t, c, "https://"+addr+"/api/status")
	if !errors.Is(err, pin.ErrMismatch) {
		t.Fatalf("要指纹不符的错误,得到 %v", err)
	}
}

func TestSniffProbeMatchesServerFingerprint(t *testing.T) {
	addr, fp := startSniff(t, schemeHandler())
	got, err := pin.Probe(t.Context(), addr)
	if err != nil || got != fp {
		t.Fatalf("Probe = %q, %v;want %q", got, err, fp)
	}
}

// 一个只连不发的连接不能挡住别人。
func TestSniffSlowClientDoesNotBlockAccept(t *testing.T) {
	addr, _ := startSniff(t, schemeHandler())
	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	c := &http.Client{Timeout: 3 * time.Second}
	if got, err := get(t, c, "http://"+addr+"/sub/y"); err != nil || got != "plain /sub/y" {
		t.Fatalf("%q %v", got, err)
	}
}

// 身份存进库里,第二次打开拿到的是同一个指纹。
func TestIdentityPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mpx.db")
	var fps []string
	for i := 0; i < 2; i++ {
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, fp, err := loadOrCreateIdentity(st)
		st.Close()
		if err != nil {
			t.Fatal(err)
		}
		fps = append(fps, fp)
	}
	if fps[0] != fps[1] {
		t.Fatalf("两次指纹不同: %v", fps)
	}
}
