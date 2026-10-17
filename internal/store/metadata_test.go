package store

import (
	"path/filepath"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestLocationMetadataPersistsReopensAndCascades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "purpose.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n := newTestNode(t, s, "one")
	in := newTestInbound(t, s, n.ID, "one")
	h := &model.Host{InboundID: in.ID, Remark: "One"}
	if err = s.CreateHost(h); err != nil {
		t.Fatal(err)
	}
	m := model.LocationMetadata{Region: "US", City: "洛杉矶", Letter: "A"}
	for kind, id := range map[string]int64{"nodes": n.ID, "hosts": h.ID} {
		if err = s.SaveLocationMetadata(kind, id, m); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.GetLocationMetadata("nodes", n.ID); err != nil || got.Region != "US" {
		t.Fatalf("%+v %v", got, err)
	}
	if err = s.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"nodes", "hosts"} {
		items, e := s.ListLocationMetadata(kind)
		if e != nil || len(items) != 0 {
			t.Fatalf("cascade %s %v %v", kind, items, e)
		}
	}
}

// newOldPurposeDB 建一个带 purposes 表的老库,并把它置成给定分流模式。
func newOldPurposeDB(t *testing.T, mode model.RoutingMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS purposes (slug TEXT PRIMARY KEY, config TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO purposes(slug, config) VALUES('media', '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRoutingState(&RoutingState{Mode: mode}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// rules 模式的老库打开后,遗留的 purposes 表被删掉。
func TestOpenDropsPurposesInRulesMode(t *testing.T) {
	path := newOldPurposeDB(t, model.RoutingRules)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if has, err := s.tableExists("purposes"); err != nil || has {
		t.Fatalf("rules 模式应删表: has=%v err=%v", has, err)
	}
}

// template 模式的老库还不能升级,表要留着等先迁移到 rules。
func TestOpenKeepsPurposesInTemplateMode(t *testing.T) {
	path := newOldPurposeDB(t, model.RoutingTemplate)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if has, err := s.tableExists("purposes"); err != nil || !has {
		t.Fatalf("template 模式应保留表: has=%v err=%v", has, err)
	}
}

// 新库不再建 purposes 表。
func TestOpenDoesNotCreatePurposes(t *testing.T) {
	s := newTestStore(t)
	if has, err := s.tableExists("purposes"); err != nil || has {
		t.Fatalf("新库不应建表: has=%v err=%v", has, err)
	}
}

// template 老库保留用途行,LegacyPurposeCount 报出来;rules 老库删表后为 0。
func TestLegacyPurposeCount(t *testing.T) {
	s, err := Open(newOldPurposeDB(t, model.RoutingTemplate))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.LegacyPurposeCount(); err != nil || n != 1 {
		t.Fatalf("template: %d %v", n, err)
	}
	s.Close()
	s, err = Open(newOldPurposeDB(t, model.RoutingRules))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.LegacyPurposeCount(); err != nil || n != 0 {
		t.Fatalf("rules: %d %v", n, err)
	}
}
