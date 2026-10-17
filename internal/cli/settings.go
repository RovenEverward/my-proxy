package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "查看与修改订阅设置",
	Long: `查看与修改订阅设置 —— 用户拿到的订阅长什么样。

三类东西在这里配:订阅地址前缀与标题、各格式的客户端模板(template)、以及按
User-Agent 决定返回哪种格式的规则(rules)。

改设置一律是"先读、改、整体写回"(settings set 收的是一份完整 JSON)。谁想只动
一项而不用手写整份 JSON,就用专门的子命令:settings rules add/rm/move 只碰规则,
settings template set 只碰一个格式 —— 它们自己会先把当前设置读回来,不会把别的
字段冲掉。

rules 出场即带内置默认,认得出主流客户端;没命中任何规则的客户端拿到的是一串
分享链接(links),这不是错误。要查看当前生效的是内置还是自定义,跑
mpx settings rules list,它第一行就写着来源。`,
}

func init() {
	settingsCmd.AddCommand(settingsShowCmd, settingsSetCmd, settingsTestCmd, settingsTemplateCmd, settingsRulesCmd)
	settingsSetCmd.Flags().StringVarP(&settingsSetFile, "file", "f", "", "设置 JSON 文件;- 表示从 stdin 读")
	settingsTemplateCmd.AddCommand(settingsTemplateGetCmd, settingsTemplateSetCmd, settingsTemplateDelCmd)
	settingsTemplateSetCmd.Flags().StringVarP(&settingsTemplateFile, "file", "f", "", "模板文件;- 表示从 stdin 读")
	settingsRulesCmd.AddCommand(settingsRulesListCmd, settingsRulesAddCmd, settingsRulesRmCmd,
		settingsRulesMoveCmd, settingsRulesResetCmd, settingsRulesTestCmd)
	settingsRulesAddCmd.Flags().StringVarP(&settingsRulesResult, "result", "r", "",
		"命中后返回哪种格式("+rulesResultFlag+")")
	settingsRulesAddCmd.Flags().StringVarP(&settingsRulesIndex, "index", "i", "",
		"插到第几位(从 1 开始);不给则加在最后")
}

var settingsSetFile string

var settingsTemplateCmd = &cobra.Command{
	Use:   "template",
	Short: "查看与修改某格式的客户端模板",
}

var settingsTemplateFile string

var settingsTemplateGetCmd = &cobra.Command{
	Use:   "get FORMAT",
	Short: "显示某格式当前生效的模板",
	Long: `显示某格式当前生效的模板正文。

FORMAT 是 clash-meta / sing-box / xray 之一。没存自定义模板时显示内置默认
模板的副本,方便拿它当改动的起点。正文原样输出,可以重定向到文件。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !validTemplateFormat(args[0]) {
			return fmt.Errorf("不支持的模板格式 %q:只能是 clash-meta / sing-box / xray", args[0])
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/settings/templates/"+args[0], nil)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(raw)
		return err
	},
}

var settingsTemplateSetCmd = &cobra.Command{
	Use:   "set FORMAT -f FILE",
	Short: "保存某格式的自定义模板",
	Long: `保存某格式的自定义模板,此后该格式的订阅按这份模板渲染。

FORMAT 是 clash-meta / sing-box / xray 之一。正文从 -f 指定的文件读,或用
-f - 从 stdin 读。服务端会先校验语法,不合法就不存。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !validTemplateFormat(args[0]) {
			return fmt.Errorf("不支持的模板格式 %q:只能是 clash-meta / sing-box / xray", args[0])
		}
		if settingsTemplateFile == "" {
			return errors.New("必须给 -f FILE(或用 -f - 从 stdin 读)")
		}
		data, err := readSettingsFile(settingsTemplateFile)
		if err != nil {
			return err
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("PUT", "/api/settings/templates/"+args[0], rawBody(data))
		if err != nil {
			return err
		}
		return report(raw, "已保存 %s 模板", args[0])
	},
}

