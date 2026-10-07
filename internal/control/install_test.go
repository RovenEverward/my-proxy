package control

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/store"
)

// seedCore 预先在缓存目录里放好一份发布,让 InstallNode 不必联网。
//
// 这不是绕开「下载」那一步取巧:「同一个版本取过第二次就不再下载」本来就是这条路径
// 的主要用法(装第二台节点、换核心重装),预置缓存走的正是真实的那一段代码。
func seedCore(t *testing.T, dir, version, goos, goarch string) {
	t.Helper()
	d := filepath.Join(dir, version+"_"+goos+"_"+goarch)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatalf("建缓存目录: %v", err)
	}
	for name, mode := range map[string]os.FileMode{"xray": 0o755, "geoip.dat": 0o644, "geosite.dat": 0o644} {
		if err := os.WriteFile(filepath.Join(d, name), []byte("假的核心 "+name), mode); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
	}
}

// pushed 报告有没有把内容推到某个路径上。PutFile 的脚本里带着目标路径,内容走 stdin。
func pushed(f *fakeRunner, path string) bool {
	for _, c := range f.callList() {
		if strings.Contains(c, "umask 077") && strings.Contains(c, path) {
			return true
		}
	}
	return false
}

// 装一台新机器要一步到位:推核心、配服务、下发配置、启动,并且中间任何一步都不该
// 在节点上留下半成品。这条测试把整条命令序列钉住。
func TestInstallNodePushesCoreAndStartsIt(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatalf("开库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cache := filepath.Join(t.TempDir(), "xray")
	seedCore(t, cache, "v26.3.27", "linux", "amd64")

	f := &fakeRunner{replies: []reply{
		{"uname -s -m", "Linux x86_64\n"},
		{"is-active", "active\n"},
		{" version ", "Xray 26.3.27 (go1.26.1 linux/amd64)\n"},
		{"-test -config", "Configuration OK.\n"},
		{"mv -f", ""},
		{"systemctl", ""},
		{"sleep 1", ""},
		{"chmod", ""},
		{"cp -f", ""},
		{"mkdir -p", ""},
		{"umask 077", ""},
	}}
	c := New(st, f, slog.New(slog.DiscardHandler), cache)

	n := addNode(t, st, "hk-1", "203.0.113.10")
	addInbound(t, st, n.ID, "vless-ws", 30443)

	version, err := c.InstallNode(t.Context(), n, "26.3.27")
	if err != nil {
		t.Fatalf("装核心: %v\n跑过的命令:%v", err, f.callList())
	}
	if !strings.Contains(version, "26.3.27") {
		t.Errorf("应当报节点上回话的版本,实际 %q", version)
	}

	// 核心和它去自己同目录找的两份 geodata 都得推过去 —— 靠 PATH 上碰巧存在的
	// 那份 xray 是这条设计要摆脱的东西。
	for _, want := range []string{"/usr/local/lib/mpx/xray.new", "/usr/local/lib/mpx/geoip.dat", "/usr/local/lib/mpx/geosite.dat"} {
		if !pushed(f, want) {
			t.Errorf("应当把 %s 推到节点上,实际推过:%v", want, f.callList())
		}
	}
	if !pushed(f, "/etc/systemd/system/mpx-xray.service") {
		t.Errorf("应当写下服务单元,实际推过:%v", f.callList())
	}

	// 顺序是这条路径上唯一不能错的地方:先拿新核心校验节点上**现有的**配置,通过了
	// 才顶替上去。反过来的话,一个跑不起来的核心已经把旧的那份覆盖掉了。
	verify := f.indexOf("/usr/local/lib/mpx/xray.new' -test -config")
	replace := f.indexOf("mv -f '/usr/local/lib/mpx/xray.new'")
	if verify < 0 {
		t.Fatalf("应当用新核心校验现有配置,实际跑过:%v", f.callList())
	}
	if replace < 0 {
		t.Fatalf("应当把新核心顶替上去,实际跑过:%v", f.callList())
	}
	if verify > replace {
		t.Errorf("必须先校验再顶替,实际先顶替了(%d > %d):%v", verify, replace, f.callList())
	}

	// 单元文件要在 daemon-reload 之前写好,否则重载的是上一份。
	if unit, reload := f.indexOf("mpx-xray.service"), f.indexOf("systemctl daemon-reload"); unit > reload {
		t.Errorf("单元文件必须在 daemon-reload 之前写好(%d > %d)", unit, reload)
	}
	for _, want := range []string{"systemctl enable 'mpx-xray'", "systemctl restart 'mpx-xray'", "systemctl is-active 'mpx-xray'"} {
		if !f.ran(want) {
			t.Errorf("应当跑过 %q,实际:%v", want, f.callList())
		}
	}
	// 装完就该有得用:配置被渲染并下发过,不只是装了核心。
	if !pushed(f, "/etc/mpx/xray/config.json") {
		t.Errorf("装完应当下发一次配置,实际推过:%v", f.callList())
	}
}

// 节点上读不出平台就别猜:核心装错架构在那台机器上是一启动就退,而报错会指向一个
// 看起来像配置问题的地方。
func TestInstallNodeRefusesUnknownPlatform(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatalf("开库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cache := filepath.Join(t.TempDir(), "xray")
	seedCore(t, cache, "v26.3.27", "linux", "amd64")

	f := &fakeRunner{replies: []reply{{"uname -s -m", "Linux mips\n"}}}
	c := New(st, f, slog.New(slog.DiscardHandler), cache)

	n := addNode(t, st, "hk-1", "203.0.113.10")
	if _, err := c.InstallNode(t.Context(), n, "26.3.27"); err == nil {
		t.Fatal("认不出的架构应当直接报错,而不是装一个跑不起来的核心")
	} else if !strings.Contains(err.Error(), "mips") {
		t.Errorf("报错应当点名那个架构,实际: %v", err)
	}
	if pushed(f, "/usr/local/lib/mpx/xray.new") {
		t.Errorf("平台都没认出来就不该往节点上推东西:%v", f.callList())
	}
}
