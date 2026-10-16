package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

// mpx plan:套餐(权限组)。一个用户最多挂一个套餐,套餐之外还能有额外授权。
// 见 docs/internal/plans-rfc.md §3.7。
//
// 节点/入站的指定一律复用 v0.5.0 的名字解析(显示名、内部名、唯一前缀),
// 显示也一律用节点显示名。

// planCmd 是套餐命令组。
var planCmd = newPlanCmd()

func newPlanCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "plan",
		Short: "管理套餐(权限组)",
		Long: `套餐决定用户能用哪些节点与入站。空套餐表示什么都不能用;要表达"全部"
请打开「全部节点」开关(all_nodes),它包含以后新加的节点与入站。

一个用户最多挂一个套餐,套餐之外还可以有额外授权(user edit --inbounds)。
系统自带一个「默认套餐」(all_nodes),新用户默认归到它;默认套餐可以编辑,
不能删除。删除还有用户挂着的套餐会被拒绝,除非用 --reassign 指定改挂去向。

  mpx plan list
  mpx plan add 全球 --node "🇺🇸 洛杉矶 A" --node 东京A
  mpx plan edit 全球 --add-node 新加坡A --rm-inbound la-1/ws --name 全球Pro
  mpx plan show 全球
  mpx plan rm 基础 --reassign 全球
  mpx plan suggest --apply`,
	}
	c.AddCommand(planListCmd(), planAddCmd(), planEditCmd(), planShowCmd(), planRmCmd(), planSuggestCmd())
	return c
}

// ---- 读接口的小工具 ----

// listPlans 取全部套餐。
func listPlans(c *Client) ([]model.Plan, error) {
	raw, err := c.do("GET", "/api/plans", nil)
	if err != nil {
		return nil, err
	}
	return decode[[]model.Plan](raw)
}

func planCandidates(plans []model.Plan) []candidate {
	cs := make([]candidate, 0, len(plans))
	for _, p := range plans {
		cs = append(cs, candidate{ID: p.ID, Name: p.Name})
	}
	return cs
}

// resolvePlan 把一个套餐参数解析成套餐(id、名字或唯一前缀)。
func resolvePlan(c *Client, arg string, echo bool) (model.Plan, error) {
	plans, err := listPlans(c)
	if err != nil {
		return model.Plan{}, err
	}
	x, prefixed, err := pick("套餐", arg, planCandidates(plans))
	if err != nil {
		return model.Plan{}, err
	}
	if prefixed && echo {
		fmt.Fprintf(os.Stderr, "→ 套餐 %d %s\n", x.ID, x.Name)
	}
	for _, p := range plans {
		if p.ID == x.ID {
			return p, nil
		}
	}
	return model.Plan{}, fmt.Errorf("没有 id 为 %d 的套餐", x.ID)
}

// resolvePlanID 是 resolvePlan 只要 id 的简写,供 --plan / --reassign 用。
func resolvePlanID(c *Client, arg string, echo bool) (int64, error) {
	arg = strings.TrimSpace(arg)
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		return id, nil
	}
	p, err := resolvePlan(c, arg, echo)
	if err != nil {
		return 0, err
	}
	return p.ID, nil
}

// planNamesContext 把 id 映射到节点显示名与入站,用于把套餐里的 id 摆成人看得懂
// 的名字。
type planNamesContext struct {
	nodes    map[int64]string // 节点 id -> 显示名(没有显示名时是节点名)
	inbounds map[int64]model.Inbound
}

func loadPlanNames(c *Client) (planNamesContext, error) {
	ctx := planNamesContext{nodes: map[int64]string{}, inbounds: map[int64]model.Inbound{}}
	views, err := listNodes(c)
	if err != nil {
		return ctx, err
	}
	for _, v := range views {
		if v.Display != "" {
			ctx.nodes[v.Node.ID] = v.Display
		} else {
			ctx.nodes[v.Node.ID] = v.Node.Name
		}
	}
	ins, err := listInbounds(c)
	if err != nil {
		return ctx, err
	}
	for _, in := range ins {
		ctx.inbounds[in.ID] = in
	}
	return ctx, nil
}

