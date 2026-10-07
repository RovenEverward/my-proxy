package node

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// xrayBin 决定节点上用的是哪个核心。它必须指向 mpx 自己的路径,不能去问 PATH ——
// 那台机器上可能还装着别人的 xray,用哪一个不该由 PATH 的先后决定。
func TestXrayBinUsesPrivatePathNotPATH(t *testing.T) {
	if strings.Contains(xrayBin, "command -v") {
		t.Fatalf("核心不该去 PATH 上找: %q", xrayBin)
	}
	if !strings.Contains(xrayBin, XrayBinPath) {
		t.Fatalf("xrayBin 应当落到 %s: %q", XrayBinPath, xrayBin)
	}

	// 真跑一遍那行前缀:没设 MPX_XRAY_BIN 时解析成私有路径,设了就认设的那个。
	for _, tc := range []struct{ env, want string }{
		{"", XrayBinPath},
		{"/opt/other/xray", "/opt/other/xray"},
	} {
		cmd := exec.Command("bash", "-c", xrayBin+`printf '%s' "$XRAY_BIN"`)
		cmd.Env = append(withoutXrayBinEnv(), "MPX_XRAY_BIN="+tc.env)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("跑 xrayBin 前缀: %v", err)
		}
		if string(out) != tc.want {
			t.Errorf("MPX_XRAY_BIN=%q 时 $XRAY_BIN = %q, want %q", tc.env, out, tc.want)
		}
	}
}

// withoutXrayBinEnv 先把环境里已有的 MPX_XRAY_BIN 摘掉。
//
// 开发时为了跑真实 xray 的用例, shell 里常常正设着这个变量;同名键在子进程环境里
// 重复出现会由 libc 决定取哪个,不能指望「后加的赢」。
func withoutXrayBinEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "MPX_XRAY_BIN=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// fakeRunner 按脚本内容回答,用来单独验证 Deploy 在「起来了」和「起来了又倒下」两条
// 路上的行为 —— 真实 xray 不会按我们的意思倒下,这一层只能在这里钉。
type fakeRunner struct {
	calls []string
	fn    func(script string) (string, error)
}

func (f *fakeRunner) Run(_ context.Context, _ model.Node, script string) (string, error) {
	f.calls = append(f.calls, script)
	if f.fn != nil {
		return f.fn(script)
	}
	return "", nil
}

func (f *fakeRunner) RunWithInput(ctx context.Context, n model.Node, script string, _ []byte) (string, error) {
	return f.Run(ctx, n, script)
}

func (f *fakeRunner) count(sub string) int {
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			n++
		}
	}
	return n
}

func deployNode() model.Node {
	return model.Node{Name: "hk", Address: "203.0.113.10", ConfigPath: "/etc/mpx/xray/config.json", ServiceName: "mpx-xray"}
}

// 配置通过校验还不够:进程可能刚 fork 出来就退掉(端口被占、核心和配置不匹配)。
// 不确认服务真的在跑,控制面就会在节点已经断流的时候报「已下发」。
func TestDeployConfirmsServiceIsUp(t *testing.T) {
	f := &fakeRunner{fn: func(script string) (string, error) {
		if strings.Contains(script, "is-active") {
			return "active\n", nil
		}
		return "", nil
	}}
	if err := Deploy(t.Context(), f, deployNode(), []byte(`{}`)); err != nil {
		t.Fatalf("服务确实起来了,Deploy 应当成功: %v", err)
	}
	if f.count("systemctl is-active") == 0 {
		t.Fatalf("下发之后必须确认服务在跑,实际跑过:%v", f.calls)
	}
}

func TestDeployRollsBackWhenServiceDiesAfterRestart(t *testing.T) {
	n := deployNode()
	f := &fakeRunner{fn: func(script string) (string, error) {
		switch {
		case strings.Contains(script, "is-active"):
			return "activating\n", nil
		case strings.Contains(script, "journalctl"):
			return "bind: address already in use\n", nil
		case strings.Contains(script, "test -f"):
			return "yes\n", nil
		}
		return "", nil
	}}

	err := Deploy(t.Context(), f, n, []byte(`{}`))
	if err == nil {
		t.Fatal("服务没起来,Deploy 必须报错")
	}
	for _, want := range []string{"已回滚", "bind: address already in use"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错里应当有 %q,实际: %v", want, err)
		}
	}
	if got := f.count("systemctl restart"); got != 2 {
		t.Errorf("应当重启两次(起新配置、回滚),实际 %d 次:%v", got, f.calls)
	}
	if f.count("cp -f '"+n.ConfigPathOrDefault()+".bak'") == 0 {
		t.Errorf("回滚应当把备份放回去,实际跑过:%v", f.calls)
	}
	// 备份放回去了,日志才有意义 —— 报的是「现在这个状态是怎么来的」。
	if !strings.Contains(err.Error(), "bind: address already in use") {
		t.Errorf("报错应当带上节点日志: %v", err)
	}
}

// 首次部署没有上一份可回滚:这时把坏配置再启动一遍只会让它继续崩,所以只报错、
// 不重启第二次。
func TestDeployFirstInstallWithoutBackupDoesNotRestartAgain(t *testing.T) {
	n := deployNode()
	f := &fakeRunner{fn: func(script string) (string, error) {
		if strings.Contains(script, "is-active") {
			return "failed\n", nil
		}
		return "", nil // test -f 不输出 yes:没有备份
	}}

	err := Deploy(t.Context(), f, n, []byte(`{}`))
	if err == nil {
		t.Fatal("服务没起来,Deploy 必须报错")
	}
	if !strings.Contains(err.Error(), "首次部署") {
		t.Errorf("报错应当说清没有可回滚的配置,实际: %v", err)
	}
	if got := f.count("systemctl restart"); got != 1 {
		t.Errorf("没有备份就不该再重启,实际 %d 次:%v", got, f.calls)
	}
}
