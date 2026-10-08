package control

import (
	"context"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// notifyBudget 是一轮用户通知的上限。
//
// 和巡检同一个道理:通知要发 HTTP,发不出去不该把采样和收敛拖住。一轮最多花
// 这么久,剩下的用户下一轮再说 —— 提醒记在库里,漏不掉。
const notifyBudget = 15 * time.Second

// notifyUsers 看一遍用户,把"快到期"和"快超限"发到 webhook 上。
//
// 和节点告警有两处不同,都是照着"用户提醒只有这一个通道"来的:
//
//   - 去重靠 user_notices 表,不靠内存。提醒的窗口很窄(比如到期前 3 天),控制面
//     重启或者某一轮超时都可能让那一轮看不到它;记在内存里,那次提醒就永久丢了,
//     而库里那张表让它下一轮还在。发成功才记账,所以对面一时不可达也不会丢。
//   - 条件消失时要**删掉**那条记录:续费之后剩余天数回到阈值以上、流量周期重置
//     之后用量掉下来,都说明这件事过去了;不销的话,下一次条件再出现就发不出来。
//
// 没配 webhook 地址时整件事不做,也不记账:没地方发就谈不上"发过了",记下来
// 只会让之后才配好地址的用户永远收不到自己那条提醒。
func (t *Task) notifyUsers(ctx context.Context, now time.Time) {
	settings, err := t.store.GetSettings()
	if err != nil {
		t.logger.Warn("读取设置失败,这一轮不发用户通知", "error", err)
		return
	}
	if settings.WebhookURL == "" {
		return
	}
	users, err := t.store.ListUsers()
	if err != nil {
		t.logger.Warn("读取用户列表失败,这一轮不发用户通知", "error", err)
		return
	}
	sent, err := t.store.ListUserNotices()
	if err != nil {
		t.logger.Warn("读取通知记录失败,这一轮不发用户通知", "error", err)
		return
	}

	nctx, cancel := context.WithTimeout(ctx, notifyBudget)
	defer cancel()

	for i := range users {
		u := &users[i]
		if !u.Enabled {
			continue // 停用的用户不打扰;他已经不打算用了
		}
		if nctx.Err() != nil {
			t.logger.Warn("这一轮用户通知超时,剩下的用户留到下一轮")
			return
		}
		t.notifyUser(nctx, u, settings, sent[u.ID], now)
	}
}

// notifyUser 判定一个用户这一轮要不要发,并按需要记账、清账。
func (t *Task) notifyUser(ctx context.Context, u *model.User, s *model.Settings, done map[string]time.Time, now time.Time) {
	// 到期提醒。已经过期的用户不发:他的问题不是"快到期了",而是已经不能用;
	// 提醒说"还有 0 天"是假消息,而且收敛那边已经在处理他了。
	if days, ok := u.DaysLeft(now); ok {
		msg := fmt.Sprintf("用户 %s 还有 %d 天到期", u.Username, days)
		if days == 0 {
			msg = fmt.Sprintf("用户 %s 今天到期", u.Username)
		}
		fire := s.NotifyDaysLeft > 0 && days <= s.NotifyDaysLeft && u.Status(now) != model.StatusExpired
		t.notice(ctx, u, model.NoticeKindExpiring, done, fire, now, msg)
	}

	// 超限提醒。不限量的用户 UsagePercent 恒为 0,自然不触发。
	pct := u.UsagePercent()
	fire := s.NotifyUsagePercent > 0 && pct >= s.NotifyUsagePercent
	t.notice(ctx, u, model.NoticeKindOverUsage, done, fire, now,
		fmt.Sprintf("用户 %s 本周期已用 %d%%", u.Username, pct))
}

// notice 是一类用户提醒的通用处理:该发就发,发成功才记账;条件已经不在而账还在,
// 就把账销掉,让条件下次再出现时能重发。
func (t *Task) notice(ctx context.Context, u *model.User, kind string, done map[string]time.Time, fire bool, now time.Time, msg string) {
	_, already := done[kind]

	if !fire {
		if already {
			if err := t.store.DeleteUserNotice(u.ID, kind); err != nil {
				t.logger.Warn("清除用户通知记录失败", "user", u.Username, "kind", kind, "error", err)
			}
		}
		return
	}
	if already {
		return
	}

	ev := WebhookEvent{
		Event:    EventUserNotice,
		Kind:     kind,
		Message:  msg,
		At:       now,
		Username: u.Username,
	}
	if err := PostWebhook(ctx, t.webhookURL(), ev, t.logger); err != nil {
		// 发失败不记账,下一轮还会再试。这正是记账放在这里而不是放在判断之后
		// 的原因:对面不可达不该让一条提醒永久消失。
		t.logger.Warn("发用户通知 webhook 失败", "user", u.Username, "kind", kind, "error", err)
		return
	}
	if err := t.store.SaveUserNotice(model.UserNotice{UserID: u.ID, Kind: kind, SentAt: now}); err != nil {
		t.logger.Warn("记录用户通知失败", "user", u.Username, "kind", kind, "error", err)
	}
}
