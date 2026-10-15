package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

// mpx rule:分流规则(见 docs/internal/routing-rules-rfc.md)。序号从 1 开始,
// 与 mpx rule list 显示的一致;--user 指向某个用户自己的规则,不带是全局规则。

func init() { rootCmd.AddCommand(newRuleCmd(), newGroupCmd()) }

type rulesResp struct {
	Global        []model.RouteRule `json:"global"`
	GlobalVersion int64             `json:"global_version"`
	Final         string            `json:"final"`
	User          string            `json:"user"`
	UserRules     []model.RouteRule `json:"user_rules"`
	UserVersion   int64             `json:"user_version"`
	UserFinal     string            `json:"user_final"`
}

var ruleTypeAlias = map[string]model.RuleType{
	"suffix": model.RuleSuffix, "后缀": model.RuleSuffix, "domain": model.RuleSuffix,
	"keyword": model.RuleKeyword, "关键词": model.RuleKeyword,
	"set": model.RuleSetRef, "规则组": model.RuleSetRef,
	"geoip": model.RuleGeoIP, "cidr": model.RuleCIDR, "ip": model.RuleCIDR,
}

func listGroups(cl *Client) ([]model.ProxyGroup, error) {
	raw, err := cl.do("GET", "/api/proxy-groups", nil)
	if err != nil {
		return nil, err
	}
	var g []model.ProxyGroup
	return g, json.Unmarshal(raw, &g)
}

// resolveTarget 把 直连/DIRECT、拦截/REJECT、策略组名或 group:<id> 换成规则去向。
func resolveTarget(cl *Client, s string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DIRECT", "直连":
		return model.TargetDirect, nil
	case "REJECT", "拦截":
		return model.TargetReject, nil
	}
	g, err := findGroup(cl, s)
	if err != nil {
		return "", err
	}
	return model.GroupTarget(g.ID), nil
}

func findGroup(cl *Client, s string) (*model.ProxyGroup, error) {
	groups, err := listGroups(cl)
	if err != nil {
		return nil, err
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(s, "group:"), 10, 64)
	for i := range groups {
		if groups[i].Name == s || (id > 0 && groups[i].ID == id) {
			return &groups[i], nil
		}
	}
	return nil, fmt.Errorf("没有叫 %q 的策略组(去向可以是 直连、拦截 或策略组名,见 mpx group list)", s)
}

func targetName(groups []model.ProxyGroup, t string) string {
	switch t {
	case model.TargetDirect:
		return "直连"
	case model.TargetReject:
		return "拦截"
	case "":
		return "-"
	}
	if id, ok := model.TargetGroupID(t); ok {
		for _, g := range groups {
			if g.ID == id {
				return g.Name
			}
		}
	}
	return t
}

func ruleValueText(r model.RouteRule) string {
	if r.Type == model.RuleSetRef {
		if rs, ok := model.RuleSetByKey(r.Value); ok {
			return rs.Name + " (" + r.Value + ")"
		}
	}
	return r.Value
}

func rulesPath(user string) string {
	if user == "" {
		return "/api/rules"
	}
	return "/api/rules?user=" + url.QueryEscape(user)
}

func getRules(cl *Client, user string) (*rulesResp, error) {
	raw, err := cl.do("GET", rulesPath(user), nil)
	if err != nil {
		return nil, err
	}
	var r rulesResp
	return &r, json.Unmarshal(raw, &r)
}

// scopeRules 取出要编辑的那一层规则和它的版本号。
func (r *rulesResp) scope(user string) ([]model.RouteRule, int64) {
	if user != "" {
		return r.UserRules, r.UserVersion
	}
	return r.Global, r.GlobalVersion
}

func putRules(cl *Client, user string, rules []model.RouteRule, version int64) error {
	// 只发规则内容;id 与位置由控制面按顺序重新编排(页面发的也是这几个字段)。
	type ruleIn struct {
		Type    model.RuleType `json:"type"`
		Value   string         `json:"value"`
		Target  string         `json:"target"`
		Enabled bool           `json:"enabled"`
		Note    string         `json:"note,omitempty"`
	}
	in := make([]ruleIn, 0, len(rules))
	for _, r := range rules {
		in = append(in, ruleIn{r.Type, r.Value, r.Target, r.Enabled, r.Note})
	}
	raw, err := cl.do("PUT", rulesPath(user), map[string]any{"rules": in, "version": version})
	if err != nil {
		return err
	}
	if opts.output == outputJSON {
		return emitJSON(raw)
	}
	fmt.Println("已保存")
	return nil
}

