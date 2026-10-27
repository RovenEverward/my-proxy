package cli

import (
	"os"

	"github.com/egoist/mygo"
	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/ui"
)

func init() {
	if buildinfo.IsDevelopment(buildinfo.Version()) {
		rootCmd.AddCommand(&cobra.Command{
			Use: "version", Short: "显示开发版版本与源码提交", Args: cobra.NoArgs,
			Run: func(cmd *cobra.Command, args []string) { cmd.Println("mpx-dev " + buildinfo.Version()) },
		})
	}
}

// configureDevelopment 只隔离本机界面状态，控制面配置仍使用正式版的 config.json。
func configureDevelopment() {
	if !buildinfo.IsDevelopment(buildinfo.Version()) {
		return
	}
	mygo.App.SetName("mpx-dev") // MyGo 的用户数据与缓存目录按应用名区分。
	_ = os.Unsetenv(ui.RestartEnv)
	uiFlags.addr = "127.0.0.1:0"
	uiCmd.Flags().Lookup("addr").DefValue = uiFlags.addr
}
