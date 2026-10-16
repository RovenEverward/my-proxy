package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "管理用户与订阅",
	Long: `管理用户与订阅 —— 谁可以用、能用多少、能连哪里。

一个用户有两套互相独立的凭证:协议凭证(xray 里认的就是它)与订阅 token(用来
取订阅的 URL)。user revoke-sub 只换后者,不影响已经连上的客户端;user rm 则把
两样一起删掉。

"入站"列决定这个用户被授权到哪些入站,留空表示全部放行 —— 这是最容易踩的一条:
想把某人挡在新入站外面,得给他明确列出允许的入站,而不是留空。

"可用"是显式开关,"状态"是按用量与到期算出来的,两者是两回事:停用的用户状态
仍可能显示 active。user disable/enable 管前者,user reset-traffic/renew 管后者的
两个来源。user links 打印订阅地址,加了 mpx settings rules 之后,同一个地址按
User-Agent 自动给出不同格式。`,
}

// userHeaders 是用户表格的列名。
//
// "可用"和"状态"是两回事:状态是算出来的(过期、超限都会反映出来),可用是
// 显式开关。停用的用户状态仍可能是 active,只有这一列能把它们分开 ——
// CSV 导出一直有"可用性"列,表格少了它就跟导出的对不上。
var userHeaders = []string{"ID", "用户名", "可用", "状态", "已用", "上限", "到期", "重置策略", "入站", "套餐", "备注"}

// userRow 是 CLI 侧对服务端 userView 的映射。
//
// 除表格要展示的字段外还带上 enabled:导出 CSV 要写"可用性",而"可用性"是
// 显式开关,不是由过期/超限推出的 status;表里两者的差别(停用 vs 过期)不加
// 这一列就分不出来。
//
// PlanName 是本地按 plan_id 查出来的名字,不来自服务端(所以 json:"-");
// 只有导出/展示前填充,拿不到时退回 #id。
type userRow struct {
	ID            int64      `json:"id"`
	Username      string     `json:"username"`
	Enabled       bool       `json:"enabled"`
	Status        string     `json:"status"`
	DataLimit     int64      `json:"data_limit"`
	UsedTraffic   int64      `json:"used_traffic"`
	ExpireAt      *time.Time `json:"expire_at"`
	ResetStrategy string     `json:"reset_strategy"`
	InboundIDs    []int64    `json:"inbound_ids"`
	PlanID        *int64     `json:"plan_id,omitempty"`
	PlanName      string     `json:"-"`
	Note          string     `json:"note"`
}

func (u userRow) row() []string {
	return []string{
		strconv.FormatInt(u.ID, 10),
		u.Username,
		enabledText(u.Enabled),
		u.Status,
		humanBytes(u.UsedTraffic),
		limitText(u.DataLimit),
		expireText(u.ExpireAt),
		strategyText(u.ResetStrategy),
		joinIDs(u.InboundIDs),
		u.planText(),
		u.Note,
	}
}

// planText 是用户表格里"套餐"列的显示:优先本地解析出的套餐名,拿不到时退回
// #id;没挂套餐显示 "-"(它按旧语义用入站范围)。
func (u userRow) planText() string {
	if u.PlanName != "" {
		return u.PlanName
	}
	if u.PlanID != nil {
		return "#" + strconv.FormatInt(*u.PlanID, 10)
	}
	return "-"
}

// userWriteBody 是发给控制面的用户请求体。
//
// 单独把 plan_id 拎出来,是因为 model.User 的 *int64 + omitempty 分不出
// 「没带这个字段」和「带了 null」—— 而控制面需要区分:没带保持原套餐,null
// 会被拒绝(不能把用户移出套餐)。PlanID 非空是数字,plan_idJSON(nil) 是显式 null,零值(省略)则不带。
type userWriteBody struct {
	model.User
	PlanID json.RawMessage `json:"plan_id,omitempty"`
}

