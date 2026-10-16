package node

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestSSHArgsDefaults(t *testing.T) {
	got := sshArgs(model.Node{Name: "hk", Address: "203.0.113.10"})
	want := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + sshControlPath(),
		"-o", "ControlPersist=60s",
		"root@203.0.113.10",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("默认参数:\n got %q\nwant %q", got, want)
	}

	// BatchMode 是这条设计里最要紧的一项:控制面是常驻进程,ssh 一旦停在等密码
	// 的提示上,整轮收敛就跟着停住。它必须一直在。
	if !strings.Contains(strings.Join(got, " "), "BatchMode=yes") {
		t.Fatal("少了 BatchMode=yes,ssh 会挂在等密码上")
	}
	// ControlMaster 三条必须同时在,漏一条要么连不上要么退化成每命令一次握手。
	for _, opt := range []string{"ControlMaster=auto", "mpx-ssh-%C", "ControlPersist=60s"} {
		if !strings.Contains(strings.Join(got, " "), opt) {
			t.Fatalf("少了连接复用参数 %q: %q", opt, got)
		}
	}
	// ControlPath 必须短到能当 unix socket 用。macOS 的 TMPDIR 本身就四五十个
	// 字符,直接拼 os.TempDir() 会超上限,ssh 报的是 "path too long for Unix
	// domain socket" —— 看着像网络问题,其实是本地路径问题,而 mpx init 恰恰
	// 跑在 macOS 上。
	//
	// 上限按最严的 Linux 104 字节算(BSD/macOS 也是 104);%C 会被替换成 40 个
	// 字符的散列键,所以按展开后的长度验。
	const unixSocketLimit = 104
	expanded := strings.ReplaceAll(sshControlPath(), "%C",
		strings.Repeat("a", 40))
	if len(expanded) >= unixSocketLimit {
		t.Fatalf("ControlPath 展开后 %d 字节,超过 unix socket 上限 %d: %q",
			len(expanded), unixSocketLimit, expanded)
	}
}

