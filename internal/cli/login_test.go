package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// runLogin 直接跑 login 命令并抓回 stdout。
//
// login 不碰网络,所以不需要假控制面:它只读写配置文件。
//
// 每次调用前得把上一轮留下的状态清掉。cobra 只在 Execute 的绑定阶段把"这次命令行
// 里给了哪些 flag"折成 Set,不会重置上一轮的 Changed 标记;persistent flag 的值也
// 会一直留在 opts 里。同进程连跑多次时(人手动敲不会),不带参数的调用会被上一条
// 命令的标记污染,于是本该"查看"却走了"切换"分支。真实使用是每次一个进程,
// 不受影响 —— 这里清干净是为了让测试反映真实行为。
func runLogin(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd, _, err := rootCmd.Find([]string{"login"})
	if err != nil {
		t.Fatalf("找 login 命令: %v", err)
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	opts.profile = ""
	t.Cleanup(func() { opts.profile = "" })

	var runErr error
	out := captureStdout(t, func() {
		rootCmd.SetArgs(append([]string{"login"}, args...))
		runErr = rootCmd.Execute()
	})
	return out, runErr
}

func TestLoginStoresAndReportsProfile(t *testing.T) {
	path := useTempConfig(t)

	out, err := runLogin(t, "--server", "http://real.example:8080", "--token", "realtoken123456")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out, "default") {
		t.Errorf("输出里没提写到哪个档: %q", out)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	p := cfg.Profiles[defaultProfile]
	if p.Server != "http://real.example:8080" || p.Token != "realtoken123456" {
		t.Errorf("存进去的不对: %+v", p)
	}
	if cfg.Current != defaultProfile {
		t.Errorf("current = %q, 想要 default", cfg.Current)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("配置文件没落盘: %v", err)
	}
}

// 这是那次事故的核心场景:临时存一个控制面,原来那套必须原封不动。
func TestLoginSecondProfileKeepsFirst(t *testing.T) {
	useTempConfig(t)

	if _, err := runLogin(t, "--server", "http://real.example:8080", "--token", "realtok"); err != nil {
		t.Fatalf("存 default: %v", err)
	}
	if _, err := runLogin(t, "--profile", "scratch",
		"--server", "http://127.0.0.1:18080", "--token", "scratchtok"); err != nil {
		t.Fatalf("存 scratch: %v", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if got := cfg.Profiles[defaultProfile].Server; got != "http://real.example:8080" {
		t.Errorf("存第二个档把第一个改了: default.server = %q", got)
	}
	if got := cfg.Profiles[defaultProfile].Token; got != "realtok" {
		t.Errorf("存第二个档把第一个的 token 改了: %q", got)
	}
	if cfg.Current != "scratch" {
		t.Errorf("current = %q, 想要 scratch(存完切过去)", cfg.Current)
	}
}

// 切档只该要一个档名:token 只在服务器上,切回来不该逼人去把它翻出来。
func TestLoginProfileAloneSwitches(t *testing.T) {
	useTempConfig(t)

	if _, err := runLogin(t, "--server", "http://real.example:8080", "--token", "realtok"); err != nil {
		t.Fatalf("存 default: %v", err)
	}
	if _, err := runLogin(t, "--profile", "scratch",
		"--server", "http://127.0.0.1:18080", "--token", "scratchtok"); err != nil {
		t.Fatalf("存 scratch: %v", err)
	}

	out, err := runLogin(t, "--profile", "default")
	if err != nil {
		t.Fatalf("切回 default: %v", err)
	}
	if !strings.Contains(out, "已切到配置档 default") {
		t.Errorf("没明说切到哪了: %q", out)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if cfg.Current != defaultProfile {
		t.Errorf("current = %q, 想要 default", cfg.Current)
	}
	// 切换不该动内容。
	if got := cfg.Profiles["scratch"].Server; got != "http://127.0.0.1:18080" {
		t.Errorf("切换把 scratch 改了: %q", got)
	}
}

// 拼错档名时静静建一个空档的话,之后每条命令都只报"没有控制面地址",
// 看不出是拼错了。
func TestLoginSwitchToUnknownProfileFails(t *testing.T) {
	useTempConfig(t)

	if _, err := runLogin(t, "--server", "http://real.example:8080", "--token", "realtok"); err != nil {
		t.Fatalf("存 default: %v", err)
	}
	_, err := runLogin(t, "--profile", "nope")
	if err == nil {
		t.Fatal("切到不存在的档没报错")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("错误里没提档名: %v", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if _, ok := cfg.Profiles["nope"]; ok {
		t.Error("报错了却还是建出了一个空档")
	}
	if cfg.Current != defaultProfile {
		t.Errorf("失败的操作改了 current: %q", cfg.Current)
	}
}

// 查看模式不写文件。
func TestLoginWithNoFlagsOnlyShows(t *testing.T) {
	path := useTempConfig(t)
	original := `{"current":"default","profiles":{"default":{"server":"http://real.example","token":"realtok"}}}`
	writeRaw(t, path, original)

	out, err := runLogin(t)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out, "http://real.example") {
		t.Errorf("没显示当前地址: %q", out)
	}
	// token 只显示前 6 位。
	if strings.Contains(out, "realtok") {
		t.Errorf("把整个 token 打出来了: %q", out)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if string(got) != original {
		t.Errorf("查看模式改了文件:\n got %s\nwant %s", got, original)
	}
}

// 覆盖已有档时要把"从什么变成什么"打出来。这不是确认步骤,是让覆盖在屏幕上
// 留一行 —— 出事时能一眼看见。
func TestLoginEchoesServerChange(t *testing.T) {
	useTempConfig(t)

	if _, err := runLogin(t, "--server", "http://old.example:8080", "--token", "tok"); err != nil {
		t.Fatalf("首次: %v", err)
	}
	out, err := runLogin(t, "--server", "http://new.example:8080")
	if err != nil {
		t.Fatalf("改地址: %v", err)
	}
	if !strings.Contains(out, "http://old.example:8080") || !strings.Contains(out, "http://new.example:8080") {
		t.Errorf("没回显地址变化: %q", out)
	}
	// 地址变了但 token 没给,应保留。
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if cfg.Profiles[defaultProfile].Token != "tok" {
		t.Errorf("只改地址时 token 被抹掉了: %q", cfg.Profiles[defaultProfile].Token)
	}
}
