package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/winpath"
)

// winpathCmd 由 setup.exe / install-cli.ps1 调用,增删用户 PATH 里的安装目录。
// 出错时 PATH 一字不改,退出码非 0,安装器据此提示用户手动加。
var winpathCmd = &cobra.Command{
	Use:    "winpath add|remove <目录>",
	Short:  "把目录加进 / 移出 Windows 用户 PATH(安装器用)",
	Hidden: true,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 || (args[0] != "add" && args[0] != "remove") {
			return fmt.Errorf("用法:mpx winpath add|remove <目录>")
		}
		return nil
	},
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		changed, backup, err := winpath.Apply(args[0], args[1])
		if err != nil {
			return fmt.Errorf("没有改动用户 PATH:%w", err)
		}
		switch {
		case !changed:
			cmd.Println("用户 PATH 已经是想要的样子，没有改动。")
		default:
			cmd.Printf("已更新用户 PATH；原来的值备份在 %s 和 HKCU\\Software\\mpx\\PathBackup。\n", backup)
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(winpathCmd) }
