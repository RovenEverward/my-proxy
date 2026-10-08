package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// errBatchFailed 表示批量命令里有失败项。
//
// 具体哪个用户名失败、为什么,已经逐行打过了;这个哨兵只负责把退出码变成非 0,
// 让脚本能判断"这一批没全成"。Execute 会把它打成一行,所以文案要能独立看懂。
var errBatchFailed = errors.New("有用户操作失败")

// userBatchOp 处理一个用户名:成功返回给人看的一句话(可为空)、服务端原始响应
// (json 模式用)和 nil;失败返回 error。
type userBatchOp func(c *Client, username string) (msg string, raw []byte, err error)

// userBatchResult 是 -o json 下批量结果的数组元素。
//
// Data 保留服务端原始响应(比如 links):JSON 是给脚本用的,把批量命令拿到的
// 数据吞掉,脚本就只能再逐个查一遍。
type userBatchResult struct {
	Username string          `json:"username"`
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// runUserBatch 对一批用户名逐个执行 op,每个的成败分开报,一个失败不中止其余。
//
// 只给一个名字时保持各子命令原来的输出(成功一句人话;json 模式原样重排响应):
// 单名字是日常用法,输出形状变了会打断已有的脚本和肌肉记忆;批量是新增用法,可以
// 有自己的形状。
//
// 名字先去重:重复同一个名字不只是白干一遍,"续期 30 天"这种命令会算两次。
func runUserBatch(names []string, op userBatchOp) error {
	names = dedupeNames(names)
	c, err := newClient()
	if err != nil {
		return err
	}
	if len(names) == 1 {
		msg, raw, err := op(c, names[0])
		if err != nil {
			return err
		}
		return printBatchMsg(msg, raw)
	}
	results := make([]userBatchResult, 0, len(names))
	okCount, failCount := 0, 0
	for _, name := range names {
		msg, raw, err := op(c, name)
		if err != nil {
			failCount++
			results = append(results, userBatchResult{Username: name, Error: err.Error()})
			if opts.output != outputJSON {
				fmt.Fprintf(os.Stdout, "%s:失败:%v\n", name, err)
			}
			continue
		}
		okCount++
		results = append(results, userBatchResult{Username: name, OK: true, Data: rawJSON(raw)})
		if opts.output != outputJSON {
			fmt.Fprintln(os.Stdout, batchLine(name, msg))
		}
	}
	if opts.output == outputJSON {
		if err := emitJSONValue(results); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "完成:%d 个成功,%d 个失败\n", okCount, failCount)
	}
	if failCount > 0 {
		return errBatchFailed
	}
	return nil
}

// printBatchMsg 是单名字时的输出:json 模式给服务端原始响应,表格模式给人话。
func printBatchMsg(msg string, raw []byte) error {
	if opts.output == outputJSON {
		return emitJSON(raw)
	}
	if msg == "" {
		return nil
	}
	_, err := fmt.Fprintln(os.Stdout, msg)
	return err
}

// batchLine 把一行结果拼成"用户名: 结果";结果本身是多行的(links 的表格、
// revoke-sub 带出的新地址)就改成抬头一行,免得缩进对不齐。
func batchLine(name, msg string) string {
	if msg == "" {
		return name + ": 完成"
	}
	if strings.Contains(msg, "\n") {
		return "== " + name + " ==\n" + msg
	}
	return name + ": " + msg
}

// rawJSON 把服务端响应塞进结果数组;不是合法 JSON 就留空,免得一个奇怪的响应
// 让整批结果都打不出来。
func rawJSON(raw []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return nil
	}
	return json.RawMessage(trimmed)
}

// dedupeNames 去掉重复的用户名,保持第一次出现的顺序。
func dedupeNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

var userRenewFlags struct {
	expire string
	days   int
}

var userRenewCmd = &cobra.Command{
	Use:   "renew <username>...",
	Short: "重设一个或多个用户的到期日",
	Long: `重设到期日,两种给法二选一:

  --expire 2026-12-31   定死一个日期(当地时区当天 23:59:59)
  --days 30             顺延 30 天:未到期的从原到期日往后数,已过期或本来
                        不过期的从今天起算

顺延按天算,时分秒固定为当天 23:59:59,与 --expire 的解释保持一致。`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// 两个都给或都不给都要让人猜哪个生效,与其猜不如挡下。
		if userRenewFlags.expire == "" && userRenewFlags.days == 0 {
			return errors.New("要给 --expire 或 --days 之一")
		}
		if userRenewFlags.expire != "" && userRenewFlags.days != 0 {
			return errors.New("--expire 和 --days 只能给一个")
		}
		var fixed *time.Time
		if userRenewFlags.expire != "" {
			t, err := parseExpire(userRenewFlags.expire)
			if err != nil {
				return err
			}
			fixed = &t
		} else if userRenewFlags.days < 0 {
			return errors.New("--days 不能是负数;要提前到期请用 --expire")
		}
		return runUserBatch(args, func(c *Client, name string) (string, []byte, error) {
			return renewUser(c, name, userRenewFlags.days, fixed)
		})
	},
}

// renewUser 读回用户、算出新到期日、再整体回写。
//
// 读回来再写,是因为 --days 的基准是每个用户自己的到期日,一批里各人的基准可能
// 都不一样。
func renewUser(c *Client, username string, days int, fixed *time.Time) (string, []byte, error) {
	path := userPath(username)
	raw, err := c.do("GET", path, nil)
	if err != nil {
		return "", nil, err
	}
	u, err := decode[model.User](raw)
	if err != nil {
		return "", nil, err
	}
	target := time.Time{}
	if fixed != nil {
		target = *fixed
	} else {
		target = renewTarget(u.ExpireAt, days, time.Now())
	}
	u.ExpireAt = &target
	raw, err = c.do("PUT", path, u)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("已把用户 %s 的到期日设为 %s", username, target.Local().Format("2006-01-02")), raw, nil
}

// renewTarget 从"当前到期日与现在中较晚的那个"往后数整天。
//
// 这么定是为了让两种常见情形都不出意外:还没到期时顺延是在原到期日上加,
// 不会因为"今天续的"就把剩余时间抹掉;已经过期或本来不过期时从今天起算,
// 不会把过期那天当基准算出一个过去的日期。
func renewTarget(current *time.Time, days int, now time.Time) time.Time {
	base := now
	if current != nil && current.After(now) {
		base = *current
	}
	day := base.Local().AddDate(0, 0, days)
	return time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, time.Local)
}