var settingsTemplateDelCmd = &cobra.Command{
	Use:   "del FORMAT",
	Short: "删除某格式的自定义模板,回落到内置默认",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !validTemplateFormat(args[0]) {
			return fmt.Errorf("不支持的模板格式 %q:只能是 clash-meta / sing-box / xray", args[0])
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("DELETE", "/api/settings/templates/"+args[0], nil)
		if err != nil {
			return err
		}
		return report(raw, "已删除 %s 模板", args[0])
	},
}

func validTemplateFormat(f string) bool {
	switch f {
	case "clash-meta", "sing-box", "xray":
		return true
	}
	return false
}

var settingsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "显示当前的订阅设置",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/settings", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		s, err := decode[model.Settings](raw)
		if err != nil {
			return err
		}
		if err := writeKV([][2]string{
			{"订阅地址前缀", s.SubURLPrefix},
			{"订阅名", s.ProfileTitle},
			{"支持链接", s.SupportLink},
			{"自动更新间隔(小时)", strconv.Itoa(s.UpdateInterval)},
			{"通知地址", webhookText(s.WebhookURL)},
			{"到期提醒", notifyDaysText(s.NotifyDaysLeft)},
			{"用量提醒", notifyPercentText(s.NotifyUsagePercent)},
		}); err != nil {
			return err
		}
		fmt.Println()
		fmt.Printf("订阅规则:%s\n", ruleSourceText(s.Rules))
		if err := writeRulesTable(s.Rules); err != nil {
			return err
		}

		// 模板正文太长,不进表格;只报哪些格式用了自定义模板。
		custom := make([]string, 0, len(s.Templates))
		for _, f := range []string{"clash-meta", "sing-box", "xray"} {
			if s.Templates[f] != "" {
				custom = append(custom, f)
			}
		}
		fmt.Println()
		if len(custom) == 0 {
			fmt.Println("客户端模板:全部用内置默认")
		} else {
			fmt.Printf("客户端模板:自定义 %s(其余用内置默认;看正文用 mpx settings template get)\n",
				strings.Join(custom, " / "))
		}
		return nil
	},
}

var settingsSetCmd = &cobra.Command{
	Use:   "set -f FILE",
	Short: "用一份 JSON 整体替换订阅设置",
	Long: `用一份 JSON 整体替换订阅设置。

JSON 的形状与 mpx -o json settings show 的输出一致(model.Settings)。先在本
地解一遍是为了把语法错误指到文件上,发出去的仍是文件里的原始内容。

注意这是整体替换:文件里没写的字段(包括 templates)会被清掉。只想改某一
份模板用 mpx settings template set,别走这里。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if settingsSetFile == "" {
			return errors.New("必须给 -f FILE(或用 -f - 从 stdin 读)")
		}
		data, err := readSettingsFile(settingsSetFile)
		if err != nil {
			return err
		}
		var s model.Settings
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("%s 不是合法的 model.Settings JSON: %w", settingsSetFile, err)
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		// 用 RawMessage 原样回传,服务端收到的就是这份文件的内容。
		raw, err := c.do("PUT", "/api/settings", json.RawMessage(data))
		if err != nil {
			return err
		}
		return report(raw, "已保存订阅设置")
	},
}

var settingsTestCmd = &cobra.Command{
	Use:   "test",
	Short: "往配置好的通知地址发一条测试消息",
	Long: `往设置里的通知地址发一条测试消息,确认对面收得到。

webhook 是一条沉默的通道:地址写错了不会有任何反馈,要等到真出事那天才发现
没收到。这条命令当场把话说出去一次 —— 对面收到就算配好了。地址本身在
mpx settings show 里看,改地址用 mpx settings set。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", "/api/settings/test-webhook", nil)
		if err != nil {
			return err
		}
		return report(raw, "测试通知已送到")
	},
}