// planIDJSON 把显式的套餐归属编成 JSON 片段:非空是数字,空是 null。
func planIDJSON(id *int64) json.RawMessage {
	if id == nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(strconv.FormatInt(*id, 10))
}

// attachPlanNames 给一批用户补上套餐名,只在需要展示/导出的地方调用。
// 名字查不到时留空,row() 会退回 #id。
func attachPlanNames(c *Client, users []userRow) error {
	plans, err := listPlans(c)
	if err != nil {
		return err
	}
	byID := make(map[int64]string, len(plans))
	for _, p := range plans {
		byID[p.ID] = p.Name
	}
	for i := range users {
		if users[i].PlanID != nil {
			users[i].PlanName = byID[*users[i].PlanID]
		}
	}
	return nil
}

// limitText 把"不限"和某个具体上限区分开:0 在模型里就是不限,显示成 0B 会
// 让人以为是没流量了。
func limitText(n int64) string {
	if n == 0 {
		return "不限"
	}
	return humanBytes(n)
}

func joinIDs(ids []int64) string {
	if len(ids) == 0 {
		return "全部"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// expireText 按 YYYY-MM-DD 显示到期日;nil 表示不过期。
func expireText(t *time.Time) string {
	if t == nil {
		return "不过期"
	}
	return t.Local().Format("2006-01-02")
}

// userListFlags 是 user list 的筛选与分页选项。
//
// 用具名 flag 而不是让用户拼服务端查询串:分页应该是"第几页/每页多少"这种
// 想得明白的东西。刻意不用 --limit 表示每页条数 —— 在 user add 里 --limit
// 已经是流量上限(1024 进制字节),一个工具里同一个词不能有两个意思。
var userListFlags struct {
	page     int
	pageSize int
	status   string
	search   string
	inbound  int64
	plan     string
}

// userPage 对应服务端的 userPage:一页用户加匹配总数。
//
// total 是筛选之后的总数,不是库里的总数;客户端靠它才能说清"还有多少"。
type userPage struct {
	Users  []userRow `json:"users"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出用户(可分页与筛选)",
	Long: `列出用户。默认只给一页(50 个),不会把上千个用户一次刷满屏幕。

末尾会写明匹配到多少、还有多少,以及带上当前筛选条件的下一页命令。

  mpx user list --status expired --search li
  mpx user list --inbound 2 --page 2 --page-size 20`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// page/page-size 先自己挡一道:page=0 会算出负数 offset,让服务端回一句
		// "offset 不能是负数"更让人糊涂。
		if userListFlags.page < 1 {
			return errors.New("--page 从 1 开始")
		}
		if userListFlags.pageSize < 1 {
			return errors.New("--page-size 至少为 1")
		}
		if userListFlags.inbound < 0 {
			return errors.New("--inbound 是入站 ID,不能是负数")
		}
		q := url.Values{}
		q.Set("limit", strconv.Itoa(userListFlags.pageSize))
		q.Set("offset", strconv.Itoa((userListFlags.page-1)*userListFlags.pageSize))
		if userListFlags.status != "" {
			q.Set("status", userListFlags.status)
		}
		if userListFlags.search != "" {
			q.Set("search", userListFlags.search)
		}
		if userListFlags.inbound > 0 {
			q.Set("inbound_id", strconv.FormatInt(userListFlags.inbound, 10))
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		if strings.TrimSpace(userListFlags.plan) != "" {
			id, err := resolvePlanID(c, userListFlags.plan, false)
			if err != nil {
				return err
			}
			q.Set("plan", strconv.FormatInt(id, 10))
		}
		raw, err := c.do("GET", "/api/users?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		page, err := decode[userPage](raw)
		if err != nil {
			return err
		}
		if err := attachPlanNames(c, page.Users); err != nil {
			return err
		}
		rows := make([][]string, 0, len(page.Users))
		for _, u := range page.Users {
			rows = append(rows, u.row())
		}
		if err := writeTable(userHeaders, rows); err != nil {
			return err
		}
		return userListHint(page)
	},
}

// userListHint 在表格末尾补一句:匹配到多少、还有多少、下一页怎么敲。
//
// 少了这句,默认一页就会让人以为用户就这么多 —— 分页最容易骗人的地方就是
// 静默截断。
func userListHint(page userPage) error {
	shown := page.Offset + len(page.Users)
	if shown >= page.Total {
		_, err := fmt.Fprintf(os.Stdout, "共 %d 个用户\n", page.Total)
		return err
	}
	_, err := fmt.Fprintf(os.Stdout, "共 %d 个用户,本页 %d 个;还有 %d 个,下一页:\n  %s\n",
		page.Total, len(page.Users), page.Total-shown, userListNextCmd(userListFlags.page+1))
	return err
}

// userListNextCmd 拼出下一页的命令,带上这次用过的筛选条件。
//
// 必须重复筛选条件:否则用户照着敲就跳出了当前筛选,那个"下一页"根本不是同一
// 份列表。
func userListNextCmd(page int) string {
	parts := []string{
		"mpx", "user", "list",
		"--page", strconv.Itoa(page),
		"--page-size", strconv.Itoa(userListFlags.pageSize),
	}
	if userListFlags.status != "" {
		parts = append(parts, "--status", userListFlags.status)
	}
	if userListFlags.search != "" {
		parts = append(parts, "--search", userListFlags.search)
	}
	if userListFlags.inbound > 0 {
		parts = append(parts, "--inbound", strconv.FormatInt(userListFlags.inbound, 10))
	}
	if userListFlags.plan != "" {
		parts = append(parts, "--plan", userListFlags.plan)
	}
	return strings.Join(parts, " ")
}

var userShowCmd = &cobra.Command{
	Use:   "show <username>",
	Short: "显示一个用户",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", userPath(args[0]), nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		u, err := decode[userRow](raw)
		if err != nil {
			return err
		}
		users := []userRow{u}
		if err := attachPlanNames(c, users); err != nil {
			return err
		}
		u = users[0]
		headers := userHeaders
		values := u.row()
		pairs := make([][2]string, 0, len(headers))
		for i, h := range headers {
			pairs = append(pairs, [2]string{h, values[i]})
		}
		return writeKV(pairs)
	},
}

var userAddFlags struct {
	limit         string
	expire        string
	resetStrategy string
	inbounds      string
	plan          string
	note          string
	disabled      bool
}

var userAddCmd = &cobra.Command{
	Use:   "add <username>",
	Short: "新增一个用户",
	Example: `  # 建一个用户,不限流量、不过期
  mpx user add alice

  # 100GB、每月重置、2026 年底到期,只允许连入站 1 和 2
  mpx user add bob --limit 100GB --reset-strategy monthly \
      --expire 2026-12-31 --inbounds 1,2

  # 建完把订阅地址打出来
  mpx user links bob`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		u := model.User{
			Username:      args[0],
			Enabled:       !userAddFlags.disabled,
			ResetStrategy: model.ResetStrategy(userAddFlags.resetStrategy),
			Note:          userAddFlags.note,
		}
		if userAddFlags.limit != "" {
			limit, err := parseBytes(userAddFlags.limit)
			if err != nil {
				return err
			}
			u.DataLimit = limit
		}
		if userAddFlags.expire != "" {
			expire, err := parseExpire(userAddFlags.expire)
			if err != nil {
				return err
			}
			u.ExpireAt = &expire
		}
		ids, err := parseInboundIDs(userAddFlags.inbounds)
		if err != nil {
			return err
		}
		u.InboundIDs = ids

		c, err := newClient()
		if err != nil {
			return err
		}
		body := userWriteBody{User: u}
		if strings.TrimSpace(userAddFlags.plan) != "" {
			id, err := resolvePlanID(c, userAddFlags.plan, false)
			if err != nil {
				return err
			}
			body.PlanID = planIDJSON(&id)
		}
		raw, err := c.do("POST", "/api/users", body)
		if err != nil {
			return err
		}
		return report(raw, "已创建用户 %s", u.Username)
	},
}

// userEditFlags 是 user edit 的可选字段。
//
// 全部走 Changed() 判断,因为好几个字段的零值是有含义的:--limit 0 是"不限",
// --expire "" 是"永不过期",--inbounds "" 是"全部入站"。用零值判断就没法
// 把已经设过的值改回这些"空"状态了。
var userEditFlags struct {
	limit         string
	expire        string
	resetStrategy string
	inbounds      string
	plan          string
	note          string
}

var userEditCmd = &cobra.Command{
	Use:   "edit <username>",
	Short: "改一个用户",
	Long: `改一个用户。只改给出来的字段,没给的保持原样。

  mpx user edit alice --limit 200GB
  mpx user edit alice --note "同事,已续费"
  mpx user edit alice --reset-strategy monthly --inbounds 2,5

把字段改回"空"状态得显式给空值,不给则是保持原样:
  --limit 0        改成不限流量
  --expire ""      改成永不过期
  --inbounds ""    改成可用全部入站
  --note ""        改成没有备注

启用/停用请用 mpx user enable / disable,重置已用流量请用 user reset-traffic,
它们各有专门的命令。

改完之后用户下次拉订阅就能看到新上限;已经连上的客户端不受影响,要立即生效
得让用户重连。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		c, err := newClient()
		if err != nil {
			return err
		}
		// 读-改-写:PUT 是整体替换,不先把现值读回来就会把没提到的字段清掉。
		// 这里读到的 userView 不含 key/sub_token(服务端对空值会沿用旧值),
		// 往返不会毁掉订阅凭证。
		path := userPath(name)
		raw, err := c.do("GET", path, nil)
		if err != nil {
			return err
		}
		u, err := decode[model.User](raw)
		if err != nil {
			return err
		}

		changed := func(f string) bool { return cmd.Flags().Changed(f) }
		if changed("limit") {
			limit, err := parseBytes(userEditFlags.limit)
			if err != nil {
				return err
			}
			u.DataLimit = limit
		}
		if changed("expire") {
			if strings.TrimSpace(userEditFlags.expire) == "" {
				u.ExpireAt = nil
			} else {
				expire, err := parseExpire(userEditFlags.expire)
				if err != nil {
					return err
				}
				u.ExpireAt = &expire
			}
		}
		if changed("reset-strategy") {
			u.ResetStrategy = model.ResetStrategy(userEditFlags.resetStrategy)
		}
		if changed("inbounds") {
			ids, err := parseInboundIDs(userEditFlags.inbounds)
			if err != nil {
				return err
			}
			u.InboundIDs = ids
		}
		if changed("note") {
			u.Note = userEditFlags.note
		}
		// 没给 --plan 就完全不带这个字段(保持原套餐);给了名字/id 就改挂。
		// --plan "" 直接拒绝:不能把用户移出套餐,否则会回落到全部入站。
		body := userWriteBody{User: u}
		if changed("plan") {
			if strings.TrimSpace(userEditFlags.plan) == "" {
				return errors.New(`--plan 不能为空:不能把用户移出套餐,请指定一个具体套餐(可以是默认套餐)`)
			} else {
				id, err := resolvePlanID(c, userEditFlags.plan, false)
				if err != nil {
					return err
				}
				body.PlanID = planIDJSON(&id)
			}
		}

		raw, err = c.do("PUT", path, body)
		if err != nil {
			return err
		}
		return report(raw, "已更新用户 %s", name)
	},
}

var userRmCmd = &cobra.Command{
	Use:   "rm <username>...",
	Short: "删除一个或多个用户",
	Long: `删除一个或多个用户 —— 不可逆。

协议凭证与订阅 token 一起删掉:已经连上的客户端会断开,订阅地址立刻 404,没有
确认提示。命令行上给了几个名字就删几个,不会只删掉认得出来的那些 —— 打错一个字
就是一个不存在的用户名报错,其余照删。

只想让人连不上、但把记录留着,用 user disable;只想换订阅地址、不动协议凭证,
用 user revoke-sub。确认用户名用 mpx user list。`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUserBatch(args, func(c *Client, name string) (string, []byte, error) {
			raw, err := c.do("DELETE", userPath(name), nil)
			if err != nil {
				return "", nil, err
			}
			return fmt.Sprintf("已删除用户 %s", name), raw, nil
		})
	},
}

