package source

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestGeoProbeBackfillsOrphans:JMS 换主机名的场景——新端点备注解析不出
// 地区、来源也没默认地区时,后台用 geoProbe 兜底查出地区自动建记录。
func TestGeoProbeBackfillsOrphans(t *testing.T) {
	st := testStore(t)
	m := NewManager(st, nil)
	m.SetGeoProbe(func(ctx context.Context, address string) (string, string) {
		if address == "c83s9.portablesubmarines.com" {
			return "US", "洛杉矶"
		}
		return "", ""
	})
	src := &model.SubscriptionSource{Name: "JMS", URL: "https://x", Enabled: true}
	if err := st.CreateSource(src); err != nil {
		t.Fatal(err)
	}
	eps := []render.Endpoint{
		{Address: "c83s9.portablesubmarines.com", Remark: "JMS-1490255@c83s9.portablesubmarines.com:443", SourceKey: "k-new"},
		{Address: "gone.example.test", Remark: "JMS-999@gone", SourceKey: "k-unreachable"},
	}
	snap, _ := json.Marshal(eps)
	m.syncExternalMetadata(src.ID, "", snap)

	deadline := time.Now().Add(3 * time.Second)
	var meta map[string]model.ExternalMetadata
	for time.Now().Before(deadline) {
		meta, _ = st.ListExternalMetadata(src.ID)
		if _, ok := meta["1\x00k-new"]; ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	m2, ok := meta["1\x00k-new"]
	if !ok {
		t.Fatalf("geo 兜底没建记录: %+v", meta)
	}
	if m2.Region != "US" || m2.City != "洛杉矶" || m2.Kind != "auto" || m2.Letter == "" {
		t.Fatalf("meta=%+v", m2)
	}
	if _, ok := meta["1\x00k-unreachable"]; ok {
		t.Fatalf("查不到的不该建记录")
	}
}

// TestGeoProbeSkipsExistingAndParsed:能解析出地区或已有记录的端点不碰探针。
func TestGeoProbeSkipsExistingAndParsed(t *testing.T) {
	st := testStore(t)
	m := NewManager(st, nil)
	calls := 0
	m.SetGeoProbe(func(ctx context.Context, address string) (string, string) {
		calls++
		return "US", "x"
	})
	src := &model.SubscriptionSource{Name: "x", URL: "https://x", Enabled: true}
	if err := st.CreateSource(src); err != nil {
		t.Fatal(err)
	}
	eps := []render.Endpoint{
		{Address: "jp.example", Remark: "🇯🇵 日本 01", SourceKey: "k-parsed"},
	}
	snap, _ := json.Marshal(eps)
	m.syncExternalMetadata(src.ID, "", snap)
	time.Sleep(150 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("解析出地区的端点不该碰探针,calls=%d", calls)
	}
}