func TestSSHArgsHonorsPortAndUser(t *testing.T) {
	got := sshArgs(model.Node{Name: "hk", Address: "node.example.com", SSHPort: 2222, SSHUser: "deploy"})
	joined := strings.Join(got, " ")

	if !strings.Contains(joined, "-p 2222") {
		t.Fatalf("非默认端口要带上 -p: %q", got)
	}
	if !strings.HasSuffix(joined, "deploy@node.example.com") {
		t.Fatalf("目标应排在最后: %q", got)
	}
	// 22 是默认值,带上 -p 22 只是噪音。
	if strings.Contains(strings.Join(sshArgs(model.Node{Address: "a", SSHPort: 22}), " "), "-p ") {
		t.Fatal("22 端口不该显式带 -p")
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"简单":         `'简单'`,
		"":           `''`,
		"/etc/xray":  `'/etc/xray'`,
		"it's":       `'it'\''s'`,
		"a b;rm -rf": `'a b;rm -rf'`,
		"$HOME":      `'$HOME'`,
		"`id`":       "'`id`'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// recordRunner 只记下令、不真执行,用来同时抓住脚本与喂进去的 stdin。
type recordRunner struct {
	script string
	stdin  []byte
}

func (r *recordRunner) Run(_ context.Context, _ model.Node, script string) (string, error) {
	return r.RunWithInput(context.Background(), model.Node{}, script, nil)
}

func (r *recordRunner) RunWithInput(_ context.Context, _ model.Node, script string, stdin []byte) (string, error) {
	r.script, r.stdin = script, stdin
	return "", nil
}

// 入站 tag 会进命令行、配置内容会走 stdin,两边都不能假定只含安全字符。这里把
// PutFile 拼出来的脚本真喂给 shell,确认带引号的路径被当字面量、内容一字不差落地。
func TestPutFileScriptSurvivesHostilePathAndContent(t *testing.T) {
	const marker = "内容里有 $HOME、`id` 和 '常'"
	dir := t.TempDir()
	// 路径里带空格与单引号,是最容易拼错的一类。
	p := dir + "/it's a dir/config.json"

	rec := &recordRunner{}
	if err := PutFile(t.Context(), rec, model.Node{}, p, []byte(marker)); err != nil {
		t.Fatalf("PutFile: %v", err)
	}

	if string(rec.stdin) != marker {
		t.Fatalf("内容应当原样走 stdin,实际 %q", rec.stdin)
	}
	if !strings.Contains(rec.script, "umask 077") {
		t.Fatalf("该先收权限再写: %q", rec.script)
	}
	if strings.Contains(rec.script, marker) {
		t.Fatalf("内容不该出现在脚本里(那会落进节点的进程表): %q", rec.script)
	}

	// 真跑一遍:内容与路径都得原样落地。
	run := localRunner{binDir: t.TempDir()}
	if out, err := run.RunWithInput(t.Context(), model.Node{}, rec.script, []byte(marker)); err != nil {
		t.Fatalf("脚本执行失败: %v: %s", err, out)
	}
	if got := readFile(t, p); got != marker {
		t.Fatalf("落地的内容不对:\n got %q\nwant %q", got, marker)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("临时文件应当已经被改名,不该留在节点上")
	}
}

// fakeSSH 把一个假的 ssh 放到 PATH 最前面。SSH.Run 是通过 PATH 找 ssh 的,于是
// 不用真机器就能控制「这条命令在外面待多久、说什么」。
//
// body 要用 `exec` 结尾:直接 `sleep` 的话,被打死的只是外层 shell, sleep 还活着
// 且握着输出管道,Wait 会一直等到它自己退出 —— 那就不是测超时,而是测等待。
func fakeSSH(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatalf("写假 ssh: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// 远端命令卡住时,Run 必须自己断掉并说清是超时 —— 而不是把整轮任务挂死。
func TestRunTimesOutInsteadOfHanging(t *testing.T) {
	fakeSSH(t, "exec sleep 30")

	start := time.Now()
	_, err := SSH{Timeout: 200 * time.Millisecond}.
		Run(t.Context(), model.Node{Name: "慢节点", Address: "203.0.113.10"}, "echo hi")
	if err == nil {
		t.Fatal("命令一直不返回,该报超时")
	}
	if !strings.Contains(err.Error(), "命令超时") {
		t.Fatalf("超时要说人话,实际:%v", err)
	}
	if !strings.Contains(err.Error(), "慢节点") {
		t.Fatalf("错误里要能看出是哪台节点:%v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("超时没生效,等了 %s", d)
	}
}

// 调用方自带 deadline 时以它为准:兜底值大得多,一旦覆盖就等于把某个节点的卡顿
// 放大成整轮任务的卡顿。
func TestRunHonorsCallerDeadline(t *testing.T) {
	fakeSSH(t, "exec sleep 30")

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	// 兜底给 10 分钟:要是它赢了这个测试会先超时结束,而不是通过。
	_, err := SSH{Timeout: 10 * time.Minute}.
		Run(ctx, model.Node{Name: "慢节点", Address: "203.0.113.10"}, "echo hi")
	if err == nil || !strings.Contains(err.Error(), "命令超时") {
		t.Fatalf("ctx 的 deadline 该生效并报超时,实际:%v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("没有用 ctx 的 deadline,等了 %s", d)
	}
}

// 超时与「节点不可达」在日志里要能一眼分开:后者要把 ssh 自己的话带出来。
func TestRunReportsUnreachableNotTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 ssh 本地套接字")
	}
	fakeSSH(t, `echo "ssh: connect to host 203.0.113.10 port 22: Connection refused" >&2; exit 255`)

	_, err := SSH{}.Run(t.Context(), model.Node{Name: "断电节点", Address: "203.0.113.10"}, "echo hi")
	if err == nil {
		t.Fatal("连不上该报错")
	}
	if strings.Contains(err.Error(), "命令超时") {
		t.Fatalf("连不上不是超时,别把两类混在一起:%v", err)
	}
	if !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("要把 ssh 的原话带出来:%v", err)
	}
}

// 兜底值必须盖得住最慢的那条合法命令(推 65MB 的 node install),否则没给
// deadline 的调用会被它误杀。
func TestDefaultTimeoutCoversSlowestCommand(t *testing.T) {
	if got := (SSH{}).timeout(); got != DefaultTimeout {
		t.Fatalf("0 值该回落到兜底值: got %s, want %s", got, DefaultTimeout)
	}
	if DefaultTimeout < 10*time.Minute {
		t.Fatalf("65MB 走 100KB/s 要约 11 分钟,兜底值 %s 太小", DefaultTimeout)
	}
}
