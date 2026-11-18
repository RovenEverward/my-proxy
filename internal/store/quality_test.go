package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func qualityFixture(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/mpx.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNodeQualityRoundTrip(t *testing.T) {
	st := qualityFixture(t)
	n := model.Node{Name: "n1", Address: "1.2.3.4"}
	if err := st.CreateNode(&n); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	q := model.NodeQuality{NodeID: n.ID, CheckedAt: now, Verdict: "ok", Score: 90, Report: json.RawMessage(`{"egress_ip":"1.2.3.4"}`)}
	if err := st.SaveNodeQuality(q); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListNodeQuality(n.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if list[0].Score != 90 || list[0].Verdict != "ok" || !list[0].CheckedAt.Equal(now) {
		t.Fatalf("roundtrip: %+v", list[0])
	}
	at, ok, err := st.LatestNodeQualityAt()
	if err != nil || !ok || !at.Equal(now) {
		t.Fatalf("latest: %v %v %v", at, ok, err)
	}
	// 覆盖:同一节点 upsert
	q2 := model.NodeQuality{NodeID: n.ID, CheckedAt: now.Add(time.Hour), Verdict: "untested", Score: 0, Report: json.RawMessage(`{"error":"no curl"}`)}
	if err := st.SaveNodeQuality(q2); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListNodeQuality(0)
	if len(list) != 1 || list[0].Verdict != "untested" {
		t.Fatalf("upsert: %+v", list)
	}
}

func TestNodeQualityValidation(t *testing.T) {
	st := qualityFixture(t)
	n := model.Node{Name: "n1", Address: "1.2.3.4"}
	if err := st.CreateNode(&n); err != nil {
		t.Fatal(err)
	}
	for _, q := range []model.NodeQuality{
		{NodeID: n.ID, CheckedAt: time.Now(), Verdict: "weird", Score: 1, Report: json.RawMessage(`{}`)},
		{NodeID: n.ID, CheckedAt: time.Now(), Verdict: "ok", Score: 101, Report: json.RawMessage(`{}`)},
		{NodeID: n.ID, CheckedAt: time.Now(), Verdict: "ok", Score: 90, Report: json.RawMessage(`not json`)},
		{NodeID: n.ID, Verdict: "ok", Score: 90, Report: json.RawMessage(`{}`)},
	} {
		if err := st.SaveNodeQuality(q); err == nil {
			t.Fatalf("应拒绝: %+v", q)
		}
	}
}

func TestSaveLocationMetadataBatch(t *testing.T) {
	st := qualityFixture(t)
	var ids []int64
	for _, name := range []string{"a", "b"} {
		n := model.Node{Name: name, Address: name + ".example.com"}
		if err := st.CreateNode(&n); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
	}
	batch := map[int64]model.LocationMetadata{
		ids[0]: {Region: "US", City: "洛杉矶", Letter: "A"},
		ids[1]: {Region: "US", City: "洛杉矶", Letter: "B"},
	}
	if err := st.SaveLocationMetadataBatch(batch); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListLocationMetadata("nodes")
	if err != nil {
		t.Fatal(err)
	}
	if got[ids[0]].Letter != "A" || got[ids[1]].Letter != "B" {
		t.Fatalf("batch: %+v", got)
	}
	// 不存在的节点整体回滚:第一批也不该写进去。
	bad := map[int64]model.LocationMetadata{
		ids[0]: {Region: "JP", Letter: "A"},
		999999: {Region: "JP", Letter: "B"},
	}
	if err := st.SaveLocationMetadataBatch(bad); err == nil {
		t.Fatal("应拒绝不存在的节点")
	}
	got, _ = st.ListLocationMetadata("nodes")
	if got[ids[0]].Region != "US" {
		t.Fatalf("回滚失败: %+v", got[ids[0]])
	}
}
