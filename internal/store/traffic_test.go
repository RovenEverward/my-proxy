package store

import (
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// recordSample 往库里追加一条采样,失败就让测试立刻停下。
func recordSample(t *testing.T, s *Store, sample model.TrafficSample) {
	t.Helper()
	if err := s.RecordTrafficSample(sample); err != nil {
		t.Fatalf("记录采样 %+v: %v", sample, err)
	}
}

// LatestTrafficSamples 是「上次累计值」的唯一来源:每用户取最近一行,不串节点。
func TestLatestTrafficSamplesPerUserAndNode(t *testing.T) {
	s := newTestStore(t)
	hk := newTestNode(t, s, "hk")
	jp := newTestNode(t, s, "jp")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: hk.ID, SampledAt: base, Uplink: 100, Downlink: 200})
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: hk.ID, SampledAt: base.Add(time.Minute), Uplink: 160, Downlink: 260})
	recordSample(t, s, model.TrafficSample{Username: "bob", NodeID: hk.ID, SampledAt: base, Uplink: 5, Downlink: 6})
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: jp.ID, SampledAt: base, Uplink: 900, Downlink: 900})

	got, err := s.LatestTrafficSamples(hk.ID)
	if err != nil {
		t.Fatalf("LatestTrafficSamples: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("hk 上应当只有 alice 与 bob 两个人,实际 %d 个:%v", len(got), got)
	}
	if a := got["alice"]; a.Uplink != 160 || a.Downlink != 260 {
		t.Fatalf("alice 应当取最近一行 160/260,实际 %d/%d", a.Uplink, a.Downlink)
	}
	if a := got["alice"]; !a.SampledAt.Equal(base.Add(time.Minute)) {
		t.Errorf("取到的应当是最近一行的时刻 %v,实际 %v", base.Add(time.Minute), a.SampledAt)
	}
	if b := got["bob"]; b.Uplink != 5 || b.Downlink != 6 {
		t.Errorf("bob 的最近一行应当是 5/6,实际 %d/%d", b.Uplink, b.Downlink)
	}

	// 另一个节点上的同名用户不能混进来:基准是按「用户 × 节点」算的。
	other, err := s.LatestTrafficSamples(jp.ID)
	if err != nil {
		t.Fatalf("LatestTrafficSamples(jp): %v", err)
	}
	if a := other["alice"]; a.Uplink != 900 || a.Downlink != 900 {
		t.Fatalf("jp 上的 alice 应当是 900/900,实际 %d/%d", a.Uplink, a.Downlink)
	}
}

// 没有任何采样时返回空表而不是报错:新库、新节点走的就是这条路。
func TestLatestTrafficSamplesEmpty(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")

	got, err := s.LatestTrafficSamples(n.ID)
	if err != nil {
		t.Fatalf("LatestTrafficSamples: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("没有采样时应当是空表,实际 %v", got)
	}
}

// 区间查询按时间升序返回,并且只看这一个用户在这个节点上的行 —— 用量重算与曲线
// 都建立在这上面。
func TestListTrafficSamplesByRange(t *testing.T) {
	s := newTestStore(t)
	hk := newTestNode(t, s, "hk")
	jp := newTestNode(t, s, "jp")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: hk.ID, SampledAt: base, Uplink: 100, Downlink: 100})
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: hk.ID, SampledAt: base.Add(time.Minute), Uplink: 160, Downlink: 150})
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: hk.ID, SampledAt: base.Add(2 * time.Minute), Uplink: 200, Downlink: 190})
	recordSample(t, s, model.TrafficSample{Username: "bob", NodeID: hk.ID, SampledAt: base, Uplink: 7, Downlink: 7})
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: jp.ID, SampledAt: base, Uplink: 8, Downlink: 8})

	rows, err := s.ListTrafficSamples("alice", hk.ID, base.Add(30*time.Second), base.Add(90*time.Second))
	if err != nil {
		t.Fatalf("ListTrafficSamples: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("区间内应当只有 1 行,实际 %d 行:%v", len(rows), rows)
	}
	if rows[0].Uplink != 160 || rows[0].Downlink != 150 {
		t.Errorf("取到的行不对: %+v", rows[0])
	}

	// 区间用量 = 两端累计值相减。放开整个区间,首尾两行相减应当等于这段时间的用量。
	all, err := s.ListTrafficSamples("alice", hk.ID, base, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ListTrafficSamples(全区间): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("全区间应当有 3 行,实际 %d 行:%v", len(all), all)
	}
	if !all[0].SampledAt.Before(all[2].SampledAt) {
		t.Errorf("结果应当按时间升序:%v", all)
	}
	if got := (all[2].Uplink + all[2].Downlink) - (all[0].Uplink + all[0].Downlink); got != 190 {
		t.Errorf("区间用量应当是 190,实际 %d", got)
	}
}

// 单连接下并发追加不能丢行、也不能报 database is locked —— 控制面的定时任务与
// 测试都可能并发写。
func TestRecordTrafficSampleConcurrent(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")

	const (
		goroutines = 8
		perRoutine = 50
	)
	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perRoutine; i++ {
				at := base.Add(time.Duration(g*perRoutine+i) * time.Second)
				if err := s.RecordTrafficSample(model.TrafficSample{
					Username: "alice", NodeID: n.ID, SampledAt: at,
					Uplink: int64(i), Downlink: int64(i),
				}); err != nil {
					errCh <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("并发 RecordTrafficSample 失败: %v", err)
	}

	rows, err := s.ListTrafficSamples("alice", n.ID, base.Add(-time.Hour), base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ListTrafficSamples: %v", err)
	}
	if want := goroutines * perRoutine; len(rows) != want {
		t.Fatalf("并发追加丢了行: got %d want %d", len(rows), want)
	}
}

// 节点删了就跟着删:节点都没了,它的采样也拼不出对账(与 inbounds / hosts 的
// 级联删除同一个做法)。
func TestTrafficSamplesFollowNodeDeletion(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")

	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: n.ID, SampledAt: base, Uplink: 1, Downlink: 1})

	if err := s.DeleteNode(n.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	rows, err := s.ListTrafficSamples("alice", n.ID, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListTrafficSamples: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("节点删掉后采样应当一起走,实际还剩 %d 行", len(rows))
	}
}
