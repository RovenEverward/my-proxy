package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/selfupdate"
)

// 这些包级变量把几件"测试里不能真做"的事隔出来:真去问发布站、真替换当前
// 二进制、真把 stdin 当终端。做成变量而不是直接调用,是为了让测试注入假实现。
var (
	// selfupdateSource 给出从哪儿取发布件,默认是本项目在 GitHub 上的仓库。
	selfupdateSource = func() selfupdate.Source {
		return selfupdate.Source{Repo: buildinfo.Repo()}
	}
	// selfExecutable 返回本机 mpx 的绝对路径,并解开符号链接 —— Install/Rollback
	// 要在同一目录里做 rename,路径必须指向真实的那个文件。
	selfExecutable = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("找不到本机可执行文件: %w", err)
		}
		resolved, err := filepath.EvalSymlinks(exe)
		if err != nil {
			return "", fmt.Errorf("解析本机可执行文件路径: %w", err)
		}
		return resolved, nil
	}
	// stdinIsTerminal 判断有没有人坐在终端前,没有就不能问 [y/N]。
	stdinIsTerminal = func() bool {
		return term.IsTerminal(int(os.Stdin.Fd()))
	}
	// cliStatePath 是自更新状态文件的路径,与配置同目录。做成函数是因为
	// configFile 本身可能失败(没有用户配置目录)。
	cliStatePath = func() (string, error) {
		cfg, err := configFile()
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Dir(cfg), "update.json"), nil
	}
)

// 部件名:三个可升级的对象。
const (
	updateComponentCLI     = "cli"
	updateComponentControl = "control"
	updateComponentNodes   = "nodes"
)

// updateComponentChoices 用于报错时列出可选值。
const updateComponentChoices = "cli、control、nodes"

// updateApplyCmd 是 mpx update apply <部件>。
var updateApplyCmd = &cobra.Command{
	Use:   "apply <cli|control|nodes>",
	Short: "升级本机 CLI、控制面或节点核心",
	Long: `升级指定部件。

本机 CLI(cli):从发布站取回指定版本(默认最新),核对 SHA256SUMS 后替换当前可执行
文件,旧文件留在 <mpx>.prev 供 mpx update rollback cli 回退。

控制面(control):把升级请求登记到控制面,由控制面的 root 一次性单元换二进制并
重启服务。

节点核心(nodes):让控制面对每台节点逐台升级核心。xray 由节点自己从 GitHub 拉,
sing-box 由控制面推;进度会一条条打在终端上,失败即停并返回错误。`,
	Example: `  mpx update apply cli --version v0.6.1 -y
  mpx update apply control -y
  mpx update apply nodes`,
	Args: cobra.ExactArgs(1),
	RunE: runUpdateApply,
}

// updateRollbackCmd 是 mpx update rollback <部件>。
var updateRollbackCmd = &cobra.Command{
	Use:   "rollback <cli|control|nodes>",
	Short: "回退到升级前的版本",
	Long: `把本机 CLI 换回上一次 mpx update apply cli 留下的 <mpx>.prev。

控制面与节点核心的回退尚未支持。`,
	Args: cobra.ExactArgs(1),
	RunE: runUpdateRollback,
}

func init() {
	updateApplyCmd.Flags().String("version", "", "要升级到的版本,默认取最新发布")
	updateApplyCmd.Flags().Bool("allow-downgrade", false, "允许 --version 指向比当前更旧的版本")
	updateApplyCmd.Flags().BoolP("yes", "y", false, "跳过确认,直接升级")
	updateRollbackCmd.Flags().BoolP("yes", "y", false, "跳过确认,直接回退")
}

// runUpdateApply 把部件名分派到各自的实现。
func runUpdateApply(cmd *cobra.Command, args []string) error {
	switch args[0] {
	case updateComponentCLI:
		return applyCLI(cmd)
	case updateComponentControl:
		return applyControl(cmd)
	case updateComponentNodes:
		return applyNodes(cmd)
	default:
		return fmt.Errorf("不认识的部件 %q，可选值：%s", args[0], updateComponentChoices)
	}
}

// runUpdateRollback 把部件名分派到各自的回退实现。
func runUpdateRollback(cmd *cobra.Command, args []string) error {
	switch args[0] {
	case updateComponentCLI:
		return rollbackCLI()
	case updateComponentControl:
		return rollbackControl(cmd)
	case updateComponentNodes:
		return errors.New("节点核心回退尚未支持（M4）")
	default:
		return fmt.Errorf("不认识的部件 %q，可选值：%s", args[0], updateComponentChoices)
	}
}

