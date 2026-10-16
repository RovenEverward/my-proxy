package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/selfupdate"
	"github.com/RovenEverward/my-proxy/internal/updatecheck"
)

// updateReport 是 mpx update -o json 的形状:model.UpdateReport 的原样三部分,
// 外加本机 CLI 最近一次自更新的结果。
//
// CLILast 的类型是 selfupdate.State,而 model 包不能反过来依赖 selfupdate ——
// 所以这一层拼在 cli 这边,而不是塞进 model.UpdateReport。
type updateReport struct {
	model.UpdateReport
	CLILast *selfupdate.State `json:"cli_last,omitempty"`
}

// updateStaleControlNotice 是控制面还没有 /api/update 时补的那句话。
//
// 版本这一部分还能从 /api/status 里拼出来,节点核心版本是新接口才有的东西,
// 旧控制面根本不知道这回事 —— 与其静静地少打几行让人以为"节点没问题",不如
// 明说这一块拿不到。
const updateStaleControlNotice = "控制面版本过旧，不提供节点核心版本"

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "查看本机 CLI、控制面与各节点核心的版本",
	Long: `显示三部分各自的当前版本与目标/最新版本,以及现在能不能升级。

- 本机 CLI:这一份 mpx 自己的版本;查更新用的是控制面问到的最新发布,CLI 自己
  不直接连 GitHub。
- 控制面:控制面二进制的版本,以及后台任务查到的上游最新发布。
- 节点核心:每台节点上装着的每个核心各一行 —— 一台节点两种核心都装就有两行。

只读:不发起更新,也不连节点。要装新版是后面的事,mpx update 只把现状说清楚。`,
	Args: cobra.NoArgs,
	RunE: runUpdate,
}

// updateShowCmd 是 mpx update 的另一种叫法。RFC 把它列成子命令,两种敲法应当
// 完全等价 —— 实现上共用一个入口就不会漂移。
var updateShowCmd = &cobra.Command{
	Use:   "show",
	Short: "查看三部分版本(与 mpx update 等价)",
	Args:  cobra.NoArgs,
	RunE:  runUpdate,
}

var updateHeaders = []string{"部分", "当前", "目标/最新", "状态"}

// localVersion 是本机 CLI 编译时带的版本号,空表示从源码直接编出来的。
//
// 做成变量而不是每处直接调 buildinfo.Version:go test 编出来的二进制没有版本号,
// 不注入一个已知值的话,所有"版本一致/不一致"的用例都会落进空版本那一支,真正
// 要测的分支一个也测不到。
var localVersion = buildinfo.Version

// runUpdate 取一次版本信息并打印。mpx update 与 mpx update show 都走这里。
func runUpdate(cmd *cobra.Command, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}

	raw, err := c.do("GET", "/api/update", nil)
	var snap model.UpdateSnapshot
	stale := false
	if err != nil {
		var apiErr *APIError
		// 只有"这个接口不存在"才回退。断网、鉴权失败这些是别的问题,照常报错
		// 才能让人看到真正的原因。
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			return err
		}
		stale = true
		statusRaw, serr := c.do("GET", "/api/status", nil)
		if serr != nil {
			return serr
		}
		st, serr := decode[model.ControlStatus](statusRaw)
		if serr != nil {
			return serr
		}
		// /api/status 里本来就带着版本与更新结论,不用另起一份口径。
		snap.Control = st.ToUpdatePart()
	} else if snap, err = decode[model.UpdateSnapshot](raw); err != nil {
		return err
	}

	cliPart := cliUpdatePart(snap.Control)
	nodes := snap.Nodes
	if stale {
		// 拿不到就如实空着,不能因为没有数据就当作"都是最新"。
		nodes = []model.UpdateNodeCore{}
	}
	cliLast := loadCLILast()

	if opts.output == outputJSON {
		report := updateReport{
			UpdateReport: model.UpdateReport{CLI: cliPart, Control: snap.Control, Nodes: nodes},
			CLILast:      cliLast,
		}
		if err := emitJSONValue(report); err != nil {
			return err
		}
		// json 模式下这句话只能打到 stderr:stdout 上那份要被解析,多一行就废了。
		if stale {
			fmt.Fprintln(os.Stderr, updateStaleControlNotice)
		}
	} else {
		if err := writeTable(updateHeaders, updateRows(cliPart, snap.Control, nodes, cliLast)); err != nil {
			return err
		}
		// 表格模式下跟在表后面,和其他说明性文字一样:它说的是"这几行为什么没有",
		// 得让人看得见,而不是塞进一个可能被重定向走的错误流。
		if stale {
			fmt.Println(updateStaleControlNotice)
		}
	}

	warnVersionMismatch(cliPart.Current, snap.Control.Current)
	return nil
}

