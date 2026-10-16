package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// useTempConfig 把配置文件指到一个临时目录。
//
// 不能靠 XDG_CONFIG_HOME:os.UserConfigDir 在 macOS 上忽略它,直接读
// $HOME/Library/Application Support。所以只能改 HOME。
func useTempConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MPX_PROFILE", "")
	t.Setenv("MPX_SERVER", "")
	t.Setenv("MPX_TOKEN", "")
	opts.profile = ""
	t.Cleanup(func() { opts.profile = "" })
	path, err := configFile()
	if err != nil {
		t.Fatalf("算配置路径: %v", err)
	}
	return path
}

// writeRaw 直接写一份配置文件的原文,用来造旧格式和坏内容。
func writeRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写文件: %v", err)
	}
}

func TestLoadConfigMigratesLegacySingleProfile(t *testing.T) {
	path := useTempConfig(t)
	writeRaw(t, path, `{"server":"http://old.example:8080","token":"oldtokens"}`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("读旧格式: %v", err)
	}
	p, ok := cfg.Profiles[defaultProfile]
	if !ok {
		t.Fatalf("旧格式没被折成 %s 档: %+v", defaultProfile, cfg)
	}
	if p.Server != "http://old.example:8080" || p.Token != "oldtokens" {
		t.Errorf("迁移后内容不对: %+v", p)
	}
	if cfg.Current != defaultProfile {
		t.Errorf("current = %q, 想要 %q", cfg.Current, defaultProfile)
	}
}

// 读旧格式不该顺手改用户的文件:一次 status 就把文件重写一遍太意外了。
func TestLoadConfigDoesNotRewriteLegacyFile(t *testing.T) {
	path := useTempConfig(t)
	legacy := `{"server":"http://old.example:8080","token":"oldtokens"}`
	writeRaw(t, path, legacy)

	if _, err := loadConfig(); err != nil {
		t.Fatalf("读: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if string(got) != legacy {
		t.Errorf("读操作改了文件:\n got %s\nwant %s", got, legacy)
	}
}

// 旧字段不再写回文件,否则新格式里会留着一份谁也不看的 server/token,
// 下次再迁移时又要判断"以哪个为准"。
func TestSaveConfigDropsLegacyFields(t *testing.T) {
	path := useTempConfig(t)
	writeRaw(t, path, `{"server":"http://old.example:8080","token":"oldtokens"}`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("读: %v", err)
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("写: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解: %v", err)
	}
	for _, k := range []string{"server", "token"} {
		if _, ok := m[k]; ok {
			t.Errorf("顶层还留着旧字段 %q: %s", k, raw)
		}
	}
}

// 写到一半失败不能留下半个 JSON:那会让之后每条命令都在 loadConfig 里报错,
// 连 login 自己都跑不起来。这里验的是"目标文件始终是完整的"。
func TestSaveConfigLeavesNoPartialFile(t *testing.T) {
	path := useTempConfig(t)

	first := Config{
		Current:  defaultProfile,
		Profiles: map[string]Profile{defaultProfile: {Server: "http://a.example", Token: "t1"}},
	}
	if err := saveConfig(first); err != nil {
		t.Fatalf("首次写: %v", err)
	}

	second := Config{
		Current: "scratch",
		Profiles: map[string]Profile{
			defaultProfile: {Server: "http://a.example", Token: "t1"},
			"scratch":      {Server: "http://b.example", Token: "t2"},
		},
	}
	if err := saveConfig(second); err != nil {
		t.Fatalf("二次写: %v", err)
	}

	// 临时文件不许残留。
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("读目录: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("目录里多了个文件: %s", e.Name())
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if cfg.Current != "scratch" || len(cfg.Profiles) != 2 {
		t.Errorf("回读内容不对: %+v", cfg)
	}
}

func TestSaveConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 Unix 文件权限位")
	}
	path := useTempConfig(t)
	if err := saveConfig(Config{
		Current:  defaultProfile,
		Profiles: map[string]Profile{defaultProfile: {Server: "http://a.example", Token: "t"}},
	}); err != nil {
		t.Fatalf("写: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("文件权限 = %o, 想要 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("目录权限 = %o, 想要 700", perm)
	}
}

// 挑不存在的档要报错并列出有哪些,不能静静返回空值 —— 那样调用方只会看到
// "没有控制面地址",看不出是档名拼错了。
func TestPickUnknownProfileListsNames(t *testing.T) {
	cfg := Config{
		Current: "a",
		Profiles: map[string]Profile{
			"a": {Server: "http://a.example"},
			"b": {Server: "http://b.example"},
		},
	}
	_, err := cfg.pick("nope")
	if err == nil {
		t.Fatal("挑不存在的档没报错")
	}
	msg := err.Error()
	for _, want := range []string{"nope", "a", "b"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息 %q 里没有 %q", msg, want)
		}
	}
}

// map 的遍历顺序是随机的,不排序的话同一条命令每次打印的顺序都不一样。
func TestNamesAreSorted(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"zeta": {}, "alpha": {}, "mid": {},
	}}
	got := strings.Join(cfg.names(), ",")
	if got != "alpha,mid,zeta" {
		t.Errorf("names() = %q, 想要排好序的 alpha,mid,zeta", got)
	}
}

func TestResolvePrefersFlagOverEnvOverProfile(t *testing.T) {
	path := useTempConfig(t)
	writeRaw(t, path, `{"current":"default","profiles":{"default":{"server":"http://file.example","token":"filetok"}}}`)

	// 都没有时用配置文件。
	s, tok, err := resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if s != "http://file.example" || tok != "filetok" {
		t.Errorf("只用配置文件时 = %q/%q", s, tok)
	}

	// 环境变量盖过配置文件。
	t.Setenv("MPX_SERVER", "http://env.example")
	t.Setenv("MPX_TOKEN", "envtok")
	s, tok, _ = resolve()
	if s != "http://env.example" || tok != "envtok" {
		t.Errorf("环境变量没盖过配置文件: %q/%q", s, tok)
	}

	// flag 盖过环境变量。
	opts.server, opts.token = "http://flag.example", "flagtok"
	t.Cleanup(func() { opts.server, opts.token = "", "" })
	s, tok, _ = resolve()
	if s != "http://flag.example" || tok != "flagtok" {
		t.Errorf("flag 没盖过环境变量: %q/%q", s, tok)
	}
}

// --server 是"这次用这个地址",不该摸配置文件。这正是把临时控制面写进
// 真实配置的那次事故要防的事。
func TestServerFlagDoesNotTouchConfig(t *testing.T) {
	path := useTempConfig(t)
	original := `{"current":"default","profiles":{"default":{"server":"http://real.example","token":"realtok"}}}`
	writeRaw(t, path, original)

	if _, _, err := resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	opts.server, opts.token = "http://scratch.example", "scratchtok"
	t.Cleanup(func() { opts.server, opts.token = "", "" })
	s, _, err := resolve()
	if err != nil {
		t.Fatalf("resolve with flag: %v", err)
	}
	if s != "http://scratch.example" {
		t.Errorf("--server 没生效: %q", s)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if string(got) != original {
		t.Errorf("--server 改了配置文件:\n got %s\nwant %s", got, original)
	}
}
