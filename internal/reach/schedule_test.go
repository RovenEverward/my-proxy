package reach

import (
	"testing"
	"time"
)

// UC-12 调度单测:到期名单与批大小是「一会儿一个、一会儿一批」的全部依据,
// 错了要么探不动、要么一次探完失去轮转意义。

func TestDueSubjectsSkipsFreshAndDedupes(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	fresh := now.Add(-10 * time.Minute)
	targets := []Target{
		{Subject: "node:1", TID: "inbound:1"},
		{Subject: "node:1", TID: "inbound:2"}, // 同对象第二个端点,不该重复出现
		{Subject: "node:2", TID: "inbound:3"},
		{Subject: "s:1:k", TID: "s:1:k"},
	}
	f := map[string]Freshness{
		"node:1": {Domestic: &old},
		"node:2": {Domestic: &fresh},
		// s:1:k 没有任何记录:从没探过,最该探。
	}
	due := DueSubjects(targets, f, SideDomestic, now, time.Hour)
	if len(due) != 2 {
		t.Fatalf("到期应为 2 个对象,得到 %v", due)
	}
	if due[0] != "s:1:k" || due[1] != "node:1" {
		t.Fatalf("从没探过的最靠前,其次是欠账最久的: %v", due)
	}
	// 另一侧没数据不影响这一侧:node:1 的 cp 侧从没探过。
	dueCP := DueSubjects(targets, f, SideCP, now, time.Hour)
	if len(dueCP) != 3 {
		t.Fatalf("cp 侧全欠账,应全部到期: %v", dueCP)
	}
}

func TestRotationBatchSpreadsAndCaps(t *testing.T) {
	hour, minute := time.Hour, time.Minute
	if got := RotationBatch(0, hour, minute); got != 0 {
		t.Fatalf("没有欠账不探: %d", got)
	}
	if got := RotationBatch(1, hour, minute); got != 1 {
		t.Fatalf("一个欠账探一个: %d", got)
	}
	// 60 个欠账摊进 60 个 tick:每 tick 一个。
	if got := RotationBatch(60, hour, minute); got != 1 {
		t.Fatalf("60 欠账摊 60 tick 应一批一个,得到 %d", got)
	}
	// 600 个欠账:理论 10/tick,但封顶一半内 —— 其实 10 < 300,取 10。
	if got := RotationBatch(600, hour, minute); got != 10 {
		t.Fatalf("600 欠账应每 tick 10 个,得到 %d", got)
	}
	// 3 个欠账:摊出来 1,上限一半(2)不碰 → 1。
	if got := RotationBatch(3, hour, minute); got != 1 {
		t.Fatalf("3 欠账应一批一个,得到 %d", got)
	}
}