// cliUpdatePart 拼出本机 CLI 那一行。
//
// CLI 的最新版本取自控制面已经问到的结论,CLI 自己不发网络请求 —— 上游检查的
// 静默期在控制面那边管着,CLI 每次运行都去问一遍只会平白多打几个接口。
func cliUpdatePart(control model.UpdatePart) model.UpdatePart {
	p := model.UpdatePart{
		Current:    localVersion(),
		Latest:     control.Latest,
		ReleaseURL: control.ReleaseURL,
	}
	// 数不出"落后几个发布"(那要拉发布列表),所以能不能升用"最新是否更新"来判,
	// 与 /api/status 里 Ahead 的判据是同一个。
	p.Available = p.Current != "" && p.Latest != "" && updatecheck.Ahead(p.Current, p.Latest)
	return p
}

// updateRows 把三部分摆成一张四列表。
//
// cli 那一行的状态后面追加最近一次自更新的结果。
func updateRows(cli, control model.UpdatePart, nodes []model.UpdateNodeCore, cliLast *selfupdate.State) [][]string {
	rows := [][]string{
		{"本机 CLI", updateVersionText(cli.Current), dash(cli.Latest), updatePartStatus(cli) + cliLastSuffix(cliLast)},
		{"控制面", updateVersionText(control.Current), dash(control.Latest), updatePartStatus(control) + controlLastSuffix(control.Last)},
	}
	for _, n := range nodes {
		label := n.Name
		if n.Display != "" && n.Display != n.Name {
			label = fmt.Sprintf("%s(%s)", n.Name, n.Display)
		}
		rows = append(rows, []string{
			"节点 " + label,
			string(n.Core) + " " + nodeCoreVersionText(n.Current),
			dash(n.Target),
			nodeCoreStatus(n.Current, n.Target),
		})
	}
	return rows
}

// updateVersionText 写一个版本号,空版本说清是源码编译的。
//
// 光留一个空白会让人以为是命令坏了;直接报"未知"又说不清为什么未知。
// 只用于 CLI 与控制面这两行 —— 节点核心的空版本是另一回事,见
// nodeCoreVersionText。
func updateVersionText(v string) string {
	if v == "" {
		return "未知（源码编译）"
	}
	return v
}

// nodeCoreVersionText 写节点核心的版本。
//
// 核心的空版本不是"源码编译",而是"还没观察到":它的版本来自最近一次巡检,
// 节点没查过、查的时候服务没起来、或者这行是刚加上入站还没轮到,都会是空的。
// 套用二进制那一套说法会把"没查过"说成"编译方式",指向完全不同的两件事。
func nodeCoreVersionText(v string) string {
	if v == "" {
		return "未知（还没查到）"
	}
	return v
}

// updatePartStatus 给 CLI 与控制面这两行判定状态。
//
// 三种词各自对应一件不同的事:最新(可以放心)、可升级(有事要做)、未知(不知道)。
// 把"未知"说成"最新"会让人错过一次该做的升级,所以最新必须建立在一个问到的
// 最新版本之上。
func updatePartStatus(p model.UpdatePart) string {
	switch {
	case p.Current == "":
		return "未知"
	case p.Available:
		if p.ReleaseURL != "" {
			return "可升级（见发布页 " + p.ReleaseURL + "）"
		}
		return "可升级"
	case p.Latest == "":
		return "未知"
	default:
		return "最新"
	}
}

