package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// UC-26:老库里的 devices / device_password 表要在 Open 的迁移里就地清掉;
// 新库干脆不该有这两张表。钉死「表不存在」比钉「数据没了」更硬:有人把表建
// 回来就是回归。
func TestLegacyDeviceTablesDropped(t *testing.T) {
	// 先手工造一个带老表的库,再交给 Open 迁移。
	dir := t.TempDir()
	db := filepath.Join(dir, "old.db")
	raw, err := sql.Open("sqlite", dsn(db))
	if err != nil {
		t.Fatalf("建老库: %v", err)
	}
	for _, ddl := range []string{
		`CREATE TABLE devices (id INTEGER PRIMARY KEY, name TEXT, token_hash TEXT)`,
		`CREATE TABLE device_password (id INTEGER PRIMARY KEY, hash TEXT)`,
	} {
		if _, err := raw.Exec(ddl); err != nil {
			t.Fatalf("建老表: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(db)
	if err != nil {
		t.Fatalf("迁移老库: %v", err)
	}
	defer s.Close()
	for _, tbl := range []string{"devices", "device_password"} {
		has, err := s.tableExists(tbl)
		if err != nil {
			t.Fatal(err)
		}
		if has {
			t.Errorf("老表 %s 应被删掉", tbl)
		}
	}
}