// rulesResultFlag 是 --result 的取值说明,多处要用同一份文案。
const rulesResultFlag = "clash-meta / clash / sing-box / xray / links / base64-links / block"

var (
	settingsRulesCmd      = &cobra.Command{Use: "rules", Short: "查看与修改 User-Agent 识别规则"}
	settingsRulesListCmd  = &cobra.Command{Use: "list", Short: "列出当前生效的规则", Args: cobra.NoArgs}
	settingsRulesAddCmd   = &cobra.Command{Use: "add PATTERN", Short: "加一条规则", Args: cobra.ExactArgs(1)}
	settingsRulesRmCmd    = &cobra.Command{Use: "rm INDEX", Short: "删掉第 INDEX 条规则", Args: cobra.ExactArgs(1)}
	settingsRulesMoveCmd  = &cobra.Command{Use: "move INDEX NEW-INDEX", Short: "调整规则的先后顺序", Args: cobra.ExactArgs(2)}
	settingsRulesResetCmd = &cobra.Command{Use: "reset", Short: "清掉自定义规则,回到内置默认", Args: cobra.NoArgs}
	settingsRulesTestCmd  = &cobra.Command{Use: "test USER-AGENT", Short: "看某个 User-Agent 会拿到哪种格式", Args: cobra.ExactArgs(1)}
)

var settingsRulesResult string

// settingsRulesIndex 是 --index 的落点:插到第几位(1-based)。默认空串表示
// 追加到最后 —— 用"有没有给"来区分,而不是用 0 表示末尾,后者会让"插到第 0 位"
// 这种写错的值悄悄变成"追加"。
var settingsRulesIndex string

// 规则表的说明放在这一处,几个子命令共用,免得各写一份还写得不一致。
const rulesLong = `控制面按 User-Agent 决定给客户端哪种格式的订阅:订阅地址只有一条,
客户端各认各的格式(Clash 要 YAML、sing-box 要 JSON、v2rayN 只认 base64 链接),
认错就白给。

规则按先后顺序匹配,第一条命中的说了算。没配过规则时用的是一套内置默认(见
mpx settings rules reset),它是开箱即用的那部分 —— 装了控制面、把订阅地址填进
客户端就该拿到对的配置,不必先来读文档写正则。

序号从 1 开始,与 list 打出来的第一列一致。`

func init() {
	settingsRulesListCmd.RunE = runSettingsRulesList
	settingsRulesAddCmd.RunE = runSettingsRulesAdd
	settingsRulesRmCmd.RunE = runSettingsRulesRm
	settingsRulesMoveCmd.RunE = runSettingsRulesMove
	settingsRulesResetCmd.RunE = runSettingsRulesReset
	settingsRulesTestCmd.RunE = runSettingsRulesTest

	settingsRulesCmd.Long = rulesLong
	settingsRulesAddCmd.Long = `加一条规则。

PATTERN 是 Go 正则,要匹配整个 User-Agent 里的任意一段;不区分大小写就写
(?i) 开头。默认加在最后(优先级最低),用 --index 插到指定位置 —— 顺序有意
义:宽的正则会盖住后面的,比如 (?i)clash 会把 clash-verge/v1.7.0 mihomo/v1.18.0
也接住,所以 mihomo 那条必须排在它前面。

RESULT 是命中后返回什么:` + rulesResultFlag + `。其中 block 表示拒绝(404),
适合把不想要的客户端挡在外面。

改的是整张规则表,因此第一次改动会把内置默认那份一起落库,此后就以你的为准;
想退回内置默认用 mpx settings rules reset。`
	settingsRulesRmCmd.Long = `删掉第 INDEX 条规则。

序号按 mpx settings rules list 打出来的第一列,从 1 开始。删完如果一条不剩,
就回到内置默认(与 reset 同效)。`
	settingsRulesMoveCmd.Long = `把第 INDEX 条挪到第 NEW-INDEX 位,其余顺次让位。

顺序即优先级,所以这是「让某条规则先说话」的办法:比如让自定义的某条比内置的
mihomo 那条更早生效,就把它 move 到 1。`
	settingsRulesResetCmd.Long = `清掉自定义规则,回到内置默认。

内置默认认 mihomo / clash-verge / clash.meta / stash / sing-box / v2rayN /
Shadowrocket / Quantumult X / Surge 等常见客户端及其变体(karing、v2rayng、
hiddify、nekobox 这些改名版也认)。`
	settingsRulesTestCmd.Long = `看某个 User-Agent 会拿到哪种格式,用来排查「客户端拿到的东西不对」。

客户端到底发的是什么,可以自己抓一份:在控制面所在机器上
curl -A '<客户端的 UA>' -i http://127.0.0.1:8080/sub/<订阅 token> 看返回的头与
正文形状。规则里写了 (?i) 就不必在意大小写。`
}

