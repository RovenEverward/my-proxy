package store

import (
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func newSourceForExternal(t *testing.T, s *Store) int64 {
	t.Helper()
	src := &model.SubscriptionSource{Name: "ext", URL: "https://example.test/sub", Enabled: true, IntervalSeconds: 60}
	if err := s.CreateSource(src); err != nil {
		t.Fatal(err)
	}
	return src.ID
}

func TestExternalMetadataBatchAndSync(t *testing.T) {
	s, err := Open(t.TempDir() + "/mpx.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srcID := newSourceForExternal(t, s)

	batch := []model.ExternalMetadata{
		{SourceID: srcID, SourceKey: "k1", Region: "JP", City: "东京", Letter: "A", Kind: "auto"},
		{SourceID: srcID, SourceKey: "k2", Region: "JP", City: "东京", Letter: "B", Kind: "auto"},
	}
	if err := s.SaveExternalMetadataBatch(batch); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListExternalMetadata(srcID)
	if err != nil || len(got) != 2 {
		t.Fatalf("list: %v %+v", err, got)
	}
	if got[extMetaKey(srcID, "k1")].Letter != "A" {
		t.Fatalf("k1: %+v", got)
	}
	// 全量列表键格式一致。
	all, _ := s.ListExternalMetadata(0)
	if len(all) != 2 {
		t.Fatalf("all: %d", len(all))
	}

	// 同步:快照只剩 k2,k1 被清理;新增 k3。
	err = s.SyncExternalMetadata(srcID, []string{"k2", "k3"}, []model.ExternalMetadata{
		{SourceID: srcID, SourceKey: "k3", Region: "US", Letter: "A", Kind: "auto"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListExternalMetadata(srcID)
	if len(got) != 2 {
		t.Fatalf("sync: %+v", got)
	}
	if _, ok := got[extMetaKey(srcID, "k1")]; ok {
		t.Fatal("k1 应被清理")
	}
	if got[extMetaKey(srcID, "k3")].Letter != "A" {
		t.Fatalf("k3: %+v", got)
	}

	// 引用不存在的来源要 ErrNotFound。
	err = s.SaveExternalMetadataBatch([]model.ExternalMetadata{{SourceID: 999, SourceKey: "x", Region: "US", Kind: "auto"}})
	if err != ErrNotFound {
		t.Fatalf("ghost source: %v", err)
	}
}
