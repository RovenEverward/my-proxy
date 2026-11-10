package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSourceRestartResumesDueRefresh verifies persisted grants and startup scheduling
// with two separate control-plane processes and one shared database.
func TestSourceRestartResumesDueRefresh(t *testing.T) {
	port := freePort(t)
	e := &liveEnv{t: t, mpx: buildMPX(t), base: fmt.Sprintf("http://127.0.0.1:%d", port), token: liveToken, home: t.TempDir()}
	db := filepath.Join(t.TempDir(), "mpx.db")
	var upstreamBody atomic.Value
	upstreamBody.Store("vless://11111111-1111-4111-8111-111111111111@before.example.test:443#before")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, upstreamBody.Load().(string)) }))
	defer upstream.Close()
	start := func() func() {
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, e.mpx, "serve", "--addr", fmt.Sprintf("127.0.0.1:%d", port), "--db", db, "--token", e.token, "--auto-locate=false", "--interval", "1h")
		cmd.Env = append(os.Environ(), "HOME="+e.home, "MPX_AUTO_LOCATE=0", "MPX_SERVER=", "MPX_TOKEN=")
		log, err := os.CreateTemp(t.TempDir(), "serve-*.log")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			cancel()
			log.Close()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			cancel()
			<-done
			log.Close()
		}
		t.Cleanup(stop)
		deadline := time.Now().Add(10 * time.Second)
		for {
			req, _ := http.NewRequest("GET", e.base+"/api/status", nil)
			req.Header.Set("Authorization", "Bearer "+e.token)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					return stop
				}
			}
			if time.Now().After(deadline) {
				stop()
				raw, _ := os.ReadFile(log.Name())
				t.Fatalf("control plane not ready: %s", raw)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	stop := start()
	e.runOK("", "user", "add", "alice")
	e.runOK(upstream.URL+"\n", "source", "add", "persistent", "--url-stdin", "--interval", "1s")
	e.runOK("", "source", "grant", "persistent", "--user", "alice")
	token := e.subTokenFor("alice")
	if got := e.subscription(token, "links"); !strings.Contains(got, "before.example.test") {
		t.Fatal("initial source not visible")
	}
	stop()
	upstreamBody.Store("vless://22222222-2222-4222-8222-222222222222@after.example.test:443#after")
	time.Sleep(1100 * time.Millisecond)
	stop = start()
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := e.subscription(token, "links")
		if strings.Contains(got, "after.example.test") && !strings.Contains(got, "before.example.test") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restart did not refresh due source: %s", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw := e.apiOK("GET", "/api/sources/1/grants", "")
	if !strings.Contains(string(raw), "alice") {
		t.Fatal("source grant lost after restart")
	}
}
