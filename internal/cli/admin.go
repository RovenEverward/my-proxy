package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// UC-26:网页管理台的账号管理。
//
// 这两条命令的服务端接口都在 requireAuth 之内:浏览器登录页进不了门时,
// 第一个账号只能由已认证凭据种下 —— 典型是服务器上 MPX_TOKEN 直改
// (env 救援),或 mpx init 收尾时建号。所以它们不做交互式登录,直接拿
// resolve() 出的凭据说话;没有凭据就是"先 mpx connect",与设备时代一致。

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "管理网页管理台的管理员账号(UC-26)",
}

var adminCreateFlags struct {
	username      string
	password      string
	passwordStdin bool
}

var adminCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "建管理员账号(只在还没有账号时有效;已有账号则改用户名)",
	Long: `建网页管理台的登录账号。没有账号时创建;已有账号时只改用户名,不改密码
(换密码走 mpx admin password,那里要验旧密码)。

凭据用全局的 --token / MPX_TOKEN:这条命令是给控制面所有者用的,典型场景是
服务器上跑 mpx --server http://127.0.0.1:8080 admin create,或 mpx init 收尾。`,
	Example: `  mpx admin create --username ops --password 'correct horse battery'
  ssh server 'MPX_TOKEN=... mpx --server http://127.0.0.1:8080 admin create --username ops --password-stdin'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		username := adminCreateFlags.username
		if username == "" {
			return errors.New("要给 --username")
		}
		pw, err := readAdminPassword(adminCreateFlags.password, adminCreateFlags.passwordStdin, "管理员密码(至少 12 个字符): ")
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", "/api/admin/account", map[string]string{
			"username": username,
			"password": pw,
		})
		if err != nil {
			return err
		}
		res, err := decode[struct {
			Username string `json:"username"`
			Created  bool   `json:"created"`
		}](raw)
		if err != nil {
			return err
		}
		if res.Created {
			fmt.Printf("管理员账号已建立:%s\n", res.Username)
		} else {
			fmt.Printf("已有账号,用户名已改为:%s\n", res.Username)
		}
		return nil
	},
}

var adminPasswordFlags struct {
	oldPassword   string
	passwordStdin bool
}

var adminPasswordCmd = &cobra.Command{
	Use:   "password",
	Short: "改管理员密码(改完全部会话吊销,重新登录)",
	Long: `改网页管理台的登录密码:验过旧密码才改,改完全部浏览器会话与 CLI token
一律吊销 —— 包括发起这次修改的那一个,之后用新密码重新登录。

救援(忘了旧密码):在服务器上用 MPX_TOKEN 跑这条命令,旧密码不用验,
--old-password 随便填个占位即可。`,
	Example: `  mpx admin password
  ssh server 'MPX_TOKEN=... mpx --server http://127.0.0.1:8080 admin password --old-password x --password-stdin'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		old := adminPasswordFlags.oldPassword
		var err error
		if old == "" {
			if old, err = promptPassword("当前的管理员密码(凭 MPX_TOKEN 救援时可留空直接回车): "); err != nil {
				return err
			}
		}
		pw, err := readAdminPassword("", adminPasswordFlags.passwordStdin, "新的管理员密码(至少 12 个字符): ")
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("PUT", "/api/admin/password", map[string]string{
			"old_password": old,
			"new_password": pw,
		})
		if err != nil {
			return err
		}
		res, err := decode[struct {
			Revoked int64 `json:"revoked"`
		}](raw)
		if err != nil {
			return err
		}
		fmt.Printf("管理员密码已更新,吊销了 %d 个会话;请用新密码重新登录。\n", res.Revoked)
		return nil
	},
}

// readAdminPassword 读一次密码:--password 直接给、--password-stdin 读一行、
// 否则交互输两次核对。与设备密码同口径:至少 12 个字符。
func readAdminPassword(flag string, fromStdin bool, prompt string) (string, error) {
	var pw string
	var err error
	switch {
	case flag != "":
		pw = flag
	case fromStdin:
		pw, err = readLine(os.Stdin)
	default:
		if pw, err = promptPassword(prompt); err != nil {
			return "", err
		}
		again, err := promptPassword("再输一次: ")
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", errors.New("两次输入不一样")
		}
	}
	if err != nil {
		return "", err
	}
	if n := len([]rune(pw)); n < 12 {
		return "", fmt.Errorf("管理员密码至少 12 个字符,这个只有 %d 个", n)
	}
	return pw, nil
}

func init() {
	adminCreateCmd.Flags().StringVar(&adminCreateFlags.username, "username", "", "管理员用户名(必填)")
	adminCreateCmd.Flags().StringVar(&adminCreateFlags.password, "password", "", "管理员密码(脚本用;不给则交互输入)")
	adminCreateCmd.Flags().BoolVar(&adminCreateFlags.passwordStdin, "password-stdin", false, "从 stdin 读管理员密码(脚本用)")
	adminPasswordCmd.Flags().StringVar(&adminPasswordFlags.oldPassword, "old-password", "", "当前密码(脚本用;不给则交互输入)")
	adminPasswordCmd.Flags().BoolVar(&adminPasswordFlags.passwordStdin, "password-stdin", false, "从 stdin 读新密码(脚本用)")
	adminCmd.AddCommand(adminCreateCmd, adminPasswordCmd)
	rootCmd.AddCommand(adminCmd)
}
