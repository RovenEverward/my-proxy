package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// doctorFailExit 是自检发现 fail 时的进程退出码。
//
// 用一个专门的码(而不是笼统的 1)是为了让它能被脚本区分:1 也可能是"命令用错
// 了""连不上控制面",那些是"没查成",而 3 是"查成了,系统有毛病"。健康检查
// 需要的是后者 —— 前者重试一下可能就好了,后者要人去看。
const doctorFailExit = 3

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "自检:控制面、每台节点、六种订阅格式、采样表",
	Long: `对整套系统做一次体检,把每一项的结论和该做什么打出来。

检查的东西:控制面的库能不能读、后台任务还在不在跳;每一台节点现场问一次(服务
在不在跑、配置有没有被改过、证书还有几天到期);六种订阅格式各渲染一次;采样表
有多少行。

末尾一行给汇总。**只要有任何一项是 fail,退出码就是 3** —— 可以直接拿去当健康
检查脚本用。有 warn 但没有 fail 时退出码是 0:warn 是"该看一眼",不是"坏了",
把它也算成失败会让健康检查天天报警,然后就没人看了。

节点确实连不上时会如实报出来,这一项就是 fail —— 这不是这个命令的故障,
是要被发现的问题。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.doWithin(doctorTimeout, "GET", "/api/doctor", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			// JSON 模式下原样打出去,但仍然按结论给退出码:脚本用 -o json
			// 解析细节的同时,也还能靠 $? 做快速判断。
			if err := emitJSON(raw); err != nil {
				return err
			}
			return doctorExit(raw)
		}

		report, err := decode[model.DoctorReport](raw)
		if err != nil {
			return err
		}
		printDoctor(report)
		return doctorExit(raw)
	},
}

// doctorExit 按报告里的结论决定退出码。
//
// 走的是"返回一个错误"这条路:RunE 返回非 nil 时 cobra 会往 stderr 打一行,
// 而这里要的是退出码 3、并且不要多打任何字。所以用一个"是个 error、但没有消息"
// 的记号,让 Execute 特判(见 root.go)。
func doctorExit(raw []byte) error {
	report, err := decode[model.DoctorReport](raw)
	if err != nil {
		return err
	}
	if report.HasFailure() {
		return errDoctorFailed
	}
	return nil
}

// errDoctorFailed 是一个"没有消息的失败":报告已经打给使用者看了,
// 再往 stderr 上补一句话只会让脚本拿到的输出变脏。
var errDoctorFailed = errors.New("自检发现故障")

// printDoctor 打那张表,末尾一行汇总。
func printDoctor(report model.DoctorReport) {
	rows := make([][]string, 0, len(report.Checks))
	var fails, warns int
	for _, c := range report.Checks {
		detail := c.Detail
		// 提示写进"说明"列而不是单开一列:表宽有限,而 hint 是"该怎么办",
		// 它紧跟在问题后面读起来最顺。
		if c.Hint != "" {
			detail = detail + " → " + c.Hint
		}
		rows = append(rows, []string{c.Name, c.Status, detail})
		switch c.Status {
		case model.DoctorFail:
			fails++
		case model.DoctorWarn:
			warns++
		}
	}
	_ = writeTable([]string{"名称", "结论", "说明"}, rows)

	total := len(report.Checks)
	switch {
	case fails > 0:
		fmt.Printf("\n%d 项检查,%d 项失败,%d 项警告 —— 失败的项要看。\n", total, fails, warns)
	case warns > 0:
		fmt.Printf("\n%d 项检查,没有失败,%d 项警告。\n", total, warns)
	default:
		fmt.Printf("\n%d 项检查,全部正常。\n", total)
	}
}
