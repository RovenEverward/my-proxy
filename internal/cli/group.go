package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

// mpx group:策略组,即按地区 / 协议 / 能力 / 标签自动收进来的一组入口。

type groupFlags struct {
	regions, protocols, caps, tags []string
	mode, icon, testURL            string
	interval                       int
}

func (f *groupFlags) bind(c *cobra.Command) {
	c.Flags().StringSliceVar(&f.regions, "region", nil, "地区代码,任一即可,如 JP,US")
	c.Flags().StringSliceVar(&f.protocols, "protocol", nil, "协议短名,任一即可,如 Reality,Hy2")
	c.Flags().StringSliceVar(&f.caps, "capability", nil, "要求的能力检测,全部通过:egress / youtube / netflix")
	c.Flags().StringSliceVar(&f.tags, "tag", nil, "标签,任一即可")
	c.Flags().StringVar(&f.mode, "mode", "", "auto(自动选最快)或 manual(手动选)")
	c.Flags().StringVar(&f.icon, "icon", "", "图标")
	c.Flags().StringVar(&f.testURL, "test-url", "", "自动模式的测速地址")
	c.Flags().IntVar(&f.interval, "interval", 0, "自动模式的测速间隔(秒)")
}

// apply 只改命令行上给了的字段;传空串(如 --region "")清空。
func (f *groupFlags) apply(c *cobra.Command, g *model.ProxyGroup) {
	clean := func(v []string) []string {
		out := []string{}
		for _, x := range v {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	if c.Flags().Changed("region") {
		g.Regions = clean(f.regions)
	}
	if c.Flags().Changed("protocol") {
		g.Protocols = clean(f.protocols)
	}
	if c.Flags().Changed("capability") {
		g.Capabilities = clean(f.caps)
	}
	if c.Flags().Changed("tag") {
		g.Tags = clean(f.tags)
	}
	if c.Flags().Changed("mode") {
		g.Mode = model.GroupMode(f.mode)
	}
	if c.Flags().Changed("icon") {
		g.Icon = f.icon
	}
	if c.Flags().Changed("test-url") {
		g.TestURL = f.testURL
	}
	if c.Flags().Changed("interval") {
		g.Interval = f.interval
	}
}

func groupConds(g model.ProxyGroup) string {
	var p []string
	if len(g.Regions) > 0 {
		p = append(p, "地区 "+strings.Join(g.Regions, "/"))
	}
	if len(g.Protocols) > 0 {
		p = append(p, "协议 "+strings.Join(g.Protocols, "/"))
	}
	if len(g.Capabilities) > 0 {
		p = append(p, "能力 "+strings.Join(g.Capabilities, "+"))
	}
	if len(g.Tags) > 0 {
		p = append(p, "标签 "+strings.Join(g.Tags, "/"))
	}
	if len(p) == 0 {
		return "全部入口"
	}
	return strings.Join(p, ",")
}

func printPreview(raw []byte) error {
	if opts.output == outputJSON {
		return emitJSON(raw)
	}
	var list []struct {
		Name     string   `json:"name"`
		Protocol string   `json:"protocol"`
		Region   string   `json:"region"`
		Admitted bool     `json:"admitted"`
		Reasons  []string `json:"reasons"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	rows, n := [][]string{}, 0
	for _, e := range list {
		in := "收进"
		if e.Admitted {
			n++
		} else {
			in = "不收:" + strings.Join(e.Reasons, "、")
		}
		rows = append(rows, []string{e.Name, e.Protocol, dash(e.Region), in})
	}
	if err := writeTable([]string{"入口", "协议", "地区", "结果"}, rows); err != nil {
		return err
	}
	if n == 0 {
		fmt.Println("注意:一个入口都没收进来,订阅里这个组只会有「直连」")
	} else {
		fmt.Printf("共收进 %d 个入口\n", n)
	}
	return nil
}

func newGroupCmd() *cobra.Command {
	c := &cobra.Command{Use: "group", Short: "管理策略组", Long: `策略组是规则的去向:按地区、协议、能力检测、标签自动收进入口,新加的节点符合条件会自动进组。
mode=auto 客户端自动选最快的,mode=manual 用户在客户端里手动选。`}

	c.AddCommand(&cobra.Command{Use: "list", Short: "列出策略组", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			raw, err := cl.do("GET", "/api/proxy-groups", nil)
			if err != nil {
				return err
			}
			return emitJSON(raw)
		}
		groups, err := listGroups(cl)
		if err != nil {
			return err
		}
		rows := [][]string{}
		for _, g := range groups {
			rows = append(rows, []string{strconv.FormatInt(g.ID, 10), g.Name, string(g.Mode), groupConds(g)})
		}
		return writeTable([]string{"ID", "名称", "方式", "条件"}, rows)
	}})

	var af groupFlags
	add := &cobra.Command{Use: "add NAME", Short: "新建策略组", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		g := model.ProxyGroup{Name: args[0], Mode: model.GroupAuto}
		af.apply(cmd, &g)
		raw, err := cl.do("POST", "/api/proxy-groups", g)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		fmt.Println("已创建")
		return nil
	}}
	af.bind(add)
	c.AddCommand(add)

	var ef groupFlags
	var rename string
	edit := &cobra.Command{Use: "edit NAME|ID", Short: "修改策略组(只改给了的选项)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		g, err := findGroup(cl, args[0])
		if err != nil {
			return err
		}
		ef.apply(cmd, g)
		if rename != "" {
			g.Name = rename
		}
		raw, err := cl.do("PUT", fmt.Sprintf("/api/proxy-groups/%d", g.ID), g)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		fmt.Println("已保存")
		return nil
	}}
	ef.bind(edit)
	edit.Flags().StringVar(&rename, "name", "", "改名")
	c.AddCommand(edit)

	c.AddCommand(&cobra.Command{Use: "rm NAME|ID", Short: "删除策略组(还有规则指向它时会被拒绝)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		g, err := findGroup(cl, args[0])
		if err != nil {
			return err
		}
		if _, err := cl.do("DELETE", fmt.Sprintf("/api/proxy-groups/%d", g.ID), nil); err != nil {
			return err
		}
		fmt.Println("已删除")
		return nil
	}})

	var pf groupFlags
	preview := &cobra.Command{Use: "preview [NAME|ID]", Short: "看看这组条件会收进哪些入口(可以只给条件,不用先建组)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		g := &model.ProxyGroup{Name: "preview", Mode: model.GroupAuto}
		if len(args) == 1 {
			if g, err = findGroup(cl, args[0]); err != nil {
				return err
			}
		}
		pf.apply(cmd, g)
		raw, err := cl.do("POST", "/api/proxy-groups/preview", g)
		if err != nil {
			return err
		}
		return printPreview(raw)
	}}
	pf.bind(preview)
	c.AddCommand(preview)
	return c
}