func (ctx planNamesContext) nodeName(id int64) string {
	if n, ok := ctx.nodes[id]; ok {
		return n
	}
	return fmt.Sprintf("#%d", id)
}

// planNodesText 把一个套餐的节点列摆成人话。
func planNodesText(p model.Plan, ctx planNamesContext) string {
	if p.AllNodes {
		return "全部节点(含以后新加的)"
	}
	if len(p.NodeIDs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(p.NodeIDs))
	for _, id := range p.NodeIDs {
		parts = append(parts, ctx.nodeName(id))
	}
	return strings.Join(parts, ", ")
}

// planInboundsText 把单独勾选的入站摆成「节点/入站」。
func planInboundsText(p model.Plan, ctx planNamesContext) string {
	if len(p.InboundIDs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(p.InboundIDs))
	for _, id := range p.InboundIDs {
		if in, ok := ctx.inbounds[id]; ok {
			parts = append(parts, ctx.nodeName(in.NodeID)+"/"+in.TagOrDefault())
		} else {
			parts = append(parts, fmt.Sprintf("#%d", id))
		}
	}
	return strings.Join(parts, ", ")
}

// ---- plan list ----

func planListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出全部套餐",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			raw, err := c.do("GET", "/api/plans", nil)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			plans, err := decode[[]model.Plan](raw)
			if err != nil {
				return err
			}
			ctx, err := loadPlanNames(c)
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(plans))
			for _, p := range plans {
				rows = append(rows, []string{
					strconv.FormatInt(p.ID, 10),
					p.Name,
					planNodesText(p, ctx),
					planInboundsText(p, ctx),
					strconv.Itoa(p.Users),
					strconv.Itoa(p.ExtraAuth),
					boolMark(p.IsDefault),
				})
			}
			return writeTable([]string{"ID", "名称", "节点", "入站", "用户", "额外授权", "默认"}, rows)
		},
	}
}

func boolMark(v bool) string {
	if v {
		return "是"
	}
	return ""
}

// ---- plan add ----

var planAddFlags struct {
	nodes    []string
	inbounds []string
	note     string
	allNodes bool
}

func planAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <名称>",
		Short: "新建套餐",
		Example: `  mpx plan add 全球 --node "🇺🇸 洛杉矶 A" --node 东京A
  mpx plan add 基础 --node 洛杉矶A --inbound la-1/reality
  mpx plan add 全部 --all-nodes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			p := model.Plan{
				Name:     args[0],
				Note:     planAddFlags.note,
				AllNodes: planAddFlags.allNodes,
			}
			if p.NodeIDs, err = resolveNodeRefs(planAddFlags.nodes); err != nil {
				return err
			}
			if p.InboundIDs, err = resolveInboundRefs(planAddFlags.inbounds); err != nil {
				return err
			}
			raw, err := c.do("POST", "/api/plans", p)
			if err != nil {
				return err
			}
			return report(raw, "已创建套餐 %s", p.Name)
		},
	}
	cmd.Flags().StringSliceVar(&planAddFlags.nodes, "node", nil, "包含的节点(id、显示名或唯一前缀,可重复)")
	cmd.Flags().StringSliceVar(&planAddFlags.inbounds, "inbound", nil, "单独包含的入站(id、节点/tag 或唯一 tag,可重复)")
	cmd.Flags().StringVar(&planAddFlags.note, "note", "", "备注")
	cmd.Flags().BoolVar(&planAddFlags.allNodes, "all-nodes", false, "包含全部节点,含以后新加的")
	return cmd
}

// ---- plan edit ----

// planEditOptions 是 plan edit 的可选字段。用具名类型而不是匿名 var:applyPlanRefs
// 要把它当参数传,匿名结构体类型无法在别处再写出来。
type planEditOptions struct {
	addNodes    []string
	rmNodes     []string
	addInbounds []string
	rmInbounds  []string
	name        string
	note        string
	allNodes    bool
}

var planEditFlags planEditOptions

func planEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <名称|ID>",
		Short: "修改套餐(只改给了的选项)",
		Example: `  mpx plan edit 全球 --add-node 新加坡A --rm-inbound la-1/ws --name 全球Pro
  mpx plan edit 基础 --all-nodes=false --note ""`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			p, err := resolvePlan(c, args[0], true)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("name") {
				p.Name = planEditFlags.name
			}
			if cmd.Flags().Changed("note") {
				p.Note = planEditFlags.note
			}
			if cmd.Flags().Changed("all-nodes") {
				p.AllNodes = planEditFlags.allNodes
			}
			if err := applyPlanRefs(&p, planEditFlags); err != nil {
				return err
			}
			raw, err := c.do("PUT", fmt.Sprintf("/api/plans/%d", p.ID), p)
			if err != nil {
				return err
			}
			return report(raw, "已保存套餐 %s", p.Name)
		},
	}
	cmd.Flags().StringSliceVar(&planEditFlags.addNodes, "add-node", nil, "增加包含的节点")
	cmd.Flags().StringSliceVar(&planEditFlags.rmNodes, "rm-node", nil, "移除包含的节点")
	cmd.Flags().StringSliceVar(&planEditFlags.addInbounds, "add-inbound", nil, "增加单独包含的入站")
	cmd.Flags().StringSliceVar(&planEditFlags.rmInbounds, "rm-inbound", nil, "移除单独包含的入站")
	cmd.Flags().StringVar(&planEditFlags.name, "name", "", "改名")
	cmd.Flags().StringVar(&planEditFlags.note, "note", "", "备注;给空串表示清掉")
	cmd.Flags().BoolVar(&planEditFlags.allNodes, "all-nodes", false, "包含全部节点(用 --all-nodes=false 关掉)")
	return cmd
}

// applyPlanRefs 把 add/rm 的节点与入站名字解析成 id 并并进套餐。add 去重、rm 删除。
func applyPlanRefs(p *model.Plan, f planEditOptions) error {
	if len(f.addNodes) > 0 {
		ids, err := resolveNodeRefs(f.addNodes)
		if err != nil {
			return err
		}
		p.NodeIDs = mergeIDs(p.NodeIDs, ids)
	}
	if len(f.rmNodes) > 0 {
		ids, err := resolveNodeRefs(f.rmNodes)
		if err != nil {
			return err
		}
		p.NodeIDs = removeIDs(p.NodeIDs, ids)
	}
	if len(f.addInbounds) > 0 {
		ids, err := resolveInboundRefs(f.addInbounds)
		if err != nil {
			return err
		}
		p.InboundIDs = mergeIDs(p.InboundIDs, ids)
	}
	if len(f.rmInbounds) > 0 {
		ids, err := resolveInboundRefs(f.rmInbounds)
		if err != nil {
			return err
		}
		p.InboundIDs = removeIDs(p.InboundIDs, ids)
	}
	return nil
}

// resolveNodeRefs 把一批节点参数解析成去重升序的 id。
func resolveNodeRefs(args []string) ([]int64, error) {
	var ids []int64
	for _, a := range args {
		id, err := resolveNodeID(a, false)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return normalizeIDs(ids), nil
}

// resolveInboundRefs 把一批入站参数解析成去重升序的 id。
func resolveInboundRefs(args []string) ([]int64, error) {
	var ids []int64
	for _, a := range args {
		id, err := resolveInboundID(a, false)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return normalizeIDs(ids), nil
}

func normalizeIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func mergeIDs(have, add []int64) []int64 {
	return normalizeIDs(append(append([]int64{}, have...), add...))
}

func removeIDs(have, drop []int64) []int64 {
	gone := map[int64]bool{}
	for _, id := range drop {
		gone[id] = true
	}
	out := make([]int64, 0, len(have))
	for _, id := range have {
		if !gone[id] {
			out = append(out, id)
		}
	}
	return normalizeIDs(out)
}

// ---- plan show ----

// planPreview* 是 GET /api/plans/{id}/preview 的响应形状(只取 CLI 要用的字段)。
type planPreviewNode struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Display  string `json:"display"`
	Draining bool   `json:"draining"`
	Reason   string `json:"reason"`
}

type planPreviewInbound struct {
	ID       int64  `json:"id"`
	Node     string `json:"node"`
	Display  string `json:"display"`
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	MaxUsers int    `json:"max_users"`
	Users    int    `json:"users"`
	Reason   string `json:"reason"`
}

type planPreview struct {
	Plan     model.Plan           `json:"plan"`
	Nodes    []planPreviewNode    `json:"nodes"`
	Inbounds []planPreviewInbound `json:"inbounds"`
}

func planShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <名称|ID>",
		Short: "显示套餐详情:节点、入站、用户数与各策略组成员数",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			p, err := resolvePlan(c, args[0], false)
			if err != nil {
				return err
			}
			raw, err := c.do("GET", fmt.Sprintf("/api/plans/%d/preview", p.ID), nil)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			pv, err := decode[planPreview](raw)
			if err != nil {
				return err
			}
			return writePlanShow(c, pv)
		},
	}
}

func writePlanShow(c *Client, pv planPreview) error {
	p := pv.Plan
	kind := "普通套餐"
	if p.IsDefault {
		kind = "默认套餐(不可删除)"
	}
	fmt.Printf("套餐 #%d %s(%s)\n", p.ID, p.Name, kind)
	if p.Note != "" {
		fmt.Printf("备注:%s\n", p.Note)
	}
	if p.AllNodes {
		fmt.Println("节点:全部节点(含以后新加的)")
	}
	userLine := fmt.Sprintf("用户:%d 个", p.Users)
	if p.ExtraAuth > 0 {
		userLine += fmt.Sprintf("(其中 %d 个有额外授权,套餐设置未完全生效)", p.ExtraAuth)
	}
	fmt.Println(userLine)

	nodeRows := make([][]string, 0, len(pv.Nodes))
	for _, n := range pv.Nodes {
		name := n.Display
		if name == "" {
			name = n.Name
		}
		state := "可用"
		if n.Reason != "" {
			state = "暂不在订阅:" + n.Reason
		}
		nodeRows = append(nodeRows, []string{strconv.FormatInt(n.ID, 10), name, state})
	}
	fmt.Println("\n节点:")
	if err := writeTable([]string{"ID", "节点", "状态"}, nodeRows); err != nil {
		return err
	}

	inRows := make([][]string, 0, len(pv.Inbounds))
	for _, in := range pv.Inbounds {
		name := in.Display
		if name == "" {
			name = in.Node
		}
		quota := "-"
		if in.MaxUsers > 0 {
			quota = fmt.Sprintf("%d/%d", in.Users, in.MaxUsers)
		}
		state := "可用"
		if in.Reason != "" {
			state = "暂不在订阅:" + in.Reason
		}
		inRows = append(inRows, []string{
			strconv.FormatInt(in.ID, 10), name, in.Tag, in.Protocol,
			strconv.Itoa(in.Port), quota, state,
		})
	}
	fmt.Println("\n入站:")
	if err := writeTable([]string{"ID", "节点", "入站", "协议", "端口", "名额", "状态"}, inRows); err != nil {
		return err
	}

	counts, err := planGroupCounts(c, p.ID)
	if err != nil {
		// 策略组成员数只是补充信息,查不到不该让整条 show 失败。
		fmt.Fprintf(os.Stderr, "读取策略组成员数失败:%v\n", err)
		return nil
	}
	if len(counts) > 0 {
		fmt.Printf("\n策略组成员数:%s\n", strings.Join(counts, " "))
	}
	return nil
}

// planGroupCounts 对每个策略组,在这个套餐范围内数一数会收进几个入口(§3.5)。
func planGroupCounts(c *Client, planID int64) ([]string, error) {
	groups, err := listGroups(c)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("plan", strconv.FormatInt(planID, 10))
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		raw, err := c.do("POST", "/api/proxy-groups/preview?"+q.Encode(), g)
		if err != nil {
			return nil, err
		}
		var entries []struct {
			Admitted bool `json:"admitted"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, err
		}
		n := 0
		for _, e := range entries {
			if e.Admitted {
				n++
			}
		}
		label := fmt.Sprintf("%s:%d", g.Name, n)
		if n == 0 {
			label += "(空)"
		}
		out = append(out, label)
	}
	return out, nil
}

