package store

import (
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// migrateCDN 建优选池表。池是「当前认为是好 IP」的集合:每次优选轮
// 由探测点上报结果刷新;不在当轮名单的记 miss,连续 miss 达到上限
// 才出池 —— 防一次抖动把可用 IP 抖掉。
func (s *Store) migrateCDN() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS cdn_pool (
	 ip TEXT PRIMARY KEY,
	 latency_ms INTEGER NOT NULL,
	 colo TEXT NOT NULL DEFAULT '',
	 first_seen INTEGER NOT NULL,
	 last_seen INTEGER NOT NULL,
	 misses INTEGER NOT NULL DEFAULT 0)`)
	return err
}

// SaveCDNResults 是一轮优选落库:winners 是新拿到边缘身份确认的 IP
// (upsert,miss 清零);池里其余 IP misses+1,超过 maxMisses 的删除。
// 返回当轮后的完整池名单。
func (s *Store) SaveCDNResults(winners []model.CDNResult, now time.Time, maxMisses int) ([]model.CDNIP, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ts := now.Unix()
	for _, w := range winners {
		if _, err := tx.Exec(`INSERT INTO cdn_pool (ip, latency_ms, colo, first_seen, last_seen, misses)
		 VALUES (?, ?, ?, ?, ?, 0)
		 ON CONFLICT(ip) DO UPDATE SET latency_ms=excluded.latency_ms, colo=excluded.colo, last_seen=excluded.last_seen, misses=0`,
			w.IP, w.LatencyMs, w.Colo, ts, ts); err != nil {
			return nil, err
		}
	}
	// 不在当轮名单的 miss+1。
	if _, err := tx.Exec(`UPDATE cdn_pool SET misses = misses + 1
	 WHERE ip NOT IN (SELECT value FROM json_each(?))`, jsonIPs(winners)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM cdn_pool WHERE misses > ?`, maxMisses); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListCDNPool()
}

func jsonIPs(rs []model.CDNResult) string {
	out := "["
	for i, r := range rs {
		if i > 0 {
			out += ","
		}
		out += `"` + r.IP + `"`
	}
	return out + "]"
}

// ListCDNPool 返回全池,按延迟升序 —— 池视图与 Host 生成都取这个序。
func (s *Store) ListCDNPool() ([]model.CDNIP, error) {
	rows, err := s.db.Query(`SELECT ip, latency_ms, colo, first_seen, last_seen, misses FROM cdn_pool ORDER BY latency_ms`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CDNIP
	for rows.Next() {
		var e model.CDNIP
		var first, last int64
		if err := rows.Scan(&e.IP, &e.LatencyMs, &e.Colo, &first, &last, &e.Misses); err != nil {
			return nil, err
		}
		e.FirstSeen, e.LastSeen = time.Unix(first, 0), time.Unix(last, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CDNLastRun 读上一轮优选完成时间;零值表示从没跑过(首轮立即可领任务)。
func (s *Store) CDNLastRun() (time.Time, error) {
	var ts int64
	if err := s.db.QueryRow(`SELECT cdn_last_run FROM settings WHERE id = 1`).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	return time.Unix(ts, 0), nil
}

// MarkCDNRun 记「这一轮优选名单已经发出去/结果已经收进来」的完成时刻,
// 用于 1 小时节流 —— 探测点每分钟 tick 都会来领,开关之外这道闸决定
// 实际频率。
func (s *Store) MarkCDNRun(now time.Time) error {
	_, err := s.db.Exec(`UPDATE settings SET cdn_last_run = ? WHERE id = 1`, now.Unix())
	return err
}
