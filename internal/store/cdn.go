package store

// migrateCDN 建 UC-21/UC-31 的优选池表。UC-32 退役内置 CDN 自动优选后
// 不再读写,表保留不删(老库升级不丢数据)。
func (s *Store) migrateCDN() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS cdn_pool (
	 ip TEXT PRIMARY KEY,
	 latency_ms INTEGER NOT NULL,
	 loss REAL NOT NULL DEFAULT 0,
	 speed_mbps REAL NOT NULL DEFAULT 0,
	 colo TEXT NOT NULL DEFAULT '',
	 first_seen INTEGER NOT NULL,
	 last_seen INTEGER NOT NULL,
	 misses INTEGER NOT NULL DEFAULT 0)`); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("cdn_pool", "loss",
		`ALTER TABLE cdn_pool ADD COLUMN loss REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	return s.addColumnIfMissing("cdn_pool", "speed_mbps",
		`ALTER TABLE cdn_pool ADD COLUMN speed_mbps REAL NOT NULL DEFAULT 0`)
}