// ---- plan rm ----

var planRmFlags struct {
	reassign string
}

func planRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <名称|ID>",
		Short: "删除套餐(有用户挂着时要用 --reassign)",
		Long: `删除一个套餐。默认套餐不能删;还有用户挂着时会被拒绝并给出用户数。

  --reassign 全球   先把这些用户改挂到「全球」再删

改挂与删除在控制面的同一个事务里完成,不会出现用户指向被删套餐的中间态。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			p, err := resolvePlan(c, args[0], false)
			if err != nil {
				return err
			}
			path := fmt.Sprintf("/api/plans/%d", p.ID)
			if cmd.Flags().Changed("reassign") {
				q := url.Values{}
				if strings.TrimSpace(planRmFlags.reassign) == "" {
					return errors.New(`--reassign 不能为空:请指定一个具体套餐(可以是默认套餐),不能把用户移出套餐`)
				} else {
					target, err := resolvePlanID(c, planRmFlags.reassign, false)
					if err != nil {
						return err
					}
					if target == p.ID {
						return errors.New("不能把用户改挂到正在删除的套餐")
					}
					q.Set("reassign", strconv.FormatInt(target, 10))
				}
				path += "?" + q.Encode()
			}
			raw, err := c.do("DELETE", path, nil)
			if err != nil {
				return err
			}
			return report(raw, "已删除套餐 %s", p.Name)
		},
	}
	cmd.Flags().StringVar(&planRmFlags.reassign, "reassign", "", "先把用户改挂到指定套餐(可以是默认套餐)")
	return cmd
}

// ---- plan suggest ----

// planSuggestion 是一组「入站集合相同的用户」以及建议建成的套餐。
type planSuggestion struct {
	Name       string   `json:"name"`
	Users      []string `json:"users"`
	InboundIDs []int64  `json:"inbound_ids"`
	All        bool     `json:"all"`
}

var planSuggestFlags struct {
	apply bool
}

func planSuggestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "suggest",
		Short: "把还没挂套餐的用户按授权集合整理成套餐建议(默认只看不写)",
		Long: `把没有挂套餐的用户按相同的入站集合分组,列出可以建成哪些套餐。

