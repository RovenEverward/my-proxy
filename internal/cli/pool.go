package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// poolRow 是 /api/pool 的一行(清洗池视图,UC-19)。
type poolRow struct {
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	Origin        string `json:"origin"`
	SourceID      int64  `json:"source_id"`
	Region        string `json:"region"`
	Excluded      bool   `json:"excluded"`
	ExcludeReason string `json:"exclude_reason"`
}

type poolView struct {
	Rows     []poolRow `json:"rows"`
	Total    int       `json:"total"`
	Included int       `json:"included"`
	Excluded int       `json:"excluded"`
}

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use: "pool", Short: "查看清洗池:全部端点的统一视图与剔除原因",
		Long: `清洗池是订阅装配读数的唯一视图(UC-19):自建与外部端点过同一管线(命名+活性判定)后落在这里。
剔除不删行 —— 标 Excluded 的端点仍在池里,带着它为什么不在订阅里的原因(活性判定或排水)。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			raw, err := cl.do("GET", "/api/pool", nil)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			var v poolView
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			rows := [][]string{}
			for _, r := range v.Rows {
				state := "可用"
				if r.Excluded {
					state = "剔除:" + r.ExcludeReason
				}
				rows = append(rows, []string{
					r.Name, r.Protocol, r.Origin,
					fmt.Sprintf("%s:%d", r.Address, r.Port),
					r.Region, state,
				})
			}
			if err := writeTable([]string{"名称", "协议", "来源", "地址", "地区", "状态"}, rows); err != nil {
				return err
			}
			fmt.Printf("\n清洗池:共 %d 个端点,订阅实际输出 %d 个,剔除 %d 个\n", v.Total, v.Included, v.Excluded)
			return nil
		},
	})
}
