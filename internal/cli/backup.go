package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/backup"
)

// backupOut 是 backup 命令的 -o:输出文件路径。
//
// 它遮蔽全局的 -o/--output(输出格式):对一条产出文件的命令,"-o 文件"比
// "输出格式"更符合直觉。本仓库只有这一个命令这样用,其它命令的 -o 仍是格式。
var backupOut string

// backupCmd 从控制面下载一份备份归档。
var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "从控制面下载一份备份归档",
	Long: `从控制面下载一份备份归档(tar.gz)。

归档里有 mpx.db(所有用户的密钥种子与节点凭据)和控制面连节点用的系统 ssh 私钥,
没有管理 token。它等于一整套凭据,当密码文件保管:不要走不加密的通道传,不要随手
拷来拷去。

默认文件名 mpx-backup-<yyyymmdd-hhmmss>.tar.gz,写完权限 0600。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		out := backupOut
		if out == "" {
			out = backup.DefaultFileName(time.Now())
		}
		// O_EXCL:不覆盖已有文件,免得手滑把一份旧备份盖掉。
		f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("创建备份文件 %s: %w", out, err)
		}
		if err := c.download("/api/backup", f); err != nil {
			f.Close()
			os.Remove(out) // 半份归档没有用,别留下
			return err
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("关闭备份文件 %s: %w", out, err)
		}
		st, err := os.Stat(out)
		if err != nil {
			return fmt.Errorf("查看备份文件 %s: %w", out, err)
		}
		if opts.output == outputJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"file": out, "bytes": st.Size()})
		}
		fmt.Printf("已保存到 %s(%s)\n", out, humanBytes(st.Size()))
		fmt.Println("里面有节点 SSH 私钥和用户密钥种子,当密码文件保管。")
		return nil
	},
}

var restoreForce bool

// restoreCmd 把一份备份发给控制面校验并暂存,重启后生效。
var restoreCmd = &cobra.Command{
	Use:   "restore <归档>",
	Short: "把一份备份暂存到控制面,重启后生效",
	Long: `把一份备份归档发给控制面校验并暂存。

控制面不会当场换库:运行中的进程正开着数据库文件。它只把校验过的库写成
<db>.restore,重启时换入,旧库改名成 <db>.pre-restore-<时间戳> 留着回退。

已经有一份待生效的恢复时会被拒绝,确认要换用 --force。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("读取归档 %s: %w", args[0], err)
		}
		path := "/api/restore"
		if restoreForce {
			path += "?force=1"
		}
		// 正文就是归档本身:rawBody 跳过 JSON 序列化原样发出。
		raw, err := c.do(http.MethodPost, path, rawBody(data))
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		msg := string(raw)
		var resp struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &resp) == nil && resp.Message != "" {
			msg = resp.Message
		}
		fmt.Println(msg)
		fmt.Println("重启命令:systemctl restart mpx")
		return nil
	},
}

func init() {
	backupCmd.Flags().StringVarP(&backupOut, "output", "o", "", "输出文件路径(默认 mpx-backup-<时间戳>.tar.gz)")
	restoreCmd.Flags().BoolVar(&restoreForce, "force", false, "覆盖已暂存的恢复")
}
