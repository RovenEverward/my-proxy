package node

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// 这一层只负责「对节点说什么」,它说出的每句话最终都落到真实 xray 身上。所以在有
// 真实 xray 二进制可用时,把话原样对着它说一遍 —— 这是唯一能证明
// `xray api adu/rmu/inbounduser/statsquery` 的用法与输出解析正确的方式。
//
// 没有二进制就跳过(设 MPX_XRAY_BIN,或把 xray 放进 PATH)。

func liveXray(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("MPX_XRAY_BIN"); p != "" {
		return p
	}
	p, err := exec.LookPath("xray")
	if err != nil {
		t.Skip("本机没有 xray:设 MPX_XRAY_BIN,或把 xray 放进 PATH 后重跑")
	}
	return p
}

// localRunner 把 Runner 的脚本直接在本机 shell 里跑,不经过 ssh。
//
// Runner 抽象本来就说了「换成别的实现」,这就是那个实现:它让整层节点操作可以对着
// 真实 xray 验证,而不必先有一台能 ssh 的机器。
type localRunner struct {
	binDir string
}

func (l localRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	return l.RunWithInput(ctx, n, script, nil)
}

func (l localRunner) RunWithInput(ctx context.Context, n model.Node, script string, stdin []byte) (string, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+l.binDir+":"+os.Getenv("PATH"))
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("本机执行失败: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func liveNode(t *testing.T, dir string, apiPort int) model.Node {
	t.Helper()
	return model.Node{
		Name:       "local",
		Address:    "127.0.0.1",
		APIPort:    apiPort,
		ConfigPath: filepath.Join(dir, "config.json"),
	}
}

func activeUser(name string) model.User {
	return model.User{Username: name, Key: "key-" + name, Enabled: true, CreatedAt: time.Now()}
}

// selfSignedCert 生成一张自签证书:渲染出的 tls 入站指向真实存在的文件,xray 才
// 会在 -test 时去读它们(证书不存在也算配置错误)。
func selfSignedCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成私钥: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "node.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"node.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发证书: %v", err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("编码私钥: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("写证书: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("写私钥: %v", err)
	}
	return certFile, keyFile
}

// 渲染出的每一种协议 / 传输 / 安全层组合都要能被真实 xray 接受。
//
// 这一条挡住的是一整类事故:字段名拼错、枚举值不属于这个版本、某个键在这个版本
// 已经改名 —— 单测里我们自己渲染自己解析,永远发现不了。
func TestLiveServerConfigAcceptedByXray(t *testing.T) {
	bin := liveXray(t)
	dir := t.TempDir()
	certFile, keyFile := selfSignedCert(t, dir)

	realityPriv, realityPub, err := model.GenerateRealityKeypair()
	if err != nil {
		t.Fatalf("生成 REALITY 密钥对: %v", err)
	}

	users := []model.User{activeUser("alice"), activeUser("bob")}
	tls := func(in *model.Inbound) {
		in.Security = model.SecurityTLS
		in.SNI = "node.example.com"
		in.CertFile = certFile
		in.KeyFile = keyFile
	}

	inbounds := []model.Inbound{
		{ID: 1, Tag: "vless-tcp-none", Protocol: model.ProtocolVLESS, Port: freePort(t), Network: model.NetworkTCP, Security: model.SecurityNone},
		{ID: 2, Tag: "vless-ws-tls", Protocol: model.ProtocolVLESS, Port: freePort(t), Network: model.NetworkWS, WSPath: "/ws", ALPN: []string{"h2", "http/1.1"}},
		{ID: 3, Tag: "vless-grpc-tls", Protocol: model.ProtocolVLESS, Port: freePort(t), Network: model.NetworkGRPC, GRPCService: "grpcsvc"},
		{ID: 4, Tag: "vless-tcp-reality", Protocol: model.ProtocolVLESS, Port: freePort(t), Network: model.NetworkTCP, Security: model.SecurityReality,
			SNI: "www.microsoft.com", RealityDest: "www.microsoft.com:443", RealityPriv: realityPriv, RealityPub: realityPub, ShortIDs: []string{"d4834461"}},
		{ID: 5, Tag: "vless-tcp-tls-vision", Protocol: model.ProtocolVLESS, Port: freePort(t), Network: model.NetworkTCP, Flow: "xtls-rprx-vision"},
		{ID: 6, Tag: "vmess-ws-tls", Protocol: model.ProtocolVMess, Port: freePort(t), Network: model.NetworkWS, WSPath: "/vm"},
		{ID: 7, Tag: "trojan-tcp-tls", Protocol: model.ProtocolTrojan, Port: freePort(t), Network: model.NetworkTCP},
		{ID: 8, Tag: "trojan-ws-none", Protocol: model.ProtocolTrojan, Port: freePort(t), Network: model.NetworkWS, WSPath: "/tj", Security: model.SecurityNone},
	}
	for i := range inbounds {
		switch inbounds[i].Tag {
		case "vless-ws-tls", "vless-grpc-tls", "vless-tcp-tls-vision", "vmess-ws-tls", "trojan-tcp-tls":
			tls(&inbounds[i])
		}
	}

	node := liveNode(t, dir, freePort(t))
	cfg, err := render.ServerConfig(node, inbounds, users, time.Now())
	if err != nil {
		t.Fatalf("渲染节点配置: %v", err)
	}
	if err := os.WriteFile(node.ConfigPath, cfg, 0o600); err != nil {
		t.Fatalf("写节点配置: %v", err)
	}

	// 与 deploy.go 用同一条命令:xray 的默认子命令就是 run,所以 `-test` 能直接跟在
	// 二进制后面。坏配置的退出码是 23(实测),deploy 正是靠它决定要不要回滚。
	out, err := exec.Command(bin, "-test", "-config", node.ConfigPath).CombinedOutput()
	if err != nil {
		t.Fatalf("真实 xray 不接受我们渲染的配置: %v\n%s\n--- 配置 ---\n%s", err, out, cfg)
	}
	if !bytes.Contains(out, []byte("Configuration OK.")) {
		t.Fatalf("xray 退出码是 0 却没报告配置正常:\n%s", out)
	}
}

