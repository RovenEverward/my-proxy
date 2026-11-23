package store

// reachReportLimit 与 migrateReach 是旧版触达存档的遗产:UC-30 退役后
// 不再读写,表保留不删(老库升级不丢数据)。
const reachReportLimit = 256 * 1024

func (s *Store) migrateReach() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS reach_checks (
 node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 checked_at INTEGER NOT NULL,
 verdict TEXT NOT NULL,
 report TEXT NOT NULL,
 PRIMARY KEY(node_id))`)
	return err
}
