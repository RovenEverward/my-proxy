package model

import "time"

// 周期流量重置。
//
// 配额问的是"这个周期内用了多少",所以除了 used_traffic 还得记住周期从哪儿算
// 起。起算点只存一个事实 —— TrafficResetAt(上次重置的时刻,未重置过则以
// CreatedAt 起算);重置点由它加周期现算,不另存"下次重置时间",否则两个字段
// 会互相矛盾,而且控制面停机时没人去更新那个字段。

// ResetPeriod 返回该策略的周期长度。ResetMonthly 是日历月,没有固定长度,
// 返回 0 并由 addResetPeriod 特判。
func ResetPeriod(r ResetStrategy) time.Duration {
	switch r {
	case ResetDaily:
		return 24 * time.Hour
	case ResetWeekly:
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

// Valid 报告这是不是一个认得的策略。
//
// 空串和拼错的值都算不认得。它们语义上等同于 none,但必须显式判定:不判定的话
// NextResetAt 会算出一个正好等于起算点的时间,ResetDue 当场为真,而 AdvanceReset
// 每轮「推进」出来的还是同一个时间 —— 后台任务就此卡在这个用户身上转到永远。
func (r ResetStrategy) Valid() bool {
	switch r {
	case ResetNone, ResetDaily, ResetWeekly, ResetMonthly:
		return true
	default:
		return false
	}
}

// ResetAnchor 返回周期起算点:重置过就是那次的时间,否则是创建时间。
func (u *User) ResetAnchor() time.Time {
	if u.TrafficResetAt != nil {
		return *u.TrafficResetAt
	}
	return u.CreatedAt
}

// NextResetAt 返回严格晚于起算点的第一个重置点;不重置(或策略不认得)返回零值。
//
// 用"上一次重置点加一个周期"而不是"now 加一个周期",是为了让周期保住原来的相
// 位:1 号创建的用户永远在 1 号重置,不会因为控制面某次晚跑了几小时而慢慢漂移。
func (u *User) NextResetAt() time.Time {
	if u.ResetStrategy == ResetNone || !u.ResetStrategy.Valid() {
		return time.Time{}
	}
	return addResetPeriod(u.ResetAnchor(), u.ResetStrategy)
}

// ResetDue 报告现在是否已经走到下一个重置点。
func (u *User) ResetDue(now time.Time) bool {
	next := u.NextResetAt()
	return !next.IsZero() && !now.Before(next)
}

// AdvanceReset 返回重置后新的起算点:严格晚于 now 的第一个重置点。
//
// 控制面停机几天再起来时,它一次跨过好几个周期,而不会让同一个用户连着被重置
// 好几次(每次重置都伴随一次节点侧摘除/恢复,没必要重复折腾)。
func (u *User) AdvanceReset(now time.Time) time.Time {
	t := u.NextResetAt()
	if t.IsZero() {
		return time.Time{}
	}
	// 固定周期直接算步数。逐次往前挪在正常情况(起算点就在几天前)下也一样,但
	// 起算点要是个很早的时间,一天一步就要转几十万次。
	if period := ResetPeriod(u.ResetStrategy); period > 0 {
		if !t.After(now) {
			t = t.Add((now.Sub(t)/period + 1) * period)
		}
		return t
	}
	// 月周期按月初推进,月份数有界。
	for !now.Before(t) {
		next := addResetPeriod(t, u.ResetStrategy)
		// 守一道「必须往前走」:策略取值一旦出了意外,这里就是死循环。
		if !next.After(t) {
			return time.Time{}
		}
		t = next
	}
	return t
}

// addResetPeriod 把 t 往后推一个周期。
//
// 月用「下一个月初」而不是 AddDate(0,1,0):后者对 1 月 31 日会归一化成 3 月 3 日
// (2 月 31 日不存在),既跳过了 2 月,相位还会一个月一个月地往后漂。月初是唯一
// 不依赖起算日、也永远不会漂的月边界,于是「每月重置」的含义就是每月 1 日 0 点
// 重置 —— 一个能直接对使用者讲清楚的规则。
//
// ResetNone 会让它原地不动,调用方必须先确认策略不为 none(AdvanceReset 从
// NextResetAt 拿到零值就退出了,不会转到这里)。
func addResetPeriod(t time.Time, r ResetStrategy) time.Time {
	if r == ResetMonthly {
		return time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
	}
	return t.Add(ResetPeriod(r))
}
