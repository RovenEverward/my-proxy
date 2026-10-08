package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// doctorFormats 是自检要把订阅挨个渲染一遍的格式。
//
// 固定顺序写死而不是遍历 ClientFormat 的所有值:FormatBlock 不是能产出的格式
// (它是"拒绝"这个决定),把它塞进自检表只会多出一行永远失败的检查。
var doctorFormats = []model.ClientFormat{
	model.FormatLinks,
	model.FormatBase64,
	model.FormatClash,
	model.FormatClashMeta,
	model.FormatSingBox,
	model.FormatXray,
}

// handleDoctor 跑一遍自检。
//
// 这个接口回答的是"现在这套系统有没有毛病",所以它只看不改:会读库、会渲染、
// 会 ssh 问节点,但不会去修任何东西。发现的问题要不要处置是人的决定 ——
// 一个会自动"修好"的诊断,在看结果的时候已经说不清最初是什么样了。
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	report := s.Doctor(r.Context(), time.Now())
	writeJSON(w, http.StatusOK, report)
}

// Doctor 跑一遍自检并返回结论。
//
// 不放在 handler 里而是导出一个方法,是因为 serve 的启动路径和测试都想直接调它
// —— 让自检必须经过一个 HTTP 请求才能跑,会把"能不能跑"和"HTTP 能不能用"
// 这两件事绑在一起。
func (s *Server) Doctor(ctx context.Context, now time.Time) model.DoctorReport {
	var checks []model.DoctorCheck
	checks = append(checks, s.checkControlPlane(now)...)
	checks = append(checks, s.checkNodes(ctx, now)...)
	checks = append(checks, s.checkSubscriptions(now)...)
	checks = append(checks, s.checkSamples(now))
	return model.DoctorReport{Checks: checks}
}

// checkControlPlane 看控制面自己:库能不能读、后台任务还在不在跳。
func (s *Server) checkControlPlane(now time.Time) []model.DoctorCheck {
	var checks []model.DoctorCheck

	// 库能读:读一样东西就知道连接和迁移都还在。
	if _, err := s.store.ListNodes(); err != nil {
		checks = append(checks, model.DoctorCheck{
			Name: "数据库", Status: model.DoctorFail,
			Detail: "读节点表失败:" + err.Error(),
		})
	} else {
		checks = append(checks, model.DoctorCheck{
			Name: "数据库", Status: model.DoctorOK, Detail: "能读能写",
		})
	}

	checks = append(checks, s.checkHeartbeat(now))
	return checks
}

// checkHeartbeat 判断后台任务是否还在正常跳动。
//
// 判据是"距上次 Tick 过了几个周期":
//   - 超过 3 个周期 → fail。30 秒的周期意味着 90 秒没有任何一轮跑完,这不是
//     抖动而是停了(进程卡住、goroutine 死掉、或者数据库锁住),此时所有
//     周期性的活都停了,而系统表面上看不出异常 —— 这正是自检该抓到的东西。
//   - 上一轮耗时贴着周期 → warn。还没落后,但已经没有余量了;再慢一点就会
//     永久落后(3a 的日志里有同样的告警)。
//
// 后台任务没接上时(s.status 为 nil)不算 fail:那是 CLI 直连库的场景,或者
// serve 刚启动还没接线,这时"心跳"这个概念不适用,报一条 warn 说明白即可。
func (s *Server) checkHeartbeat(now time.Time) model.DoctorCheck {
	if s.status == nil {
		return model.DoctorCheck{
			Name: "后台任务", Status: model.DoctorWarn,
			Detail: "没有接上后台任务的心跳,这一项查不了",
			Hint:   "本条只在 serve 里才有意义;用 CLI 直连数据库时看不到心跳是正常的",
		}
	}

	st := s.status.Snapshot(now)
	if st.LastTickAt == nil {
		return model.DoctorCheck{
			Name: "后台任务", Status: model.DoctorOK,
			Detail: "刚启动,还没有跑过一轮",
		}
	}

	age := now.Sub(*st.LastTickAt)
	interval := time.Duration(st.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}

	if age > 3*interval {
		return model.DoctorCheck{
			Name: "后台任务", Status: model.DoctorFail,
			Detail: fmt.Sprintf("距上一轮已经 %s(周期 %s),后台任务多半停了",
				age.Round(time.Second), interval),
			Hint: "看控制面的日志有没有 panic 或数据库锁;也可看 mpx status 的 ticks 还在不在涨",
		}
	}

	last := time.Duration(st.LastTickSeconds * float64(time.Second))
	if last > interval {
		return model.DoctorCheck{
			Name: "后台任务", Status: model.DoctorWarn,
			Detail: fmt.Sprintf("上一轮花了 %s,已经超过 %s 的周期",
				last.Round(time.Millisecond), interval),
			Hint: "现在已经在落后了,节点多起来会更明显",
		}
	}
	return model.DoctorCheck{
		Name: "后台任务", Status: model.DoctorOK,
		Detail: fmt.Sprintf("上一轮 %s,距现在 %s", last.Round(time.Millisecond), age.Round(time.Second)),
	}
}

