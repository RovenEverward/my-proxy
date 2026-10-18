package cli

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// CSV 的列名。用中文:这份文件的读者是人(导出来改一行再导回去),列名与
// user list 的表头共用同一套词,不另造一套英文名。
const (
	csvColumnUsername   = "用户名"
	csvColumnLimit      = "流量上限"
	csvColumnExpire     = "到期时间"
	csvColumnEnabled    = "可用性"
	csvColumnStrategy   = "重置策略"
	csvColumnInbounds   = "入站范围"
	csvColumnPlan       = "套餐"
	csvColumnNote       = "备注"
	csvColumnLastActive = "最后在线（UTC+8）"
)

// userCSVHeader 是导出的表头,也是导入时认识的全部列(顺序固定)。
// 「最后在线（UTC+8）」是导出专有的只读列:导入时不认它,回到控制面的数据才算数。
var userCSVHeader = []string{
	csvColumnUsername, csvColumnLimit, csvColumnExpire,
	csvColumnEnabled, csvColumnStrategy, csvColumnInbounds, csvColumnPlan, csvColumnNote,
	csvColumnLastActive,
}

var userExportCmd = &cobra.Command{
	Use:   "export",
	Short: "把全部用户导出成 CSV(标准输出)",
	Long: `把全部用户导出成 CSV 到标准输出,表头是列名。

  mpx user export > users.csv
  mpx user export > users.csv && mpx user import -f users.csv

导出的内容可以直接用 user import 导回来:两边对空值与"不限/不过期/全部"的
写法是同一套。最后一列"最后在线（UTC+8）"只读:按 UTC+8 写这个用户最后一次
有流量的时间,从没连过的人留空;导入时这一列被忽略。导出不看 -o,它本身就是一种输出格式。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		users, err := fetchAllUsers(c)
		if err != nil {
			return err
		}
		if err := attachPlanNames(c, users); err != nil {
			return err
		}
		return writeUsersCSV(os.Stdout, users)
	},
}

// fetchAllUsers 翻页取回全部用户。
//
// 列表默认只给一页,导出要的是全部,所以按服务端每页上限翻页;靠 total 决定
// 何时停,而不是"这页没满就到底了"—— 用户数恰好是页大小的整数倍时,那页是满的
// 但确实已经到底,靠"不满"判断会多打一次空请求;反过来更糟的情况是被截断。
func fetchAllUsers(c *Client) ([]userRow, error) {
	// 与服务端 maxUserPageSize 一致:再大就被 400 挡下。
	const pageSize = 1000
	var all []userRow
	for offset := 0; ; offset += pageSize {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", strconv.Itoa(offset))
		raw, err := c.do("GET", "/api/users?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		page, err := decode[userPage](raw)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Users...)
		// 空页兜底:万一 total 与数据不一致,不至于原地打转。
		if len(page.Users) == 0 || len(all) >= page.Total {
			return all, nil
		}
	}
}

// writeUsersCSV 把用户写成 CSV。
func writeUsersCSV(w io.Writer, users []userRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(userCSVHeader); err != nil {
		return err
	}
	for _, u := range users {
		row := []string{
			u.Username,
			limitText(u.DataLimit),
			expireText(u.ExpireAt),
			enabledText(u.Enabled),
			strategyText(u.ResetStrategy),
			joinIDs(u.InboundIDs),
			u.PlanName,
			u.Note,
			lastActiveText(u.Presence),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// lastActiveText 写「最后在线（UTC+8）」列:固定按 UTC+8 写,和操作机时区无关 ——
// 同一份导出在哪台机器上读到的时刻都一样。从没连过的人留空。
func lastActiveText(p *userPresence) string {
	if p == nil || p.LastActiveAt == nil {
		return ""
	}
	return p.LastActiveAt.UTC().Add(8 * time.Hour).Format("2006-01-02 15:04:05")
}

// enabledText 写 CSV 的"可用性"列。
func enabledText(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "停用"
}

// strategyText 把空的重置策略写成 none:空是 Go 的零值,不该让读文件的人去猜
// 它是什么意思。
func strategyText(s string) string {
	if s == "" {
		return string(model.ResetNone)
	}
	return s
}

var userImportFlags struct {
	file string
}

var userImportCmd = &cobra.Command{
	Use:   "import",
	Short: "从 CSV 导入用户(按用户名新增或整体覆盖)",
	Long: `从 CSV 导入用户,逐行处理:

  文件里有、库里没有  -> 新增
  两边都有            -> 用文件里的定义字段整体覆盖(已用流量与凭证不动)

每行的成败分开报(带上第几行),一行坏掉不影响其余行。按用户名覆盖,所以同一
份文件重复导几遍结果一样,坏了哪行改哪行再导一次即可。

行号按 CSV 的记录序号算,表头是第 1 行。

  mpx user import -f users.csv`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if userImportFlags.file == "" {
			return errors.New("要指定 CSV 文件:-f users.csv")
		}
		f, err := os.Open(userImportFlags.file)
		if err != nil {
			return fmt.Errorf("打开 CSV: %w", err)
		}
		defer f.Close()

		c, err := newClient()
		if err != nil {
			return err
		}
		rep, err := importUsersCSV(c, f)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			if err := emitJSONValue(rep); err != nil {
				return err
			}
		} else {
			for _, e := range rep.Errors {
				fmt.Fprintf(os.Stdout, "第 %d 行:%s\n", e.Line, e.Error)
			}
			fmt.Fprintf(os.Stdout, "导入完成:%d 个新增,%d 个更新,%d 个失败\n",
				rep.Added, rep.Updated, rep.Failed)
		}
		if rep.Failed > 0 {
			return errBatchFailed
		}
		return nil
	},
}

// importError 是一行导入失败的原因;Line 是 CSV 记录序号(表头算第 1 行)。
type importError struct {
	Line  int    `json:"line"`
	Error string `json:"error"`
}

// importReport 是导入结果汇总。
type importReport struct {
	Added   int           `json:"added"`
	Updated int           `json:"updated"`
	Failed  int           `json:"failed"`
	Errors  []importError `json:"errors,omitempty"`
}

// importUsersCSV 逐行导入,表头级别的毛病当整份文件的错误返回。
//
// 逐行独立提交,而不是"整份校验完再写",理由:
//   - 导入是按用户名 upsert,天然幂等可重试,坏了哪行改哪行再导一次就行;
//   - 一行坏掉不该挡住其余几百行 —— 那正是批量操作要避免的事;
//   - 不必把整份文件先读进内存再写。
//
// 代价是失去"要么全成要么全不成"的原子性。这份数据的用途(批量建/改用户)不值得
// 为它放弃逐行的好处,见 decisions.md 第 25 条。
func importUsersCSV(c *Client, r io.Reader) (importReport, error) {
	var rep importReport
	cr := csv.NewReader(r)
	// 列数不一时自己报错,好用行号说清是这一行的问题;交给 csv 包会先断了整份
	// 文件。默认的 -1 之外还要自己比对长度,见下。
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return rep, fmt.Errorf("读 CSV 表头:%w", err)
	}
	cols, err := userCSVColumns(header)
	if err != nil {
		return rep, err
	}

	// 有「套餐」列时把套餐名→id 映出来一次,逐行复用。空单元格表示不改编归属。
	var planIDs map[string]int64
	if _, ok := cols[csvColumnPlan]; ok {
		plans, err := listPlans(c)
		if err != nil {
			return rep, err
		}
		planIDs = make(map[string]int64, len(plans))
		for _, p := range plans {
			planIDs[p.Name] = p.ID
		}
	}

	readErrors := 0
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return rep, nil
		}
		if err != nil {
			// csv.Reader 遇到坏引号会连着报错;连续几行都读不出来,说明这份文件
			// 从这儿起结构已经烂了,再读下去只是刷屏。
			readErrors++
			if readErrors >= 3 {
				rep.Failed++
				rep.Errors = append(rep.Errors, importError{Line: line, Error: "连续多行读不出来,就此停下;检查这附近的引号与逗号"})
				return rep, nil
			}
			rep.Failed++
			rep.Errors = append(rep.Errors, importError{Line: line, Error: fmt.Sprintf("读这行失败:%v", err)})
			continue
		}
		readErrors = 0
		if len(rec) != len(header) {
			rep.Failed++
			rep.Errors = append(rep.Errors, importError{Line: line,
				Error: fmt.Sprintf("列数与表头不一致(表头 %d 列,这行 %d 列)", len(header), len(rec))})
			continue
		}
		u, err := csvRowToUser(cols, rec)
		if err != nil {
			rep.Failed++
			rep.Errors = append(rep.Errors, importError{Line: line, Error: err.Error()})
			continue
		}
		if u.PlanName != "" {
			id, ok := planIDs[u.PlanName]
			if !ok {
				rep.Failed++
				rep.Errors = append(rep.Errors, importError{Line: line, Error: fmt.Sprintf("套餐 %q 不存在", u.PlanName)})
				continue
			}
			u.PlanID = &id
		}
		created, err := upsertUser(c, u)
		if err != nil {
			rep.Failed++
			rep.Errors = append(rep.Errors, importError{Line: line, Error: err.Error()})
			continue
		}
		if created {
			rep.Added++
		} else {
			rep.Updated++
		}
	}
}

// userCSVColumns 把表头映射成"列名 -> 下标"。
//
// 不认识的列直接忽略:以后加列时,旧文件还能读。缺"用户名"这份文件就没法用了
// (那一行要写谁都不知道),直接挡下。
func userCSVColumns(header []string) (map[string]int, error) {
	cols := make(map[string]int, len(header))
	for i, name := range header {
		cols[strings.TrimSpace(name)] = i
	}
	if _, ok := cols[csvColumnUsername]; !ok {
		return nil, fmt.Errorf("CSV 缺「%s」列;表头要包含:%s",
			csvColumnUsername, strings.Join(userCSVHeader, ","))
	}
	return cols, nil
}

// csvRowToUser 把一行 CSV 解析成要写入的用户定义。
//
// 空单元格一律当"不限/不过期/全部/默认策略":导出的文件里空值本来就这么写,
// 手写一行时留空也应该得到同样的结果。
func csvRowToUser(cols map[string]int, rec []string) (userRow, error) {
	u := userRow{Enabled: true}
	cell := func(name string) string {
		i, ok := cols[name]
		if !ok {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	u.Username = cell(csvColumnUsername)
	if u.Username == "" {
		return u, errors.New("用户名为空")
	}
	if v := cell(csvColumnLimit); v != "" && v != "不限" {
		n, err := parseBytes(v)
		if err != nil {
			return u, fmt.Errorf("流量上限:%w", err)
		}
		u.DataLimit = n
	}
	if v := cell(csvColumnExpire); v != "" && v != "不过期" {
		t, err := parseExpire(v)
		if err != nil {
			return u, fmt.Errorf("到期时间:%w", err)
		}
		u.ExpireAt = &t
	}
	if v := cell(csvColumnEnabled); v != "" {
		enabled, err := parseEnabled(v)
		if err != nil {
			return u, err
		}
		u.Enabled = enabled
	}
	u.ResetStrategy = strategyText(cell(csvColumnStrategy))
	if v := cell(csvColumnInbounds); v != "" && v != "全部" {
		ids, err := parseInboundIDs(v)
		if err != nil {
			return u, fmt.Errorf("入站范围:%w", err)
		}
		u.InboundIDs = ids
	}
	// 空的「套餐」单元格表示不改归属;非空在这里先记下名字,由导入方解析成 id。
	u.PlanName = cell(csvColumnPlan)
	u.Note = cell(csvColumnNote)
	return u, nil
}

// parseEnabled 认几种常见写法;认不出就报错,而不是默默当启用 —— 一个拼错的
// "停用"被当成启用,账号就悄悄开了。
func parseEnabled(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "启用", "是", "true", "enabled", "yes", "y", "1":
		return true, nil
	case "停用", "否", "false", "disabled", "no", "n", "0":
		return false, nil
	}
	return false, fmt.Errorf("可用性只能是 启用 / 停用(或 true / false),收到 %q", s)
}

// upsertUser 把一行 CSV 写进去:有就覆盖,没有就新增。返回是否新建。
//
// 覆盖走"先读回来再改定义字段":used_traffic / lifetime_used_traffic / 协议
// 凭证是运行状态,CSV 里没有它们,直接 PUT 一个新建的结构体会把它们清零、把
// 用户正在用的凭证换掉。
func upsertUser(c *Client, u userRow) (bool, error) {
	path := userPath(u.Username)
	raw, err := c.do("GET", path, nil)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			if _, err := c.do("POST", "/api/users", u); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, err
	}
	old, err := decode[model.User](raw)
	if err != nil {
		return false, err
	}
	old.DataLimit = u.DataLimit
	old.ExpireAt = u.ExpireAt
	old.Enabled = u.Enabled
	old.ResetStrategy = model.ResetStrategy(u.ResetStrategy)
	old.InboundIDs = u.InboundIDs
	old.Note = u.Note
	body := userWriteBody{User: old}
	// 只有文件里给了套餐名(已解析成 id)才改归属;空的「套餐」单元格保持原样。
	if u.PlanID != nil {
		body.PlanID = planIDJSON(u.PlanID)
	}
	if _, err := c.do("PUT", path, body); err != nil {
		return false, err
	}
	return false, nil
}