var userEnableCmd = &cobra.Command{
	Use:   "enable <username>...",
	Short: "启用一个或多个用户",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUserBatch(args, func(c *Client, name string) (string, []byte, error) {
			return setUserEnabled(c, name, true)
		})
	},
}

var userDisableCmd = &cobra.Command{
	Use:   "disable <username>...",
	Short: "停用一个或多个用户",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUserBatch(args, func(c *Client, name string) (string, []byte, error) {
			return setUserEnabled(c, name, false)
		})
	},
}

// setUserEnabled 用"读-改-写"实现启用/停用。
//
// PUT 是整体替换,所以先把用户读回来再回写,只改 Enabled 这一项。返回的原始
// 响应交给 runUserBatch 决定怎么打;单名字时就是原来那条 GET→PUT 的行为。
func setUserEnabled(c *Client, username string, enabled bool) (string, []byte, error) {
	path := userPath(username)
	raw, err := c.do("GET", path, nil)
	if err != nil {
		return "", nil, err
	}
	u, err := decode[model.User](raw)
	if err != nil {
		return "", nil, err
	}
	u.Enabled = enabled
	raw, err = c.do("PUT", path, userWriteBody{User: u})
	if err != nil {
		return "", nil, err
	}
	word := "已停用"
	if enabled {
		word = "已启用"
	}
	return fmt.Sprintf("%s用户 %s", word, username), raw, nil
}