默认只打印建议,不动库。加 --apply 才真正建套餐:对每个非空集合新建一个套餐,
把这些用户挂上去,并清空他们的额外授权(inbound_ids),让套餐完全生效。
入站为空的用户是「全部可用」,单独列出且不会被 --apply 处理 —— 要归到某个
all_nodes 套餐请用 mpx user edit 明确指定。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			sugs, err := buildPlanSuggestions(c)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				if err := emitJSONValue(sugs); err != nil {
					return err
				}
				return applySuggestions(c, sugs, planSuggestFlags.apply)
			}
			if err := printPlanSuggestions(sugs); err != nil {
				return err
			}
			return applySuggestions(c, sugs, planSuggestFlags.apply)
		},
	}
	cmd.Flags().BoolVar(&planSuggestFlags.apply, "apply", false, "真正创建套餐并挂上用户(默认只看不动)")
	return cmd
}

// buildPlanSuggestions 分组「没挂套餐」的用户。入站集合相同的归一组,入站为空的
// 单独一组(All=true)。
func buildPlanSuggestions(c *Client) ([]planSuggestion, error) {
	users, err := fetchAllUsers(c)
	if err != nil {
		return nil, err
	}
	ctx, err := loadPlanNames(c)
	if err != nil {
		return nil, err
	}
	groups := map[string]*planSuggestion{}
	var order []string
	for _, u := range users {
		if u.PlanID != nil {
			continue
		}
		canon := normalizeIDs(u.InboundIDs)
		key := idsKey(canon)
		g, ok := groups[key]
		if !ok {
			g = &planSuggestion{InboundIDs: canon, All: len(canon) == 0}
			g.Name = suggestionName(canon, ctx)
			groups[key] = g
			order = append(order, key)
		}
		g.Users = append(g.Users, u.Username)
	}
	out := make([]planSuggestion, 0, len(order))
	for _, key := range order {
		out = append(out, *groups[key])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].All && !out[j].All })
	return out, nil
}

