package node

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestSSHArgsDefaults(t *testing.T) {
	got := sshArgs(model.Node{Name: "hk", Address: "203.0.113.10"})
	want := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
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
