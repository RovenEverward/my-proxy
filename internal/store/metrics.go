package store

// migrateMetrics 建 UC-20 的两张指标表(reach_source_samples 活性探测滚动
// 样本、bandwidth_samples 带宽测量存档)。UC-30 退役后不再读写,表保留
// 不删(老库升级不丢数据)。
func (s *Store) migrateMetrics() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS reach_source_samples (
 subject_key TEXT NOT NULL,
 source_id TEXT NOT NULL,
 scope TEXT NOT NULL,
 checked_at INTEGER NOT NULL,
 probed INTEGER NOT NULL DEFAULT 0,
 reachable INTEGER NOT NULL DEFAULT 0,
 failed INTEGER NOT NULL DEFAULT 0,
 latency_ms INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(subject_key, source_id, checked_at))`); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS bandwidth_samples (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 subject_key TEXT NOT NULL,
 source_id TEXT NOT NULL,
 checked_at INTEGER NOT NULL,
 status TEXT NOT NULL,
 bytes_per_sec INTEGER NOT NULL DEFAULT 0,
 duration_ms INTEGER NOT NULL DEFAULT 0,
 bytes INTEGER NOT NULL DEFAULT 0,
 test_url TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '')`)
	return err
}