// linkOrder 固定订阅链接的显示顺序:map 的遍历顺序是随机的,不固定下来每次
// 输出的行序都不一样。
var linkOrder = []string{"auto", string(model.FormatLinks), string(model.FormatBase64), string(model.FormatClash), string(model.FormatClashMeta), string(model.FormatSingBox), string(model.FormatXray)}

var userLinksCmd = &cobra.Command{
	Use:   "links <username>...",
	Short: "显示一个或多个用户的各格式订阅地址",
	Example: `  # 看 alice 的各格式订阅地址
  mpx user links alice

  # 要能直接解析的输出,就给 alice 的订阅地址加上 -o json
  mpx user links alice -o json

  # 批量看几个人的
  mpx user links alice bob carol`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUserBatch(args, userLinksText)
	},
}

// userLinksText 取回一个用户的全部格式订阅地址并排成表格文本。
func userLinksText(c *Client, username string) (string, []byte, error) {
	raw, err := c.do("GET", userPath(username)+"/links", nil)
	if err != nil {
		return "", nil, err
	}
	links, err := decode[map[string]string](raw)
	if err != nil {
		return "", nil, err
	}
	rows := make([][]string, 0, len(links))
	for _, key := range linkOrder {
		if v, ok := links[key]; ok {
			rows = append(rows, []string{key, v})
			delete(links, key)
		}
	}
	// 服务端以后加了新格式,也别让它悄悄消失。
	rest := make([]string, 0, len(links))
	for key := range links {
		rest = append(rest, key)
	}
	sort.Strings(rest)
	for _, key := range rest {
		rows = append(rows, []string{key, links[key]})
	}
	return strings.TrimSuffix(tableText([]string{"格式", "订阅地址"}, rows), "\n"), raw, nil
}

