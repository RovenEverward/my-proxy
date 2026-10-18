package store

import (
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// activityAt 取某个用户在某台节点上的活跃时间;没有记录就直接让测试停下。
func activityAt(t *testing.T, s *Store, username string, nodeID int64) time.Time {
	t.Helper()
	list, err := s.ListUserActivity()
	if err != nil {
		t.Fatalf("ListUserActivity: %v", err)
	}
	for _, a := range list {
		if a.Username == username && a.NodeID == nodeID {
			return a.LastActiveAt
		}
	}
	t.Fatalf("没有找到 %s 在节点 %d 上的活跃记录:%v", username, nodeID, list)
	return time.Time{}
}

// 只有增量为正的采样才算「在用」:重启(计数回退)与零增量都不动活跃时间。
func TestUserActivityWrittenOnlyOnPositiveDelta(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	newTestUser(t, s, "alice")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	apply := func(at time.Time, up, down int64) {
		t.Helper()
		if err := s.ApplyCoreTrafficSample(model.TrafficSample{Username: "alice", NodeID: n.ID, SampledAt: at, Uplink: up, Downlink: down}, model.CoreXray); err != nil {
			t.Fatalf("ApplyCoreTrafficSample(%v, %d/%d): %v", at, up, down, err)
		}
	}

	// 第一次采样按累计值记账,增量为正。
	apply(base, 100, 100)
	if got := activityAt(t, s, "alice", n.ID); !got.Equal(base) {
		t.Fatalf("第一次有流量后活跃时间应当是 %v,实际 %v", base, got)
	}

	// 零增量:写了采样,但活跃时间不动。
	apply(base.Add(time.Minute), 100, 100)
	if got := activityAt(t, s, "alice", n.ID); !got.Equal(base) {
		t.Fatalf("零增量不应当刷新活跃时间,实际 %v", got)
	}

	// 计数回退(重启)按增量 0 处理,活跃时间同样不动。
	apply(base.Add(2*time.Minute), 5, 5)
	if got := activityAt(t, s, "alice", n.ID); !got.Equal(base) {
		t.Fatalf("重启不应当刷新活跃时间,实际 %v", got)
	}

	// 恢复增长后再往前走。
	apply(base.Add(3*time.Minute), 10, 10)
	if got := activityAt(t, s, "alice", n.ID); !got.Equal(base.Add(3 * time.Minute)) {
		t.Fatalf("重启后再次有流量,活跃时间应当是 %v,实际 %v", base.Add(3*time.Minute), got)
	}
}

// 回填:表空时按历史采样重算一次,取上次累计值增长的那一行的时刻。
func TestMigrateActivityBackfill(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	rows := []struct {
		offset time.Duration
		up     int64
		down   int64
	}{
		{0, 100, 100},               // 第一行没有上一条,不算增量
		{time.Minute, 160, 150},     // 增长
		{2 * time.Minute, 160, 150}, // 没变
		{3 * time.Minute, 10, 10},   // 重启回退
		{4 * time.Minute, 30, 20},   // 增长:最后一行增长的
		{5 * time.Minute, 30, 20},   // 没变
	}
	for _, r := range rows {
		if _, err := s.db.Exec(`INSERT INTO traffic_samples(username,node_id,sampled_at,uplink,downlink,core) VALUES(?,?,?,?,?,?)`,
			"alice", n.ID, base.Add(r.offset).Unix(), r.up, r.down, model.CoreXray); err != nil {
			t.Fatalf("写采样行: %v", err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM user_activity`); err != nil {
		t.Fatalf("清空 user_activity: %v", err)
	}
	if err := s.migrateActivity(); err != nil {
		t.Fatalf("migrateActivity: %v", err)
	}

	want := base.Add(4 * time.Minute)
	if got := activityAt(t, s, "alice", n.ID); !got.Equal(want) {
		t.Fatalf("回填的活跃时间应当是 %v,实际 %v", want, got)
	}

	// 表里已有记录时不再回填。
	if _, err := s.db.Exec(`UPDATE user_activity SET last_active_at = ?`, base.Add(9*time.Minute).Unix()); err != nil {
		t.Fatalf("改活跃时间: %v", err)
	}
	if err := s.migrateActivity(); err != nil {
		t.Fatalf("migrateActivity(第二次): %v", err)
	}
	if got := activityAt(t, s, "alice", n.ID); !got.Equal(base.Add(9 * time.Minute)) {
		t.Fatalf("已有记录时不应回填,实际 %v", got)
	}
}

// 连续使用按 gap 切段:t、t+30s、t+60s 一段,t+10min、t+10min30s 另一段。
func TestActivitySegments(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	newTestUser(t, s, "alice")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	// 累计值依次增长,每行增量都是 10+10=20。
	samples := []struct {
		offset time.Duration
		up     int64
		down   int64
	}{
		{0, 10, 10},
		{30 * time.Second, 20, 20},
		{60 * time.Second, 30, 30},
		{10 * time.Minute, 40, 40},
		{10*time.Minute + 30*time.Second, 50, 50},
	}
	for _, sm := range samples {
		at := base.Add(sm.offset)
		if err := s.ApplyCoreTrafficSample(model.TrafficSample{Username: "alice", NodeID: n.ID, SampledAt: at, Uplink: sm.up, Downlink: sm.down}, model.CoreXray); err != nil {
			t.Fatalf("ApplyCoreTrafficSample(%v): %v", at, err)
		}
	}

	// 每行增量都是 20;t+60s 与 t+10min 相隔 9 分钟,超过 gap,切成两段。
	wantNewer := model.ActivitySegment{NodeID: n.ID, Start: base.Add(10 * time.Minute), End: base.Add(10*time.Minute + 30*time.Second), Bytes: 40}
	segs, err := s.ActivitySegments("alice", base.Add(30*time.Second), time.Minute, 0)
	if err != nil {
		t.Fatalf("ActivitySegments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("应当切成 2 段,实际 %d 段:%v", len(segs), segs)
	}
	wantOlder := model.ActivitySegment{NodeID: n.ID, Start: base.Add(30 * time.Second), End: base.Add(60 * time.Second), Bytes: 40}
	if segs[0] != wantNewer {
		t.Errorf("最新一段应当是 %+v,实际 %+v", wantNewer, segs[0])
	}
	if segs[1] != wantOlder {
		t.Errorf("较早一段应当是 %+v,实际 %+v", wantOlder, segs[1])
	}

	// 从起点之前查起:第一行没有上一条,增量算不出,所以从 t+30s 到的结果与上面相同。
	same, err := s.ActivitySegments("alice", base, time.Minute, 0)
	if err != nil {
		t.Fatalf("ActivitySegments(从 base 起): %v", err)
	}
	if len(same) != 2 || same[0] != wantNewer || same[1] != wantOlder {
		t.Errorf("从 base 起也应当是 %+v 与 %+v,实际 %v", wantNewer, wantOlder, same)
	}

	// max 只留最新的若干段。
	one, err := s.ActivitySegments("alice", base.Add(30*time.Second), time.Minute, 1)
	if err != nil {
		t.Fatalf("ActivitySegments(max=1): %v", err)
	}
	if len(one) != 1 || one[0] != wantNewer {
		t.Fatalf("max=1 应当只返回最新一段 %+v,实际 %v", wantNewer, one)
	}

	// 没有采样的人返回空切片而不是 nil。
	empty, err := s.ActivitySegments("bob", base, time.Minute, 0)
	if err != nil {
		t.Fatalf("ActivitySegments(bob): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("没有采样时应当是空切片,实际 %v", empty)
	}
}

// CountActiveSince 只数窗口内有过流量的人,同一人多台节点只算一次。
func TestCountActiveSince(t *testing.T) {
	s := newTestStore(t)
	hk := newTestNode(t, s, "hk")
	jp := newTestNode(t, s, "jp")
	newTestUser(t, s, "alice")
	newTestUser(t, s, "bob")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	apply := func(username string, nodeID int64, at time.Time) {
		t.Helper()
		if err := s.ApplyCoreTrafficSample(model.TrafficSample{Username: username, NodeID: nodeID, SampledAt: at, Uplink: 10, Downlink: 10}, model.CoreXray); err != nil {
			t.Fatalf("ApplyCoreTrafficSample: %v", err)
		}
	}
	apply("alice", hk.ID, base)
	apply("alice", jp.ID, base.Add(time.Hour))
	apply("bob", hk.ID, base)

	n, err := s.CountActiveSince(base)
	if err != nil {
		t.Fatalf("CountActiveSince: %v", err)
	}
	if n != 2 {
		t.Fatalf("从 base 起应当是 2 人活跃,实际 %d", n)
	}
	n, err = s.CountActiveSince(base.Add(30 * time.Minute))
	if err != nil {
		t.Fatalf("CountActiveSince: %v", err)
	}
	if n != 1 {
		t.Fatalf("从 base+30min 起应当只有 alice 活跃,实际 %d", n)
	}
	n, err = s.CountActiveSince(base.Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("CountActiveSince: %v", err)
	}
	if n != 0 {
		t.Fatalf("窗口内没有人活跃时应当是 0,实际 %d", n)
	}
}

// MarkNodeSampled 每台节点一行,后来的覆盖前面的;删节点跟着删。
func TestMarkNodeSampled(t *testing.T) {
	s := newTestStore(t)
	hk := newTestNode(t, s, "hk")
	jp := newTestNode(t, s, "jp")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	if err := s.MarkNodeSampled(hk.ID, base); err != nil {
		t.Fatalf("MarkNodeSampled: %v", err)
	}
	if err := s.MarkNodeSampled(hk.ID, base.Add(time.Minute)); err != nil {
		t.Fatalf("MarkNodeSampled(第二次): %v", err)
	}
	if err := s.MarkNodeSampled(jp.ID, base); err != nil {
		t.Fatalf("MarkNodeSampled(jp): %v", err)
	}

	got, err := s.NodeSampledTimes()
	if err != nil {
		t.Fatalf("NodeSampledTimes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应当有两台节点的采样时间,实际 %v", got)
	}
	if !got[hk.ID].Equal(base.Add(time.Minute)) {
		t.Errorf("hk 的采样时间应当被覆盖成 %v,实际 %v", base.Add(time.Minute), got[hk.ID])
	}
	if !got[jp.ID].Equal(base) {
		t.Errorf("jp 的采样时间应当是 %v,实际 %v", base, got[jp.ID])
	}

	if err := s.DeleteNode(hk.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	got, err = s.NodeSampledTimes()
	if err != nil {
		t.Fatalf("NodeSampledTimes: %v", err)
	}
	if _, ok := got[hk.ID]; ok {
		t.Fatalf("节点删掉后采样时间应当一起走,实际 %v", got)
	}
}

// 删用户清掉他的活跃时间;删节点级联清掉该节点上的活跃时间。
func TestUserActivityDeletedWithUserAndNode(t *testing.T) {
	s := newTestStore(t)
	hk := newTestNode(t, s, "hk")
	jp := newTestNode(t, s, "jp")
	newTestUser(t, s, "alice")
	newTestUser(t, s, "bob")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	apply := func(username string, nodeID int64) {
		t.Helper()
		if err := s.ApplyCoreTrafficSample(model.TrafficSample{Username: username, NodeID: nodeID, SampledAt: base, Uplink: 10, Downlink: 10}, model.CoreXray); err != nil {
			t.Fatalf("ApplyCoreTrafficSample: %v", err)
		}
	}
	apply("alice", hk.ID)
	apply("alice", jp.ID)
	apply("bob", hk.ID)

	if err := s.DeleteUser("alice"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	list, err := s.ListUserActivity()
	if err != nil {
		t.Fatalf("ListUserActivity: %v", err)
	}
	if len(list) != 1 || list[0].Username != "bob" || list[0].NodeID != hk.ID {
		t.Fatalf("删掉 alice 后应当只剩 bob 在 hk 上的记录,实际 %v", list)
	}

	if err := s.DeleteNode(hk.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	list, err = s.ListUserActivity()
	if err != nil {
		t.Fatalf("ListUserActivity: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("节点删掉后该节点上的活跃记录应当一起走,实际 %v", list)
	}
}
