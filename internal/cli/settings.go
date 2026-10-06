package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "查看与修改订阅设置",
}

func init() {
	settingsCmd.AddCommand(settingsShowCmd, settingsSetCmd)
	settingsSetCmd.Flags().StringVarP(&settingsSetFile, "file", "f", "", "设置 JSON 文件;- 表示从 stdin 读")
}

var settingsSetFile string

var settingsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "显示当前的订阅设置",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/settings", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		s, err := decode[model.Settings](raw)
		if err != nil {
			return err
		}
		if err := writeKV([][2]string{
			{"订阅地址前缀", s.SubURLPrefix},
			{"订阅名", s.ProfileTitle},
			{"支持链接", s.SupportLink},
			{"自动更新间隔(小时)", strconv.Itoa(s.UpdateInterval)},
		}); err != nil {
			return err
		}
		rows := make([][]string, 0, len(s.Rules))
		for i, r := range s.Rules {
			rows = append(rows, []string{strconv.Itoa(i + 1), string(r.Result), r.Pattern})
		}
		fmt.Println()
		fmt.Println("订阅规则:")
		return writeTable([]string{"序号", "匹配结果", "User-Agent 正则"}, rows)
	},
}

var settingsSetCmd = &cobra.Command{
	Use:   "set -f FILE",
	Short: "用一份 JSON 整体替换订阅设置",
	Long: `用一份 JSON 整体替换订阅设置。

JSON 的形状与 mpx -o json settings show 的输出一致(model.Settings)。先在本
地解一遍是为了把语法错误指到文件上,发出去的仍是文件里的原始内容。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if settingsSetFile == "" {
			return errors.New("必须给 -f FILE(或用 -f - 从 stdin 读)")
		}
		data, err := readSettingsFile(settingsSetFile)
		if err != nil {
			return err
		}
		var s model.Settings
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("%s 不是合法的 model.Settings JSON: %w", settingsSetFile, err)
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		// 用 RawMessage 原样回传,服务端收到的就是这份文件的内容。
		raw, err := c.do("PUT", "/api/settings", json.RawMessage(data))
		if err != nil {
			return err
		}
		return report(raw, "已保存订阅设置")
	},
}

// readSettingsFile 读设置文件;"-" 表示从 stdin 读,方便管道与 heredoc。
func readSettingsFile(path string) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("读 stdin: %w", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