var userResetTrafficCmd = &cobra.Command{
	Use:   "reset-traffic <username>...",
	Short: "把一个或多个用户的已用流量清零",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUserBatch(args, func(c *Client, name string) (string, []byte, error) {
			raw, err := c.do("POST", userPath(name)+"/reset-traffic", nil)
			if err != nil {
				return "", nil, err
			}
			return fmt.Sprintf("已清零用户 %s 的已用流量", name), raw, nil
		})
	},
}

var userRevokeSubCmd = &cobra.Command{
	Use:   "revoke-sub <username>...",
	Short: "换掉一个或多个用户的订阅 token(不影响协议凭证)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUserBatch(args, revokeUserSub)
	},
}

// revokeUserSub 换掉一个用户的订阅 token,并尽量把新地址带出来。
//
// 换完之后旧订阅地址就失效了,顺手打新地址省一次链接查询;查不到也不影响
// "已经换掉"这个事实,所以不当成错误。
func revokeUserSub(c *Client, username string) (string, []byte, error) {
	path := userPath(username)
	raw, err := c.do("POST", path+"/revoke-sub", nil)
	if err != nil {
		return "", nil, err
	}
	if links, err := c.do("GET", path+"/links", nil); err == nil {
		if m, err := decode[map[string]string](links); err == nil {
			if sub := m["auto"]; sub != "" {
				return fmt.Sprintf("已换掉用户 %s 的订阅 token,新地址:\n%s", username, sub), raw, nil
			}
		}
	}
	return fmt.Sprintf("已换掉用户 %s 的订阅 token", username), raw, nil
}

