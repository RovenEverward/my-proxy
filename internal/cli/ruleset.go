package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// mpx ruleset:上游规则集经控制面镜像分发(UC-05)。
//
// 分流规则引用的内置规则组(set 类型)本来要客户端直连上游地址下载;
// 规则集私有化把上游内容镜像到控制面,订阅里的地址改写成控制面地址。
// 这组命令只是镜像状态的客户端:拉取、校验与快照都在服务端,这里不碰内容。

func init() { rootCmd.AddCommand(newRulesetCmd()) }

const (
	// rulesetPollEvery 是 refresh --wait 的轮询间隔。规则集文件少但单个体积
	// 在 MB 级,2 秒一眼足够快,也不会给 /api 令牌桶添压力。
	rulesetPollEvery = 2 * time.Second

	// rulesetWaitCap 是等待镜像刷新结束的上限。刷新真正在等的是控制面拉上游
	// (服务端自己有 30s 超时与单飞合并),客户端只盯 refreshing 字段;到点
	// 退出,刷新本身在服务端继续跑。
	rulesetWaitCap = 5 * time.Minute
)

// rulesetFileView 对应 GET /api/rulesets 里单个镜像文件的视图。
type rulesetFileView struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Ready     bool   `json:"ready"`
	LastError string `json:"last_error"`
}

// rulesetSideView 是某个客户端格式(clash 或 sing_box)下这组文件的聚合。
type rulesetSideView struct {
	Files []rulesetFileView `json:"files"`
	Ready int               `json:"ready"`
	Total int               `json:"total"`
}

// rulesetGroupView 是一个内置规则组的镜像状态:两个格式分列,外加组级
// 刷新标记与上次尝试时间(Unix 毫秒,0 = 还没拉过)。服务端只给被规则表
// 引用的组;geoip 规则会合成一个 key 为 "geoip" 的组排在最后。
type rulesetGroupView struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	PrivateIP   bool            `json:"private_ip"`
	Clash       rulesetSideView `json:"clash"`
	SingBox     rulesetSideView `json:"sing_box"`
	Refreshing  bool            `json:"refreshing"`
	LastAttempt int64           `json:"last_attempt_at"`
}

// rulesetRefreshResult 是 --wait 结束后 -o json 给出的汇总:
// 触发结果(202 回执)、目标组的最终视图、不在清单里的 key。
type rulesetRefreshResult struct {
	Started     int                `json:"started"`
	Groups      []rulesetGroupView `json:"groups"`
	UnknownKeys []string           `json:"unknown_keys,omitempty"`
}

func newRulesetCmd() *cobra.Command {
	c := &cobra.Command{Use: "ruleset", Short: "管理上游规则集的本地镜像", Long: `上游规则集的本地镜像:分流规则引用的内置规则组(mpx rule catalog)由
控制面定时拉取并保存快照;「私有化分发」开启后,订阅里这些规则集的地址改写成
控制面地址,客户端不再直连上游(开关见 mpx settings ruleset-mode)。

status 看每个组的镜像就绪情况;refresh 立即拉一轮。镜像失败且已有快照时
控制面继续发旧内容,刷新失败不是停服。`}

	c.AddCommand(&cobra.Command{Use: "status", Short: "列出各规则组的镜像状态", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cl, err := newClient()
		if err != nil {
			return err
		}
		raw, err := cl.do("GET", "/api/rulesets", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		groups, err := decode[[]rulesetGroupView](raw)
		if err != nil {
			return err
		}
		rows := [][]string{}
		for _, g := range groups {
			rows = append(rows, []string{
				g.Key,
				g.Name,
				rulesetFilesText(&g),
				rulesetReadyText(&g),
				rulesetTimeText(g.LastAttempt),
				rulesetStatusText(&g),
			})
		}
		return writeTable([]string{"组 KEY", "组名", "文件", "就绪", "上次尝试", "状态"}, rows)
	}})

	var refreshWait bool
	refresh := &cobra.Command{
		Use: "refresh [KEY...]", Short: "手动刷新规则集镜像(默认刷全部并等它拉完)",
		Example: `  mpx ruleset refresh            # 刷新全部被引用的组,等它结束
  mpx ruleset refresh lan ads    # 只刷这两个组
  mpx ruleset refresh --no-wait  # 只触发,立即返回`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			keys := rulesetKeys(args)
			var body any
			if len(keys) > 0 {
				body = map[string]any{"keys": keys}
			}
			raw, err := cl.do("POST", "/api/rulesets/refresh", body)
			if err != nil {
				return err
			}
			if !refreshWait {
				return report(raw, "已提交刷新(异步进行,用 mpx ruleset status 看结果)")
			}
			started := 0
			var ack struct {
				Started int `json:"started"`
			}
			if err := json.Unmarshal(raw, &ack); err == nil {
				started = ack.Started
			}
			if opts.output != outputJSON {
				fmt.Println("已提交刷新,等待镜像就绪…")
			}
			list, unknown, err := waitRulesetRefresh(cl, keys)
			result := rulesetRefreshResult{Started: started, Groups: list, UnknownKeys: unknown}
			if err != nil {
				if opts.output == outputJSON {
					_ = emitJSONValue(result)
				}
				return err
			}
			failures := rulesetFailures(list)
			if opts.output == outputJSON {
				_ = emitJSONValue(result)
				// JSON 模式同样靠退出码判断成败,不能 grep 正文。
				if len(failures) > 0 {
					return fmt.Errorf("刷新失败: %s", strings.Join(failures, "; "))
				}
				return nil
			}
			rows := [][]string{}
			for _, g := range list {
				rows = append(rows, []string{g.Key, g.Name, rulesetReadyText(&g), rulesetStatusText(&g)})
			}
			if err := writeTable([]string{"组 KEY", "组名", "就绪", "状态"}, rows); err != nil {
				return err
			}
			for _, k := range unknown {
				fmt.Printf("注意:%s 不在被引用的规则组里,服务端没刷它(见 mpx ruleset status)\n", k)
			}
			if len(failures) > 0 {
				for _, f := range failures {
					fmt.Printf("刷新失败:%s\n", f)
				}
				return fmt.Errorf("有 %d 个规则组刷新失败(详见 mpx ruleset status -o json)", len(failures))
			}
			return nil
		},
	}
	refresh.Flags().BoolVar(&refreshWait, "wait", true, "等刷新结束再返回(--no-wait 立即返回;失败会报原因并以非零退出)")
	c.AddCommand(refresh)

	return c
}

