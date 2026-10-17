package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/ui"
)

const ssSample = `LISTEN 0      511          0.0.0.0:8080      0.0.0.0:*    users:(("nginx",pid=1234,fd=6),("nginx",pid=1235,fd=6))
LISTEN 0      4096         0.0.0.0:22        0.0.0.0:*    users:(("sshd",pid=800,fd=3))
LISTEN 0      4096            [::]:443          [::]:*    users:(("caddy",pid=900,fd=7))
LISTEN 0      128        127.0.0.1:18080     0.0.0.0:*
LISTEN 0      4096       127.0.0.53%lo:53    0.0.0.0:*    users:(("systemd-resolve",pid=500,fd=14))`

func TestParseListeners(t *testing.T) {
	got := parseListeners(ssSample)
	want := map[int]string{8080: "nginx(进程 1234)", 22: "sshd(进程 800)", 443: "caddy(进程 900)", 18080: "另一个程序", 53: "systemd-resolve(进程 500)"}
	for p, o := range want {
		if got[p] != o {
			t.Errorf("端口 %d: got %q want %q", p, got[p], o)
		}
	}
}

func TestCheckPortsBusy(t *testing.T) {
	api, inb := checkPorts(map[string]string{"ss": ssSample}, 8080, 443)
	if api.Owner != "nginx(进程 1234)" || api.Suggest != 28080 { // 18080 也被占了
		t.Errorf("api = %+v", api)
	}
	if inb.Owner != "caddy(进程 900)" || inb.Suggest != 2087 {
		t.Errorf("inbound = %+v", inb)
	}
	err := apiPortBusyError(api, true)
	for _, want := range []string{"8080", "nginx(进程 1234)", "28080", "监听地址", "还没有往这台机器装任何东西"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错里缺 %q: %v", want, err)
		}
	}
	if e := apiPortBusyError(api, false); !strings.Contains(e.Error(), "--addr 0.0.0.0:28080") {
		t.Errorf("命令行报错应给出 --addr: %v", e)
	}
}

func TestCheckPortsFreeAndRerun(t *testing.T) {
	// 端口空着
	api, inb := checkPorts(map[string]string{"ss": ""}, 8080, 443)
	if api.Owner != "" || inb.Owner != "" {
		t.Errorf("空机器不该报占用: %+v %+v", api, inb)
	}
	// 重跑:8080 是 mpx 自己,443 由已有数据决定
	ss := `LISTEN 0 4096 0.0.0.0:8080 0.0.0.0:* users:(("mpx",pid=42,fd=8))
LISTEN 0 4096 0.0.0.0:443 0.0.0.0:* users:(("xray",pid=43,fd=9))`
	api, inb = checkPorts(map[string]string{"ss": ss, "mpxdb": "1"}, 8080, 443)
	if api.Owner != "" || inb.Owner != "" {
		t.Errorf("重跑不该报占用: %+v %+v", api, inb)
	}
	// 没有 ss 输出(机器上没装 ss):看不出来就不拦
	api, _ = checkPorts(map[string]string{}, 8080, 443)
	if api.Owner != "" {
		t.Errorf("看不出来时不该拦: %+v", api)
	}
}

func TestPortProbeFields(t *testing.T) {
	info := map[string]string{"ss": ssSample}
	api, inb := checkPorts(info, 8080, 443)
	portProbeFields(info, api, inb)
	if info["api_busy"] != "nginx(进程 1234)" || info["api_suggest"] != "28080" || info["api_port"] != "8080" {
		t.Errorf("api 字段: %v", info)
	}
	if info["inbound_suggest"] != "2087" {
		t.Errorf("inbound 字段: %v", info)
	}
}

func TestSetupRunnerPortBusy(t *testing.T) {
	// 8080 被 nginx 占着:在装任何东西之前停下,中文说清楚。
	fake := &fakeRemote{osName: "Linux", arch: "x86_64", uid: "0", fetch: "/usr/bin/curl", bootstrapOK: true, fp: validFP,
		ss: `LISTEN 0 511 0.0.0.0:8080 0.0.0.0:* users:(("nginx",pid=1234,fd=6))`}
	setupTestEnv(t, fake, "tok-port")
	_, err := setupRunner{}.Run(context.Background(), ui.SetupParams{Host: "203.0.113.10", DevicePassword: "device-password-1234"}, &fakeSetupProgress{})
	if err == nil || !strings.Contains(err.Error(), "nginx(进程 1234)") || !strings.Contains(err.Error(), "8080") {
		t.Fatalf("应报 8080 被 nginx 占用,得到 %v", err)
	}
	for _, c := range fake.calls {
		if strings.Contains(c, "mpx-install.sh") {
			t.Fatalf("端口被占时不该开始安装: %q", c)
		}
	}
}

func TestSetupRunnerInboundBusySwitches(t *testing.T) {
	// 443 被 caddy 占着:入站自动换到 2087,bootstrap 带上 --inbound-port。
	fake := &fakeRemote{osName: "Linux", arch: "x86_64", uid: "0", fetch: "/usr/bin/curl", bootstrapOK: true, fp: validFP,
		ss: `LISTEN 0 4096 [::]:443 [::]:* users:(("caddy",pid=900,fd=7))`}
	setupTestEnv(t, fake, "tok-port2")
	prog := &fakeSetupProgress{}
	_, err := (setupRunner{}).Run(context.Background(), ui.SetupParams{Host: "203.0.113.10", DevicePassword: "device-password-1234"}, prog)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var boot string
	for _, c := range fake.calls {
		if strings.Contains(c, "'bootstrap' '--address'") {
			boot = c
		}
	}
	if !strings.Contains(boot, "'--inbound-port' '2087'") {
		t.Errorf("bootstrap 应改用 2087: %q", boot)
	}
	if !hasLine(prog.lines, "caddy(进程 900)") || !hasLine(prog.lines, "改用 2087") {
		t.Errorf("进度里应说明换了端口: %q", prog.lines)
	}
}
