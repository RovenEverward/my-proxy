package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// mpx source:外部订阅源(UC-03)。
//
// 来源由控制面定时拉取并保存快照;操作者按用户显式授权,接收用户的原订阅链接
// 里同时出现自建与外部节点。所有命令都只是 /api/sources 的客户端,不缓存、
// 不直接碰库;拉取与解析都在服务端。
//
// 真实订阅地址不放进命令行参数(会落进 shell 历史):add 默认交互读入且不回显,
// 自动化走 --url-stdin 从标准输入喂;edit 用同一个 flag 换地址。

// sourceView 对应控制面 GET /api/sources/{id} 的 sourceView 契约。
// 它只有元数据:遮蔽后的 URL、状态、计数与原因 —— 真实凭证只进快照与授权订阅,
// 详情接口从不回吐。
type sourceView struct {
	ID              int64                   `json:"id"`
	Name            string                  `json:"name"`
	URLMasked       string                  `json:"url_masked"`
	Enabled         bool                    `json:"enabled"`
	IntervalSeconds int                     `json:"interval_seconds"`
	Region          string                  `json:"region"`
	Revision        int64                   `json:"revision"`
	LastAttemptAt   string                  `json:"last_attempt_at"`
	LastGoodAt      string                  `json:"last_good_at"`
	LastError       string                  `json:"last_error"`
	Refreshing      bool                    `json:"refreshing"`
	NodeCount       int                     `json:"node_count"`
	InputFormat     string                  `json:"input_format"`
	Issues          []sourceIssue           `json:"issues"`
	Formats         map[string]sourceFormat `json:"formats"`
}

// sourceIssue 是快照里被跳过条目的一条说明:位置、显示名与原因。
type sourceIssue struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// sourceFormat 是某个输出格式下这批节点的兼容结果。
type sourceFormat struct {
	Count  int           `json:"count"`
	Issues []sourceIssue `json:"issues"`
}

// sourcePreview 对应 GET /api/users/{username}/subscription-preview:
// 按用户与格式合并后的端点清单,自建与外部来源分列,外加整份级问题。
type sourcePreview struct {
	Nodes []struct {
		Name       string   `json:"name"`
		Protocol   string   `json:"protocol"`
		SourceID   int64    `json:"source_id"`
		SourceName string   `json:"source_name"`
		Groups     []string `json:"groups"`
		Reasons    []string `json:"reasons"`
	} `json:"nodes"`
	Issues []sourceIssue `json:"issues"`
}

const (
	// sourceDefaultInterval 与契约一致:默认 6 小时,0 表示只手动刷新。
	sourceDefaultInterval = 21600 * time.Second

	// refreshPollEvery 是 refresh/add 等待时的轮询间隔。
	refreshPollEvery = 500 * time.Millisecond
	// refreshWaitCap 是 CLI 等待一次刷新结束的上限。
	//
	// 刷新真正在等的是上游的 HTTP 拉取(服务端自己有超时与体积上限),客户端
	// 只负责盯 refreshing 字段;给个有限上限是为了上游或服务端卡住时
	// CLI 不会永远转下去 —— 到时退出,刷新本身在服务端继续跑。
	refreshWaitCap = 3 * time.Minute
)

// sourceFlags 是 add / edit 的可写字段。除 URL 外都经 PATCH/POST 的对应键发送。
type sourceFlags struct {
	interval time.Duration
	region   string
	disabled bool
	enabled  bool
	name     string
	urlStdin bool
	wait     bool
}

func (f *sourceFlags) bind(c *cobra.Command, forAdd bool) {
	c.Flags().DurationVar(&f.interval, "interval", sourceDefaultInterval, "刷新间隔(如 6h;0 = 只手动刷新)")
	c.Flags().StringVar(&f.region, "region", "", "来源默认地区(外部节点不凭名字判地区,这里给一个兜底)")
	if forAdd {
		c.Flags().BoolVar(&f.disabled, "disabled", false, "先建出来但停用(不输出、不自动拉取)")
		c.Flags().BoolVar(&f.urlStdin, "url-stdin", false, "从标准输入读订阅地址(自动化用;交互下不回显输入)")
		c.Flags().BoolVar(&f.wait, "wait", true, "等首次拉取结束再返回(--wait=false 立即返回)")
	} else {
		c.Flags().StringVar(&f.name, "name", "", "改名")
		c.Flags().BoolVar(&f.urlStdin, "url-stdin", false, "从标准输入读新的订阅地址")
	}
}