// nodeCoreStatus 比较节点上观察到的版本与目标版本。
//
// 目标为空(xray 还没有钉死版本)时只能报未知:没有一个目标,说"最新"就是替
// 控制面许了一个它没许过的承诺。
func nodeCoreStatus(current, target string) string {
	switch {
	case current == "" || target == "":
		return "未知"
	case sameVersion(current, target):
		return "最新"
	default:
		return "可升级"
	}
}

// sameVersion 比较两个版本号是不是同一个,忽略打头的 v。
//
// 节点上读回来的是 xray/sing-box 自己报的原始版本(没有 v 前缀),而控制面钉的
// 目标版本带着 v —— 直接比会把同一版判成不一致。
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(strings.TrimSpace(a), "v") == strings.TrimPrefix(strings.TrimSpace(b), "v")
}

// warnVersionMismatch 在 CLI 与控制面不是同一代时提醒一句。
//
// 打到 stderr:json 模式下 stdout 必须干净,警告混进去就没法解析了。
//
// 只警告不拦:管理接口只新增不修改,差一代通常还能用 —— 直接拒绝会让人在升级
// 控制面的窗口里连"看一下现在是什么版本"都做不了。空版本不参与比较,不知道
// 自己是谁就没资格说别人不对。
func warnVersionMismatch(cli, control string) {
	if cli == "" || control == "" {
		return
	}
	if updatecheck.SameMajorMinor(cli, control) {
		return
	}
	fmt.Fprintf(os.Stderr, "注意：本机 CLI 是 %s，控制面是 %s，主/次版本不一致；有些接口可能对不上，建议把两者升到同一代\n", cli, control)
}

// loadCLILast 读本机 CLI 上一次自更新的状态。读不到就返回 nil —— 没升级过
// 是正常状态,不该在版本表里多出一行错误。
func loadCLILast() *selfupdate.State {
	path, err := cliStatePath()
	if err != nil {
		return nil
	}
	st, ok, err := selfupdate.LoadState(path)
	if err != nil || !ok {
		return nil
	}
	return &st
}

// cliLastSuffix 把上一次自更新结果拼成一句话,附在本机 CLI 那行的状态后面。
//
// 失败要说失败:状态栏里"最新"说的是版本,不代表上一次装成了 —— 两者是两件事,
// 不能因为版本对上了就把失败盖掉。
func cliLastSuffix(st *selfupdate.State) string {
	if st == nil || st.Result == "" {
		return ""
	}
	return lastRunSuffix(st.State, st.Result, st.From, st.To, st.FinishedAt.Local().Format("01-02 15:04"))
}

// controlLastSuffix 把控制面最近一次升级/回退结果附在控制面那行后面。
//
// 进行中时不说"上次",而是直接报阶段 —— 那是一个此刻仍然成立的状态,用过去式
// 会让人以为已经结束了。
func controlLastSuffix(last *model.UpdateRun) string {
	if last == nil || last.State == "" {
		return ""
	}
	if last.State != "idle" {
		return " · 进行中：" + controlStageName(last.State)
	}
	if last.Result == "" {
		return ""
	}
	return lastRunSuffix(last.State, last.Result, last.From, last.To, last.FinishedAt.Local().Format("01-02 15:04"))
}

// lastRunSuffix 是本机 CLI 与控制面共用的"最近一次结果"格式化。
//
// 两行用同一个函数,免得同一个状态在两处被说成不同的话。
func lastRunSuffix(state, result, from, to, when string) string {
	word := map[string]string{
		"ok":          "成功",
		"failed":      "失败",
		"rolled-back": "已回退",
	}[result]
	if word == "" {
		word = result
	}
	return fmt.Sprintf(" · 上次 %s %s→%s %s", when, dash(from), dash(to), word)
}