// applyCLI 完成一次本机 CLI 自更新。
func applyCLI(cmd *cobra.Command) error {
	if err := selfupdate.CheckLocalUpdate(localVersion()); err != nil {
		return err
	}
	version, _ := cmd.Flags().GetString("version")
	allowDowngrade, _ := cmd.Flags().GetBool("allow-downgrade")
	yes, _ := cmd.Flags().GetBool("yes")

	// --allow-downgrade 只对显式 --version 有意义:没有目标版本就没有"降级"可言。
	if allowDowngrade && version == "" {
		return errors.New("--allow-downgrade 必须和 --version 一起用")
	}

	ctx := cmd.Context()
	src := selfupdateSource()
	current := localVersion()

	target := version
	if target == "" {
		latest, err := src.Latest(ctx)
		if err != nil {
			return err
		}
		target = latest
	}

	if err := selfupdate.Decide(current, target, version != "", allowDowngrade); err != nil {
		if errors.Is(err, selfupdate.ErrUpToDate) {
			fmt.Printf("本机 CLI 已是最新（%s）\n", current)
			return nil
		}
		return err
	}

	exe, err := selfExecutable()
	if err != nil {
		return err
	}
	fmt.Printf("本机 CLI %s → %s（文件 %s）\n", current, target, exe)

	if !yes {
		// 没有 -y 就必须能问到人:管道、重定向下 readLine 会立刻拿到 EOF,
		// 那种"确认"等于没确认,直接要求显式加 -y。
		if !stdinIsTerminal() {
			return errors.New("非交互环境请加 -y")
		}
		fmt.Print("确认升级?[y/N] ")
		ans, err := readLine(os.Stdin)
		if err != nil {
			return err
		}
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Println("已取消,没有升级。")
			return nil
		}
	}

	started := time.Now()
	bin, verified, err := src.Fetch(ctx, target, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		saveCLIState(current, target, started, "failed", err)
		return err
	}
	if !verified {
		fmt.Fprintln(os.Stderr, "该版本没有 SHA256SUMS，未校验哈希")
	}

	if err := selfupdate.Install(exe, bin, target); err != nil {
		saveCLIState(current, target, started, "failed", err)
		// ErrNeedSudo 原样返回:调用方(以及人)要能一眼认出这是权限问题。
		return err
	}
	saveCLIState(current, target, started, "ok", nil)

	fmt.Printf("已升级到 %s；正在运行的 mpx ui 需重启后生效\n", target)
	warnControlAfterUpdate(cmd)
	return nil
}

// rollbackCLI 把本机 CLI 换回 .prev。
func rollbackCLI() error {
	if err := selfupdate.CheckLocalUpdate(localVersion()); err != nil {
		return err
	}
	exe, err := selfExecutable()
	if err != nil {
		return err
	}
	out, err := selfupdate.Rollback(exe)
	if err != nil {
		return err
	}
	fmt.Printf("已回退，本机 CLI 现在是 %s\n", out)
	saveRollbackState()
	return nil
}

// saveCLIState 把命令行发起的一次自更新结果写进 update.json。写失败只字不提:
// 升级本身已经成功或失败,状态文件是附带信息,不该反过来把结果变成一个错误。
func saveCLIState(from, to string, started time.Time, result string, cause error) {
	saveCLIStateWithTrigger("manual", from, to, started, result, cause)
}

// saveCLIStateFromUI 记下 mpx ui 发起的一次本机 CLI 升级。
//
// 页面走的是 ui 包里的同一套下载与替换,但那条路不认识 cli 包;ui.Options.CLIUpdated
// 把它交回这里,好让 mpx update show 看得到页面点出来的这次升级。成功与失败的
// result 与命令行完全一致("ok"/"failed"),只有 trigger 不同。
func saveCLIStateFromUI(from, to string, started time.Time, cause error) {
	result := "ok"
	if cause != nil {
		result = "failed"
	}
	saveCLIStateWithTrigger("ui", from, to, started, result, cause)
}

// saveCLIStateWithTrigger 是 saveCLIState 与 saveCLIStateFromUI 的共同实现。
func saveCLIStateWithTrigger(trigger, from, to string, started time.Time, result string, cause error) {
	path, err := cliStatePath()
	if err != nil {
		return
	}
	st := selfupdate.State{
		Component:  updateComponentCLI,
		State:      "idle",
		From:       from,
		To:         to,
		StartedAt:  started,
		FinishedAt: time.Now(),
		Result:     result,
		Trigger:    trigger,
	}
	if cause != nil {
		st.Error = cause.Error()
	}
	_ = selfupdate.SaveState(path, st)
}

// saveRollbackState 记下这次回退。方向和上一次升级相反,所以尝试从旧状态里
// 把 from/to 倒过来,拿不到就只写结果。
func saveRollbackState() {
	path, err := cliStatePath()
	if err != nil {
		return
	}
	now := time.Now()
	st := selfupdate.State{
		Component:  updateComponentCLI,
		State:      "idle",
		Result:     "rolled-back",
		Trigger:    "manual",
		StartedAt:  now,
		FinishedAt: now,
	}
	if prev, ok, err := selfupdate.LoadState(path); err == nil && ok {
		st.From, st.To = prev.To, prev.From
	}
	_ = selfupdate.SaveState(path, st)
}