// rulesetKeys 归一化组 key:去空白、去重,保持命令行给定的顺序。
// 返回 nil 表示「没给」= 刷全部被引用的组。
func rulesetKeys(args []string) []string {
	var keys []string
	seen := map[string]bool{}
	for _, a := range args {
		k := strings.TrimSpace(a)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	return keys
}

// listRulesets 取回规则组镜像清单。
func listRulesets(cl *Client) ([]rulesetGroupView, error) {
	raw, err := cl.do("GET", "/api/rulesets", nil)
	if err != nil {
		return nil, err
	}
	return decode[[]rulesetGroupView](raw)
}

// waitRulesetRefresh 轮询镜像状态,直到目标组(全部或 keys 指定的)都不再
// refreshing,返回最后一眼的清单与 keys 里不在清单中的名字。
func waitRulesetRefresh(cl *Client, keys []string) ([]rulesetGroupView, []string, error) {
	deadline := time.Now().Add(rulesetWaitCap)
	for {
		list, err := listRulesets(cl)
		if err != nil {
			return nil, nil, err
		}
		filtered, unknown := filterRulesets(list, keys)
		if !rulesetAnyRefreshing(filtered) {
			return filtered, unknown, nil
		}
		if time.Now().After(deadline) {
			return filtered, unknown, fmt.Errorf("等待刷新超过 %s:服务端还在拉,用 mpx ruleset status 看结果", rulesetWaitCap)
		}
		time.Sleep(rulesetPollEvery)
	}
}

// filterRulesets 在 keys 非空时挑出目标组;keys 里服务端清单没有的名字放进
// unknown 还给调用方 —— 拼错的 key 不该静默等满 5 分钟。
func filterRulesets(list []rulesetGroupView, keys []string) ([]rulesetGroupView, []string) {
	if len(keys) == 0 {
		return list, nil
	}
	byKey := map[string]*rulesetGroupView{}
	for i := range list {
		byKey[list[i].Key] = &list[i]
	}
	var filtered []rulesetGroupView
	var unknown []string
	for _, k := range keys {
		if g, ok := byKey[k]; ok {
			filtered = append(filtered, *g)
		} else {
			unknown = append(unknown, k)
		}
	}
	return filtered, unknown
}

func rulesetAnyRefreshing(groups []rulesetGroupView) bool {
	for _, g := range groups {
		if g.Refreshing {
			return true
		}
	}
	return false
}

// rulesetFailures 收集各组文件级的 last_error,格式为「组名: 原因」。
// 镜像失败时服务端保留旧快照继续分发,所以这是「刷新没成功」,不是「分发坏了」。
func rulesetFailures(groups []rulesetGroupView) []string {
	var out []string
	for _, g := range groups {
		for _, e := range rulesetErrors(&g) {
			out = append(out, fmt.Sprintf("%s: %s", g.Key, e))
		}
	}
	return out
}

// rulesetErrors 汇总两个格式下非空的文件错误(去重:同一原因不必报两次)。
func rulesetErrors(g *rulesetGroupView) []string {
	seen := map[string]bool{}
	var out []string
	for _, side := range []rulesetSideView{g.Clash, g.SingBox} {
		for _, f := range side.Files {
			if f.LastError != "" && !seen[f.LastError] {
				seen[f.LastError] = true
				out = append(out, f.LastError)
			}
		}
	}
	return out
}

// rulesetStatusText 是状态列的一句话:刷新中优先,再看有没有错,再看就绪数。
func rulesetStatusText(g *rulesetGroupView) string {
	switch {
	case g.Refreshing:
		return "刷新中"
	case len(rulesetErrors(g)) > 0:
		return "刷新失败"
	}
	total := g.Clash.Total + g.SingBox.Total
	ready := g.Clash.Ready + g.SingBox.Ready
	switch {
	case total > 0 && ready >= total:
		return "就绪"
	case ready > 0:
		return "部分就绪"
	default:
		return "未就绪"
	}
}

// rulesetTimeText 把 Unix 毫秒的上次尝试时间截到秒显示;0(还没拉过)显示 "-"。
func rulesetTimeText(t int64) string {
	if t <= 0 {
		return "-"
	}
	return time.UnixMilli(t).Local().Format("01-02 15:04:05")
}

// rulesetReadyText 把两个格式的就绪数并排显示;没引用文件的格式打 "-"。
func rulesetReadyText(g *rulesetGroupView) string {
	side := func(s rulesetSideView) string {
		if s.Total == 0 {
			return "-"
		}
		return fmt.Sprintf("%d/%d", s.Ready, s.Total)
	}
	return fmt.Sprintf("clash %s · sing-box %s", side(g.Clash), side(g.SingBox))
}

// rulesetFilesText 把两个格式的文件名并排列出,失败的文件挂「(错)」。
// 表格模式只给名字,原因太长,留在 -o json 与 UI 里看。
func rulesetFilesText(g *rulesetGroupView) string {
	side := func(tag string, s rulesetSideView) string {
		if s.Total == 0 {
			return ""
		}
		names := make([]string, 0, len(s.Files))
		for _, f := range s.Files {
			name := f.Name
			if f.LastError != "" {
				name += "(错)"
			}
			names = append(names, name)
		}
		return tag + ": " + strings.Join(names, "、")
	}
	parts := []string{side("clash", g.Clash), side("sing-box", g.SingBox)}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "  ")
}

