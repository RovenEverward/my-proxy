package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// 更新与回退必须在接触二进制和状态文件之前拒绝开发版。
func TestDevelopmentRejectsLocalUpdates(t *testing.T) {
	oldVersion, oldExecutable, oldSource := localVersion, selfExecutable, selfupdateSource
	t.Cleanup(func() { localVersion, selfExecutable, selfupdateSource = oldVersion, oldExecutable, oldSource })
	localVersion = func() string { return "dev-123456789abc-clean" }
	selfExecutable = func() (string, error) { t.Fatal("不应读取本机更新目标"); return "", nil }
	selfupdateSource = nil // 拒绝更新前不能尝试解析发布来源。
	for _, c := range []struct {
		name string
		run  func() error
	}{
		{"更新", func() error { return applyCLI(&cobra.Command{}) }},
		{"回退", rollbackCLI},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(); err == nil || !strings.Contains(err.Error(), "mpx-dev-update") {
				t.Fatalf("想要重编提示，得到 %v", err)
			}
		})
	}
}
