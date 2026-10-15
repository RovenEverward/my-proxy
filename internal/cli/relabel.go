package cli

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

var nodeRelabelFlags struct {
	all, dryRun, yes bool
}

var nodeRelabelCmd = &cobra.Command{
	Use:   "relabel [节点...]",
	Short: "给还没有显示名的节点批量识别地区/城市,先出对照表再确认写入",
	Long: `给还没有显示名的节点批量起显示名(docs/internal/node-naming-rfc.md §6)。

对每个目标节点做一次 locate(ipwho.is),字母按节点 ID 顺序分配(la-1 是 A、la-2 是 B),
先打印「旧名 → 新名」对照表,确认后才写入。已经有显示名的节点一律跳过,用户写过的
值不会被覆盖;要改用 mpx node edit。

升级时不会自动执行:它要访问外网,结果也需要人看一眼。写入后客户端里的节点名会变
一次(订阅默认名改用显示名),节点的内部名不变,脚本照常能用。

  mpx node relabel --all --dry-run    # 只看对照表
  mpx node relabel --all              # 看完确认写入
  mpx node relabel la-1 la-2 --yes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && !nodeRelabelFlags.all {
			return fmt.Errorf("要么列出节点,要么加 --all")
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		views, err := listNodes(c)
		if err != nil {
			return err
		}
		var targets []model.NodeView
		if nodeRelabelFlags.all {
			targets = views
		} else {
			byID := map[int64]model.NodeView{}
			for _, v := range views {
				byID[v.ID] = v
			}
			for _, a := range args {
				cand, _, err := pick("节点", a, nodeCandidates(views))
				if err != nil {
					return err
				}
				targets = append(targets, byID[cand.ID])
			}
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })

		// 字母要和控制面分配的一致:先收齐各组已用的字母,再按 ID 顺序模拟。
		meta := map[int64]model.PurposeMetadata{}
		used := map[string]map[string]bool{}
		for _, v := range views {
			raw, err := c.do("GET", nodePath(v.ID)+"/metadata", nil)
			if err != nil {
				return err
			}
			m, err := decode[model.PurposeMetadata](raw)
			if err != nil {
				return err
			}
			meta[v.ID] = m
			if m.Letter != "" && strings.TrimSpace(m.Region) != "" {
				g := model.LetterGroup(m)
				if used[g] == nil {
					used[g] = map[string]bool{}
				}
				used[g][m.Letter] = true
			}
		}

		type plan struct {
			v model.NodeView
			m model.PurposeMetadata
		}
		var plans []plan
		var rows [][]string
		seen := map[int64]bool{}
		for _, v := range targets {
			if seen[v.ID] {
				continue
			}
			seen[v.ID] = true
			if v.Display != "" {
				rows = append(rows, []string{strconv.FormatInt(v.ID, 10), v.Name, v.Display, "-", "已有显示名,跳过"})
				continue
			}
			raw, err := c.do("POST", nodePath(v.ID)+"/locate", nil)
			if err != nil {
				rows = append(rows, []string{strconv.FormatInt(v.ID, 10), v.Name, v.Name, "-", "识别失败:" + err.Error()})
				continue
			}
			sg, err := decode[locateSuggestion](raw)
			if err != nil {
				return err
			}
			m := meta[v.ID]
			m.Region, m.City = sg.Region, sg.City
			g := model.LetterGroup(m)
			if used[g] == nil {
				used[g] = map[string]bool{}
			}
			m.Letter = model.NextLetter(used[g])
			used[g][m.Letter] = true
			note := "ipwho.is " + sg.IP
			if sg.City == "" && sg.CityEn != "" {
				note += "(" + sg.CityEn + " 不在对照表,只给国家)"
			}
			rows = append(rows, []string{strconv.FormatInt(v.ID, 10), v.Name, v.Name, model.NodeDisplay(m), note})
			plans = append(plans, plan{v, m})
		}
		if err := writeTable([]string{"ID", "内部名", "旧名", "新名", "依据"}, rows); err != nil {
			return err
		}
		if len(plans) == 0 {
			fmt.Println("没有要写入的节点。")
			return nil
		}
		if nodeRelabelFlags.dryRun {
			fmt.Printf("--dry-run:没有写入。去掉 --dry-run 写入这 %d 个节点。\n", len(plans))
			return nil
		}
		if !nodeRelabelFlags.yes {
			fmt.Printf("写入以上 %d 个节点?[y/N] ", len(plans))
			ans, err := readLine(os.Stdin)
			if err != nil {
				return err
			}
			if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
				fmt.Println("已取消,没有写入。")
				return nil
			}
		}
		for _, p := range plans {
			raw, err := c.do("PUT", nodePath(p.v.ID)+"/metadata", explicitMetadata(p.m))
			if err != nil {
				return fmt.Errorf("节点 %d(%s)写入失败: %w", p.v.ID, p.v.Name, err)
			}
			saved, err := decode[model.PurposeMetadata](raw)
			if err != nil {
				return err
			}
			fmt.Printf("节点 %d(%s)→ %s\n", p.v.ID, p.v.Name, model.NodeDisplay(saved))
		}
		return nil
	},
}

func init() {
	f := nodeRelabelCmd.Flags()
	f.BoolVar(&nodeRelabelFlags.all, "all", false, "所有还没有显示名的节点")
	f.BoolVar(&nodeRelabelFlags.dryRun, "dry-run", false, "只打印对照表,不写入")
	f.BoolVar(&nodeRelabelFlags.yes, "yes", false, "不再询问,直接写入")
	nodeCmd.AddCommand(nodeRelabelCmd)
}
