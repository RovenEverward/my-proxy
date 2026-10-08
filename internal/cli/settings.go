package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "查看与修改订阅设置",
}

func init() {
	settingsCmd.AddCommand(settingsShowCmd, settingsSetCmd, settingsTemplateCmd)
	settingsSetCmd.Flags().StringVarP(&settingsSetFile, "file", "f", "", "设置 JSON 文件;- 表示从 stdin 读")
	settingsTemplateCmd.AddCommand(settingsTemplateGetCmd, settingsTemplateSetCmd, settingsTemplateDelCmd)
	settingsTemplateSetCmd.Flags().StringVarP(&settingsTemplateFile, "file", "f", "", "模板文件;- 表示从 stdin 读")
}

var settingsSetFile string

var settingsTemplateCmd = &cobra.Command{
	Use:   "template",
	Short: "查看与修改某格式的客户端模板",
}

var settingsTemplateFile string

var settingsTemplateGetCmd = &cobra.Command{
	Use:   "get FORMAT",
	Short: "显示某格式当前生效的模板",
	Long: `显示某格式当前生效的模板正文。

FORMAT 是 clash-meta / sing-box / xray 之一。没存自定义模板时显示内置默认
模板的副本,方便拿它当改动的起点。正文原样输出,可以重定向到文件。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !validTemplateFormat(args[0]) {
			return fmt.Errorf("不支持的模板格式 %q:只能是 clash-meta / sing-box / xray", args[0])
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/settings/templates/"+args[0], nil)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(raw)
		return err
	},
}

var settingsTemplateSetCmd = &cobra.Command{
	Use:   "set FORMAT -f FILE",
	Short: "保存某格式的自定义模板",
	Long: `保存某格式的自定义模板,此后该格式的订阅按这份模板渲染。

FORMAT 是 clash-meta / sing-box / xray 之一。正文从 -f 指定的文件读,或用
-f - 从 stdin 读。服务端会先校验语法,不合法就不存。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !validTemplateFormat(args[0]) {
			return fmt.Errorf("不支持的模板格式 %q:只能是 clash-meta / sing-box / xray", args[0])
		}
		if settingsTemplateFile == "" {
			return errors.New("必须给 -f FILE(或用 -f - 从 stdin 读)")
		}
		data, err := readSettingsFile(settingsTemplateFile)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("PUT", "/api/settings/templates/"+args[0], rawBody(data))
		if err != nil {
			return err
		}
		return report(raw, "已保存 %s 模板", args[0])
	},
}

var settingsTemplateDelCmd = &cobra.Command{
	Use:   "del FORMAT",
	Short: "删除某格式的自定义模板,回落到内置默认",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !validTemplateFormat(args[0]) {
			return fmt.Errorf("不支持的模板格式 %q:只能是 clash-meta / sing-box / xray", args[0])
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("DELETE", "/api/settings/templates/"+args[0], nil)
		if err != nil {
			return err
		}
		return report(raw, "已删除 %s 模板", args[0])
	},
}

func validTemplateFormat(f string) bool {
	switch f {
	case "clash-meta", "sing-box", "xray":
		return true
	}
	return false
}

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
		if err := writeTable([]string{"序号", "匹配结果", "User-Agent 正则"}, rows); err != nil {
			return err
		}

		// 模板正文太长,不进表格;只报哪些格式用了自定义模板。
		custom := make([]string, 0, len(s.Templates))
		for _, f := range []string{"clash-meta", "sing-box", "xray"} {
			if s.Templates[f] != "" {
				custom = append(custom, f)
			}
		}
		fmt.Println()
		if len(custom) == 0 {
			fmt.Println("客户端模板:全部用内置默认")
		} else {
			fmt.Printf("客户端模板:自定义 %s(其余用内置默认;看正文用 mpx settings template get)\n",
				strings.Join(custom, " / "))
		}
		return nil
	},
}

var settingsSetCmd = &cobra.Command{
	Use:   "set -f FILE",
	Short: "用一份 JSON 整体替换订阅设置",
	Long: `用一份 JSON 整体替换订阅设置。

JSON 的形状与 mpx -o json settings show 的输出一致(model.Settings)。先在本
地解一遍是为了把语法错误指到文件上,发出去的仍是文件里的原始内容。

注意这是整体替换:文件里没写的字段(包括 templates)会被清掉。只想改某一
份模板用 mpx settings template set,别走这里。`,
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
