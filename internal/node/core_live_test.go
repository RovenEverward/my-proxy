package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// Opt-in real Linux/systemd node. The caller supplies an isolated SSH target;
// this test installs and restarts mpx-sing-box on that target only.
func TestLiveSingBoxSSHSystemd(t *testing.T) {
	remote := os.Getenv("MPX_SINGBOX_LIVE_NODE")
	if remote == "" {
		t.Skip("set MPX_SINGBOX_LIVE_NODE=host:port, MPX_SINGBOX_LIVE_KEY, MPX_SINGBOX_LIVE_BINARY, MPX_SINGBOX_LIVE_CLIENT for an isolated Linux systemd node")
	}
	host, portText, err := net.SplitHostPort(remote)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	bin, client, key := os.Getenv("MPX_SINGBOX_LIVE_BINARY"), os.Getenv("MPX_SINGBOX_LIVE_CLIENT"), os.Getenv("MPX_SINGBOX_LIVE_KEY")
	if bin == "" || client == "" || key == "" {
		t.Fatal("isolated binaries and SSH identity required")
	}
	originalSSH, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wrapper := fmt.Sprintf("#!/bin/sh\nexec %s -F /dev/null -i %s -o IdentitiesOnly=yes -o UserKnownHostsFile=%s -o ControlMaster=no -o ControlPath=none \"$@\"\n", shellQuote(originalSSH), shellQuote(key), shellQuote(filepath.Join(dir, "known_hosts")))
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	n := model.Node{ID: 9300, Name: "isolated-quic-live", Address: host, SSHPort: port, SSHUser: "root"}
	d := model.CoreDeployment{NodeID: n.ID, Core: model.CoreSingBox, APIPort: 19386}
	r := SSH{}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	if _, err := r.Run(ctx, n, "systemctl is-active ssh"); err != nil {
		t.Fatal(err)
	}
	if err := InstallSingBox(ctx, r, n, d, bin); err != nil {
		t.Fatal(err)
	}
	version, err := SingBoxVersion(ctx, r, n)
	if err != nil || version != "1.14.3" {
		t.Fatalf("installed version %q: %v", version, err)
	}
	t.Logf("real SSH installed sing-box %s with QUIC+Stats tags", version)
	cert, keyFile := selfSignedCert(t, dir)
	for src, dest := range map[string]string{cert: "/etc/mpx/sing-box/live-cert.pem", keyFile: "/etc/mpx/sing-box/live-key.pem"} {
		body, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := PutFile(ctx, r, n, dest, body); err != nil {
			t.Fatal(err)
		}
	}
	// Echo stays alive across the QUIC server restart so closure proves revocation.
	echo := `import socket,threading
s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('127.0.0.1',19390));s.listen()
def worker(c):
 try:
  while True:
   b=c.recv(65536)
   if not b:break
   c.sendall(b)
 finally:c.close()
def udp():
 u=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);u.bind(('127.0.0.1',19391))
 while True:
  b,a=u.recvfrom(65536);u.sendto(b,a)
threading.Thread(target=udp,daemon=True).start()
while True:
 c,a=s.accept();threading.Thread(target=worker,args=(c,),daemon=True).start()
`
	if err := PutFile(ctx, r, n, "/tmp/mpx-quic-echo.py", []byte(echo)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, n, "nohup python3 /tmp/mpx-quic-echo.py >/tmp/mpx-quic-echo.log 2>&1 </dev/null & echo $! >/tmp/mpx-quic-echo.pid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.Run(context.Background(), n, "if test -f /tmp/mpx-quic-echo.pid; then kill \"$(cat /tmp/mpx-quic-echo.pid)\"; fi")
	})
	users := []model.User{activeUser("hy-alice"), activeUser("tuic-bob")}
	users[0].InboundIDs = []int64{9301}
	users[1].InboundIDs = []int64{9302}
	inbounds := []model.Inbound{
		{ID: 9301, NodeID: n.ID, Tag: "live-hy", Protocol: model.ProtocolHysteria2, Network: model.NetworkUDP, Security: model.SecurityTLS, Port: 19301, SNI: "node.example.com", CertFile: "/etc/mpx/sing-box/live-cert.pem", KeyFile: "/etc/mpx/sing-box/live-key.pem"},
		{ID: 9302, NodeID: n.ID, Tag: "live-tuic", Protocol: model.ProtocolTUIC, Network: model.NetworkUDP, Security: model.SecurityTLS, Port: 19302, SNI: "node.example.com", CertFile: "/etc/mpx/sing-box/live-cert.pem", KeyFile: "/etc/mpx/sing-box/live-key.pem"},
	}
	cfg := liveQUICConfig(t, n, d, inbounds, users)
	if err := DeploySingBox(ctx, r, n, d, cfg, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.Run(context.Background(), n, "systemctl stop mpx-sing-box") })
	var conns []net.Conn
	for i, in := range inbounds {
		socks := 19331 + i
		startSingBoxClient(t, client, dir, socks, host, in, users[i])
		c := singBoxSocksConnect(t, socks, 19390)
		conns = append(conns, c)
		defer c.Close()
		payload := strings.Repeat(string(rune('a'+i)), 16384*(i+1))
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(c, payload); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(c, got); err != nil {
			t.Fatal(err)
		}
		if string(got) != payload {
			t.Fatal("QUIC echo mismatch")
		}
	}
	traffic, err := QuerySingBoxTraffic(ctx, r, n, d)
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range users {
		got := traffic[u.Username]
		want := int64(16384 * (i + 1))
		if got.Uplink != want || got.Downlink != want {
			t.Fatalf("per-user real traffic %s=%+v want each=%d; all=%+v", u.Username, got, want, traffic)
		}
	}
	t.Logf("real SSH -W StatsService per-user TCP while sessions open: %+v", traffic)
	for i := range users {
		singBoxUDPExchange(t, 19331+i, 19391, 1024*(i+1))
	}
	traffic, err = QuerySingBoxTraffic(ctx, r, n, d)
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range users {
		want := int64((16384 + 1024) * (i + 1))
		got := traffic[u.Username]
		if got.Uplink != want || got.Downlink != want {
			t.Fatalf("UDP per-user traffic %s=%+v expected each=%d", u.Username, got, want)
		}
	}
	t.Logf("TCP plus UDP payload counts through both QUIC protocols: %+v", traffic)
	next := liveQUICConfig(t, n, d, inbounds, nil)
	before, after := false, false
	err = DeploySingBox(ctx, r, n, d, next, func() error {
		before = true
		sample, err := QuerySingBoxTraffic(ctx, r, n, d)
		if err != nil {
			return err
		}
		if sample[users[0].Username].Uplink == 0 {
			return fmt.Errorf("pre-stop final counter disappeared")
		}
		return nil
	}, func() error {
		after = true
		out, err := r.Run(ctx, n, "systemctl is-active mpx-sing-box || true")
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) != "inactive" {
			return fmt.Errorf("afterStop service=%q", out)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !before || !after {
		t.Fatal("lifecycle callbacks missing")
	}
	for _, c := range conns {
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = c.Write([]byte("revoked"))
		if _, err := io.ReadFull(c, make([]byte, 7)); err == nil {
			t.Fatal("revoked active QUIC session still forwarded data")
		}
	}
	reset, err := QuerySingBoxTraffic(ctx, r, n, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(reset) != 0 {
		t.Fatalf("stats did not reset: %+v", reset)
	}
	// The clients keep running, so this forces authentication against empty users.
	for i := range users {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", 19331+i), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		err = singBoxSocksHandshake(c, 19390)
		if err == nil {
			_, _ = c.Write([]byte("revoked"))
			_, err = io.ReadFull(c, make([]byte, 7))
		}
		_ = c.Close()
		if err == nil {
			t.Fatal("removed user authenticated on fresh QUIC connection")
		}
	}
	t.Log("systemd stop/start callbacks ran, both active QUIC sessions closed, empty-auth config rejects reconnect, StatsService counters reset")
}

func liveQUICConfig(t *testing.T, n model.Node, d model.CoreDeployment, in []model.Inbound, users []model.User) []byte {
	t.Helper()
	cfg, err := render.SingBoxServerConfig(n, d, in, users, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Local echo traffic is only allowed by this test fixture.
	var value map[string]any
	if err := json.Unmarshal(cfg, &value); err != nil {
		t.Fatal(err)
	}
	value["route"] = map[string]any{"final": "direct"}
	cfg, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func startSingBoxClient(t *testing.T, bin, dir string, socks int, host string, in model.Inbound, u model.User) {
	t.Helper()
	outbound := map[string]any{"type": string(in.Protocol), "server": host, "server_port": in.Port, "password": model.DerivePassword(u.Key), "tls": map[string]any{"enabled": true, "server_name": "node.example.com", "insecure": true}}
	if in.Protocol == model.ProtocolTUIC {
		outbound["uuid"] = model.DeriveUUID(u.Key)
		outbound["congestion_control"] = "cubic"
	}
	cfg := map[string]any{"log": map[string]any{"level": "warn"}, "inbounds": []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": socks}}, "outbounds": []any{outbound}}
	body, _ := json.Marshal(cfg)
	file := filepath.Join(dir, fmt.Sprintf("client-%d.json", socks))
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(dir, fmt.Sprintf("client-%d.log", socks)))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", file)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = logFile.Close() })
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socks), 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("client did not listen on %d", socks)
}

func singBoxSocksConnect(t *testing.T, socks, target int) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socks), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if err := singBoxSocksHandshake(c, target); err != nil {
		_ = c.Close()
		t.Fatal(err)
	}
	return c
}
func singBoxUDPExchange(t *testing.T, socks, target, size int) {
	t.Helper()
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socks), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = control.Write([]byte{5, 1, 0})
	if err != nil {
		t.Fatal(err)
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(control, greeting); err != nil {
		t.Fatal(err)
	}
	_, err = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(control, head); err != nil {
		t.Fatal(err)
	}
	if head[1] != 0 || head[3] != 1 {
		t.Fatalf("UDP associate response: %v", head)
	}
	address := make([]byte, 6)
	if _, err := io.ReadFull(control, address); err != nil {
		t.Fatal(err)
	}
	relayPort := int(address[4])<<8 | int(address[5])
	udp, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: relayPort})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(5 * time.Second))
	payload := []byte(strings.Repeat("u", size))
	packet := append([]byte{0, 0, 0, 1, 127, 0, 0, 1, byte(target >> 8), byte(target)}, payload...)
	if _, err := udp.Write(packet); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, size+256)
	n, err := udp.Read(response)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(packet) || string(response[10:n]) != string(payload) {
		t.Fatalf("UDP echo mismatch len=%d expected=%d", n, len(packet))
	}
}

func singBoxSocksHandshake(c net.Conn, target int) error {
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(c, response); err != nil {
		return err
	}
	if response[1] != 0 {
		return fmt.Errorf("socks auth rejected: %v", response)
	}
	if _, err := c.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, byte(target >> 8), byte(target)}); err != nil {
		return err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		return err
	}
	if head[1] != 0 {
		return fmt.Errorf("socks connect rejected: %v", head)
	}
	size := 0
	switch head[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(c, length[:]); err != nil {
			return err
		}
		size = int(length[0])
	default:
		return fmt.Errorf("socks address type %d", head[3])
	}
	_, err := io.ReadFull(c, make([]byte, size+2))
	return err
}
