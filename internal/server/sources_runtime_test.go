package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"golang.org/x/net/proxy"
)

func sourceRuntimePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func sourceRuntimeProcess(t *testing.T, bin string, args []string, port int) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, args...)
	logPath := filepath.Join(t.TempDir(), "core.log")
	log, err := os.Create(logPath)
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
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return stop
		}
		select {
		case err := <-done:
			stopped = true
			cancel()
			log.Close()
			body, _ := os.ReadFile(logPath)
			t.Fatalf("core exited: %v\n%s", err, body)
		case <-deadline.C:
			stop()
			body, _ := os.ReadFile(logPath)
			t.Fatalf("core did not listen\n%s", body)
		case <-tick.C:
		}
	}
}

// Run with official cores to verify actual proxy traffic, not only config syntax.
func TestSourcesOfficialClientsConnect(t *testing.T) {
	singbox, mihomo := os.Getenv("MPX_TEST_SINGBOX"), os.Getenv("MPX_TEST_MIHOMO")
	if singbox == "" || mihomo == "" {
		t.Skip("set MPX_TEST_SINGBOX and MPX_TEST_MIHOMO for official-client proxy traffic checks")
	}
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "UC03-proxy-ok") }))
	defer target.Close()
	managedPort, externalPort := sourceRuntimePort(t), sourceRuntimePort(t)
	uview := e.createUser("runtime")
	user, err := e.st.GetUser("runtime")
	if err != nil {
		t.Fatal(err)
	}
	n := e.createNode("managed", "127.0.0.1")
	in := model.Inbound{NodeID: n.ID, Tag: "vless", Protocol: model.ProtocolVLESS, Port: managedPort, Network: model.NetworkTCP, Security: model.SecurityNone}
	if code, data := e.api("POST", "/api/inbounds", in); code != 201 {
		t.Fatalf("managed inbound: %d %s", code, data)
	}
	serverConfig := fmt.Sprintf(`{"log":{"level":"error"},"inbounds":[{"type":"vless","listen":"127.0.0.1","listen_port":%d,"users":[{"uuid":%q}]},{"type":"shadowsocks","listen":"127.0.0.1","listen_port":%d,"method":"aes-128-gcm","password":"runtime-test-password"}],"outbounds":[{"type":"direct"}]}`, managedPort, model.DeriveUUID(user.Key), externalPort)
	serverFile := filepath.Join(t.TempDir(), "server.json")
	if err := os.WriteFile(serverFile, []byte(serverConfig), 0600); err != nil {
		t.Fatal(err)
	}
	stopServer := sourceRuntimeProcess(t, singbox, []string{"run", "-c", serverFile}, managedPort)
	defer stopServer()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "proxies:\n  - name: external-ss\n    type: ss\n    server: 127.0.0.1\n    port: %d\n    cipher: aes-128-gcm\n    password: runtime-test-password\n", externalPort)
	}))
	defer upstream.Close()
	src := addTestSource(t, e, upstream.URL)
	if src.NodeCount != 1 || src.LastError != "" {
		t.Fatalf("source: %#v", src)
	}
	if err := e.st.SetSourceGrant(src.ID, "runtime", true); err != nil {
		t.Fatal(err)
	}
	group := &model.ProxyGroup{Name: "runtime-group", Mode: model.GroupManual}
	if err := e.st.CreateProxyGroup(group); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SetRouteFinal(0, model.GroupTarget(group.ID)); err != nil {
		t.Fatal(err)
	}
	for _, client := range []struct {
		name, bin string
		format    model.ClientFormat
	}{{"sing-box", singbox, model.FormatSingBox}, {"mihomo", mihomo, model.FormatClashMeta}} {
		for _, origin := range []string{"managed", model.SourceOrigin(src.ID)} {
			t.Run(client.name+"/"+origin, func(t *testing.T) {
				port := sourceRuntimePort(t)
				settings, err := e.st.GetSettings()
				if err != nil {
					t.Fatal(err)
				}
				template := fmt.Sprintf(`{"log":{"level":"error"},"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[],"route":{}}`, port)
				if client.format == model.FormatClashMeta {
					template = fmt.Sprintf("mixed-port: %d\nbind-address: 127.0.0.1\nmode: rule\nlog-level: error\nrule-providers: {}\n", port)
				}
				settings.Templates = map[string]string{string(client.format): template}
				if err := e.st.SaveSettings(settings); err != nil {
					t.Fatal(err)
				}
				if code, data := e.api("PUT", fmt.Sprintf("/api/proxy-groups/%d/sources", group.ID), map[string]any{"sources": []string{origin}}); code != 200 {
					t.Fatalf("source filter: %d %s", code, data)
				}
				code, _, body := e.sub("/sub/"+uview.SubToken+"/"+string(client.format), "")
				if code != 200 {
					t.Fatalf("subscription: %d %s", code, body)
				}
				dir := t.TempDir()
				file := filepath.Join(dir, "config")
				if err := os.WriteFile(file, body, 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{"check", "-c", file}
				if client.format == model.FormatClashMeta {
					args = []string{"-t", "-d", dir, "-f", file}
				}
				if out, err := exec.Command(client.bin, args...).CombinedOutput(); err != nil {
					t.Fatalf("official config check: %v\n%s", err, out)
				}
				args = []string{"run", "-c", file}
				if client.format == model.FormatClashMeta {
					args = []string{"-d", dir, "-f", file}
				}
				stop := sourceRuntimeProcess(t, client.bin, args, port)
				defer stop()
				dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, &net.Dialer{Timeout: 5 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				tr := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					return dialer.(proxy.ContextDialer).DialContext(ctx, network, address)
				}}
				defer tr.CloseIdleConnections()
				httpClient := &http.Client{Transport: tr, Timeout: 10 * time.Second}
				resp, err := httpClient.Get(target.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				got, err := io.ReadAll(resp.Body)
				if err != nil || string(got) != "UC03-proxy-ok" {
					t.Fatalf("proxy traffic: %q %v", got, err)
				}
			})
		}
	}
}
