package reach

import (
	"sort"
	"time"
)

// Freshness 是一个探测对象各侧最近一次探测的时刻(UC-12)。
// nil 表示这一侧还没有任何来源报过结果。
type Freshness struct {
	Domestic *time.Time `json:"domestic,omitempty"`
	CP       *time.Time `json:"cp,omitempty"`
}

// Side 是探测的一侧:domestic 是境内侧(操作机/探测点),cp 是控制面侧。
type Side string

const (
	SideDomestic Side = "domestic"
	SideCP       Side = "cp"
)

// lastChecked 取一侧最近探测时刻。
func (f Freshness) lastChecked(side Side) (time.Time, bool) {
	if side == SideDomestic {
		if f.Domestic == nil {
			return time.Time{}, false
		}
		return *f.Domestic, true
	}
	if f.CP == nil {
		return time.Time{}, false
	}
	return *f.CP, true
}

// DueSubjects 按(对象,侧)算到期名单:从没探过、或距上次探测已超过 interval
// 的对象算到期;返回按欠账从多到少排序的对象身份(从没探过的最靠前)。
func DueSubjects(targets []Target, fresh map[string]Freshness, side Side, now time.Time, interval time.Duration) []string {
	type due struct {
		subject string
		last    time.Time
	}
	var list []due
	seen := map[string]bool{}
	for _, t := range targets {
		if t.Subject == "" || seen[t.Subject] {
			continue
		}
		seen[t.Subject] = true
		last, ok := fresh[t.Subject].lastChecked(side)
		if ok && now.Sub(last) < interval {
			continue
		}
		list = append(list, due{subject: t.Subject, last: last})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].last.Before(list[j].last)
	})
	out := make([]string, 0, len(list))
	for _, d := range list {
		out = append(out, d.subject)
	}
	return out
}

// RotationBatch 算一次调度 tick 该处理几个到期对象:把到期量摊进整个周期,
// 每个 tick 取一份,实现「一会儿一个、一会儿一批」而不是一次性全探。
// 至少探 1 个(due>0 时);至多一半,防止重启后一次清完全部欠账。
func RotationBatch(due int, interval, tick time.Duration) int {
	if due <= 0 {
		return 0
	}
	n := int(interval / tick)
	if n <= 0 {
		n = 1
	}
	batch := (due + n - 1) / n
	if batch < 1 {
		batch = 1
	}
	if half := (due + 1) / 2; batch > half {
		batch = half
	}
	return batch
}
