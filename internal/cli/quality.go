package cli

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

// nodeQualityWire 对应控制面返回的 model.NodeQuality;明细 report 展开成
// QualityReport 渲染表格。
type nodeQualityWire struct {
	NodeID    int64               `json:"node_id"`
	CheckedAt string              `json:"checked_at"`
	Verdict   string              `json:"verdict"`
	Score     int                 `json:"score"`
	Report    model.QualityReport `json:"report"`
}

// regradePacket 对应 POST /api/regrade 的回答。
type regradePacket struct {
	Rows    []serverRegradeRow `json:"rows"`
	Applied bool               `json:"applied,omitempty"`
}

type serverRegradeRow struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Display string `json:"display,omitempty"`
	Region  string `json:"region,omitempty"`
	City    string `json:"city,omitempty"`
	Score   *int   `json:"score,omitempty"`
	Verdict string `json:"verdict"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Note    string `json:"note,omitempty"`
	Changed bool   `json:"changed"`
}

var nodeGradeCmd = &cobra.Command{
	Use:   "grade [节点]",
	Short: "在节点上探测出口 IP 质量并打分(ipapi.is + 可选 AbuseIPDB)",
	Long: `对节点做一次 IP 质量探测并打分。

控制面经 SSH 在节点上查出口 IP、ipwho.is 地区、ipapi.is 风险标记(机房/VPN/代理/
Tor/滥用),控制面配了 MPX_ABUSEIPDB_KEY 时再查 AbuseIPDB。基准 100 分按标记扣分,
结果存档,供 mpx node regrade 排名。

不给节点名时探测全部节点;节点上要有 curl。探测会把节点出口 IP 发给上述第三方;
配了 MPX_IPAPI_IS_KEY 时 ipapi.is 走密钥请求。分数只反映 IP 信誉,不代表速度或
解锁能力。

  mpx node grade          # 全部节点
  mpx node grade la-1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		var targets []model.NodeView
		if len(args) > 0 {
			views, err := listNodes(c)
			if err != nil {
				return err
			}
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
		} else {
			targets, err = listNodes(c)
			if err != nil {
				return err
			}
		}
		if len(targets) == 0 {
			fmt.Println("没有节点。")
			return nil
		}
		type result struct {
			view model.NodeView
			q    nodeQualityWire
			err  error
		}
		results := make([]result, len(targets))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for i, v := range targets {
			wg.Add(1)
			go func(i int, v model.NodeView) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				raw, err := c.doWithin(60e9, "POST", nodePath(v.ID)+"/quality", nil)
				if err != nil {
					results[i] = result{view: v, err: err}
					return
				}
				q, err := decode[nodeQualityWire](raw)
				results[i] = result{view: v, q: q, err: err}
			}(i, v)
		}
		wg.Wait()
		sort.Slice(results, func(i, j int) bool { return results[i].view.ID < results[j].view.ID })
		if opts.output == outputJSON {
			out := make([]nodeQualityWire, 0, len(results))
			for _, r := range results {
				if r.err == nil {
					out = append(out, r.q)
				}
			}
			return emitJSONValue(out)
		}
		rows := make([][]string, 0, len(results))
		failed := 0
		for _, r := range results {
			name := r.view.Name
			if r.view.Display != "" {
				name += " " + r.view.Display
			}
			if r.err != nil {
				failed++
				rows = append(rows, []string{strconv.FormatInt(r.view.ID, 10), name, "-", "-", "-", "-", "探测失败: " + r.err.Error()})
				continue
			}
			rep := r.q.Report
			score := "-"
			marks := "-"
			if r.q.Verdict != "untested" {
				score = strconv.Itoa(r.q.Score)
				marks = flagSummary(rep.Flags)
				if rep.AbuseScore != nil && *rep.AbuseScore > 0 {
					if marks == "-" {
						marks = ""
					}
					marks += "滥用" + strconv.Itoa(*rep.AbuseScore) + "分"
				}
			}
			region := rep.Region
			if region == "" {
				region = "-"
			}
			verdict := map[string]string{"ok": "正常", "partial": "部分失败", "untested": "未测"}[r.q.Verdict]
			if r.q.Verdict == "untested" && rep.Error != "" {
				verdict = "未测:" + rep.Error
			}
			rows = append(rows, []string{strconv.FormatInt(r.view.ID, 10), name, orDash(rep.EgressIP), region, score, marks, verdict})
		}
		if err := writeTable([]string{"ID", "节点", "出口 IP", "地区", "分数", "标记", "判定"}, rows); err != nil {
			return err
		}
		if failed > 0 {
			return fmt.Errorf("%d 台节点探测失败", failed)
		}
		fmt.Println("按分重排字母:mpx node regrade")
		return nil
	},
}