func init() {
	userCmd.AddCommand(userListCmd, userShowCmd, userAddCmd, userEditCmd, userRmCmd,
		userEnableCmd, userDisableCmd, userRenewCmd, userLinksCmd,
		userResetTrafficCmd, userRevokeSubCmd, userExportCmd, userImportCmd)

	userListCmd.Flags().IntVar(&userListFlags.page, "page", 1, "第几页(从 1 开始)")
	userListCmd.Flags().IntVar(&userListFlags.pageSize, "page-size", 50, "每页多少条")
	userListCmd.Flags().StringVar(&userListFlags.status, "status", "", "只看某种状态:active / expired / limited / disabled")
	userListCmd.Flags().StringVar(&userListFlags.search, "search", "", "用户名包含这段文字(不分大小写)")
	userListCmd.Flags().Var(inboundFlag(&userListFlags.inbound), "inbound", "只看能用某个入站的用户(id、节点/tag 或唯一 tag)")
	userListCmd.Flags().StringVar(&userListFlags.plan, "plan", "", "只看挂某个套餐的用户(套餐名、id 或唯一前缀)")

	userRenewCmd.Flags().StringVar(&userRenewFlags.expire, "expire", "", "新的到期日 YYYY-MM-DD(当地时区当天 23:59:59)")
	userRenewCmd.Flags().IntVar(&userRenewFlags.days, "days", 0, "从当前到期日(已过期则从今天)起顺延的天数")

	userImportCmd.Flags().StringVarP(&userImportFlags.file, "file", "f", "", "要导入的 CSV 文件")

	userAddCmd.Flags().StringVar(&userAddFlags.limit, "limit", "", "流量上限,如 100GB / 1.5TB;不填或 0 表示不限")
	userAddCmd.Flags().StringVar(&userAddFlags.expire, "expire", "", "到期日 YYYY-MM-DD(当地时区当天 23:59:59)")
	userAddCmd.Flags().StringVar(&userAddFlags.resetStrategy, "reset-strategy", string(model.ResetNone), "流量重置策略:none / daily / weekly / monthly")
	userAddCmd.Flags().StringVar(&userAddFlags.inbounds, "inbounds", "", "可用的入站 id,逗号分隔;空表示全部")
	userAddCmd.Flags().StringVar(&userAddFlags.plan, "plan", "", "挂到某个套餐(套餐名或 id);不填则空入站归默认套餐")
	userAddCmd.Flags().StringVar(&userAddFlags.note, "note", "", "备注")
	userAddCmd.Flags().BoolVar(&userAddFlags.disabled, "disabled", false, "建好后先停用")

	userEditCmd.Flags().StringVar(&userEditFlags.limit, "limit", "", "新的流量上限,如 100GB;给 0 表示不限")
	userEditCmd.Flags().StringVar(&userEditFlags.expire, "expire", "", "新的到期日 YYYY-MM-DD;给空串表示永不过期")
	userEditCmd.Flags().StringVar(&userEditFlags.resetStrategy, "reset-strategy", "", "新的重置策略:none / daily / weekly / monthly")
	userEditCmd.Flags().StringVar(&userEditFlags.inbounds, "inbounds", "", "新的可用入站 id,逗号分隔;给空串表示全部")
	userEditCmd.Flags().StringVar(&userEditFlags.plan, "plan", "", "改挂到某个套餐(套餐名或 id,可以是默认套餐);不能为空")
	userEditCmd.Flags().StringVar(&userEditFlags.note, "note", "", "新的备注;给空串表示清掉")
}