func newSourceCmd() *cobra.Command {
	c := &cobra.Command{Use: "source", Short: "管理外部订阅源", Long: `外部订阅源:MPX 定时拉取一份订阅快照,授权给指定用户后,对方的原订阅地址里
同时出现自建节点与外部节点。

真实订阅地址不进命令行:source add 交互输入(不回显),自动化用 --url-stdin 从
stdin 喂。interval 用 Go 时长写法(6h、30m),0 表示只手动刷新。`}

	var addF sourceFlags
	add := &cobra.Command{
		Use:   "add NAME",
		Short: "添加一个外部订阅源(交互输入 URL,默认 6h 刷新,自动首次拉取)",
		Example: `  mpx source add "常用订阅"            # 提示输入 URL,等待首次拉取
  echo "$URL" | mpx source add "备用" --url-stdin --interval 12h
  mpx source add "手动源" --interval 0 --wait=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			rawURL, err := readSourceURL(addF.urlStdin)
			if err != nil {
				return err
			}
			body := map[string]any{
				"name":             strings.TrimSpace(args[0]),
				"url":              rawURL,
				"interval_seconds": int(addF.interval.Seconds()),
				"enabled":          !addF.disabled,
			}
			if strings.TrimSpace(addF.region) != "" {
				body["region"] = strings.TrimSpace(addF.region)
			}
			raw, err := cl.do("POST", "/api/sources", body)
			if err != nil {
				return err
			}
			v, err := decode[sourceView](raw)
			if err != nil {
				return err
			}
			waited := false
			if addF.wait && v.Refreshing {
				if opts.output != outputJSON {
					fmt.Printf("已创建来源 %d(%s),等待首次拉取…\n", v.ID, v.Name)
				}
				if v, err = waitSourceRefresh(cl, v.ID); err != nil {
					return err
				}
				raw, _ = json.Marshal(v)
				waited = true
			}
			if opts.output == outputJSON {
				if err := emitJSON(raw); err != nil {
					return err
				}
				// --wait 且拉取已败(等完或本已结束)也要非零退出:脚本靠退出码。
				if addF.wait && v.LastError != "" {
					return fmt.Errorf("来源 %d 首次拉取失败: %s", v.ID, v.LastError)
				}
				return nil
			}
			if !waited {
				fmt.Printf("已创建来源 %d(%s)\n", v.ID, v.Name)
			}
			if addF.wait && v.LastError != "" {
				fmt.Printf("首次拉取失败:%s\n", v.LastError)
				printSourceSummary(&v)
				return fmt.Errorf("来源 %d 首次拉取失败: %s", v.ID, v.LastError)
			}
			printSourceSummary(&v)
			return nil
		},
	}
	addF.bind(add, true)
	c.AddCommand(add)

	c.AddCommand(&cobra.Command{
		Use: "list", Short: "列出外部订阅源", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			raw, err := cl.do("GET", "/api/sources", nil)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			list, err := decode[[]sourceView](raw)
			if err != nil {
				return err
			}
			rows := [][]string{}
			for _, s := range list {
				rows = append(rows, []string{
					strconv.FormatInt(s.ID, 10),
					s.Name,
					s.URLMasked,
					sourceState(&s),
					strconv.Itoa(s.NodeCount),
					sourceIntervalText(s.IntervalSeconds),
					sourceTimeText(s.LastGoodAt),
				})
			}
			return writeTable([]string{"ID", "名称", "订阅地址", "状态", "节点", "刷新间隔", "最近成功"}, rows)
		},
	})

	c.AddCommand(&cobra.Command{
		Use: "show NAME|ID", Short: "查看来源快照、刷新状态与各格式兼容报告", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			v, err := findSource(cl, args[0])
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				raw, err := cl.do("GET", sourcePath(v.ID), nil)
				if err != nil {
					return err
				}
				return emitJSON(raw)
			}
			return printSourceDetail(cl, v)
		},
	})

	var editF sourceFlags
	edit := &cobra.Command{
		Use: "edit NAME|ID", Short: "修改来源(只改给了的选项;换 URL 走 --url-stdin)", Args: cobra.ExactArgs(1),
		Example: `  mpx source edit 7 --interval 12h
  mpx source edit 7 --region JP --name "新名字"
  echo "$URL" | mpx source edit 7 --url-stdin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			v, err := findSource(cl, args[0])
			if err != nil {
				return err
			}
			patch := map[string]any{}
			if cmd.Flags().Changed("name") {
				patch["name"] = editF.name
			}
			if cmd.Flags().Changed("region") {
				patch["region"] = editF.region
			}
			if cmd.Flags().Changed("interval") {
				patch["interval_seconds"] = int(editF.interval.Seconds())
			}
			if editF.urlStdin {
				rawURL, err := readSourceURL(true)
				if err != nil {
					return err
				}
				patch["url"] = rawURL
			}
			if len(patch) == 0 {
				return fmt.Errorf("没给要改的字段(--name/--region/--interval/--url-stdin;启停用 source enable|disable)")
			}
			raw, err := cl.do("PATCH", sourcePath(v.ID), patch)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			fmt.Println("已保存")
			return nil
		},
	}
	editF.bind(edit, false)
	c.AddCommand(edit)

	var refreshWait bool
	refresh := &cobra.Command{
		Use: "refresh NAME|ID", Short: "手动刷新来源(默认等它拉完)", Args: cobra.ExactArgs(1),
		Example: `  mpx source refresh 7                # 等待刷新结束,失败会报原因
  mpx source refresh 7 --wait=false   # 只触发,立即返回`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			v, err := findSource(cl, args[0])
			if err != nil {
				return err
			}
			raw, err := cl.do("POST", sourcePath(v.ID)+"/refresh", nil)
			if err != nil {
				return err
			}
			if !refreshWait {
				return report(raw, "已提交刷新(异步进行,用 mpx source show %d 看结果)", v.ID)
			}
			cur, err := decode[sourceView](raw)
			if err != nil {
				return err
			}
			if cur.Refreshing {
				v2, err := waitSourceRefresh(cl, v.ID)
				if err != nil {
					return err
				}
				cur = v2
			}
			raw, _ = json.Marshal(cur)
			if opts.output == outputJSON {
				if cur.LastError != "" {
					// JSON 模式也保证非零退出码:脚本靠它判断成败,不 grep 正文。
					_ = emitJSON(raw)
					return fmt.Errorf("刷新失败: %s", cur.LastError)
				}
				return emitJSON(raw)
			}
			if cur.LastError != "" {
				fmt.Printf("刷新失败(沿用上次快照):%s\n", cur.LastError)
				printSourceSummary(&cur)
				return fmt.Errorf("来源 %d 刷新失败: %s", cur.ID, cur.LastError)
			}
			fmt.Printf("已刷新,快照里有 %d 个节点\n", cur.NodeCount)
			printSourceSummary(&cur)
			return nil
		},
	}
	refresh.Flags().BoolVar(&refreshWait, "wait", true, "等刷新结束再返回(失败会报原因并以非零退出)")
	c.AddCommand(refresh)

	c.AddCommand(&cobra.Command{
		Use: "grants NAME|ID", Short: "列出已授权的用户", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			v, err := findSource(cl, args[0])
			if err != nil {
				return err
			}
			raw, err := cl.do("GET", sourcePath(v.ID)+"/grants", nil)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			users, err := decodeGrants(raw)
			if err != nil {
				return err
			}
			if len(users) == 0 {
				fmt.Println("(未授权给任何用户)")
				return nil
			}
			for _, u := range users {
				fmt.Println(u)
			}
			return nil
		},
	})

	var grantUsers []string
	grant := &cobra.Command{
		Use: "grant NAME|ID --user USER...", Short: "把来源授权给用户(可重复 --user;重复授权幂等)",
		Example: `  mpx source grant 7 --user alice
  mpx source grant 7 --user alice --user bob`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return sourceGrantEach(args[0], grantUsers, func(cl *Client, id int64, u string) ([]byte, error) {
				return cl.do("PUT", fmt.Sprintf("%s/grants/%s", sourcePath(id), url.PathEscape(u)), nil)
			}, "已授权 %s")
		},
	}
	grant.Flags().StringSliceVar(&grantUsers, "user", nil, "接收这个来源的用户(必填,可重复或逗号分隔)")
	_ = grant.MarkFlagRequired("user")
	c.AddCommand(grant)

	var revokeUsers []string
	revoke := &cobra.Command{
		Use: "revoke NAME|ID --user USER...", Short: "撤回某用户对该来源的授权(不影响对方已保存的上游凭证)",
		Example: `  mpx source revoke 7 --user alice`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return sourceGrantEach(args[0], revokeUsers, func(cl *Client, id int64, u string) ([]byte, error) {
				return cl.do("DELETE", fmt.Sprintf("%s/grants/%s", sourcePath(id), url.PathEscape(u)), nil)
			}, "已撤回 %s")
		},
	}
	revoke.Flags().StringSliceVar(&revokeUsers, "user", nil, "要撤回授权的用户(必填,可重复或逗号分隔)")
	_ = revoke.MarkFlagRequired("user")
	c.AddCommand(revoke)

	c.AddCommand(&cobra.Command{
		Use: "disable NAME|ID", Short: "停用来源(停止输出与定时拉取,保留配置、授权与快照)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return sourceSetEnabled(args[0], false)
		},
	})
	c.AddCommand(&cobra.Command{
		Use: "enable NAME|ID", Short: "恢复来源(之后用 source refresh 拉新快照)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return sourceSetEnabled(args[0], true)
		},
	})

	c.AddCommand(&cobra.Command{
		Use: "delete NAME|ID", Aliases: []string{"rm"}, Short: "删除来源(连同授权与快照;引用它的策略组条件保留为「来源已删除」)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			v, err := findSource(cl, args[0])
			if err != nil {
				return err
			}
			raw, err := cl.do("DELETE", sourcePath(v.ID), nil)
			if err != nil {
				return err
			}
			return report(raw, "已删除来源 %d(%s)", v.ID, v.Name)
		},
	})

	var previewUser, previewFormat string
	preview := &cobra.Command{
		Use:   "preview --user USER",
		Short: "按用户预览混合订阅:自建与外部节点、所在策略组与排除原因",
		Example: `  mpx source preview --user alice
  mpx source preview --user alice --format=sing-box`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			q := url.Values{}
			if previewFormat != "" {
				q.Set("format", previewFormat)
			}
			path := fmt.Sprintf("/api/users/%s/subscription-preview", url.PathEscape(previewUser))
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			raw, err := cl.do("GET", path, nil)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emitJSON(raw)
			}
			p, err := decode[sourcePreview](raw)
			if err != nil {
				return err
			}
			rows := [][]string{}
			for _, n := range p.Nodes {
				rows = append(rows, []string{
					n.Name,
					n.Protocol,
					dash(n.SourceName),
					dash(strings.Join(n.Groups, "/")),
					strings.Join(n.Reasons, "、"),
				})
			}
			if err := writeTable([]string{"节点", "协议", "来源", "策略组", "说明"}, rows); err != nil {
				return err
			}
			for _, i := range p.Issues {
				fmt.Printf("注意:第 %d 条 %s 跳过(%s)\n", i.Index, i.Name, i.Reason)
			}
			return nil
		},
	}
	preview.Flags().StringVar(&previewUser, "user", "", "按这个用户预览(必填)")
	_ = preview.MarkFlagRequired("user")
	preview.Flags().StringVar(&previewFormat, "format", "clash-meta", "输出格式:links / base64-links / clash / clash-meta / sing-box / xray")
	c.AddCommand(preview)

	return c
}

