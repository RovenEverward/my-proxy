package store

// migrateProbes 建 UC-12 的探测点与触达存档表。UC-30/UC-32 退役后不再读写,
// 表保留不删(老库升级不丢数据)。
func (s *Store) migrateProbes() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS probe_points (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 name TEXT NOT NULL,
 token_hash TEXT NOT NULL DEFAULT '',
 join_token_hash TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 last_seen_at INTEGER,
 last_ip TEXT NOT NULL DEFAULT '')`)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS reach_subject_checks (
 subject_key TEXT PRIMARY KEY,
 checked_at INTEGER NOT NULL,
 verdict TEXT NOT NULL,
 report TEXT NOT NULL)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS reach_source_checks (
 subject_key TEXT NOT NULL,
 source_id TEXT NOT NULL,
 scope TEXT NOT NULL,
 checked_at INTEGER NOT NULL,
 summary TEXT NOT NULL,
 results TEXT,
 PRIMARY KEY(subject_key, source_id))`); err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO reach_subject_checks(subject_key,checked_at,verdict,report)
 SELECT 'node:' || node_id, checked_at, verdict, report FROM reach_checks`)
	return err
}
