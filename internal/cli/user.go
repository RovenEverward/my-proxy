package cli

import (
	"errors"
	"fmt"
	"math"
	"net/url"
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
}

var userHeaders = []string{"ID", "用户名", "状态", "已用", "上限", "到期", "重置策略", "入站", "备注"}

// userRow 只声明表格要展示的字段。
//
// 它对应服务端的 userView(不含 Key),字段名与服务端 JSON 标签一致;json
// 模式不走这里,而是原样重排服务端响应。
type userRow struct {
	ID            int64      `json:"id"`
	Username      string     `json:"username"`
	Status        string     `json:"status"`
	DataLimit     int64      `json:"data_limit"`
	UsedTraffic   int64      `json:"used_traffic"`
	ExpireAt      *time.Time `json:"expire_at"`
	ResetStrategy string     `json:"reset_strategy"`
	InboundIDs    []int64    `json:"inbound_ids"`
	Note          string     `json:"note"`
}

func (u userRow) row() []string {
	return []string{
		strconv.FormatInt(u.ID, 10),
		u.Username,
		u.Status,
		humanBytes(u.UsedTraffic),
		limitText(u.DataLimit),
		expireText(u.ExpireAt),
		u.ResetStrategy,
		joinIDs(u.InboundIDs),
		u.Note,
	}
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

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有用户",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/users", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		users, err := decode[[]userRow](raw)
		if err != nil {
			return err
		}
		rows := make([][]string, 0, len(users))
		for _, u := range users {
			rows = append(rows, u.row())
		}
		return writeTable(userHeaders, rows)
	},
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
	note          string
	disabled      bool
}

var userAddCmd = &cobra.Command{
	Use:   "add <username>",
	Short: "新增一个用户",
	Args:  cobra.ExactArgs(1),
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
		raw, err := c.do("POST", "/api/users", u)
		if err != nil {
			return err
		}
		return report(raw, "已创建用户 %s", u.Username)
	},
}

var userRmCmd = &cobra.Command{
	Use:   "rm <username>",
	Short: "删除一个用户",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("DELETE", userPath(args[0]), nil)
		if err != nil {
			return err
		}
		return report(raw, "已删除用户 %s", args[0])
	},
}

var userEnableCmd = &cobra.Command{
	Use:   "enable <username>",
	Short: "启用一个用户",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setUserEnabled(args[0], true)
	},
}

var userDisableCmd = &cobra.Command{
	Use:   "disable <username>",
	Short: "停用一个用户",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setUserEnabled(args[0], false)
	},
}

// setUserEnabled 用"读-改-写"实现启用/停用。
//
// PUT 是整体替换,所以先把用户读回来再回写;只带 Enabled 的改动。服务端的
// userView 不含 Key,回写时留空,服务端会沿用原值。
func setUserEnabled(username string, enabled bool) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	path := userPath(username)
	raw, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	u, err := decode[model.User](raw)
	if err != nil {
		return err
	}
	u.Enabled = enabled
	raw, err = c.do("PUT", path, u)
	if err != nil {
		return err
	}
	word := "已停用"
	if enabled {
		word = "已启用"
	}
	return report(raw, "%s用户 %s", word, username)
}

// linkOrder 固定订阅链接的显示顺序:map 的遍历顺序是随机的,不固定下来每次
// 输出的行序都不一样。
var linkOrder = []string{"auto", string(model.FormatLinks), string(model.FormatBase64), string(model.FormatClash), string(model.FormatClashMeta), string(model.FormatSingBox), string(model.FormatXray)}

var userLinksCmd = &cobra.Command{
	Use:   "links <username>",
	Short: "显示一个用户的各格式订阅地址",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", userPath(args[0])+"/links", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		links, err := decode[map[string]string](raw)
		if err != nil {
			return err
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
		return writeTable([]string{"格式", "订阅地址"}, rows)
	},
}

var userResetTrafficCmd = &cobra.Command{
	Use:   "reset-traffic <username>",
	Short: "把用户已用流量清零",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", userPath(args[0])+"/reset-traffic", nil)
		if err != nil {
			return err
		}
		return report(raw, "已清零用户 %s 的已用流量", args[0])
	},
}

var userRevokeSubCmd = &cobra.Command{
	Use:   "revoke-sub <username>",
	Short: "换掉用户的订阅 token(不影响协议凭证)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		path := userPath(args[0])
		raw, err := c.do("POST", path+"/revoke-sub", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		// 换完之后旧订阅地址就失效了,顺手把新地址打出来,省一次链接查询;
		// 查不到也不影响"已经换掉"这个事实,所以不当成错误。
		if links, err := c.do("GET", path+"/links", nil); err == nil {
			if m, err := decode[map[string]string](links); err == nil {
				if sub := m["auto"]; sub != "" {
					return report(raw, "已换掉用户 %s 的订阅 token,新地址:\n%s", args[0], sub)
				}
			}
		}
		return report(raw, "已换掉用户 %s 的订阅 token", args[0])
	},
}

func init() {
	userCmd.AddCommand(userListCmd, userShowCmd, userAddCmd, userRmCmd,
		userEnableCmd, userDisableCmd, userLinksCmd, userResetTrafficCmd, userRevokeSubCmd)

	userAddCmd.Flags().StringVar(&userAddFlags.limit, "limit", "", "流量上限,如 100GB / 1.5TB;不填或 0 表示不限")
	userAddCmd.Flags().StringVar(&userAddFlags.expire, "expire", "", "到期日 YYYY-MM-DD(当地时区当天 23:59:59)")
	userAddCmd.Flags().StringVar(&userAddFlags.resetStrategy, "reset-strategy", string(model.ResetNone), "流量重置策略:none / daily / weekly / monthly")
	userAddCmd.Flags().StringVar(&userAddFlags.inbounds, "inbounds", "", "可用的入站 id,逗号分隔;空表示全部")
	userAddCmd.Flags().StringVar(&userAddFlags.note, "note", "", "备注")
	userAddCmd.Flags().BoolVar(&userAddFlags.disabled, "disabled", false, "建好后先停用")
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
