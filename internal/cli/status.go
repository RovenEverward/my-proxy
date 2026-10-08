package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看控制面现在怎么样",
	Long: `显示控制面的心跳与规模:启动时间、已运行多久、上一轮后台任务是什么时候跑的、
花了多久、跑过多少轮,以及各类对象的数量和正在退避的节点。

数据全部来自控制面内存与本地库,不发任何 SSH,所以它总是能立刻回答 —— 节点
不通的时候它照样可用。单台节点自身的情况看 node status。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/status", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		st, err := decode[model.ControlStatus](raw)
		if err != nil {
			return err
		}

		now := time.Now()
		if err := writeKV(statusPairs(st, now)); err != nil {
			return err
		}
		if len(st.Backoffs) == 0 {
			return nil
		}
		rows := make([][]string, 0, len(st.Backoffs))
		for _, b := range st.Backoffs {
			rows = append(rows, statusBackoffRow(b, now))
		}
		return writeTable(statusBackoffHeaders, rows)
	},
}

var statusBackoffHeaders = []string{"节点 ID", "名称", "连续失败", "再试时间"}

// statusPairs 把心跳拍平成两列。now 由调用方传入,表格里的"多久之前"才测得准。
func statusPairs(st model.ControlStatus, now time.Time) [][2]string {
	lastTick := "还没跑过"
	if st.LastTickAt != nil {
		lastTick = humanDuration(now.Sub(*st.LastTickAt)) + "前(" +
			st.LastTickAt.Format("2006-01-02 15:04:05") + ")"
	}
	backoff := "无"
	if len(st.Backoffs) > 0 {
		backoff = fmt.Sprintf("%d 台", len(st.Backoffs))
	}
	return [][2]string{
		{"启动时间", st.StartedAt.Format("2006-01-02 15:04:05")},
		{"已运行", humanDuration(time.Duration(st.UptimeSeconds * float64(time.Second)))},
		{"后台周期", humanDuration(time.Duration(st.IntervalSeconds * float64(time.Second)))},
		{"上次 Tick", lastTick},
		{"上一轮耗时", fmt.Sprintf("%.2f 秒", st.LastTickSeconds)},
		{"Tick 次数", strconv.FormatUint(st.Ticks, 10)},
		{"节点", strconv.Itoa(st.Counts.Nodes)},
		{"入站", strconv.Itoa(st.Counts.Inbounds)},
		{"对外入口", strconv.Itoa(st.Counts.Hosts)},
		{"用户", strconv.Itoa(st.Counts.Users)},
		{"退避中的节点", backoff},
	}
}

// statusBackoffRow 是一台退避中节点的表格行。
func statusBackoffRow(b model.NodeBackoff, now time.Time) []string {
	until := b.SkipUntil.Format("15:04:05")
	if left := b.SkipUntil.Sub(now); left > 0 {
		until = fmt.Sprintf("%s(还剩 %s)", until, humanDuration(left))
	}
	return []string{
		strconv.FormatInt(b.NodeID, 10),
		b.NodeName,
		strconv.Itoa(b.Failures),
		until,
	}
}

// humanDuration 把时长写成「1 天 2 小时 3 分」这样的粗粒度文字。
//
// 状态页看的是量级,秒以下的抖动没有意义;不足一分钟才精确到秒,免得刚启动的
// 控制面显示成"0 分"。
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)

	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	mins := d / time.Minute
	d -= mins * time.Minute

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d 天", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d 小时", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%d 分", mins))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d 秒", int(d/time.Second)))
	}
	return strings.Join(parts, " ")
}