// ---- mpx settings ruleset-mode ----

// rulesetModeCmd 查看与切换规则集分发方式:upstream(现状,订阅直接给上游
// 地址)或 privatized(客户端经控制面镜像下载)。挂在 settings.go 的 init() 里。
// 空串与 "upstream" 同义 —— 老库读出来的默认就是空。
var rulesetModeCmd = &cobra.Command{
	Use:   "ruleset-mode",
	Short: "查看与切换规则集分发方式(upstream / privatized)",
	Long: `规则集分发方式决定客户端从哪下载内置规则组的上游规则集:

upstream     订阅直接给上游地址(默认,与历史行为一致)
privatized   控制面镜像上游内容,订阅地址改指控制面

客户端够不到上游规则集地址时分流规则加载失败,这时切到 privatized。
切换不改变规则本身,只改订阅里规则集的来源;镜像状态见 mpx ruleset status。`,
}

var rulesetModeShowCmd = &cobra.Command{Use: "show", Short: "显示当前的分发方式", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	s, err := readSettings(c)
	if err != nil {
		return err
	}
	mode := s.RulesetMode
	if opts.output == outputJSON {
		return emitJSONValue(map[string]string{"ruleset_mode": mode})
	}
	return writeKV([][2]string{
		{"分发方式", rulesetModeText(mode)},
		{"当前值", mode},
	})
}}

var rulesetModeSetCmd = &cobra.Command{Use: "set MODE", Short: "设置分发方式(upstream / privatized)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	mode := args[0]
	// 本地先拦一次,与服务端 handleSaveSettings 用同一个枚举:空串虽合法但
	// 那是「老库没写过」的零值,人命令行里显式传空没有意义。
	if mode == "" || !model.RulesetModeValid(mode) {
		return fmt.Errorf("分发方式只能是 upstream 或 privatized,收到 %q", mode)
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	s, err := readSettings(c)
	if err != nil {
		return err
	}
	s.RulesetMode = mode
	raw, err := saveSettings(c, s)
	if err != nil {
		return err
	}
	return report(raw, "已设置规则集分发方式: %s", mode)
}}

func init() {
	rulesetModeCmd.AddCommand(rulesetModeShowCmd, rulesetModeSetCmd)
}

// rulesetModeText 把存储值翻译成人话;空串是老库的默认,与 upstream 同义。
func rulesetModeText(mode string) string {
	switch mode {
	case "privatized":
		return "privatized(经控制面镜像分发)"
	case "", "upstream":
		return "upstream(客户端直连上游,默认)"
	default:
		return mode + "(未知值)"
	}
}