// userPath 拼出用户资源的路径。用户名虽然限定了字符集,仍然转义一次,
// 免得以后放宽规则时这里成了漏洞。
func userPath(username string) string {
	return "/api/users/" + url.PathEscape(username)
}

// parseBytes 把人类可读的大小解析成字节数。
//
// 支持 B / KB / MB / GB / TB(1024 进制,大小写不敏感,可带小数);不带单位
// 的纯数字按字节算。整数走 ParseInt,免得大数经 float64 丢精度。
func parseBytes(s string) (int64, error) {
	text := strings.TrimSpace(s)
	if text == "" {
		return 0, errors.New("大小不能为空")
	}
	units := []struct {
		suffix string
		mult   int64
	}{
		{"TB", 1 << 40},
		{"GB", 1 << 30},
		{"MB", 1 << 20},
		{"KB", 1 << 10},
		{"B", 1},
	}
	upper := strings.ToUpper(text)
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(upper, u.suffix) {
			mult = u.mult
			text = strings.TrimSpace(text[:len(text)-len(u.suffix)])
			break
		}
	}
	if text == "" {
		return 0, fmt.Errorf("大小缺少数字: %q", s)
	}
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("大小不能是负数: %q", s)
		}
		if n > math.MaxInt64/mult {
			return 0, fmt.Errorf("大小超出范围: %q", s)
		}
		return n * mult, nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("无法解析大小 %q,单位用 B/KB/MB/GB/TB", s)
	}
	if f < 0 {
		return 0, fmt.Errorf("大小不能是负数: %q", s)
	}
	v := f * float64(mult)
	// float64(math.MaxInt64) 恰好等于 2^63,达到它就已经越界了。
	if v >= float64(math.MaxInt64) {
		return 0, fmt.Errorf("大小超出范围: %q", s)
	}
	return int64(v), nil
}

// parseExpire 把 YYYY-MM-DD 解析成当地时区当天 23:59:59。
//
// 到期日是"这一天结束时失效",用零点会让用户在到期日当天就被断掉。
func parseExpire(s string) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("到期日要写成 YYYY-MM-DD: %q", s)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, time.Local), nil
}

// parseInboundIDs 解析逗号分隔的入站 id;空字符串得到 nil,表示不限制。
func parseInboundIDs(s string) ([]int64, error) {
	var ids []int64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("入站 id 不是整数: %q", part)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// humanBytes 用 1024 进制把字节数写成人类可读的大小。
func humanBytes(n int64) string {
	if n <= 0 {
		return "0B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + "B"
	}
	// 最多两位小数,再去掉末尾的 0:1.50GB 读起来不如 1.5GB 干脆。
	text := strconv.FormatFloat(v, 'f', 2, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimSuffix(text, ".")
	return text + units[i]
}