// warnControlAfterUpdate 升级完顺手问一下控制面还在不在。
//
// 只警告不报错:CLI 装好了就是装好了,控制面连不上是另一件事(可能本来就没配
// 控制面)。没配置控制面时静默跳过,不拿一句"连不上"去吓人。
func warnControlAfterUpdate(cmd *cobra.Command) {
	c, err := newClient()
	if err != nil {
		return
	}
	if _, err := c.do("GET", "/api/status", nil); err != nil {
		fmt.Fprintf(os.Stderr, "注意：升级后联系控制面失败:%v\n", err)
	}
}

// 节点核心升级的轮询参数。做成包级变量,测试把间隔压到毫秒级,不然真等 2 秒
// 一轮的用例没法跑。
var (
	nodeUpdatePollInterval = 2 * time.Second
	// nodeUpdatePollTimeout 是整轮等待的上限。多台节点串行升级可能很久,给足;
	// 再久就不是慢,而是卡住了。
	nodeUpdatePollTimeout = time.Hour
)

// applyNodes 让控制面起一轮节点核心一键升级,然后轮询进度直到跑完。
//
// CLI 不自己连节点:它只发起任务、把控制面产生的事件一条条打出来,失败时以
// 非零退出(返回 error)。任务状态只在控制面内存里,控制面重启会让它消失,这时
// 明确报错而不是继续等。
func applyNodes(cmd *cobra.Command) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	raw, err := c.do("POST", "/api/update/nodes", nil)
	if err != nil {
		return err
	}
	started, err := decode[struct {
		Job int64 `json:"job"`
	}](raw)
	if err != nil {
		return err
	}
	fmt.Printf("节点核心升级任务 #%d 已启动\n", started.Job)
	return waitNodeUpdate(c, started.Job)
}

// waitNodeUpdate 按 since 增量拉取进度,打印每条新事件,直到任务 done/failed。
//
// 只有 done 才算成功;failed 把最后一条失败事件的原因带回去。控制面重启后任务
// 会变回 idle 且 job 对不上,这时直接报错,不空等。
func waitNodeUpdate(c *Client, job int64) error {
	deadline := time.Now().Add(nodeUpdatePollTimeout)
	var since int64
	var lastErr string

	for {
		if time.Now().After(deadline) {
			return errors.New("等待节点核心升级超时；用 mpx update 查看结果")
		}
		raw, err := c.do("GET", fmt.Sprintf("/api/update/nodes?since=%d", since), nil)
		if err != nil {
			return err
		}
		p, err := decode[model.UpdateNodesProgress](raw)
		if err != nil {
			return err
		}
		if p.Job != job {
			return errors.New("控制面上的节点核心升级任务不见了(可能刚重启过)；用 mpx update 查看结果")
		}

		for _, ev := range p.Events {
			fmt.Println(nodeUpdateEventLine(ev))
			since = ev.Seq
			if ev.Status == "failed" || ev.Status == "rolled_back" {
				lastErr = ev.Msg
			}
		}

		switch p.State {
		case model.UpdateNodesDone:
			// 事件还没拉完(被 20 条上限截断)就再拉一轮,别把尾巴丢掉。
			if since < p.Seq {
				break
			}
			fmt.Println("节点核心升级完成")
			return nil
		case model.UpdateNodesFailed:
			if since < p.Seq {
				break
			}
			if lastErr == "" {
				lastErr = "有一台节点升级失败"
			}
			return fmt.Errorf("节点核心升级失败：%s", lastErr)
		case model.UpdateNodesIdle:
			return errors.New("控制面没有正在跑的节点核心升级任务")
		}
		time.Sleep(nodeUpdatePollInterval)
	}
}

// nodeUpdateEventLine 把一条事件翻成一行给人看的话。
func nodeUpdateEventLine(ev model.UpdateNodeEvent) string {
	head := fmt.Sprintf("%s %s", ev.Node, ev.Core)
	switch ev.Status {
	case "start":
		return fmt.Sprintf("%s：%s → %s 开始", head, dash(ev.From), dash(ev.To))
	case "skip":
		return fmt.Sprintf("%s：已是 %s，跳过", head, dash(ev.To))
	case "ok":
		return fmt.Sprintf("%s：%s → %s 完成", head, dash(ev.From), dash(ev.To))
	case "rolled_back":
		return fmt.Sprintf("%s：升级失败，已回滚（%s）", head, ev.Msg)
	case "failed":
		return fmt.Sprintf("%s：升级失败（%s）", head, ev.Msg)
	default:
		return head + "：" + ev.Status
	}
}