func idsKey(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// suggestionName 用节点显示名给建议套餐起名,形如「整理 · 🇺🇸 洛杉矶 A + 🇯🇵 东京 A」。
func suggestionName(ids []int64, ctx planNamesContext) string {
	if len(ids) == 0 {
		return "全部入站"
	}
	nodeSet := map[int64]bool{}
	for _, id := range ids {
		if in, ok := ctx.inbounds[id]; ok {
			nodeSet[in.NodeID] = true
		}
	}
	nodeIDs := make([]int64, 0, len(nodeSet))
	for id := range nodeSet {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	names := make([]string, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		names = append(names, ctx.nodeName(id))
	}
	if len(names) == 0 {
		return "整理套餐"
	}
	return "整理 · " + strings.Join(names, " + ")
}

func printPlanSuggestions(sugs []planSuggestion) error {
	if len(sugs) == 0 {
		fmt.Println("没有需要整理的未挂套餐用户")
		return nil
	}
	rows := make([][]string, 0, len(sugs))
	for _, s := range sugs {
		content := "-"
		if s.All {
			content = "全部入站"
		} else if len(s.InboundIDs) > 0 {
			parts := make([]string, 0, len(s.InboundIDs))
			for _, id := range s.InboundIDs {
				parts = append(parts, fmt.Sprintf("#%d", id))
			}
			content = strings.Join(parts, ",")
		}
		rows = append(rows, []string{s.Name, strconv.Itoa(len(s.Users)), content})
	}
	if err := writeTable([]string{"建议套餐", "用户数", "入站"}, rows); err != nil {
		return err
	}
	fmt.Println("默认只打印建议;加 --apply 才创建并挂上用户")
	return nil
}

// applySuggestions 在 --apply 时创建套餐并挂上用户。入站为空的组跳过。
func applySuggestions(c *Client, sugs []planSuggestion, apply bool) error {
	if !apply {
		return nil
	}
	for _, s := range sugs {
		if s.All {
			fmt.Printf("跳过「%s」(%d 个用户):全部入站请用 mpx user edit --plan 指定\n", s.Name, len(s.Users))
			continue
		}
		p, err := createSuggestedPlan(c, s)
		if err != nil {
			return err
		}
		for _, name := range s.Users {
			if err := setUserPlanAndClearInbounds(c, name, p.ID); err != nil {
				return err
			}
		}
		fmt.Printf("已创建套餐 %s(挂上 %d 个用户)\n", p.Name, len(s.Users))
	}
	return nil
}

// createSuggestedPlan 建套餐;名字撞车时补序号重试。
func createSuggestedPlan(c *Client, s planSuggestion) (model.Plan, error) {
	for i := 0; i < 100; i++ {
		name := s.Name
		if i > 0 {
			name = fmt.Sprintf("%s（%d）", s.Name, i+1)
		}
		raw, err := c.do("POST", "/api/plans", model.Plan{Name: name, InboundIDs: s.InboundIDs})
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
				continue
			}
			return model.Plan{}, err
		}
		return decode[model.Plan](raw)
	}
	return model.Plan{}, fmt.Errorf("给建议套餐 %q 找不到可用的名字", s.Name)
}

// setUserPlanAndClearInbounds 把用户改挂到套餐并清空额外授权,即 RFC 里
// plan suggest --apply 的动作。
func setUserPlanAndClearInbounds(c *Client, username string, planID int64) error {
	path := userPath(username)
	raw, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	u, err := decode[model.User](raw)
	if err != nil {
		return err
	}
	u.InboundIDs = nil
	body := userWriteBody{User: u, PlanID: planIDJSON(&planID)}
	_, err = c.do("PUT", path, body)
	return err
}
