package cli

import "testing"

// 窗口里切换控制面后要记住:SetCurrent 把配置文件里的 current 改过去,
// 不存在的名字报错且不落盘。
func TestProfilesRunnerSetCurrent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := saveConfig(Config{Current: "default", Profiles: map[string]Profile{
		"default": {Server: "http://127.0.0.1:9001"}, "prod": {Server: "https://cp.example.net"}}}); err != nil {
		t.Fatal(err)
	}
	if err := (profilesRunner{}).SetCurrent("prod"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil || cfg.Current != "prod" {
		t.Fatalf("current 应变成 prod,得到 %q %v", cfg.Current, err)
	}
	if err := (profilesRunner{}).SetCurrent("nope"); err == nil {
		t.Fatal("不存在的控制面应报错")
	}
	if cfg, _ = loadConfig(); cfg.Current != "prod" {
		t.Fatalf("报错时不该改 current,得到 %q", cfg.Current)
	}
}