// 起一个真实 xray,把增删用户、读名单、读流量四件事按控制面的真实顺序做一遍。
func TestLiveXrayAPIContract(t *testing.T) {
	bin := liveXray(t)
	dir := t.TempDir()
	runner := localRunner{binDir: filepath.Dir(bin)}

	apiPort := freePort(t)
	node := liveNode(t, dir, apiPort)

	alice := activeUser("alice")
	in := model.Inbound{
		ID: 1, Tag: "vless-live", Protocol: model.ProtocolVLESS,
		Listen: "127.0.0.1", Port: freePort(t),
		Network: model.NetworkTCP, Security: model.SecurityNone,
	}

	cfg, err := render.ServerConfig(node, []model.Inbound{in}, []model.User{alice}, time.Now())
	if err != nil {
		t.Fatalf("渲染节点配置: %v", err)
	}
	// 生产配置里的 `geoip:private → block` 会把本机源站(127.0.0.1)黑洞掉 —— 这是
	// 该有的行为,它挡住的是客户端借节点打节点自己的内网。为了让这个测试的源站可达,
	// 在这里插一条优先级更高的直连规则。渲染本身已由
	// TestLiveServerConfigAcceptedByXray 与 internal/server 的用例钉住。
	cfg = allowLoopbackToDirect(t, cfg)
	if err := os.WriteFile(node.ConfigPath, cfg, 0o600); err != nil {
		t.Fatalf("写节点配置: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	proc := startLiveXray(t, bin, node.ConfigPath)
	defer stopLiveXray(t, proc)
	waitPort(t, apiPort)

	// ① 读节点现状:配置里带着的用户应当就在那里。
	if got := inboundUsers(t, ctx, runner, node, in.Tag); !equalNames(got, "alice") {
		t.Fatalf("初始名单应当是 [alice],实际 %v", got)
	}

	// ② 增量加一个用户,不重启 —— 这是配额恢复、续费走的路径。
	bob := activeUser("bob")
	if err := AddUser(ctx, runner, node, in, bob, time.Now()); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if got := inboundUsers(t, ctx, runner, node, in.Tag); !equalNames(got, "alice", "bob") {
		t.Fatalf("加完 bob 后名单应当是 [alice bob],实际 %v", got)
	}

	// ③ 让 bob 真的连上去跑一趟流量,再读计数。这一步才证明「渲染出的服务端配置
	// 能用」和「流量归到按用户的计数器上」两件事同时成立。
	socksPort := freePort(t)
	payload := strings.Repeat("mpx-live-", 200)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, payload)
	}))
	defer origin.Close()
	startLiveClient(t, bin, dir, socksPort, in.Port, model.DeriveUUID(bob.Key))
	waitPort(t, socksPort)

	body := curlThroughSocks(t, socksPort, origin.URL)
	if body != payload {
		t.Fatalf("经代理取回的正文不对: 长度 %d,前 40 字节 %q", len(body), firstN(body, 40))
	}

	up, down := waitTraffic(t, ctx, runner, node, "bob")
	if up <= 0 || down <= 0 {
		t.Fatalf("bob 的流量计数应当上下行都大于 0,实际 up=%d down=%d", up, down)
	}
	t.Logf("真实 xray 上读到 bob 的计数: uplink=%d downlink=%d", up, down)

	// ④ 读走即清零:控制面按周期取增量累加,第二个周期不该把同一段流量再算一次。
	up2, down2 := liveTraffic(t, ctx, runner, node, "bob")
	if up2 != 0 || down2 != 0 {
		t.Fatalf("采样应当把计数清零,第二次却读到 up=%d down=%d", up2, down2)
	}

	// ⑤ 摘掉用户(到期、超限走的路径)。
	if err := RemoveUser(ctx, runner, node, in.Tag, "alice"); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}
	if got := inboundUsers(t, ctx, runner, node, in.Tag); !equalNames(got, "bob") {
		t.Fatalf("摘掉 alice 后名单应当是 [bob],实际 %v", got)
	}

	// ⑥ adu 的输入片段必须独立成立:它只含一个入站、一个用户。若它渲染的入站缺少
	// xray 需要的字段,上面那步就会失败;这里再确认一次片段本身能被解出来。
	frag, err := render.InboundAddUserConfig(in, bob, time.Now())
	if err != nil {
		t.Fatalf("渲染 adu 片段: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(frag, &probe); err != nil {
		t.Fatalf("adu 片段不是合法 JSON: %v", err)
	}
}

