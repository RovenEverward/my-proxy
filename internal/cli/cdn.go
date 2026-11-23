package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

type cdnPoolEntry struct {
	IP        string    `json:"ip"`
	LatencyMs int       `json:"latency_ms"`
	Loss      float64   `json:"loss"`
	SpeedMbps float64   `json:"speed_mbps"`
	Colo      string    `json:"colo"`
	Misses    int       `json:"misses"`
	LastSeen  time.Time `json:"last_seen"`
}

type cdnPoolView struct {
	Enabled bool           `json:"enabled"`
	Pool    []cdnPoolEntry `json:"pool"`
	Size    int            `json:"size"`
	Target  int            `json:"target"`
	Updated string         `json:"updated"`
}

func init() {
	c := &cobra.Command{
		Use: "cdn", Short: "CDN IP 优选:查看优选池与开关",
		Long: `CDN IP 优选(UC-21):境内探测点每小时对 Cloudflare IP 段做
TCP 粗扫 + CF-RAY 验证,选出的真边缘 IP 自动挂成各 CDN 入站的优选
Host(kind=cdn),手工 Host 不受影响。`,
	}
	c.AddCommand(&cobra.Command{
		Use: "pool", Short: "查看当前优选池", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			raw, err := cl.do("GET", "/api/cdn-pool", nil)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			var v cdnPoolView
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if !v.Enabled {
				fmt.Println("CDN 优选未开启(mpx settings 里打开 cdn_optimize)")
				return nil
			}
			rows := [][]string{}
			for _, e := range v.Pool {
				state := "在池"
				if e.Misses > 0 {
					state = fmt.Sprintf("miss %d", e.Misses)
				}
				speed := "-"
				if e.SpeedMbps > 0 {
					speed = fmt.Sprintf("%.1fMB/s", e.SpeedMbps)
				}
				rows = append(rows, []string{e.IP, e.Colo,
					fmt.Sprintf("%dms", e.LatencyMs),
					fmt.Sprintf("%.0f%%", e.Loss*100), speed, state,
					e.LastSeen.Format("01-02 15:04")})
			}
			if err := writeTable([]string{"IP", "机房", "延迟", "丢包", "速度", "状态", "最近命中"}, rows); err != nil {
				return err
			}
			fmt.Printf("\n优选池 %d 个 IP(目标 %d),按速度排序,最近更新 %s\n", v.Size, v.Target, v.Updated)
			return nil
		},
	})
	rootCmd.AddCommand(c)
}
