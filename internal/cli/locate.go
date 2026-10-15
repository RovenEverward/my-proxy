package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// locateSuggestion 对应控制面 POST /api/nodes/{id}/locate 的回答。
type locateSuggestion struct {
	Region  string `json:"region"`
	City    string `json:"city"`
	CityEn  string `json:"city_en"`
	IP      string `json:"ip"`
	Source  string `json:"source"`
	Via     string `json:"via"`
	Display string `json:"display"`
}

var nodeLocateFlags struct{ apply bool }

var nodeLocateCmd = &cobra.Command{
	Use:   "locate <节点>",
	Short: "按节点 IP 给出地区/城市建议(ipwho.is),--apply 才写入",
	Long: `按节点 IP 问一次 ipwho.is,给出显示名的地区和城市建议。默认只显示建议、不写入;
确认无误再加 --apply,或者用 mpx node edit 手动改。

节点地址是 IP 时由控制面去查;是域名时控制面会 SSH 到节点上探测它的出口 IP
(节点上要有 curl)。节点 IP 会发给 ipwho.is 这个第三方。

城市不在内置对照表里时只给国家,显示成「🇺🇸 美国 A」,可以用 --city 补上。

  mpx node locate la-1
  mpx node locate la-1 --apply`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolveNodeID(args[0], nodeLocateFlags.apply)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", nodePath(id)+"/locate", nil)
		if err != nil {
			return err
		}
		sg, err := decode[locateSuggestion](raw)
		if err != nil {
			return err
		}
		if !nodeLocateFlags.apply {
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			printLocate(sg, args[0])
			return nil
		}
		nodeEditFlags.region, nodeEditFlags.city = sg.Region, sg.City
		return editNodeMetadata(c, id, true, true, false)
	},
}

func printLocate(sg locateSuggestion, arg string) {
	via := "依据节点 IP"
	if sg.Via == "node" {
		via = "依据节点出口 IP"
	}
	fmt.Printf("建议:%s(%s,%s %s)\n", sg.Display, sg.Source, via, sg.IP)
	if sg.City == "" && sg.CityEn != "" {
		fmt.Printf("城市 %s 不在对照表里,只给了国家;要补上用 --city。\n", sg.CityEn)
	}
	city := sg.City
	if city == "" {
		city = "<城市>"
	}
	fmt.Printf("写入:mpx node locate %s --apply  或  mpx node edit %s --region %s --city %s\n", arg, arg, sg.Region, city)
}

func init() {
	nodeLocateCmd.Flags().BoolVar(&nodeLocateFlags.apply, "apply", false, "把建议写进节点元数据(字母自动分配)")
	nodeCmd.AddCommand(nodeLocateCmd)
}