// readSettings 拉一份当前设置。改规则的子命令都要先读再写:PUT /api/settings
// 是整体替换,不回传原有的模板与通知配置就会被一起清掉。
func readSettings(c *Client) (*model.Settings, error) {
	raw, err := c.do("GET", "/api/settings", nil)
	if err != nil {
		return nil, err
	}
	s, err := decode[model.Settings](raw)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// saveSettings 把改好的设置整体写回去。
//
// 之所以不直接发手写的片段:模板正文、通知阈值这些都在同一份设置里,整体替换
// 语义下漏掉谁就是清掉谁。读-改-写能保证只动规则这一处。
func saveSettings(c *Client, s *model.Settings) ([]byte, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("序列化设置: %w", err)
	}
	return c.do("PUT", "/api/settings", json.RawMessage(data))
}

// rulesAreBuiltin 判断一份规则是不是就是内置默认那份。
//
// 服务端对空规则回落到内置默认,所以拿回来的永远是一张有内容的表 —— 光看长度
// 分不出「没配过」和「照着内置抄了一遍」。客户端与服务端同属一个模块,直接比
// 内容即可,不必再给接口加字段。
func rulesAreBuiltin(rules []model.SubscriptionRule) bool {
	builtin := model.BuiltinRules()
	if len(rules) != len(builtin) {
		return false
	}
	for i := range rules {
		if !rules[i].SameMatch(builtin[i]) {
			return false
		}
	}
	return true
}

// ruleSourceText 描述规则表的来路,给 list/show 用。
func ruleSourceText(rules []model.SubscriptionRule) string {
	if rulesAreBuiltin(rules) {
		return fmt.Sprintf("内置默认(%d 条)", len(rules))
	}
	return fmt.Sprintf("自定义(%d 条)", len(rules))
}

func runSettingsRulesList(cmd *cobra.Command, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	s, err := readSettings(c)
	if err != nil {
		return err
	}
	if opts.output == outputJSON {
		return emitJSONValue(s.Rules)
	}
	// 表本身看不出「这就是内置默认」还是「有人照着内置抄了一遍」,而那正是用户
	// 决定要不要 reset 的依据,所以单独报一行。
	fmt.Printf("规则来源:%s\n\n", ruleSourceText(s.Rules))
	return writeRulesTable(s.Rules)
}

// writeRulesTable 打规则表;空表要说清是"回到内置默认"而不是"没有规则"。
// writeRulesTable 打规则表。调用方拿到的规则已经过服务端回落,所以这里实际
// 上打不出空表;真出现空表说明控制面没做回落,那时"退回分享链接"才是对的描述。
func writeRulesTable(rules []model.SubscriptionRule) error {
	if len(rules) == 0 {
		_, err := os.Stdout.WriteString("(空;服务端会回落到内置默认,不该看到这一行)\n")
		return err
	}
	rows := make([][]string, 0, len(rules))
	for i, r := range rules {
		rows = append(rows, []string{strconv.Itoa(i + 1), string(r.Result), r.Pattern})
	}
	return writeTable([]string{"序号", "匹配结果", "User-Agent 正则"}, rows)
}

