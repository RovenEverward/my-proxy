package cli

import (
	"encoding/json"
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
	Kind      string `json:"kind"`
	ID        int64  `json:"id"`
	SourceID  int64  `json:"source_id,omitempty"`
	SourceKey string `json:"source_key,omitempty"`
	Name      string `json:"name"`
	Display   string `json:"display,omitempty"`
	Region    string `json:"region,omitempty"`
	City      string `json:"city,omitempty"`
	Score     *int   `json:"score,omitempty"`
	Verdict   string `json:"verdict"`
	Old       string `json:"old"`
	New       string `json:"new"`
	Note      string `json:"note,omitempty"`
	Changed   bool   `json:"changed"`
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

// extQualityWire 对应控制面返回的 model.ExternalQuality;键是 (来源,指纹)。
type extQualityWire struct {
	SourceID  int64               `json:"source_id"`
	SourceKey string              `json:"source_key"`
	CheckedAt string              `json:"checked_at"`
	Verdict   string              `json:"verdict"`
	Score     int                 `json:"score"`
	Report    model.QualityReport `json:"report"`
}

var sourceGradeCmd = &cobra.Command{
	Use:   "grade 来源",
	Short: "对来源里的外部节点做降级 IP 质量探测(控制面侧,不走 SSH)",
	Long: `对一个外部订阅源的全部端点做降级探测并打分。

与 mpx node grade 同一套权重和判定,但探测在控制面侧执行:域名在控制面解析,
ipwho.is/ipapi.is/可选 AbuseIPDB 由控制面查询,不通过 SSH 也不依赖节点上的 curl。
分数反映端点声明地址的 IP 信誉,不代表机场真实出口质量或速度;域名/CDN 端点测
到的是那个入口地址。结果存档,供 mpx node regrade 统一重排字母。

  mpx source grade 星空奶酪`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/sources", nil)
		if err != nil {
			return err
		}
		sources, err := decode[[]sourceView](raw)
		if err != nil {
			return err
		}
		var src *sourceView
		for i, sv := range sources {
			if sv.Name == args[0] || strconv.FormatInt(sv.ID, 10) == args[0] {
				src = &sources[i]
				break
			}
		}
		if src == nil {
			return fmt.Errorf("找不到来源 %q", args[0])
		}
		raw, err = c.do("GET", fmt.Sprintf("/api/sources/%d", src.ID), nil)
		if err != nil {
			return err
		}
		full, err := decode[sourceView](raw)
		if err != nil {
			return err
		}
		if len(full.Nodes) == 0 {
			fmt.Println("这个来源的快照里没有端点;先 mpx source refresh。")
			return nil
		}
		type result struct {
			ref sourceNodeView
			q   extQualityWire
			err error
		}
		results := make([]result, len(full.Nodes))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for i, ref := range full.Nodes {
			wg.Add(1)
			go func(i int, ref sourceNodeView) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				raw, err := c.doWithin(30e9, "POST", fmt.Sprintf("/api/sources/%d/endpoints/%s/quality", src.ID, ref.Key), nil)
				if err != nil {
					results[i] = result{ref: ref, err: err}
					return
				}
				q, err := decode[extQualityWire](raw)
				results[i] = result{ref: ref, q: q, err: err}
			}(i, ref)
		}
		wg.Wait()
		if opts.output == outputJSON {
			out := make([]extQualityWire, 0, len(results))
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
			name := r.ref.Name
			if r.ref.RawName != "" && r.ref.RawName != r.ref.Name {
				name = r.ref.Name + "(" + r.ref.RawName + ")"
			}
			if r.err != nil {
				failed++
				rows = append(rows, []string{name, "-", "-", "-", "-", "探测失败: " + r.err.Error()})
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
			verdict := map[string]string{"ok": "正常", "partial": "部分失败", "untested": "未测"}[r.q.Verdict]
			if r.q.Verdict == "untested" && rep.Error != "" {
				verdict = "未测:" + rep.Error
			}
			rows = append(rows, []string{name, orDash(rep.EgressIP), orDash(rep.Region), score, marks, verdict})
		}
		if err := writeTable([]string{"端点", "地址 IP", "地区", "分数", "标记", "判定"}, rows); err != nil {
			return err
		}
		if failed > 0 {
			return fmt.Errorf("%d 个端点探测失败", failed)
		}
		fmt.Println("按分重排字母:mpx node regrade")
		return nil
	},
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
			id := strconv.FormatInt(r.ID, 10)
			name := r.Name
			if r.Kind == "external" {
				id = "s" + strconv.FormatInt(r.SourceID, 10)
				// 外部行的内部名是上游原名,太长会撑爆表格;保留前 20 个字符。
				runes := []rune(r.Name)
				if len(runes) > 20 {
					name = string(runes[:20]) + "…"
				}
			}
			rows = append(rows, []string{id, name, oldName, score, orDash(r.New), orDash(r.Note)})
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
			if !r.Changed {
				continue
			}
			if r.Kind == "external" {
				fmt.Printf("来源 %d 端点(%s)→ %s\n", r.SourceID, r.Name, r.New)
				continue
			}
			fmt.Printf("节点 %d(%s)→ %s\n", r.ID, r.Name, r.New)
		}
		return nil
	},
}