// flagSummary 把风险标记翻成短词,表里没有命中时返回 "-"。
func flagSummary(flags map[string]bool) string {
	names := map[string]string{
		"is_datacenter": "机房", "is_vpn": "VPN", "is_proxy": "代理",
		"is_tor": "Tor", "is_abuser": "滥用", "is_bogon": "bogon",
	}
	var out []string
	for _, k := range []string{"is_tor", "is_bogon", "is_abuser", "is_proxy", "is_vpn", "is_datacenter"} {
		if flags[k] {
			out = append(out, names[k])
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ",")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var nodeRegradeFlags struct{ dryRun, yes bool }

var nodeRegradeCmd = &cobra.Command{
	Use:   "regrade",
	Short: "按质量分重排同地区的节点字母,先出对照表再确认写入",
	Long: `按最近一次 IP 质量探测的分数,给同地区同城市的节点重新排字母:分最高的拿 A。

先打印对照表(节点、分数、新旧显示名),确认后才写入;字母就是订阅里用户看到的
节点名,重排等于批量改名。未测出分的节点保留原字母;已设地区与探测结果不一致
的节点不参与重排,只提示。地区为空或不是标准国家码的节点会顺带归一化。

先跑 mpx node grade 刷新分数再来重排;不重新探测,只用存档的分。

  mpx node regrade --dry-run   # 只看对照表
  mpx node regrade             # 看完确认写入
  mpx node regrade --yes`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", "/api/regrade", nil)
		if err != nil {
			return err
		}
		pkt, err := decode[regradePacket](raw)
		if err != nil {
			return err
		}
		if opts.output == outputJSON && nodeRegradeFlags.dryRun {
			return emitJSON(raw)
		}
		changed := 0
		rows := make([][]string, 0, len(pkt.Rows))
		for _, r := range pkt.Rows {
			if r.Changed {
				changed++
			}
			score := "-"
			if r.Score != nil {
				score = strconv.Itoa(*r.Score)
			}
			oldName := r.Display
			if oldName == "" {
				oldName = r.Name
			}
			rows = append(rows, []string{strconv.FormatInt(r.ID, 10), r.Name, oldName, score, orDash(r.New), orDash(r.Note)})
		}
		if err := writeTable([]string{"ID", "内部名", "旧名", "分数", "新名", "备注"}, rows); err != nil {
			return err
		}
		if changed == 0 {
			fmt.Println("没有要改动的节点。")
			return nil
		}
		if nodeRegradeFlags.dryRun {
			fmt.Printf("--dry-run:没有写入。去掉 --dry-run 写入这 %d 个节点。\n", changed)
			return nil
		}
		if !nodeRegradeFlags.yes {
			fmt.Printf("写入以上 %d 个节点的改名?[y/N] ", changed)
			ans, err := readLine(os.Stdin)
			if err != nil {
				return err
			}
			if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
				fmt.Println("已取消,没有写入。")
				return nil
			}
		}
		raw, err = c.do("POST", "/api/regrade", map[string]bool{"apply": true})
		if err != nil {
			return err
		}
		pkt, err = decode[regradePacket](raw)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		for _, r := range pkt.Rows {
			if r.Changed {
				fmt.Printf("节点 %d(%s)→ %s\n", r.ID, r.Name, r.New)
			}
		}
		return nil
	},
}

func init() {
	nodeGradeCmd.Args = cobra.ArbitraryArgs
	f := nodeRegradeCmd.Flags()
	f.BoolVar(&nodeRegradeFlags.dryRun, "dry-run", false, "只打印对照表,不写入")
	f.BoolVar(&nodeRegradeFlags.yes, "yes", false, "不再询问,直接写入")
	nodeCmd.AddCommand(nodeGradeCmd, nodeRegradeCmd)
}
