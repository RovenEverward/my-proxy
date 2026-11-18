package cli

import (
	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/ui"
)

func init() {
	// ui fingerprint:打印嵌进这份二进制的前端源码指纹。scripts/ui-fingerprint.mjs
	// 在工作树里重新算同一份清单,两边一致才说明这份 mpx 里的 UI 就是当前源码。
	// 发版二进制也保留:排查"页面是不是旧构建"时这是最快的证据。
	uiCmd.AddCommand(&cobra.Command{
		Use: "fingerprint", Short: "打印嵌入 UI 的源码指纹", Args: cobra.NoArgs, Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			if fp := ui.EmbeddedSources(); fp != "" {
				cmd.Println(fp)
			} else {
				cmd.Println("(none)")
			}
		},
	})
}