// checkNodes 现场问每一台节点,把结论翻成检查项。
//
// 这里走的是和 `node status` 完全相同的路径(CheckNode + 落库),所以自检看到的
// 结论和手工查出来的必然是同一个 —— 两条路径各写一套判断,迟早会在某台机器上
// 给出两个答案。
func (s *Server) checkNodes(ctx context.Context, now time.Time) []model.DoctorCheck {
	nodes, err := s.store.ListNodes()
	if err != nil {
		return []model.DoctorCheck{{
			Name: "节点", Status: model.DoctorFail, Detail: "读节点列表失败:" + err.Error(),
		}}
	}
	if len(nodes) == 0 {
		return []model.DoctorCheck{{
			Name: "节点", Status: model.DoctorWarn,
			Detail: "还没有任何节点",
			Hint:   "mpx node add 加一台,再加一个入站才会有订阅",
		}}
	}

	var checks []model.DoctorCheck
	for _, n := range nodes {
		checks = append(checks, s.checkOneNode(ctx, n, now))
	}
	return checks
}

// checkOneNode 是一台节点的全部结论。
func (s *Server) checkOneNode(ctx context.Context, n model.Node, now time.Time) model.DoctorCheck {
	name := "节点 " + n.Name

	if s.nodeOps == nil {
		return model.DoctorCheck{
			Name: name, Status: model.DoctorFail,
			Detail: "服务端没有接上节点操作,查不了这台节点",
		}
	}

	st, err := s.freshNodeStatus(ctx, n, now)
	if err != nil {
		return model.DoctorCheck{
			Name: name, Status: model.DoctorFail,
			Detail: "检查失败:" + err.Error(),
		}
	}

	if !st.Reachable {
		return model.DoctorCheck{
			Name: name, Status: model.DoctorFail,
			Detail: "连不上:" + st.Error,
			Hint:   "确认地址、ssh 端口和控制面的私钥;BatchMode 下密码登录是不通的",
		}
	}

	// 一台节点可能同时有好几个毛病,但一行检查只能有一个结论。这里按严重程度
	// 取最重的那条来定 Status,并把所有发现都写进 Detail —— 分成多行的话,
	// 同一台节点在表里占好几行,反而看不出"这台机器整体什么状况"。
	var problems []string
	status := model.DoctorOK
	worstHint := ""

	raise := func(s model.DoctorCheck) {
		problems = append(problems, s.Detail)
		if doctorRank(s.Status) > doctorRank(status) {
			status = s.Status
			worstHint = s.Hint
			return
		}
		// 同等严重的问题里,谁先给了提示就用谁的;最重的那条自己没提示时,
		// 一条次重问题的提示也比没有强。
		if worstHint == "" {
			worstHint = s.Hint
		}
	}

	if !st.Active {
		raise(model.DoctorCheck{Status: model.DoctorFail, Detail: "核心服务没在跑",
			Hint: "ssh 上去 systemctl status " + n.ServiceNameOrDefault() + " 看原因"})
	}
	if !n.Deployed() {
		raise(model.DoctorCheck{Status: model.DoctorWarn, Detail: "从没下发过配置",
			Hint: "mpx node sync " + n.Name + " 推一次"})
	} else if st.ObservedHash != "" && st.ObservedHash != n.DeployedHash {
		raise(model.DoctorCheck{Status: model.DoctorWarn, Detail: "配置和下发的不一致(节点上被改过)",
			Hint: "可能是你自己改的,也可能是别人改的;确认后 mpx node sync " + n.Name + " 覆盖回去"})
	}

	for _, c := range st.Certificates {
		if c.NotAfter == nil {
			raise(model.DoctorCheck{Status: model.DoctorWarn,
				Detail: fmt.Sprintf("证书 %s 读不到到期时间:%s", c.Path, c.Error)})
			continue
		}
		switch {
		case c.DaysLeft <= model.CertFailDays:
			raise(model.DoctorCheck{Status: model.DoctorFail,
				Detail: fmt.Sprintf("证书 %s 还有 %d 天到期", c.Path, c.DaysLeft),
				Hint:   "这一批不做自动续期,需要手工换证书;换完记得 mpx node sync"})
		case c.DaysLeft <= model.CertWarnDays:
			raise(model.DoctorCheck{Status: model.DoctorWarn,
				Detail: fmt.Sprintf("证书 %s 还有 %d 天到期", c.Path, c.DaysLeft),
				Hint:   "这一批不做自动续期,提前安排换证书"})
		}
	}

	if len(problems) == 0 {
		detail := fmt.Sprintf("可达,服务在跑(v%s),配置没有漂移", dashIfEmpty(st.Version))
		if len(st.Certificates) > 0 {
			detail += fmt.Sprintf(",%d 张证书都不临期", len(st.Certificates))
		}
		return model.DoctorCheck{Name: name, Status: model.DoctorOK, Detail: detail}
	}
	return model.DoctorCheck{
		Name:   name,
		Status: status,
		Detail: strings.Join(problems, ";"),
		Hint:   worstHint,
	}
}

