package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 控制面远程升级的轮询参数。做成包级变量是为了让测试把间隔与总超时压到毫秒级
// ——真等 3 分钟一轮的测试没法跑。
var (
	// controlPollInterval 是两次查询之间的间隔。控制面重启期间接口必然失联,
	// 间隔给短一点能早点看到它回来。
	controlPollInterval = 2 * time.Second
	// controlPollTimeout 是整轮等待的上限。升级要下载、校验、重启、健康检查,
	// 3 分钟足够;再久就不是慢,而是卡住了。
	controlPollTimeout = 3 * time.Minute
)

// controlStageNames 把状态文件里的阶段名翻成给人看的中文。
//
// 只翻已知阶段:不认识的词原样带出,总好过编一个名字出来。
func controlStageName(state string) string {
	switch state {
	case "downloading":
		return "下载"
	case "verifying":
		return "校验"
	case "switching":
		return "切换"
	case "health-check":
		return "健康检查"
	default:
		return state
	}
}

// applyControl 完成一次控制面远程升级。
//
// CLI 自己不动控制面的二进制:它只把请求登记到控制面上,由 root 的一次性单元
// 去换文件、重启服务,CLI 在这里轮询状态直到有结果。
func applyControl(cmd *cobra.Command) error {
	version, _ := cmd.Flags().GetString("version")
	allowDowngrade, _ := cmd.Flags().GetBool("allow-downgrade")
	yes, _ := cmd.Flags().GetBool("yes")

	// 与控制面接口同一套判据:空版本表示"最新",所以只有显式降级才必须带版本。
	if allowDowngrade && version == "" {
		return errors.New("--allow-downgrade 必须和 --version 一起用")
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	raw, err := c.do("GET", "/api/update", nil)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return errors.New("控制面版本过旧，不支持远程升级；请先在控制面上用 install.sh 升级一次")
		}
		return err
	}
	snap, err := decode[model.UpdateSnapshot](raw)
	if err != nil {
		return err
	}

	// 正在跑的那一轮还没结束:再登记一次只会撞 409,不如先说清卡在哪一步。
	if last := snap.Control.Last; last != nil && last.State != "" && last.State != "idle" {
		return fmt.Errorf("控制面上一次升级还在进行（阶段 %s）", controlStageName(last.State))
	}

	target := version
	if target == "" {
		target = "最新版"
	}
	fmt.Printf("控制面 %s → %s；会重启 mpx.service，用户连接不受影响\n",
		updateVersionText(snap.Control.Current), target)

	if !yes {
		// 与控制面本机升级同一套确认:没有 -y 就必须能问到人。
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

	// 记下发请求前的本地时间:轮询据此判断看到的是不是这一轮的状态。控制面与
	// 本机时钟可能差几秒,减一分钟容错。
	startedAfter := time.Now()
	prev := snap.Control.Last
	if _, err := c.do("POST", "/api/update/control", map[string]any{
		"action":          "upgrade",
		"version":         version,
		"allow_downgrade": allowDowngrade,
	}); err != nil {
		// 409 等由服务端给出的错误原样返回:它比客户端能拼的任何话都准确。
		return err
	}

	return waitControl(c, startedAfter, prev, "upgrade")
}

// rollbackControl 让控制面回退到升级前的版本。
func rollbackControl(cmd *cobra.Command) error {
	yes, _ := cmd.Flags().GetBool("yes")

	c, err := newClient()
	if err != nil {
		return err
	}

	fmt.Println("控制面会回退到升级前的版本；会重启 mpx.service，用户连接不受影响")
	if !yes {
		if !stdinIsTerminal() {
			return errors.New("非交互环境请加 -y")
		}
		fmt.Print("确认回退?[y/N] ")
		ans, err := readLine(os.Stdin)
		if err != nil {
			return err
		}
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Println("已取消,没有回退。")
			return nil
		}
	}

	startedAfter := time.Now()
	prev := currentControlLast(c)
	if _, err := c.do("POST", "/api/update/control", map[string]any{"action": "rollback"}); err != nil {
		return err
	}
	return waitControl(c, startedAfter, prev, "rollback")
}

// waitControl 轮询控制面升级状态,直到这一轮跑完或超时。
//
// want 是这次请求的动作(upgrade/rollback),决定成功那句话怎么说。
//
// 控制面重启期间接口会连不上、也可能回 5xx —— 那正是升级在进行的证据,不能
// 因此判失败,只能容忍着继续等。只有连续失败时打一行提示,免得刷屏。
func waitControl(c *Client, startedAfter time.Time, prev *model.UpdateRun, want string) error {
	deadline := time.Now().Add(controlPollTimeout)
	// 控制面与本机时钟可能有偏差,给一分钟容错;在这之前的状态一律是上一轮。
	cutoff := startedAfter.Add(-time.Minute)

	lastState := ""
	failures := 0
	for {
		if time.Now().After(deadline) {
			return errors.New("等待超时；用 mpx update 查看结果")
		}

		raw, err := c.do("GET", "/api/update", nil)
		if err != nil {
			// 连不上或 5xx 都当作"控制面正在重启",除非超时,否则不中断。
			failures++
			if failures == 1 {
				fmt.Println("控制面重启中…")
			}
			time.Sleep(controlPollInterval)
			continue
		}
		failures = 0

		snap, err := decode[model.UpdateSnapshot](raw)
		if err != nil {
			// 响应暂时不可解析也可能是重启途中的半个响应,同样容忍。
			time.Sleep(controlPollInterval)
			continue
		}
		last := snap.Control.Last
		// 发请求前已经看到过的那一轮(哪怕就在一分钟内结束)也不算:只认开始
		// 时间比它更晚的。
		if last == nil || last.StartedAt.Before(cutoff) || (prev != nil && !last.StartedAt.After(prev.StartedAt)) {
			// 还是上一轮的结果(或干脆没有),这一轮还没开始。
			time.Sleep(controlPollInterval)
			continue
		}

		// 阶段变化就打一行,让人知道卡在哪一步。
		if last.State != lastState {
			if name := controlStageName(last.State); name != "" && last.State != "idle" {
				fmt.Printf("阶段：%s\n", name)
			}
			lastState = last.State
		}

		if last.State != "idle" {
			time.Sleep(controlPollInterval)
			continue
		}

		switch last.Result {
		case "ok":
			if want == "rollback" {
				fmt.Printf("已回退到 %s\n", dash(last.To))
				return nil
			}
			if last.From == last.To {
				fmt.Printf("控制面已是最新（%s）\n", dash(last.To))
				return nil
			}
			fmt.Printf("已升级到 %s\n", dash(last.To))
			return nil
		case "rolled-back":
			return fmt.Errorf("升级失败，已自动回退到 %s：%s", dash(last.From), last.Error)
		case "failed":
			return fmt.Errorf("升级失败：%s", last.Error)
		default:
			// idle 但结果还没写全:再等一轮。
			time.Sleep(controlPollInterval)
		}
	}
}

// currentControlLast 取发请求前控制面最近一次升级的记录,取不到就当没有。
func currentControlLast(c *Client) *model.UpdateRun {
	raw, err := c.do("GET", "/api/update", nil)
	if err != nil {
		return nil
	}
	snap, err := decode[model.UpdateSnapshot](raw)
	if err != nil {
		return nil
	}
	return snap.Control.Last
}