func init() { rootCmd.AddCommand(newSourceCmd()) }

// sourcePath 是单个来源的 API 前缀。
func sourcePath(id int64) string { return fmt.Sprintf("/api/sources/%d", id) }

// listSources 取回全部来源。
func listSources(cl *Client) ([]sourceView, error) {
	raw, err := cl.do("GET", "/api/sources", nil)
	if err != nil {
		return nil, err
	}
	var list []sourceView
	err = json.Unmarshal(raw, &list)
	return list, err
}

// findSource 把位置参数换成一条来源:数字按 ID,其余按名字匹配(与 group 的口径一致)。
func findSource(cl *Client, s string) (*sourceView, error) {
	id, idErr := idArg(s)
	if idErr == nil {
		raw, err := cl.do("GET", sourcePath(id), nil)
		if err != nil {
			return nil, err
		}
		v, err := decode[sourceView](raw)
		return &v, err
	}
	list, err := listSources(cl)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == s {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("没有叫 %q 的外部订阅源(见 mpx source list)", s)
}

// getSource 读一个来源的当前视图。
func getSource(cl *Client, id int64) (sourceView, error) {
	raw, err := cl.do("GET", sourcePath(id), nil)
	if err != nil {
		return sourceView{}, err
	}
	return decode[sourceView](raw)
}

// waitSourceRefresh 轮询来源视图直到 refreshing 变假,返回最后一眼。
func waitSourceRefresh(cl *Client, id int64) (sourceView, error) {
	deadline := time.Now().Add(refreshWaitCap)
	for {
		v, err := getSource(cl, id)
		if err != nil {
			return v, err
		}
		if !v.Refreshing {
			return v, nil
		}
		if time.Now().After(deadline) {
			return v, fmt.Errorf("等待刷新超过 %s:服务端还在拉,用 mpx source show %d 看结果", refreshWaitCap, id)
		}
		time.Sleep(refreshPollEvery)
	}
}

// sourceGrantEach 是 grant/revoke 的公共骨架:逐个用户对幂等的按人接口发请求。
// 授权不是一条 PUT 整表替换,是为了「重复执行不重复授权」与部分失败可重试。
func sourceGrantEach(arg string, users []string, act func(cl *Client, id int64, user string) ([]byte, error), doneFmt string) error {
	cl, err := newClient()
	if err != nil {
		return err
	}
	v, err := findSource(cl, arg)
	if err != nil {
		return err
	}
	for _, u := range users {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		raw, err := act(cl, v.ID, u)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			if err := emitJSON(raw); err != nil {
				return err
			}
			continue
		}
		fmt.Printf(doneFmt+"\n", u)
	}
	return nil
}

