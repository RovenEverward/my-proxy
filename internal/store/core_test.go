package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestCoreMigrationAndMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n := newTestNode(t, s, "legacy")
	in := newTestInbound(t, s, n.ID, "old")
	recordSample(t, s, model.TrafficSample{Username: "alice", NodeID: n.ID, Uplink: 17})
	for _, ddl := range []string{`DROP INDEX idx_traffic_samples_core`, `ALTER TABLE traffic_samples DROP COLUMN core`, `ALTER TABLE traffic_samples DROP COLUMN generation`, `ALTER TABLE inbounds DROP COLUMN congestion_control`, `ALTER TABLE node_status DROP COLUMN cores`, `DROP TABLE core_deployments`} {
		if _, err := s.db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	latest, err := s.LatestCoreTrafficSamples(n.ID, model.CoreXray)
	if err != nil {
		t.Fatal(err)
	}
	if latest["alice"].Core != model.CoreXray || latest["alice"].Uplink != 17 {
		t.Fatalf("legacy counters: %+v", latest)
	}
	other, err := s.LatestCoreTrafficSamples(n.ID, model.CoreSingBox)
	if err != nil || len(other) != 0 {
		t.Fatalf("legacy counter leaked: %+v %v", other, err)
	}
	stored, err := s.GetInbound(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.CongestionControl = "bbr"
	if err := s.UpdateInbound(stored); err != nil {
		t.Fatal(err)
	}
	stored, err = s.GetInbound(in.ID)
	if err != nil || stored.CongestionControl != "bbr" {
		t.Fatalf("congestion persistence: %+v %v", stored, err)
	}
	d, err := s.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if d.APIPort != 10086 || d.ConfigPath != "/etc/mpx/sing-box/config.json" || d.ServiceName != "mpx-sing-box" {
		t.Fatalf("defaults: %+v", d)
	}
	d.APIPort = 11086
	if err := s.SetCoreDeployment(*d); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SetCoreDeployed(n.ID, model.CoreSingBox, "sb-hash", now); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if got.APIPort != 11086 || got.DeployedHash != "sb-hash" || got.DeployedAt == nil || !got.DeployedAt.Equal(now) {
		t.Fatalf("metadata: %+v", got)
	}
	xr, err := s.GetCoreDeployment(n.ID, model.CoreXray)
	if err != nil {
		t.Fatal(err)
	}
	if xr.APIPort != n.APIPort || xr.DeployedHash != "" {
		t.Fatalf("metadata leaked: %+v", xr)
	}
	status := model.NodeStatus{NodeID: n.ID, Cores: []model.CoreStatus{{Core: model.CoreSingBox, Active: true, Version: "1.12", ObservedHash: "obs", StatsAvailable: true}}}
	if err := s.UpsertNodeStatus(status); err != nil {
		t.Fatal(err)
	}
	st, err := s.GetNodeStatus(n.ID)
	if err != nil || len(st.Cores) != 1 || st.Cores[0] != status.Cores[0] {
		t.Fatalf("core status: %+v %v", st, err)
	}
	if err := s.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM core_deployments`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deployment cascade: %d %v", count, err)
	}
}

func TestCoreAtomicTrafficIsolationAndReset(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	jp := newTestNode(t, s, "jp")
	newTestUser(t, s, "alice")
	now := time.Now().UTC().Truncate(time.Second)
	apply := func(nodeID int64, core model.Core, up, down int64) {
		t.Helper()
		if err := s.ApplyCoreTrafficSample(model.TrafficSample{Username: "alice", NodeID: nodeID, SampledAt: now, Uplink: up, Downlink: down}, core); err != nil {
			t.Fatal(err)
		}
	}
	apply(n.ID, model.CoreXray, 100, 200)
	apply(n.ID, model.CoreSingBox, 10, 20)
	apply(n.ID, model.CoreXray, 110, 230)
	apply(jp.ID, model.CoreSingBox, 5, 5)
	apply(n.ID, model.CoreSingBox, 2, 25) // sing-box rollback: counts belong to a fresh process
	apply(n.ID, model.CoreSingBox, 3, 26)
	check := func(want int64) {
		t.Helper()
		u, err := s.GetUser("alice")
		if err != nil || u.UsedTraffic != want || u.LifetimeUsedTraffic != want {
			t.Fatalf("used want %d: %+v %v", want, u, err)
		}
	}
	check(409)
	if err := s.ResetCoreTrafficBaselines(n.ID, model.CoreSingBox, now); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestCoreTrafficSamples(n.ID, model.CoreSingBox)
	if err != nil || latest["alice"].Uplink != 0 || latest["alice"].Downlink != 0 {
		t.Fatalf("zero baseline: %+v %v", latest, err)
	}
	xr, err := s.LatestTrafficSamples(n.ID)
	if err != nil || xr["alice"].Uplink != 110 {
		t.Fatalf("reset leaked: %+v %v", xr, err)
	}
	apply(n.ID, model.CoreSingBox, 9, 11)
	check(429)
	history, err := s.ListTrafficSamples("alice", n.ID, now.Add(-time.Second), now.Add(time.Second))
	if err != nil || len(history) != 7 {
		t.Fatalf("combined history: %d %v", len(history), err)
	}
	filtered, err := s.ListCoreTrafficSamples("alice", n.ID, now.Add(-time.Second), now.Add(time.Second), model.CoreXray)
	if err != nil || len(filtered) != 2 {
		t.Fatalf("filtered history: %+v %v", filtered, err)
	}
	// Foreign-key failure occurs after user accounting: the transaction must undo it.
	if err := s.ApplyCoreTrafficSample(model.TrafficSample{Username: "alice", NodeID: 9999, Uplink: 20}, model.CoreXray); err == nil {
		t.Fatal("missing node should fail")
	}
	check(429)
	if err := s.ApplyCoreTrafficSample(model.TrafficSample{Username: "alice", NodeID: n.ID, Uplink: -1}, model.CoreXray); err == nil {
		t.Fatal("negative count should fail")
	}
	check(429)
	if err := s.ApplyCoreTrafficSample(model.TrafficSample{Username: "missing", NodeID: n.ID, Uplink: 1}, model.CoreXray); err == nil {
		t.Fatal("missing user should fail")
	}
}

func TestCoreAtomicTrafficGeneration(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	newTestUser(t, s, "alice")
	for _, sample := range []model.TrafficSample{
		{Generation: "first", Uplink: 100, Downlink: 200},
		{Generation: "first", Uplink: 110, Downlink: 220},
		{Generation: "second", Uplink: 500, Downlink: 600},
		{Generation: "second", Uplink: 510, Downlink: 620},
	} {
		sample.Username = "alice"
		sample.NodeID = n.ID
		if err := s.ApplyCoreTrafficSample(sample, model.CoreSingBox); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.GetUser("alice")
	if err != nil || u.UsedTraffic != 1460 {
		t.Fatalf("generation accounting: %+v %v", u, err)
	}
	latest, err := s.LatestCoreTrafficSamples(n.ID, model.CoreSingBox)
	if err != nil || latest["alice"].Generation != "second" {
		t.Fatalf("generation persistence: %+v %v", latest, err)
	}
}

func TestCoreAtomicTrafficConcurrentDuplicates(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	newTestUser(t, s, "alice")
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- s.ApplyCoreTrafficSample(model.TrafficSample{Username: "alice", NodeID: n.ID, Uplink: 100, Downlink: 200}, model.CoreSingBox)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.GetUser("alice")
	if err != nil || u.UsedTraffic != 300 || u.LifetimeUsedTraffic != 300 {
		t.Fatalf("duplicate accounting: %+v %v", u, err)
	}
}

// DeployedNodesForCore 只报"下发过基准"的节点:建了行但从没有过哈希的(比如
// 只改过监听端口)不算装过,空哈希也是。
func TestDeployedNodesForCore(t *testing.T) {
	s := newTestStore(t)
	withHash := newTestNode(t, s, "a")
	withPort := newTestNode(t, s, "b")
	none := newTestNode(t, s, "c")

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SetCoreDeployed(withHash.ID, model.CoreSingBox, "sb-hash", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCoreDeployment(model.CoreDeployment{
		NodeID: withPort.ID, Core: model.CoreSingBox, APIPort: 11086,
	}); err != nil {
		t.Fatal(err)
	}
	_ = none

	got, err := s.DeployedNodesForCore(model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if !got[withHash.ID] || got[withPort.ID] || got[none.ID] {
		t.Fatalf("下发过基准的节点集合不对:%+v", got)
	}
	if len(got) != 1 {
		t.Fatalf("只有一台下发过,实际 %d:%+v", len(got), got)
	}

	// xray 的基准在 nodes 表上,这个函数只回答第二核心。
	xray, err := s.DeployedNodesForCore(model.CoreXray)
	if err != nil {
		t.Fatal(err)
	}
	if len(xray) != 0 {
		t.Fatalf("xray 不该有独立的下发记录:%+v", xray)
	}
}