// checkSubscriptions 把六种格式各渲染一次。
//
// 用的是第一个"可用"用户,不是第一个用户:一个被停用或过期的用户渲染出来的是
// 占位节点(订阅的正常行为),拿它当样本会把"渲染器坏了"和"这个用户本来就
// 不可用"混在一起。没有可用用户时报 warn 而不是 fail —— 那是一个还没配好的
// 系统,不是坏掉的系统。
func (s *Server) checkSubscriptions(now time.Time) []model.DoctorCheck {
	users, err := s.store.ListUsers()
	if err != nil {
		return []model.DoctorCheck{{
			Name: "订阅渲染", Status: model.DoctorFail, Detail: "读用户列表失败:" + err.Error(),
		}}
	}

	var sample *model.User
	for i := range users {
		if users[i].Status(now) == model.StatusActive && users[i].Enabled {
			sample = &users[i]
			break
		}
	}
	if sample == nil {
		return []model.DoctorCheck{{
			Name: "订阅渲染", Status: model.DoctorWarn,
			Detail: "没有用户可渲染",
			Hint:   "mpx user add 加一个用户,六种订阅格式才会被真正产出一次",
		}}
	}

	var checks []model.DoctorCheck
	for _, format := range doctorFormats {
		out, err := s.renderSubscription(sample, format)
		if err != nil {
			checks = append(checks, model.DoctorCheck{
				Name: "订阅 " + string(format), Status: model.DoctorFail,
				Detail: "渲染失败:" + err.Error(),
				Hint:   "错误原文在上面;模板有语法问题时也会走到这里",
			})
			continue
		}
		checks = append(checks, model.DoctorCheck{
			Name: "订阅 " + string(format), Status: model.DoctorOK,
			Detail: fmt.Sprintf("渲染出 %d 字节", len(out)),
		})
	}
	return checks
}

// checkSamples 只报采样表的现状,不做判断。
//
// 「最近没有采样」不能算故障:批 1 的设计是流量计数变了才写一行,一个没人用的
// 系统本来就不会有新行。把它判成 fail 会让自检在一个完全健康的系统上报红。
// 所以这一项永远是 ok,信息全在 Detail 里。
func (s *Server) checkSamples(now time.Time) model.DoctorCheck {
	stats, err := s.store.TrafficSampleStats()
	if err != nil {
		return model.DoctorCheck{
			Name: "流量采样", Status: model.DoctorFail, Detail: "读采样表失败:" + err.Error(),
		}
	}
	if stats.LatestAt == nil {
		return model.DoctorCheck{
			Name: "流量采样", Status: model.DoctorOK,
			Detail: "0 行(还没有流量经过,不是故障)",
		}
	}
	return model.DoctorCheck{
		Name: "流量采样", Status: model.DoctorOK,
		Detail: fmt.Sprintf("%d 行,最近一条在 %s(%s 前)",
			stats.Count, stats.LatestAt.Format(time.RFC3339), now.Sub(*stats.LatestAt).Round(time.Second)),
	}
}

// doctorRank 把检查结论排个序:0 没事、1 提醒、2 故障。
//
// 一台节点可能同时有好几处毛病,而一行检查只能有一个结论 —— 用它挑最重的那条,
// 而不是让后面出现的一条覆盖前面(那会让"顺序"决定结论,和谁更严重无关)。
func doctorRank(s string) int {
	switch s {
	case model.DoctorFail:
		return 2
	case model.DoctorWarn:
		return 1
	default:
		return 0
	}
}

// dashIfEmpty 给空字符串一个占位,免得表里出现"v"这种看不懂的东西。
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
