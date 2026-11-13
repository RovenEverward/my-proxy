package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/reach"
)

// reachCheckTimeout 给控制面探测与结果上传的上限,和 server 侧 reachProbeTimeout
// 同级:真实探测在每个探针自己的 4s 上,这里只是兜底。
const reachCheckTimeout = 130 * time.Second

var reachCheckLast bool

var nodeCheckCmd = &cobra.Command{
	Use:   "check [节点]",
	Short: "检查节点能不能被触达(本机与控制面各探一轮 TCP/UDP)",
	Long: `发起一次触达检查:控制面先对范围内每台节点的每个入站探一轮,本机接着
探同一批,两边结果合起来给每台节点一个判定。

判定口径:双通=本机与控制面都可达;境内不通=本机探不通、控制面可达(更像境内
路径被封);都不通=两边都失败(更像节点本身故障);未知=至少一侧探测没结果,
不下结论。探测本身是只读的,结果会存回控制面,node list 与 mpx ui 的节点列表
之后显示的就是这次检查。

--last 不发起检查,只把最近一次的结果打出来。`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		if reachCheckLast {
			return printReachLast(c)
		}

		nodes, err := resolveNodeArgs(c, args)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(nodes))
		for _, n := range nodes {
			ids = append(ids, n.ID)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "正在检查 %d 台节点:控制面探一轮,本机再探一轮…\n", len(nodes))
		probeRaw, err := c.doWithin(reachCheckTimeout, "POST", "/api/reach/probe", map[string]any{"node_ids": ids})
		if err != nil {
			return err
		}
		var probe struct {
			CheckedAt time.Time              `json:"checked_at"`
			Targets   []reach.Target         `json:"targets"`
			Results   map[int64]*reach.Result `json:"results"`
		}
		if err := json.Unmarshal(probeRaw, &probe); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), reachCheckTimeout)
		defer cancel()
		local := reach.Probe(ctx, probe.Targets, reach.Options{})

		// 按节点合并明细、算判定。
		checks := make([]reach.NodeCheck, 0, len(nodes))
		byNode := map[int64][]reach.Target{}
		for _, t := range probe.Targets {
			byNode[t.NodeID] = append(byNode[t.NodeID], t)
		}
		for _, n := range nodes {
			targets := byNode[n.ID]
			if len(targets) == 0 {
				continue
			}
			rows, lv, cv := reach.Merge(targets, local, probe.Results)
			checks = append(checks, reach.NodeCheck{
				NodeID: n.ID, NodeName: n.Name, CheckedAt: probe.CheckedAt,
				Verdict: reach.Verdict(lv, cv), Local: lv, CP: cv, Rows: rows,
			})
		}
		if len(checks) == 0 {
			return errors.New("范围内的节点都没有入站,没有可探的目标")
		}
		if _, err := c.doWithin(reachCheckTimeout, "POST", "/api/reach/results", map[string]any{
			"checked_at": probe.CheckedAt, "results": checks,
		}); err != nil {
			return err
		}
		if opts.output == outputJSON {
			raw, _ := json.Marshal(map[string]any{"checked_at": probe.CheckedAt, "results": checks})
			return emitJSON(raw)
		}
		printReachChecks(checks, len(checks) == 1)
		return nil
	},
}

func init() {
	nodeCheckCmd.Flags().BoolVar(&reachCheckLast, "last", false, "不发起检查,只看最近一次结果")
}

var reachVerdictText = map[string]string{
	reach.VerdictOK: "双通", reach.VerdictBlocked: "境内不通",
	reach.VerdictDown: "都不通", reach.VerdictUnknown: "未知",
}

func reachSourceText(s reach.SourceSummary) string {
	switch {
	case s.Reachable:
		return fmt.Sprintf("可达 %d ms", s.LatencyMs)
	case s.Failed:
		return "不通"
	case s.Probed:
		return "未知"
	default:
		return "—"
	}
}

// printReachChecks 打节点级判定表;detail 为真(单台检查)时再附逐入站明细。
func printReachChecks(checks []reach.NodeCheck, detail bool) {
	rows := make([][]string, 0, len(checks))
	for _, nc := range checks {
		rows = append(rows, []string{
			nc.NodeName,
			reachVerdictText[nc.Verdict],
			reachSourceText(nc.Local),
			reachSourceText(nc.CP),
		})
	}
	if err := writeTable([]string{"节点", "判定", "本机 → 节点", "控制面 → 节点"}, rows); err != nil {
		return
	}
	if !detail || len(checks) != 1 {
		return
	}
	fmt.Println()
	cell := func(c *reach.Cell) string {
		if c == nil {
			return "—"
		}
		switch c.Status {
		case reach.CellReachable:
			return fmt.Sprintf("可达 %d ms", c.LatencyMs)
		case reach.CellTimeout:
			return "超时"
		case reach.CellRefused:
			return "拒绝"
		case reach.CellUnreachable:
			return "不可达"
		default:
			return "未知"
		}
	}
	protos := func(p *reach.Protos) string {
		if p == nil {
			return "—"
		}
		return fmt.Sprintf("TCP %s · UDP %s", cell(p.TCP), cell(p.UDP))
	}
	nr := make([][]string, 0, len(checks[0].Rows))
	for _, r := range checks[0].Rows {
		nr = append(nr, []string{
			fmt.Sprintf("%s:%d", r.Label, r.Port), r.Protocol, protos(r.Local), protos(r.CP),
		})
	}
	_ = writeTable([]string{"入站", "协议", "本机", "控制面"}, nr)
}

func printReachLast(c *Client) error {
	raw, err := c.do("GET", "/api/reach/last", nil)
	if err != nil {
		return err
	}
	if opts.output == outputJSON {
		return emitJSON(raw)
	}
	var last struct {
		CheckedAt time.Time         `json:"checked_at"`
		Counts    map[string]int    `json:"counts"`
		Nodes     []reach.NodeCheck `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &last); err != nil {
		return err
	}
	fmt.Printf("检查于 %s\n", last.CheckedAt.Local().Format("2006-01-02 15:04"))
	printReachChecks(last.Nodes, false)
	return nil
}
