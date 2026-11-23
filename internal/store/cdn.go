package store

import (
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// migrateCDN 建优选池表。池是「最近确认可用、或仍在观察期」的 IP 集合:
// 每轮优选由探测点复检全体成员 + 抽样新 IP,结论回传后刷新 —— 通过
// 的清零 miss,未通过(或旧探测端没给通过结论)的 miss+1,连续 miss
// 达到上限才出池 —— 防一次抖动把可用 IP 抖掉。
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
	// UC-31:老库补丢包/吞吐两列。
	if err := s.addColumnIfMissing("cdn_pool", "loss",
		`ALTER TABLE cdn_pool ADD COLUMN loss REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	return s.addColumnIfMissing("cdn_pool", "speed_mbps",
		`ALTER TABLE cdn_pool ADD COLUMN speed_mbps REAL NOT NULL DEFAULT 0`)
}

// SaveCDNResults 是一轮优选落库:results 里 edge=true 的是当轮拿到边缘
// 身份确认的 IP(upsert,miss 清零);池内成员不在通过名单的 miss+1,
// 超过 maxMisses 的删除,池整体裁剪到 maxPool(留 miss 少、延迟低的)。
// miss 只由「本轮名单」决定:新探测端会把 recheck 成员无论成败都报回
// (UC-24);旧探测端只报通过项时,缺席即 miss,沿用旧语义兜底。
// 返回当轮后的完整池名单。
func (s *Store) SaveCDNResults(results []model.CDNResult, now time.Time, maxMisses, maxPool int) ([]model.CDNIP, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ts := now.Unix()
	var winners []string
	for _, w := range results {
		if !w.Edge {
			continue
		}
		winners = append(winners, w.IP)
		if _, err := tx.Exec(`INSERT INTO cdn_pool (ip, latency_ms, loss, speed_mbps, colo, first_seen, last_seen, misses)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0)
		 ON CONFLICT(ip) DO UPDATE SET latency_ms=excluded.latency_ms, loss=excluded.loss, speed_mbps=excluded.speed_mbps, colo=excluded.colo, last_seen=excluded.last_seen, misses=0`,
			w.IP, w.LatencyMs, w.Loss, w.SpeedMbps, w.Colo, ts, ts); err != nil {
			return nil, err
		}
	}
	// 不在当轮通过名单的 miss+1。
	if _, err := tx.Exec(`UPDATE cdn_pool SET misses = misses + 1
	 WHERE ip NOT IN (SELECT value FROM json_each(?))`, jsonIPs(winners)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM cdn_pool WHERE misses > ?`, maxMisses); err != nil {
		return nil, err
	}
	// 软上限:复检成本随池线性涨,只留 miss 少、延迟低的。
	if maxPool > 0 {
		if _, err := tx.Exec(`DELETE FROM cdn_pool WHERE ip NOT IN
		 (SELECT ip FROM cdn_pool ORDER BY misses, latency_ms LIMIT ?)`, maxPool); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListCDNPool()
}

func jsonIPs(ips []string) string {
	out := "["
	for i, ip := range ips {
		if i > 0 {
			out += ","
		}
		out += `"` + ip + `"`
	}
	return out + "]"
}

// ListCDNPool 返回全池,按吞吐降序再按延迟升序 —— UC-31 起选优以
// 实传速度为主键;没测过速的(speed=0)按延迟排,自然落在测过速的
// 后面 —— 测速是更强的资格信号。
func (s *Store) ListCDNPool() ([]model.CDNIP, error) {
	rows, err := s.db.Query(`SELECT ip, latency_ms, loss, speed_mbps, colo, first_seen, last_seen, misses FROM cdn_pool ORDER BY speed_mbps DESC, latency_ms`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CDNIP
	for rows.Next() {
		var e model.CDNIP
		var first, last int64
		if err := rows.Scan(&e.IP, &e.LatencyMs, &e.Loss, &e.SpeedMbps, &e.Colo, &first, &last, &e.Misses); err != nil {
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

// MarkCDNRun 记「这一轮优选名单已经发出去」的时刻,用于 1 小时节流 ——
// 探测点每分钟 tick 都会来领,开关之外这道闸决定实际频率。UC-24 起在
// 发名单时记而不是收结果时记:探测端不回存的空轮也占掉本轮配额。
func (s *Store) MarkCDNRun(now time.Time) error {
	_, err := s.db.Exec(`UPDATE settings SET cdn_last_run = ? WHERE id = 1`, now.Unix())
	return err
}
