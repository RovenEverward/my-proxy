package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// reachCmd 是自动剔除的操作口(UC-12):全局开关、探测周期、按对象的保留
// 豁免。判定数据本身由探测点与 mpx node check 攒,这里只管「通不通要不要
// 变成摘不摘」。
var reachCmd = &cobra.Command{
	Use:   "reach",
	Short: "自动剔除不通的节点与外部端点(UC-12)",
	Long: `触达检查攒出每个对象的判定(双通/境内不通/都不通/未知)之后,
reach 命令决定要不要把它用进订阅:exclude 开启后,判定为「境内不通」或
「都不通」且判定没过期的对象不再出现在订阅里;keep 给单个对象开豁免,
判定再差也保留。

剔除是读时计算,不设单独的名单:开关、豁免、判定任意一项变了,下一次
订阅请求立即生效。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		s, err := readSettings(c)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			raw, err := c.do("GET", "/api/reach", nil)
			if err != nil {
				return err
			}
			return emitJSON(raw)
		}
		onoff := "关"
		if s.ReachExclude {
			onoff = "开"
		}
		return writeKV([][2]string{
			{"自动剔除", onoff},
			{"探测周期", strconv.Itoa(s.ReachInterval()) + " 分钟"},
			{"保留豁免", strconv.Itoa(len(s.ReachKeeps)) + " 个对象"},
		})
	},
}

var reachExcludeCmd = &cobra.Command{
	Use:   "exclude <on|off>",
	Short: "开/关「不通就下架」",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := onOff(args[0])
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		s, err := readSettings(c)
		if err != nil {
			return err
		}
		s.ReachExclude = v
		if _, err := saveSettings(c, s); err != nil {
			return err
		}
		if v {
			fmt.Println("自动剔除已开:判定为不通且没过期的对象不再进入订阅。")
		} else {
			fmt.Println("自动剔除已关:订阅恢复包含全部对象,判定数据保留。")
		}
		return nil
	},
}

var reachIntervalCmd = &cobra.Command{
	Use:   "interval <分钟>",
	Short: "设置探测周期(分钟;判定过期窗口固定 24 小时)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := strconv.Atoi(args[0])
		if err != nil || m <= 0 || m > 24*60 {
			return errors.New("探测周期须为 1–1440 分钟")
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		s, err := readSettings(c)
		if err != nil {
			return err
		}
		s.ReachIntervalMinutes = m
		if _, err := saveSettings(c, s); err != nil {
			return err
		}
		fmt.Printf("探测周期已设为 %d 分钟。\n", m)
		return nil
	},
}

var reachKeepCmd = &cobra.Command{
	Use:   "keep <对象>",
	Short: "给对象开剔除豁免(对象身份见 mpx reach 或页面上的「保留」)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return reachKeep(args[0], true)
	},
}

var reachUnkeepCmd = &cobra.Command{
	Use:   "unkeep <对象>",
	Short: "取消对象的剔除豁免",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return reachKeep(args[0], false)
	},
}

// reachKeep 调 PUT /api/reach/keep。对象身份两种:node:<节点ID> 与
// s:<来源ID>:<指纹>;命令行上给节点 ID 数字也认,省得抄前缀。
func reachKeep(subject string, keep bool) error {
	subject = strings.TrimSpace(subject)
	if id, err := strconv.ParseInt(subject, 10, 64); err == nil && id > 0 {
		subject = string(model.NodeSubjectKey(id))
	}
	if subject == "" {
		return errors.New("缺少对象身份")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	raw, err := c.do("PUT", "/api/reach/keep", map[string]any{"subject": subject, "keep": keep})
	if err != nil {
		return err
	}
	if opts.output == outputJSON {
		return emitJSON(raw)
	}
	if keep {
		fmt.Printf("已保留 %s:判定再差也不会被剔除。\n", subject)
	} else {
		fmt.Printf("已取消保留 %s:重新按判定参与剔除。\n", subject)
	}
	return nil
}

// onOff 把命令行里的开关词翻成布尔:on/off、1/0、true/false、开/关 都认。
func onOff(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "1", "true", "开", "开启", "enable", "enabled":
		return true, nil
	case "off", "0", "false", "关", "关闭", "disable", "disabled":
		return false, nil
	}
	return false, fmt.Errorf("开关只认 on/off: %q", s)
}

func init() {
	reachCmd.AddCommand(reachExcludeCmd, reachIntervalCmd, reachKeepCmd, reachUnkeepCmd)
	rootCmd.AddCommand(reachCmd)
}