func routeRuleIndex(s string, n int) (int, error) {
	i, err := strconv.Atoi(s)
	if err != nil || i < 1 || i > n {
		return 0, fmt.Errorf("序号 %q 不对:这一层有 %d 条规则,序号从 1 开始", s, n)
	}
	return i - 1, nil
}

func newRuleCmd() *cobra.Command {
	var user string
	c := &cobra.Command{Use: "rule", Short: "管理分流规则", Long: `管理分流规则:哪些网站走哪个策略组、直连或拦截。
规则从上到下匹配,第一条命中的生效;都没命中走「其他全部」(mpx rule final)。
--user 编辑某个用户自己的规则,它们排在全局规则前面。

类型:suffix(域名后缀)、keyword(关键词)、set(内置规则组,见 mpx rule catalog)、geoip、cidr
去向:直连 / DIRECT、拦截 / REJECT,或策略组名(mpx group list)`}
	c.PersistentFlags().StringVar(&user, "user", "", "编辑这个用户的规则(默认全局)")

	c.AddCommand(&cobra.Command{Use: "list", Short: "列出规则", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			raw, err := cl.do("GET", rulesPath(user), nil)
			if err != nil {
				return err
			}
			return emitJSON(raw)
		}
		r, err := getRules(cl, user)
		if err != nil {
			return err
		}
		groups, err := listGroups(cl)
		if err != nil {
			return err
		}
		rows := [][]string{}
		add := func(scope string, rules []model.RouteRule) {
			for i, x := range rules {
				on := "是"
				if !x.Enabled {
					on = "停用"
				}
				rows = append(rows, []string{scope, strconv.Itoa(i + 1), string(x.Type), ruleValueText(x), targetName(groups, x.Target), on, dash(x.Note)})
			}
		}
		if user != "" {
			add(user, r.UserRules)
		}
		add("全局", r.Global)
		if err := writeTable([]string{"层", "序号", "类型", "匹配", "去向", "启用", "备注"}, rows); err != nil {
			return err
		}
		final := r.Final
		if user != "" && r.UserFinal != "" {
			final = targetName(groups, r.UserFinal) + "(该用户覆盖)"
		} else {
			final = targetName(groups, final)
		}
		fmt.Printf("其他全部 → %s\n", final)
		return nil
	}})

	var at int
	var off bool
	var note string
	add := &cobra.Command{Use: "add TYPE VALUE TARGET", Short: "加一条规则(默认加在这一层最前面)", Args: cobra.ExactArgs(3), RunE: func(cmd *cobra.Command, args []string) error {
		t, ok := ruleTypeAlias[strings.ToLower(args[0])]
		if !ok {
			return fmt.Errorf("不认识的类型 %q,可选 suffix keyword set geoip cidr", args[0])
		}
		cl, err := newClient()
		if err != nil {
			return err
		}
		target, err := resolveTarget(cl, args[2])
		if err != nil {
			return err
		}
		r, err := getRules(cl, user)
		if err != nil {
			return err
		}
		rules, ver := r.scope(user)
		pos := at - 1
		if pos < 0 || pos > len(rules) {
			pos = 0
			if at > len(rules) {
				pos = len(rules)
			}
		}
		nr := model.RouteRule{Type: t, Value: args[1], Target: target, Enabled: !off, Note: note}
		rules = append(rules[:pos], append([]model.RouteRule{nr}, rules[pos:]...)...)
		return putRules(cl, user, rules, ver)
	}}
	add.Flags().IntVar(&at, "at", 1, "放在第几条(1 = 最前,超过条数 = 最后)")
	add.Flags().BoolVar(&off, "off", false, "先加成停用")
	add.Flags().StringVar(&note, "note", "", "备注")
	c.AddCommand(add)

	c.AddCommand(&cobra.Command{Use: "move FROM TO", Short: "调整规则顺序", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		r, err := getRules(cl, user)
		if err != nil {
			return err
		}
		rules, ver := r.scope(user)
		from, err := routeRuleIndex(args[0], len(rules))
		if err != nil {
			return err
		}
		to, err := routeRuleIndex(args[1], len(rules))
		if err != nil {
			return err
		}
		x := rules[from]
		rules = append(rules[:from], rules[from+1:]...)
		rules = append(rules[:to], append([]model.RouteRule{x}, rules[to:]...)...)
		return putRules(cl, user, rules, ver)
	}})

	c.AddCommand(&cobra.Command{Use: "rm N", Short: "删除一条规则", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		r, err := getRules(cl, user)
		if err != nil {
			return err
		}
		rules, ver := r.scope(user)
		i, err := routeRuleIndex(args[0], len(rules))
		if err != nil {
			return err
		}
		return putRules(cl, user, append(rules[:i], rules[i+1:]...), ver)
	}})

	c.AddCommand(&cobra.Command{Use: "toggle N", Short: "启用 / 停用一条规则", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		r, err := getRules(cl, user)
		if err != nil {
			return err
		}
		rules, _ := r.scope(user)
		i, err := routeRuleIndex(args[0], len(rules))
		if err != nil {
			return err
		}
		on := !rules[i].Enabled
		raw, err := cl.do("PATCH", fmt.Sprintf("/api/rules/%d", rules[i].ID), map[string]any{"enabled": on})
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		fmt.Println(map[bool]string{true: "已启用", false: "已停用"}[on])
		return nil
	}})

	c.AddCommand(&cobra.Command{Use: "final TARGET", Short: "设置「其他全部」的去向(--user 时传 - 取消覆盖)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		target := ""
		if args[0] != "-" {
			if target, err = resolveTarget(cl, args[0]); err != nil {
				return err
			}
		}
		path := "/api/rules/final"
		if user != "" {
			path += "?user=" + url.QueryEscape(user)
		}
		raw, err := cl.do("PUT", path, map[string]any{"target": target})
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		fmt.Println("已保存")
		return nil
	}})

	c.AddCommand(&cobra.Command{Use: "test DOMAIN", Short: "看一个网址会走哪条规则", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		raw, err := cl.do("POST", "/api/rules/test", map[string]any{"domain": args[0], "user": user})
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		var res struct {
			Domain  string `json:"domain"`
			Matched *struct {
				Scope string          `json:"scope"`
				Index int             `json:"index"`
				Rule  model.RouteRule `json:"rule"`
			} `json:"matched"`
			TargetName string   `json:"target_name"`
			Members    []string `json:"members"`
			Uncertain  []struct {
				Scope  string          `json:"scope"`
				Index  int             `json:"index"`
				Rule   model.RouteRule `json:"rule"`
				Reason string          `json:"reason"`
			} `json:"uncertain"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if res.Matched != nil {
			scope := map[string]string{"global": "全局", "user": user}[res.Matched.Scope]
			fmt.Printf("%s 命中 %s第 %d 条:%s %s\n", res.Domain, scope, res.Matched.Index, res.Matched.Rule.Type, ruleValueText(res.Matched.Rule))
		} else {
			fmt.Printf("%s 没有命中任何规则,走「其他全部」\n", res.Domain)
		}
		fmt.Printf("去向:%s\n", res.TargetName)
		if len(res.Members) > 0 {
			fmt.Printf("组内入口:%s\n", strings.Join(res.Members, "、"))
		}
		for _, u := range res.Uncertain {
			fmt.Printf("注意:第 %d 条 %s %s 无法判断(%s),客户端上可能先命中它\n", u.Index, u.Rule.Type, u.Rule.Value, u.Reason)
		}
		return nil
	}})

	c.AddCommand(&cobra.Command{Use: "catalog", Short: "列出内置规则组", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		raw, err := cl.do("GET", "/api/rule-catalog", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		var cat []model.RuleSet
		if err := json.Unmarshal(raw, &cat); err != nil {
			return err
		}
		rows := [][]string{}
		for _, s := range cat {
			rows = append(rows, []string{s.Key, s.Name, s.Desc})
		}
		return writeTable([]string{"KEY", "名称", "包含"}, rows)
	}})
	c.AddCommand(newRuleMigrateCmd(), newRuleModeCmd())
	return c
}

type migrationReport struct {
	Status        string   `json:"status"`
	Source        string   `json:"source"`
	SingBoxCustom bool     `json:"singbox_template_custom"`
	Groups        []string `json:"groups"`
	Rules         int      `json:"rules"`
	Final         string   `json:"final"`
	Purposes      []struct {
		Slug    string   `json:"slug"`
		Group   string   `json:"group"`
		Rules   int      `json:"rules"`
		Final   bool     `json:"final"`
		Dropped []string `json:"dropped"`
	} `json:"purposes"`
	Unconvertible []string `json:"unconvertible"`
	Notes         []string `json:"notes"`
}

var migrateStatusText = map[string]string{
	"migrated":      "已迁移,订阅改由规则表生成",
	"needs_confirm": "需要确认:存过自定义 sing-box 模板,加 --confirm 后迁移",
	"blocked":       "没有迁移:自定义 clash-meta 模板有转换不了的规则,订阅继续按模板生成",
	"skipped":       "没有迁移:已经有规则表(加 --replace 按旧配置重新生成,会覆盖现有规则)",
	"dry_run":       "演算结果(未写入)",
}

func printMigrationReport(r *migrationReport) {
	fmt.Println(migrateStatusText[r.Status])
	fmt.Printf("来源:%s\n", r.Source)
	if len(r.Groups) > 0 {
		fmt.Printf("策略组(%d):%s\n", len(r.Groups), strings.Join(r.Groups, "、"))
		fmt.Printf("全局规则:%d 条;其他全部 → %s\n", r.Rules, r.Final)
	}
	for _, p := range r.Purposes {
		line := fmt.Sprintf("用途 %s → 策略组 %s,%d 条规则", p.Slug, p.Group, p.Rules)
		if p.Final {
			line += ",并作为「其他全部」"
		}
		fmt.Println(line)
		for _, d := range p.Dropped {
			fmt.Printf("  去掉了:%s\n", d)
		}
	}
	for _, u := range r.Unconvertible {
		fmt.Printf("无法转换:%s\n", u)
	}
	for _, n := range r.Notes {
		fmt.Printf("注意:%s\n", n)
	}
}

func newRuleMigrateCmd() *cobra.Command {
	var dry, confirm, replace, report bool
	c := &cobra.Command{Use: "migrate", Short: "把模板和用途迁移成分流规则(--report 只看上次的报告)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		var raw []byte
		if report {
			if raw, err = cl.do("GET", "/api/routing", nil); err != nil {
				return err
			}
			var v struct {
				Mode      string           `json:"mode"`
				Migration *migrationReport `json:"migration"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSONValue(v)
			}
			fmt.Printf("当前模式:%s\n", v.Mode)
			if v.Migration == nil {
				fmt.Println("还没有迁移报告")
				return nil
			}
			printMigrationReport(v.Migration)
			return nil
		}
		if raw, err = cl.do("POST", "/api/routing/migrate", map[string]bool{"dry_run": dry, "confirm": confirm, "replace": replace}); err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		var r migrationReport
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		printMigrationReport(&r)
		return nil
	}}
	c.Flags().BoolVar(&report, "report", false, "只显示上次迁移的报告")
	c.Flags().BoolVar(&dry, "dry-run", false, "只演算,不写入")
	c.Flags().BoolVar(&confirm, "confirm", false, "确认 sing-box 的分流改为跟随规则表")
	c.Flags().BoolVar(&replace, "replace", false, "覆盖已有的规则和策略组")
	return c
}

func newRuleModeCmd() *cobra.Command {
	return &cobra.Command{Use: "mode [rules|template]", Short: "查看或切换分流模式(template = 回到旧模板与用途)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		var raw []byte
		if len(args) == 0 {
			raw, err = cl.do("GET", "/api/routing", nil)
		} else {
			raw, err = cl.do("PUT", "/api/routing", map[string]string{"mode": args[0]})
		}
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		var v struct {
			Mode     string   `json:"mode"`
			Warnings []string `json:"warnings"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		fmt.Printf("分流模式:%s\n", v.Mode)
		for _, w := range v.Warnings {
			fmt.Printf("注意:%s\n", w)
		}
		return nil
	}}
}
