package store

// migrateDiagnostics 建旧版综合体检存档表。UC-32 退役内置重型体检后不再读写,
// 表保留不删(老库升级不丢数据)。
func (s *Store) migrateDiagnostics() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS diagnostic_reports (
 node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 source TEXT NOT NULL CHECK(source IN ('node','control')),
 measured_address TEXT NOT NULL DEFAULT '',
 checked_at INTEGER NOT NULL,
 report TEXT NOT NULL,
 PRIMARY KEY(node_id,source))`)
	return err
}