// sourceSetEnabled 是 enable/disable 的公共骨架:PATCH 只改 enabled 一个字段。
func sourceSetEnabled(arg string, on bool) error {
	cl, err := newClient()
	if err != nil {
		return err
	}
	v, err := findSource(cl, arg)
	if err != nil {
		return err
	}
	raw, err := cl.do("PATCH", sourcePath(v.ID), map[string]any{"enabled": on})
	if err != nil {
		return err
	}
	if on {
		return report(raw, "已启用来源 %d(%s)", v.ID, v.Name)
	}
	return report(raw, "已停用来源 %d(%s)", v.ID, v.Name)
}

// decodeGrants 解出 {users:[...]} 里的名单。
func decodeGrants(raw []byte) ([]string, error) {
	var g struct {
		Users []string `json:"users"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("解析授权列表: %w", err)
	}
	return g.Users, nil
}

// readSourceURL 取真实订阅地址。
//
// urlStdin 为真、或 stdin 不是终端(管道/重定向)时从 stdin 读一行;
// 终端上交互输入且不回显 —— 这条命令的全部意义就是不让 URL 落进命令历史,
// 屏幕上也尽量不留下。
func readSourceURL(urlStdin bool) (string, error) {
	if urlStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
		if err != nil {
			return "", fmt.Errorf("从 stdin 读订阅地址: %w", err)
		}
		u := strings.TrimSpace(string(data))
		if u == "" {
			return "", fmt.Errorf("stdin 里没有订阅地址")
		}
		return u, nil
	}
	fmt.Fprint(os.Stderr, "外部订阅地址(输入不回显): ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // ReadPassword 不吃回车,补个换行让提示符回行首
	if err != nil {
		return "", fmt.Errorf("读订阅地址: %w", err)
	}
	u := strings.TrimSpace(string(b))
	if u == "" {
		return "", fmt.Errorf("订阅地址不能为空")
	}
	return u, nil
}

// sourceState 是列表里「状态」一列的一句话。
func sourceState(s *sourceView) string {
	switch {
	case !s.Enabled:
		return "已停用"
	case s.Refreshing:
		return "刷新中"
	case s.LastError != "":
		return "失败(用上次快照)"
	case s.LastGoodAt == "":
		return "未就绪"
	default:
		return "正常"
	}
}

// sourceIntervalText 把秒数显示成时长;0 表示手动。
func sourceIntervalText(sec int) string {
	if sec <= 0 {
		return "手动"
	}
	return (time.Duration(sec) * time.Second).String()
}

// sourceTimeText 把时间字段截短显示;空值显示 "-"。
// 时间字段按 RFC3339 传;截到秒足够认出来"多久以前"。
func sourceTimeText(s string) string {
	if s == "" {
		return "-"
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Local().Format("01-02 15:04:05")
	}
	return s
}

// printSourceSummary 在创建/刷新后给一段紧凑的回执。
func printSourceSummary(v *sourceView) {
	fmt.Printf("  状态:%s  节点:%d  输入格式:%s\n", sourceState(v), v.NodeCount, dash(v.InputFormat))
	if len(v.Issues) > 0 {
		fmt.Printf("  跳过 %d 条(用 mpx source show %d 看原因)\n", len(v.Issues), v.ID)
	}
}

// printSourceDetail 是 source show 的表格输出。
func printSourceDetail(cl *Client, v *sourceView) error {
	pairs := [][2]string{
		{"ID", strconv.FormatInt(v.ID, 10)},
		{"名称", v.Name},
		{"订阅地址", v.URLMasked},
		{"状态", sourceState(v)},
		{"刷新间隔", sourceIntervalText(v.IntervalSeconds)},
		{"默认地区", dash(v.Region)},
		{"快照版本", strconv.FormatInt(v.Revision, 10)},
		{"节点数", strconv.Itoa(v.NodeCount)},
		{"输入格式", dash(v.InputFormat)},
		{"最近尝试", sourceTimeText(v.LastAttemptAt)},
		{"最近成功", sourceTimeText(v.LastGoodAt)},
	}
	if v.LastError != "" {
		pairs = append(pairs, [2]string{"最近错误", v.LastError})
	}
	if err := writeKV(pairs); err != nil {
		return err
	}
	if len(v.Formats) > 0 {
		// 按格式名排序,不然 map 序每次都不一样。
		names := make([]string, 0, len(v.Formats))
		for name := range v.Formats {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := [][]string{}
		for _, n := range names {
			rows = append(rows, []string{n, strconv.Itoa(v.Formats[n].Count), strconv.Itoa(len(v.Formats[n].Issues))})
		}
		fmt.Println("各格式输出:")
		if err := writeTable([]string{"格式", "节点", "跳过"}, rows); err != nil {
			return err
		}
	}
	if len(v.Issues) > 0 {
		fmt.Println("跳过的条目:")
		rows := [][]string{}
		for _, i := range v.Issues {
			rows = append(rows, []string{strconv.Itoa(i.Index), dash(i.Name), i.Reason})
		}
		if err := writeTable([]string{"序号", "名称", "原因"}, rows); err != nil {
			return err
		}
	}
	// 授权名单顺手摆出来:show 是「这个来源现在什么样」的完整答案,
	// 「授权给谁」是其中一半;接口是现成的,不该让人再敲一条命令。
	raw, err := cl.do("GET", sourcePath(v.ID)+"/grants", nil)
	if err != nil {
		return err
	}
	users, err := decodeGrants(raw)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		fmt.Println("授权:无(还没有用户能看到这个来源)")
	} else {
		fmt.Printf("授权:%s\n", strings.Join(users, "、"))
	}
	return nil
}
