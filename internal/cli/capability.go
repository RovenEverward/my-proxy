package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

func init() {
	nodeCmd.AddCommand(newCapabilityProbeCmd(), newCapabilitiesCmd(), newCapabilitySetCmd())
}

func capabilityFlags(cmd *cobra.Command, scheduled bool) {
	cmd.Flags().Var(inboundFlag(new(int64)), "inbound", "检测使用的入站,必填(id、节点/tag 或唯一 tag)")
	cmd.Flags().Int64("host", 0, "检测使用的入口 ID;0 仅用于没有显式入口的入站")
	cmd.Flags().StringSlice("targets", append([]string(nil), model.CapabilityTargets...), "检测目标:egress,youtube,netflix")
	if scheduled {
		cmd.Flags().Bool("enabled", false, "启用定时检测;--enabled=false 停止检测")
		cmd.Flags().Duration("interval", time.Hour, "检测间隔,5m–24h")
		cmd.Flags().Duration("ttl", 3*time.Hour, "结果有效期,不小于间隔且不超过 168h")
	}
}

func capabilityConfigFromFlags(cmd *cobra.Command, scheduled bool) (model.CapabilityConfig, error) {
	inbound, _ := strconv.ParseInt(cmd.Flags().Lookup("inbound").Value.String(), 10, 64)
	host, _ := cmd.Flags().GetInt64("host")
	targets, _ := cmd.Flags().GetStringSlice("targets")
	cfg := model.CapabilityConfig{InboundID: inbound, HostID: host, Targets: targets}
	if scheduled {
		cfg.Enabled, _ = cmd.Flags().GetBool("enabled")
		interval, _ := cmd.Flags().GetDuration("interval")
		ttl, _ := cmd.Flags().GetDuration("ttl")
		// Keep exact second values; duration limits belong to the model validator.
		if interval <= 0 || ttl <= 0 || interval%time.Second != 0 || ttl%time.Second != 0 {
			return cfg, fmt.Errorf("--interval 和 --ttl 必须为正的整秒时长")
		}
		cfg.IntervalSeconds = int(interval / time.Second)
		cfg.TTLSeconds = int(ttl / time.Second)
	}
	return cfg, cfg.Validate()
}

func newCapabilityProbeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "probe <节点>", Short: "通过指定入站和入口现场检测节点能力",
		Long:    "通过实际代理入口检测出口、YouTube 和 Netflix。手动检测只保存结果,不会启用定时检测。稳定状态需要连续两次一致观测。",
		Args:    cobra.ExactArgs(1),
		Example: "  mpx node probe 1 --inbound 2 --host 3 --targets egress,youtube,netflix",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveNodeID(args[0], false)
			if err != nil {
				return err
			}
			cfg, err := capabilityConfigFromFlags(cmd, false)
			if err != nil {
				return err
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			raw, err := c.doWithin(3*time.Minute, "POST", nodePath(id)+"/capabilities/probe", cfg)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return capabilityJSON(cmd, raw)
			}
			results, err := decode[[]model.CapabilityResult](raw)
			if err != nil {
				return err
			}
			printCapabilityResults(cmd, results)
			return nil
		},
	}
	capabilityFlags(cmd, false)
	return cmd
}

func newCapabilitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use: "capabilities <节点>", Short: "查看节点的能力检测配置和结果", Args: cobra.ExactArgs(1),
		Example: "  mpx node capabilities 1\n  mpx node capabilities 1 --output json",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveNodeID(args[0], false)
			if err != nil {
				return err
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			raw, err := c.do("GET", nodePath(id)+"/capabilities", nil)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return capabilityJSON(cmd, raw)
			}
			view, err := decode[struct {
				Configs []model.CapabilityConfig `json:"configs"`
				Results []model.CapabilityResult `json:"results"`
			}](raw)
			if err != nil {
				return err
			}
			cmd.Println("检测配置")
			rows := make([][]string, 0, len(view.Configs))
			for _, cfg := range view.Configs {
				rows = append(rows, []string{strconv.FormatInt(cfg.InboundID, 10), strconv.FormatInt(cfg.HostID, 10), yesNo(cfg.Enabled), strings.Join(cfg.Targets, ","), (time.Duration(cfg.IntervalSeconds) * time.Second).String(), (time.Duration(cfg.TTLSeconds) * time.Second).String()})
			}
			cmd.Print(tableText([]string{"入站", "入口", "定时检测", "目标", "间隔", "有效期"}, rows))
			cmd.Println("检测结果")
			printCapabilityResults(cmd, view.Results)
			return nil
		},
	}
}

func newCapabilitySetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "capability-set <节点>", Short: "保存指定入口的定时能力检测配置", Args: cobra.ExactArgs(1),
		Long:    "保存整份检测配置。省略选项使用默认值;--enabled 默认 false。此命令不会立即检测。",
		Example: "  mpx node capability-set 1 --inbound 2 --host 3 --enabled --interval 1h --ttl 3h\n  mpx node capability-set 1 --inbound 2 --host 3 --enabled=false",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveNodeID(args[0], true)
			if err != nil {
				return err
			}
			cfg, err := capabilityConfigFromFlags(cmd, true)
			if err != nil {
				return err
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			raw, err := c.do("PUT", nodePath(id)+"/capabilities", cfg)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return capabilityJSON(cmd, raw)
			}
			cmd.Printf("已保存节点 %d 入站 %d/入口 %d 的检测配置,定时检测:%s\n", id, cfg.InboundID, cfg.HostID, yesNo(cfg.Enabled))
			return nil
		},
	}
	capabilityFlags(cmd, true)
	return cmd
}

func capabilityJSON(cmd *cobra.Command, raw []byte) error {
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, bytes.TrimSpace(raw), "", "  "); err != nil {
		return err
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), formatted.String())
	return err
}

func printCapabilityResults(cmd *cobra.Command, results []model.CapabilityResult) {
	rows := make([][]string, 0, len(results))
	for _, r := range results {
		rows = append(rows, []string{strconv.FormatInt(r.InboundID, 10), strconv.FormatInt(r.HostID, 10), r.Target, string(r.Status), string(r.ObservedStatus), dash(r.Region), dash(r.ExitIP), capabilityTime(r.CheckedAt), capabilityTime(r.ExpiresAt), dash(r.Location), dash(r.Detector), dash(r.Reason)})
	}
	cmd.Print(tableText([]string{"入站", "入口", "目标", "稳定状态", "原始观测", "地区", "出口 IP", "检查时间", "到期时间", "检测位置", "检测器", "原因"}, rows))
}

func capabilityTime(at time.Time) string {
	if at.IsZero() {
		return "从未"
	}
	return at.Format(time.RFC3339)
}

func capabilitySummary(results []model.CapabilityResult) string {
	if len(results) == 0 {
		return "未检测"
	}
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("%d/%d %s:%s", r.InboundID, r.HostID, r.Target, r.Status))
	}
	return strings.Join(parts, "; ")
}
