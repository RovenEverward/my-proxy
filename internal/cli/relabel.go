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

// doer 是 relabelPlan / relabelApply 要用到的最小客户端接口:*Client 满足它,
// page 那边的运行器也能拿一个临时 client 进来 —— 计算逻辑 CLI 与页面共用一份。
type doer interface {
	do(method, path string, body any) ([]byte, error)
}

// relabelRow 是对照表的一行。JSON 标签给页面用(ui 包自己有同形的类型,
// 不 import cli)。
type relabelRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"` // 内部名
	Old  string `json:"old"`  // 旧名(没有显示名时就是内部名)
	New  string `json:"new"`  // 新显示名;"-" 表示这行不写入
	Note string `json:"note"` // 依据或跳过原因
}

// relabelPlanItem 是一条待写入计划:apply 只写这份 metadata,绝不重新 locate。
type relabelPlanItem struct {
	ID       int64                 `json:"id"`
	Name     string                `json:"name"`
	Metadata model.PurposeMetadata `json:"metadata"`
}

// relabelResult 是 apply 里一个节点的结果。Skipped 为真时 Reason 说明原因。
type relabelResult struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Display string `json:"display,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// relabelPlan 是 CLI 与页面共用的对照表计算:对每个目标节点做一次 locate
// (ipwho.is),字母按节点 ID 顺序分配;已经有显示名的跳过。返回对照表与要写入
// 的那份 metadata。
func relabelPlan(c doer, views, targets []model.NodeView) ([]relabelRow, []relabelPlanItem, error) {
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })

	// 字母要和控制面分配的一致:先收齐各组已用的字母,再按 ID 顺序模拟。
	meta := map[int64]model.PurposeMetadata{}
	used := map[string]map[string]bool{}
	for _, v := range views {
		raw, err := c.do("GET", nodePath(v.ID)+"/metadata", nil)
		if err != nil {
			return nil, nil, err
		}
		m, err := decode[model.PurposeMetadata](raw)
		if err != nil {
			return nil, nil, err
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

	var rows []relabelRow
	var plans []relabelPlanItem
	seen := map[int64]bool{}
	for _, v := range targets {
		if seen[v.ID] {
			continue
		}
		seen[v.ID] = true
		if v.Display != "" {
			rows = append(rows, relabelRow{ID: v.ID, Name: v.Name, Old: v.Display, New: "-", Note: "已有显示名,跳过"})
			continue
		}
		raw, err := c.do("POST", nodePath(v.ID)+"/locate", nil)
		if err != nil {
			rows = append(rows, relabelRow{ID: v.ID, Name: v.Name, Old: v.Name, New: "-", Note: "识别失败:" + err.Error()})
			continue
		}
		sg, err := decode[locateSuggestion](raw)
		if err != nil {
			return nil, nil, err
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
		rows = append(rows, relabelRow{ID: v.ID, Name: v.Name, Old: v.Name, New: model.NodeDisplay(m), Note: note})
		plans = append(plans, relabelPlanItem{ID: v.ID, Name: v.Name, Metadata: m})
	}
	return rows, plans, nil
}

// relabelApply 写回计划里的那份 metadata。写之前对每个 id 重新读一次:该节点
// 此时已经有显示名(或地区跟计划不一样)就跳过 —— 页面看到的和现在写的必须一致,
// 别人中途改过的值不能被覆盖。
func relabelApply(c doer, plans []relabelPlanItem) ([]relabelResult, error) {
	results := make([]relabelResult, 0, len(plans))
	for _, p := range plans {
		raw, err := c.do("GET", nodePath(p.ID)+"/metadata", nil)
		if err != nil {
			return results, err
		}
		cur, err := decode[model.PurposeMetadata](raw)
		if err != nil {
			return results, err
		}
		if model.NodeDisplay(cur) != "" || (strings.TrimSpace(cur.Region) != "" && cur.Region != p.Metadata.Region) {
			results = append(results, relabelResult{ID: p.ID, Name: p.Name, Skipped: true, Reason: "已被改过,跳过"})
			continue
		}
		raw, err = c.do("PUT", nodePath(p.ID)+"/metadata", explicitMetadata(p.Metadata))
		if err != nil {
			return results, fmt.Errorf("节点 %d(%s)写入失败: %w", p.ID, p.Name, err)
		}
		saved, err := decode[model.PurposeMetadata](raw)
		if err != nil {
			return results, err
		}
		results = append(results, relabelResult{ID: p.ID, Name: p.Name, Display: model.NodeDisplay(saved)})
	}
	return results, nil
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

		rows, plans, err := relabelPlan(c, views, targets)
		if err != nil {
			return err
		}
		tableRows := make([][]string, 0, len(rows))
		for _, r := range rows {
			tableRows = append(tableRows, []string{strconv.FormatInt(r.ID, 10), r.Name, r.Old, r.New, r.Note})
		}
		if err := writeTable([]string{"ID", "内部名", "旧名", "新名", "依据"}, tableRows); err != nil {
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
		results, err := relabelApply(c, plans)
		if err != nil {
			return err
		}
		for _, r := range results {
			if r.Skipped {
				fmt.Printf("节点 %d(%s)%s\n", r.ID, r.Name, r.Reason)
				continue
			}
			fmt.Printf("节点 %d(%s)→ %s\n", r.ID, r.Name, r.Display)
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
