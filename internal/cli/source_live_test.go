package cli

// 真实二进制集成冒烟:UC-03 的端到端验收。
//
// 不走任何 mock:编译 mpx 真二进制、起真控制面(mpx serve + 临时 SQLite)、
// 起可控 HTTP 上游,然后只经 CLI 与 /api、/sub 说话 —— 和运维人员的用法一致。
// 契约未实现的期间这些断言保持红:红就是「还没做完」,不是「测错了」。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// liveEnv 是一次冒烟的全部家当。
type liveEnv struct {
	t           *testing.T
	mpx         string        // 编译出来的 mpx 二进制
	base        string        // 控制面地址,如 http://127.0.0.1:12345
	token       string        // 管理 token
	home        string        // 干净的 HOME(隔离真实 ~/.config/mpx)
	upstream    *atomic.Value // 上游应答正文;换值即换"上游世界"
	upstreamURL string        // 带 secret 的上游订阅地址
}

const liveToken = "smoke-admin-token"

// buildMPX 把 ./cmd/mpx 编成一份真二进制。
func buildMPX(t *testing.T) string {
	t.Helper()
	name := "mpx"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/mpx")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 mpx: %v\n%s", err, out)
	}
	return bin
}

// freePort 抢一个回环端口。先占后放有一小段竞争窗,测试环境可接受。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("抢端口: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startLive 编译二进制、起上游与控制面,返回就绪的环境。
func startLive(t *testing.T) *liveEnv {
	t.Helper()
	e := &liveEnv{t: t, mpx: buildMPX(t), token: liveToken, home: t.TempDir()}

	// 上游:第一次给两个外部节点的 clash-meta YAML,之后测试里换成 500。
	e.upstream = &atomic.Value{}
	e.upstream.Store(upstreamClashMeta)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := e.upstream.Load().(string)
		if body == "__FAIL__" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("upstream exploded"))
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(up.Close)
	e.upstreamURL = up.URL + "/clash-meta?secret=rawsecret"

	work := t.TempDir()
	port := freePort(t)
	e.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	db := filepath.Join(work, "mpx.db")

	serve := exec.Command(e.mpx, "serve",
		"--addr", fmt.Sprintf("127.0.0.1:%d", port),
		"--db", db,
		"--token", e.token,
		"--auto-locate=false",
		"--interval", "1h") // 收敛与采样别来搅场:本测试不验证它们
	serve.Env = append(os.Environ(),
		"HOME="+e.home,
		"MPX_AUTO_LOCATE=0",
		"MPX_SERVER=", "MPX_TOKEN=")
	var serveLog bytes.Buffer
	serve.Stdout, serve.Stderr = &serveLog, &serveLog
	if err := serve.Start(); err != nil {
		t.Fatalf("启动 mpx serve: %v", err)
	}
	t.Cleanup(func() {
		_ = serve.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- serve.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = serve.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("mpx serve 输出:\n%s", serveLog.String())
		}
	})

	// 等服务肯答管理接口(带 token 的 200),而不是只起了 socket。
	// 这里不能走 e.api:它对连不上是 Fatal,而启动期连不上正是常态。
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest("GET", e.base+"/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+e.token)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			code := resp.StatusCode
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if code == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("控制面 20 秒内没就绪,日志:\n%s", serveLog.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	return e
}

// api 对控制面发一次管理请求,返回状态码与正文。
func (e *liveEnv) api(method, path, jsonBody string) (int, []byte) {
	e.t.Helper()
	var reader io.Reader
	if jsonBody != "" {
		reader = strings.NewReader(jsonBody)
	}
	req, err := http.NewRequest(method, e.base+path, reader)
	if err != nil {
		e.t.Fatalf("拼请求: %v", err)
	}
	if jsonBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// apiOK 发请求并断言 2xx,返回正文。
func (e *liveEnv) apiOK(method, path, jsonBody string) []byte {
	e.t.Helper()
	code, body := e.api(method, path, jsonBody)
	if code < 200 || code >= 300 {
		e.t.Fatalf("%s %s -> %d: %s", method, path, code, body)
	}
	return body
}

// run 跑一次真二进制 CLI,返回退出码与输出。stdin 喂订阅地址等交互输入。
func (e *liveEnv) run(stdin string, args ...string) (int, string, string) {
	e.t.Helper()
	cmd := exec.Command(e.mpx, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+e.home,
		"MPX_SERVER="+e.base,
		"MPX_TOKEN="+e.token)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatalf("跑 mpx %v: %v", args, err)
	}
	return code, stdout.String(), stderr.String()
}

// runOK 跑命令并断言退出码为 0。
func (e *liveEnv) runOK(stdin string, args ...string) string {
	e.t.Helper()
	code, out, errOut := e.run(stdin, args...)
	if code != 0 {
		e.t.Fatalf("mpx %v 退出码 %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	return out
}

// runFail 跑命令并断言非零退出。
func (e *liveEnv) runFail(stdin string, args ...string) (string, string) {
	e.t.Helper()
	code, out, errOut := e.run(stdin, args...)
	if code == 0 {
		e.t.Fatalf("mpx %v 预期失败但退出了 0\nstdout: %s", args, out)
	}
	return out, errOut
}

// subscription 拉一次某用户的某格式订阅,断言 200 并返回正文。
func (e *liveEnv) subscription(subToken, format string) string {
	e.t.Helper()
	req, err := http.NewRequest("GET", e.base+"/sub/"+subToken+"/"+format, nil)
	if err != nil {
		e.t.Fatalf("拼订阅请求: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("拉订阅: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET /sub/.../%s -> %d: %s", format, resp.StatusCode, data)
	}
	return string(data)
}

// subTokenFor 取用户的订阅 token。先查用户详情,查不到就从 links 里拆。
func (e *liveEnv) subTokenFor(username string) string {
	e.t.Helper()
	body := e.apiOK("GET", "/api/users/"+username, "")
	var u struct {
		SubToken string `json:"sub_token"`
	}
	if json.Unmarshal(body, &u) == nil && u.SubToken != "" {
		return u.SubToken
	}
	body = e.apiOK("GET", "/api/users/"+username+"/links", "")
	var links map[string]string
	if err := json.Unmarshal(body, &links); err != nil {
		e.t.Fatalf("取不到 %s 的订阅 token(详情与 links 都没有)", username)
	}
	for _, v := range links {
		if i := strings.Index(v, "/sub/"); i >= 0 {
			tok := v[i+len("/sub/"):]
			if j := strings.IndexByte(tok, '/'); j >= 0 {
				tok = tok[:j]
			}
			if tok != "" {
				return tok
			}
		}
	}
	e.t.Fatalf("links 里没有 /sub/ 地址: %v", links)
	return ""
}

// singBoxTags 解出 sing-box 订阅里所有 outbound 的 tag。
func singBoxTags(t *testing.T, body string) []string {
	t.Helper()
	var cfg struct {
		Outbounds []struct {
			Tag       string   `json:"tag"`
			Type      string   `json:"type"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("sing-box 订阅不是合法 JSON: %v\n%s", err, body)
	}
	var tags []string
	for _, o := range cfg.Outbounds {
		tags = append(tags, o.Tag)
	}
	return tags
}

// singBoxGroupMembers 找出名为 tag 的组出站引用的成员列表。
func singBoxGroupMembers(t *testing.T, body, tag string) []string {
	t.Helper()
	var cfg struct {
		Outbounds []struct {
			Tag       string   `json:"tag"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("sing-box 订阅不是合法 JSON: %v", err)
	}
	for _, o := range cfg.Outbounds {
		if o.Tag == tag {
			return o.Outbounds
		}
	}
	return nil
}

// upstreamClashMeta 是上游第一次应答:两个 SS 外部节点。
// 用两条 SS 而不是 SS+Trojan:Trojan 不带 TLS 会被兼容性检查忠实跳过,
// 而冒烟要的是「两条身份都进得了全部格式」。
const upstreamClashMeta = `proxies:
  - name: ext-jp-tokyo
    type: ss
    server: 198.51.100.5
    port: 8388
    cipher: aes-256-gcm
    password: ss-secret
  - name: ext-sg
    type: ss
    server: 198.51.100.9
    port: 8488
    cipher: chacha20-ietf-poly1305
    password: sg-secret
`

// TestSourceLifecycleEndToEnd 是 UC-03 的真实冒烟:
// 真实二进制 + 真实控制面 + 可控上游,把「加来源 → 授权 → 混合订阅 → 失败保留
// → 撤权 → 删除」整条链走一遍,顺带钉住旧 PUT 不清新关系、详情不含凭据、
// 两个外部节点在 sing-box 里身份独立、策略组来源条件生效。
func TestSourceLifecycleEndToEnd(t *testing.T) {
	e := startLive(t)

	// ---- 一台自建节点(只登记记录,不下发:本冒烟不需要真节点) ----
	nodeRaw := e.apiOK("POST", "/api/nodes", `{"name":"smoke-node","address":"203.0.113.10"}`)
	var node struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(nodeRaw, &node); err != nil || node.ID == 0 {
		t.Fatalf("建节点响应里没有 id: %s", nodeRaw)
	}
	inRaw := e.apiOK("POST", "/api/inbounds",
		fmt.Sprintf(`{"node_id":%d,"protocol":"shadowsocks","port":8388,"network":"tcp","security":"none"}`, node.ID))
	var inbound struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(inRaw, &inbound); err != nil || inbound.ID == 0 {
		t.Fatalf("建入站响应里没有 id: %s", inRaw)
	}
	e.apiOK("POST", fmt.Sprintf("/api/inbounds/%d/hosts", inbound.ID), `{"remark":"自建专线"}`)

	// ---- 两个用户:alice 会被授权,bob 永远看不到外部节点 ----
	for _, name := range []string{"alice", "bob"} {
		e.apiOK("POST", "/api/users", fmt.Sprintf(`{"username":%q,"enabled":true,"reset_strategy":"none"}`, name))
	}
	aliceTok := e.subTokenFor("alice")
	bobTok := e.subTokenFor("bob")

	// 基线:只有自建节点。
	if sub := e.subscription(aliceTok, "clash-meta"); !strings.Contains(sub, "自建专线") {
		t.Fatalf("基线订阅里没有自建节点:\n%s", sub)
	}

	// ---- 用 CLI 加来源:URL 从 stdin 进,等待首次拉取 ----
	e.runOK(e.upstreamURL+"\n", "source", "add", "测试源", "--url-stdin", "--wait=true")

	listOut := e.runOK("", "source", "list")
	if !strings.Contains(listOut, "测试源") {
		t.Fatalf("source list 里没有新来源: %s", listOut)
	}

	// 详情:两个节点、URL 遮蔽、零凭据。
	showRaw := e.runOK("", "source", "show", "测试源", "-o", "json")
	var view struct {
		ID        int64  `json:"id"`
		NodeCount int    `json:"node_count"`
		URLMasked string `json:"url_masked"`
		LastError string `json:"last_error"`
	}
	if err := json.Unmarshal([]byte(showRaw), &view); err != nil {
		t.Fatalf("source show -o json 不是合法 JSON: %v\n%s", err, showRaw)
	}
	if view.ID == 0 {
		t.Fatal("source show 没带 id")
	}
	if view.NodeCount != 2 {
		t.Fatalf("首次拉取应收进 2 个外部节点,得到 %d(show: %s)", view.NodeCount, showRaw)
	}
	for _, secret := range []string{"rawsecret", "ss-secret", "sg-secret", e.upstreamURL} {
		if strings.Contains(showRaw, secret) {
			t.Fatalf("来源详情泄露了 %q:\n%s", secret, showRaw)
		}
	}
	sid := fmt.Sprint(view.ID)

	// 未授权时双方订阅都看不到外部节点。
	for username, tok := range map[string]string{"alice": aliceTok, "bob": bobTok} {
		sub := e.subscription(tok, "clash-meta")
		if strings.Contains(sub, "ext-jp") || strings.Contains(sub, "ext-sg") {
			t.Fatalf("未授权时 %s 的订阅不该有外部节点", username)
		}
	}

	// ---- 授权 alice:重复执行幂等 ----
	e.runOK("", "source", "grant", sid, "--user", "alice")
	e.runOK("", "source", "grant", sid, "--user", "alice")
	grants := e.runOK("", "source", "grants", sid, "-o", "json")
	if !strings.Contains(grants, "alice") {
		t.Fatalf("grants 里没有 alice: %s", grants)
	}

	// ---- alice 的订阅混合两类;bob 仍然只有自建 ----
	aliceClash := e.subscription(aliceTok, "clash-meta")
	for _, want := range []string{"自建专线", "ext-jp", "ext-sg"} {
		if !strings.Contains(aliceClash, want) {
			t.Fatalf("alice 的 clash-meta 缺 %q:\n%s", want, aliceClash)
		}
	}
	bobClash := e.subscription(bobTok, "clash-meta")
	if strings.Contains(bobClash, "ext-jp") || strings.Contains(bobClash, "ext-sg") {
		t.Fatalf("bob 的订阅不该有外部节点:\n%s", bobClash)
	}

	// sing-box:两个外部节点是两个独立出站身份,不能互相覆盖;
	// 自建节点也各自独立。
	aliceSB := e.subscription(aliceTok, "sing-box")
	tags := singBoxTags(t, aliceSB)
	var extCount, managedCount int
	for _, tag := range tags {
		if strings.Contains(tag, "ext-jp") {
			extCount++
		}
		if strings.Contains(tag, "ext-sg") {
			extCount++
		}
		if strings.Contains(tag, "自建专线") {
			managedCount++
		}
	}
	if extCount != 2 {
		t.Fatalf("sing-box 里外部出站应有独立的两条身份,tag = %v", tags)
	}
	if managedCount != 1 {
		t.Fatalf("sing-box 里应有一条自建出站,tag = %v", tags)
	}
	bobSB := e.subscription(bobTok, "sing-box")
	for _, tag := range singBoxTags(t, bobSB) {
		if strings.Contains(tag, "ext-") {
			t.Fatalf("bob 的 sing-box 不该有外部出站 %q", tag)
		}
	}

	// ---- 混合策略组:来源条件走独立 API,组引用进 sing-box ----
	e.runOK("", "group", "add", "混合组", "--mode", "manual", "--source", "managed,source:"+sid)
	sourcesOut := e.runOK("", "group", "sources", "混合组")
	if !strings.Contains(sourcesOut, "managed") || !strings.Contains(sourcesOut, "source:"+sid) {
		t.Fatalf("group sources 应答不对: %s", sourcesOut)
	}
	// 策略组预览按用户:外部节点应进得了这个组。
	previewOut := e.runOK("", "group", "preview", "混合组", "--user", "alice", "--format", "sing-box")
	if !strings.Contains(previewOut, "ext-") {
		t.Fatalf("按用户预览里外部节点应被收进混合组: %s", previewOut)
	}
	// 订阅里的组引用:混合组应把外部节点列进自己的成员。
	var gid int64
	groupsRaw := e.apiOK("GET", "/api/proxy-groups", "")
	var groups []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(groupsRaw, &groups)
	for _, g := range groups {
		if g.Name == "混合组" {
			gid = g.ID
		}
	}
	if gid == 0 {
		t.Fatalf("找不到新建的混合组: %s", groupsRaw)
	}
	aliceSB2 := e.subscription(aliceTok, "sing-box")
	members := singBoxGroupMembers(t, aliceSB2, "混合组")
	var refExt, refManaged int
	for _, m := range members {
		if strings.Contains(m, "ext-") {
			refExt++
		}
		if strings.Contains(m, "自建专线") {
			refManaged++
		}
	}
	if refExt != 2 || refManaged != 1 {
		t.Fatalf("混合组应引用 2 条外部与 1 条自建,成员 = %v", members)
	}

	// ---- 旧式整组 PUT 不清来源条件;旧式用户 PUT 不清授权 ----
	groupRaw := e.apiOK("GET", fmt.Sprintf("/api/proxy-groups/%d", gid), "")
	e.apiOK("PUT", fmt.Sprintf("/api/proxy-groups/%d", gid), string(groupRaw))
	sourcesAfter := e.runOK("", "group", "sources", "混合组")
	if !strings.Contains(sourcesAfter, "source:"+sid) {
		t.Fatalf("旧整组 PUT 后来源条件丢了: %s", sourcesAfter)
	}
	userRaw := e.apiOK("GET", "/api/users/alice", "")
	var userMap map[string]any
	_ = json.Unmarshal(userRaw, &userMap)
	userMap["note"] = "旧客户端原样写回"
	userBody, _ := json.Marshal(userMap)
	e.apiOK("PUT", "/api/users/alice", string(userBody))
	if sub := e.subscription(aliceTok, "clash-meta"); !strings.Contains(sub, "ext-jp") {
		t.Fatalf("旧用户 PUT 后授权丢了:alice 的订阅缺外部节点")
	}

	// ---- 刷新失败:旧快照保留,订阅不受损 ----
	e.upstream.Store("__FAIL__")
	out, errOut := e.runFail("", "source", "refresh", sid)
	if !strings.Contains(out+errOut, "失败") && !strings.Contains(errOut, "error") {
		t.Logf("refresh 失败输出(供参考): out=%q err=%q", out, errOut)
	}
	showAfter := e.runOK("", "source", "show", sid, "-o", "json")
	var view2 struct {
		NodeCount int    `json:"node_count"`
		LastError string `json:"last_error"`
	}
	if err := json.Unmarshal([]byte(showAfter), &view2); err != nil {
		t.Fatalf("show 不是合法 JSON: %v", err)
	}
	if view2.NodeCount != 2 {
		t.Fatalf("失败刷新动了快照:node_count = %d, 期望 2", view2.NodeCount)
	}
	if view2.LastError == "" {
		t.Fatalf("失败刷新没记 last_error: %s", showAfter)
	}
	if sub := e.subscription(aliceTok, "clash-meta"); !strings.Contains(sub, "ext-jp") {
		t.Fatalf("失败刷新后 alice 丢了外部节点(快照应保留)")
	}

	// ---- 撤权:alice 立即失去外部节点,bob 的权限模型没变 ----
	e.runOK("", "source", "revoke", sid, "--user", "alice")
	if sub := e.subscription(aliceTok, "clash-meta"); strings.Contains(sub, "ext-") {
		t.Fatalf("撤权后 alice 的订阅仍有外部节点")
	}

	// ---- 停用与删除 ----
	e.runOK("", "source", "disable", sid)
	e.runOK("", "source", "enable", sid)
	e.runOK("", "source", "delete", sid)
	if out := e.runOK("", "source", "list"); strings.Contains(out, "测试源") {
		t.Fatalf("删除后 list 仍有来源: %s", out)
	}
}