// ruleIndex 把命令行的 1-based 序号翻成切片下标,顺带把越界说清楚。
func ruleIndex(arg string, n int) (int, error) {
	i, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("序号要写成整数(比如 2),得到 %q", arg)
	}
	if i < 1 || i > n {
		if n == 0 {
			return 0, errors.New("当前一条规则都没有")
		}
		return 0, fmt.Errorf("序号 %d 超出范围:当前 %d 条", i, n)
	}
	return i - 1, nil
}

func runSettingsRulesAdd(cmd *cobra.Command, args []string) error {
	if settingsRulesResult == "" {
		return errors.New("必须给 --result(或 -r):命中的客户端要拿到哪种格式")
	}
	result := model.ClientFormat(settingsRulesResult)
	if !result.Valid() {
		return fmt.Errorf("不支持的结果 %q:只能是 %s", settingsRulesResult, rulesResultFlag)
	}
	// 正则先本地编一遍:服务端也会拒,但在这里拒能立刻指出是哪条,不必等一次
	// 往返;而且空正则会被服务端当成"不合法"报回来,理由不如这里直白。
	if _, err := regexp.Compile(args[0]); err != nil {
		return fmt.Errorf("正则不合法: %w", err)
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	s, err := readSettings(c)
	if err != nil {
		return err
	}
	rule := model.SubscriptionRule{Pattern: args[0], Result: result}
	at := len(s.Rules)
	if cmd.Flags().Changed("index") {
		i, err := ruleIndex(settingsRulesIndex, len(s.Rules)+1)
		if err != nil {
			return err
		}
		at = i
	}
	s.Rules = insertRule(s.Rules, at, rule)

	raw, err := saveSettings(c, s)
	if err != nil {
		return err
	}
	return report(raw, "已加规则 %d: %s → %s", at+1, args[0], result)
}

func runSettingsRulesRm(cmd *cobra.Command, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	s, err := readSettings(c)
	if err != nil {
		return err
	}
	i, err := ruleIndex(args[0], len(s.Rules))
	if err != nil {
		return err
	}
	removed := s.Rules[i]
	s.Rules = removeRule(s.Rules, i)

	raw, err := saveSettings(c, s)
	if err != nil {
		return err
	}
	return report(raw, "已删规则 %d: %s → %s", i+1, removed.Pattern, removed.Result)
}

func runSettingsRulesMove(cmd *cobra.Command, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	s, err := readSettings(c)
	if err != nil {
		return err
	}
	// 目标位按"挪完之后它排第几"来读(与 help 里的说法一致),因此 1-based 的
	// NEW-INDEX 直接翻成下标即可,往前挪往后挪都是这一套:摘掉之后再插到同一
	// 下标上,结果就是它落在那一格。上限是 n 而不是 n+1 —— n 条规则里不存在
	// 第 n+1 位这种落点。
	from, err := ruleIndex(args[0], len(s.Rules))
	if err != nil {
		return err
	}
	to, err := ruleIndex(args[1], len(s.Rules))
	if err != nil {
		return err
	}
	if from == to {
		return fmt.Errorf("第 %d 条已经在第 %d 位了", from+1, to+1)
	}

	rule := s.Rules[from]
	s.Rules = insertRule(removeRule(s.Rules, from), to, rule)

	raw, err := saveSettings(c, s)
	if err != nil {
		return err
	}
	return report(raw, "已把 %s 从第 %d 位挪到第 %d 位", rule.Pattern, from+1, to+1)
}

// insertRule 在 at 处插入一条规则,返回新切片。
//
// 不就地改传入的切片:规则表来自服务端响应,就地改会顺带改到别的引用(比如
// 上面刚读出来的那份),排查起来很费劲。新切片还留了余量,连续几次 add 不会
// 每次都重新分配。
func insertRule(rules []model.SubscriptionRule, at int, r model.SubscriptionRule) []model.SubscriptionRule {
	out := make([]model.SubscriptionRule, 0, len(rules)+1)
	out = append(out, rules[:at]...)
	out = append(out, r)
	return append(out, rules[at:]...)
}

// removeRule 摘掉第 i 条,返回新切片。
func removeRule(rules []model.SubscriptionRule, i int) []model.SubscriptionRule {
	out := make([]model.SubscriptionRule, 0, len(rules)-1)
	out = append(out, rules[:i]...)
	return append(out, rules[i+1:]...)
}

func runSettingsRulesReset(cmd *cobra.Command, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	s, err := readSettings(c)
	if err != nil {
		return err
	}
	if rulesAreBuiltin(s.Rules) {
		return errors.New("现在的规则就是内置默认,不用重置")
	}
	// 置空即可:服务端与存储层都把空规则当"没配过",读回来就是内置默认那份。
	s.Rules = nil

	raw, err := saveSettings(c, s)
	if err != nil {
		return err
	}
	return report(raw, "已清掉自定义规则,回到内置默认")
}

func runSettingsRulesTest(cmd *cobra.Command, args []string) error {
	ua := args[0]
	c, err := newClient()
	if err != nil {
		return err
	}
	s, err := readSettings(c)
	if err != nil {
		return err
	}

	// 判定直接调服务端用的同一个函数,免得这里"算"的结果和真发的对不上。
	format, hit := s.MatchFormat(ua)
	if opts.output == outputJSON {
		out := map[string]any{
			"user_agent": ua,
			"format":     string(format),
			"matched":    hit,
		}
		if i, r, ok := firstMatchingRule(s.RuleList(), ua); ok {
			out["index"] = i + 1
			out["pattern"] = r.Pattern
		}
		return emitJSONValue(out)
	}

	if !hit {
		fmt.Printf("User-Agent %s\n", strconv.Quote(ua))
		fmt.Println("没命中任何规则,回落到分享链接(links)。")
		fmt.Println("这不是错误:真正自己解析订阅的客户端就该拿到链接列表。")
		return nil
	}
	i, r, _ := firstMatchingRule(s.RuleList(), ua)
	fmt.Printf("User-Agent %s\n", strconv.Quote(ua))
	fmt.Printf("命中第 %d 条规则 %s,返回 %s。\n", i+1, strconv.Quote(r.Pattern), format)
	return nil
}

// firstMatchingRule 找第一条命中的规则,用来在 test 的输出里说清是"哪条"接住的。
// 判定顺序与 MatchFormat 一致(坏正则跳过),返回的下标是 0-based。
func firstMatchingRule(rules []model.SubscriptionRule, userAgent string) (int, model.SubscriptionRule, bool) {
	for i, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			continue
		}
		if re.MatchString(userAgent) {
			return i, r, true
		}
	}
	return 0, model.SubscriptionRule{}, false
}

// webhookText 把通知地址显示成人话:没配时说清楚是"不发通知",而不是留一格空。
func webhookText(url string) string {
	if url == "" {
		return "未配置(不发通知)"
	}
	return url
}

// notifyDaysText 把"提前几天提醒"翻译成人话。
//
// 0 在这两个阈值字段里表示关掉,直接显示 0 会读成"到期当天才提醒",而那是另一
// 回事。负数不该出现(写入口会拒),真出现了也当关掉显示。
func notifyDaysText(days int) string {
	if days <= 0 {
		return "关"
	}
	return fmt.Sprintf("到期前 %d 天", days)
}

// notifyPercentText 同 notifyDaysText,把用量阈值翻译成人话。
func notifyPercentText(pct int) string {
	if pct <= 0 {
		return "关"
	}
	return fmt.Sprintf("用量达到 %d%%", pct)
}

// readSettingsFile 读设置文件;"-" 表示从 stdin 读,方便管道与 heredoc。
func readSettingsFile(path string) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("读 stdin: %w", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