// allowLoopbackToDirect 在渲染好的配置最前面插一条「回环地址直连」规则。
func allowLoopbackToDirect(t *testing.T, cfg []byte) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(cfg, &doc); err != nil {
		t.Fatalf("解节点配置: %v", err)
	}
	routing, _ := doc["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	allow := map[string]any{"type": "field", "ip": []string{"127.0.0.1/32"}, "outboundTag": "direct"}
	// 插在第一条之后:第一条把 api 入站的流量导回 api 出站,插到它前面会把 gRPC
	// 自己送进 direct,接口当场失联。
	rules = append([]any{rules[0], allow}, rules[1:]...)
	routing["rules"] = rules
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("重新编码节点配置: %v", err)
	}
	return out
}

func startLiveXray(t *testing.T, bin, cfgPath string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin, "run", "-config", cfgPath)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 xray: %v", err)
	}
	// 收尾必须在动日志之前等进程和它的输出拷贝协程都结束:exec 会在后台把
	// stdout 拷进 log,这边同时去读就是一场数据竞争(-race 会直接报出来)。
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
			t.Logf("xray 退出异常(%v),输出:\n%s", cmd.ProcessState, log.String())
		}
	})
	return cmd
}

func stopLiveXray(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	// 用 cmd.Wait 而不是 cmd.Process.Wait:前者会等到 stdout 拷贝完成,后者不会。
	_ = cmd.Wait()
}

// startLiveClient 起一个客户端 xray:socks 入站 → vless 出站,用来产生真实流量。
func startLiveClient(t *testing.T, bin, dir string, socksPort, serverPort int, uuid string) {
	t.Helper()
	clientCfg := fmt.Sprintf(`{
  "log": {"loglevel": "warning"},
  "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": %d, "protocol": "socks", "settings": {"auth": "noauth"}}],
  "outbounds": [{
    "tag": "proxy", "protocol": "vless",
    "settings": {"vnext": [{"address": "127.0.0.1", "port": %d, "users": [{"id": %q, "encryption": "none"}]}]},
    "streamSettings": {"network": "tcp", "security": "none"}
  }]
}`, socksPort, serverPort, uuid)

	cfgPath := filepath.Join(dir, "client.json")
	if err := os.WriteFile(cfgPath, []byte(clientCfg), 0o600); err != nil {
		t.Fatalf("写客户端配置: %v", err)
	}
	cmd := exec.Command(bin, "run", "-config", cfgPath)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动客户端 xray: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		// 同 startLiveXray:先等输出拷贝结束,再读 log。
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("客户端 xray 输出:\n%s", log.String())
		}
	})
}

func waitPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("端口 %d 一直没有开始监听", port)
}

func curlThroughSocks(t *testing.T, socksPort int, url string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "curl", "-sS", "--socks5", fmt.Sprintf("127.0.0.1:%d", socksPort), url)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("经 socks 取 %s 失败: %v: %s", url, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String()
}

func inboundUsers(t *testing.T, ctx context.Context, r Runner, n model.Node, tag string) []string {
	t.Helper()
	got, err := InboundUsers(ctx, r, n, tag)
	if err != nil {
		t.Fatalf("InboundUsers(%s): %v", tag, err)
	}
	return got
}

func liveTraffic(t *testing.T, ctx context.Context, r Runner, n model.Node, name string) (int64, int64) {
	t.Helper()
	all, err := QueryUserTraffic(ctx, r, n)
	if err != nil {
		t.Fatalf("QueryUserTraffic: %v", err)
	}
	tr := all[name]
	return tr.Uplink, tr.Downlink
}

// waitTraffic 等计数出现:xray 是异步累计的,连接刚断开时可能还没落账。
func waitTraffic(t *testing.T, ctx context.Context, r Runner, n model.Node, name string) (int64, int64) {
	t.Helper()
	var up, down int64
	for i := 0; i < 20; i++ {
		up, down = liveTraffic(t, ctx, r, n, name)
		if up > 0 || down > 0 {
			return up, down
		}
		time.Sleep(200 * time.Millisecond)
	}
	return up, down
}

func equalNames(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			return false
		}
		seen[w]--
	}
	return true
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