func init() {
	sourceMetaCmd.Flags().StringVar(&sourceMetaFlags.region, "region", "", "两位国家码(US);空串清除记录")
	sourceMetaCmd.Flags().StringVar(&sourceMetaFlags.city, "city", "", "城市名,可选")
	nodeGradeCmd.Args = cobra.ArbitraryArgs
	f := nodeRegradeCmd.Flags()
	f.BoolVar(&nodeRegradeFlags.dryRun, "dry-run", false, "只打印对照表,不写入")
	f.BoolVar(&nodeRegradeFlags.yes, "yes", false, "不再询问,直接写入")
	nodeCmd.AddCommand(nodeGradeCmd, nodeRegradeCmd)
	// source grade 挂进来源命令树:newSourceCmd 在另一个 init 里建树,
	// init 顺序不保证,这里只存命令,由 newSourceCmd 带上。
	sourceCmdExtras = append(sourceCmdExtras, sourceGradeCmd, sourceMetaCmd)
}

// sourceMetaCmd 人工改/清一个外部端点的地区与城市(UC-17):上游名解析不出
// 地区(如 JMS 的主机名备注)时靠它进清洗层。写进去的记录是 graded,
// 刷新回填和探测都覆盖不了。空 --region 删记录,订阅回退到「源名 · 原名」。
var sourceMetaCmd = &cobra.Command{
	Use:   "meta 来源 端点名",
	Short: "改/清一个外部端点的地区与城市(上游名解析不出时兜底)",
	Long: `上游名解析不出地区时(比如 JMS 的主机名备注)手工告诉清洗层这个端点在
哪个地区。端点名按上游原名或清洗名子串匹配,有歧义就列出来让你挑。写进去的
记录不会被动重排;订阅名随之下一次生效。

例子:
  mpx source meta JMS c83s2 --region JP --city 东京   # 标成日本东京
  mpx source meta JMS c83s2 --region ""             # 清掉记录,回退原名`,
	Args: cobra.ExactArgs(2),
	RunE: runSourceMeta,
}

var sourceMetaFlags struct {
	region string
	city   string
}

func runSourceMeta(cmd *cobra.Command, args []string) error {
	cl, err := newClient()
	if err != nil {
		return err
	}
	v, err := findSource(cl, args[0])
	if err != nil {
		return err
	}
	want := strings.ToLower(strings.TrimSpace(args[1]))
	var hit []sourceNodeView
	for _, n := range v.Nodes {
		if strings.Contains(strings.ToLower(n.Name), want) ||
			(n.RawName != "" && strings.Contains(strings.ToLower(n.RawName), want)) {
			hit = append(hit, n)
		}
	}
	switch len(hit) {
	case 0:
		return fmt.Errorf("在 %s 的 %d 个端点里没找到 %q(见 mpx source show %s)", v.Name, len(v.Nodes), args[1], v.Name)
	case 1:
	default:
		var b strings.Builder
		for _, n := range hit {
			fmt.Fprintf(&b, "\n  %s (key %s)", n.RawName, n.Key)
		}
		return fmt.Errorf("%q 在 %s 里匹配到 %d 个端点,写长一点区分:%s", args[1], v.Name, len(hit), b.String())
	}
	n := hit[0]
	// region 默认空串:只在显式给 --region 时才发送,否则没带 flag 就跑会
	// 把已有的清洗记录清掉。city 同理只在 Changed 时写。
	body := map[string]any{}
	changed := func(name string) bool { return cmd.Flags().Changed(name) }
	if !changed("region") && !changed("city") {
		return fmt.Errorf("要改什么?--region 两位国家码(空串清记录),--city 城市名")
	}
	if changed("region") {
		body["region"] = sourceMetaFlags.region
	}
	if changed("city") {
		body["city"] = sourceMetaFlags.city
	}
	raw, err := cl.do("PATCH", fmt.Sprintf("%s/endpoints/%s", sourcePath(v.ID), n.Key), body)
	if err != nil {
		return err
	}
	var out struct {
		Deleted  bool   `json:"deleted"`
		Name     string `json:"name"`
		Metadata struct {
			Region string `json:"region"`
			City   string `json:"city"`
			Letter string `json:"letter"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	shown := out.Name
	if shown == "" {
		shown = n.RawName
	}
	if shown == "" {
		shown = n.Name
	}
	if out.Deleted {
		fmt.Printf("已清除 %s 的清洗记录,订阅回退到 %s · 原名\n", shown, v.Name)
		return nil
	}
	fmt.Printf("%s → %s %s %s(已写进清洗层)\n", shown, out.Metadata.Region, out.Metadata.City, out.Metadata.Letter)
	return nil
}
