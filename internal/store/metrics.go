package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/reach"
)

// UC-20 的两张指标表:
//
// reach_source_samples 是(对象 × 来源)每次活性探测的滚动样本,
// 稳定度从这里聚合;reach_source_checks 仍只存最新一行供判定,不重复。
//
// bandwidth_samples 是手动带宽测量的一次次存档,展示取最近一次 ok。

// 保留口径:样本按(对象 × 来源)留近 sourceSampleKeep 轮,超龄按
// sourceSampleMaxAge 天裁;带宽样本每对象留近 bandwidthSampleKeep 条。
const (
	sourceSampleKeep        = 50
	sourceSampleMaxAge      = 7 * 24 * time.Hour
	bandwidthSampleKeep     = 30
	stabilityMinSampleValue = 0
)

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

// AppendSourceSample 把一次探测摘要存成稳定度样本并顺手裁剪同(对象,来源)
// 的过期与超额行。由 SaveSourceCheck 在同一条数据通路里调用。
func (s *Store) AppendSourceSample(subjectKey model.ProbeSubjectKey, sourceID, scope string, checkedAt time.Time, sum reach.SourceSummary) error {
	if subjectKey == "" || sourceID == "" || checkedAt.IsZero() {
		return fmt.Errorf("稳定度样本缺少对象、来源或时间")
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO reach_source_samples
 (subject_key, source_id, scope, checked_at, probed, reachable, failed, latency_ms)
 VALUES(?,?,?,?,?,?,?,?)`,
		string(subjectKey), sourceID, scope, checkedAt.Unix(),
		boolToInt(sum.Probed), boolToInt(sum.Reachable), boolToInt(sum.Failed), int64(sum.LatencyMs))
	if err != nil {
		return fmt.Errorf("写稳定度样本: %w", err)
	}
	// 裁剪:同(对象,来源)只留最近 sourceSampleKeep 轮,且不留超龄行。
	// 一轮一次探测,内联裁剪就够了,不需要单独的周期任务。
	if _, err := s.db.Exec(`DELETE FROM reach_source_samples
 WHERE subject_key=? AND source_id=? AND checked_at NOT IN (
   SELECT checked_at FROM reach_source_samples
   WHERE subject_key=? AND source_id=? ORDER BY checked_at DESC LIMIT ?)`,
		string(subjectKey), sourceID, string(subjectKey), sourceID, sourceSampleKeep); err != nil {
		return fmt.Errorf("裁剪稳定度样本: %w", err)
	}
	_, err = s.db.Exec(`DELETE FROM reach_source_samples WHERE checked_at<?`,
		time.Now().Add(-sourceSampleMaxAge).Unix())
	if err != nil {
		return fmt.Errorf("清理过期稳定度样本: %w", err)
	}
	return nil
}

// ListSourceSamples 返回某对象(或全部)的样本,可按 scope 过滤(空=全部)。
func (s *Store) ListSourceSamples(subject model.ProbeSubjectKey, scope string, since time.Time) ([]model.ReachSourceSample, error) {
	query := `SELECT subject_key, source_id, scope, checked_at, probed, reachable, failed, latency_ms
 FROM reach_source_samples WHERE checked_at>=?`
	args := []any{since.Unix()}
	if subject != "" {
		query += ` AND subject_key=?`
		args = append(args, string(subject))
	}
	if scope != "" {
		query += ` AND scope=?`
		args = append(args, scope)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ReachSourceSample
	for rows.Next() {
		var c model.ReachSourceSample
		var at int64
		var key string
		var probed, reachable, failed int
		if err := rows.Scan(&key, &c.SourceID, &c.Scope, &at, &probed, &reachable, &failed, &c.LatencyMs); err != nil {
			return nil, err
		}
		c.SubjectKey = model.ProbeSubjectKey(key)
		c.CheckedAt = time.Unix(at, 0).UTC()
		c.Probed, c.Reachable, c.Failed = probed != 0, reachable != 0, failed != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveBandwidthSample 存一条带宽测量记录并按对象裁剪历史。
func (s *Store) SaveBandwidthSample(b model.BandwidthSample) error {
	if b.SubjectKey == "" || b.SourceID == "" || b.CheckedAt.IsZero() {
		return fmt.Errorf("带宽样本缺少对象、来源或时间")
	}
	if !model.BandwidthStatuses[b.Status] {
		return fmt.Errorf("带宽样本状态 %q 无效", b.Status)
	}
	_, err := s.db.Exec(`INSERT INTO bandwidth_samples
 (subject_key, source_id, checked_at, status, bytes_per_sec, duration_ms, bytes, test_url, error)
 VALUES(?,?,?,?,?,?,?,?,?)`,
		string(b.SubjectKey), b.SourceID, b.CheckedAt.Unix(), b.Status,
		b.BytesPerSec, b.DurationMs, b.Bytes, b.TestURL, b.Error)
	if err != nil {
		return fmt.Errorf("写带宽样本: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM bandwidth_samples WHERE subject_key=? AND id NOT IN (
   SELECT id FROM bandwidth_samples WHERE subject_key=? ORDER BY checked_at DESC, id DESC LIMIT ?)`,
		string(b.SubjectKey), string(b.SubjectKey), bandwidthSampleKeep); err != nil {
		return fmt.Errorf("裁剪带宽样本: %w", err)
	}
	return nil
}

// ListBandwidthSamples 按检测时间倒序返回某对象的带宽样本。
func (s *Store) ListBandwidthSamples(subject model.ProbeSubjectKey, limit int) ([]model.BandwidthSample, error) {
	if limit <= 0 {
		limit = bandwidthSampleKeep
	}
	rows, err := s.db.Query(`SELECT subject_key, source_id, checked_at, status,
 bytes_per_sec, duration_ms, bytes, test_url, error
 FROM bandwidth_samples WHERE subject_key=? ORDER BY checked_at DESC, id DESC LIMIT ?`,
		string(subject), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBandwidthSamples(rows)
}

// LatestBandwidthViews 返回每对象的带宽展示视图:最近一次状态 +
// 最近一次 ok 的实测值。失败不清旧值是刻意的产品口径(UC-20):
// 带宽测出来后就一直用,复测失败只是说明这次没测成。
func (s *Store) LatestBandwidthViews() (map[string]model.BandwidthView, error) {
	// 两条子查询:每对象最新一行(任意状态)+ 最新一行 ok。
	// 同秒多次回写按 id 定胜负,不让并列的行互相覆盖。
	rows, err := s.db.Query(`SELECT b.subject_key, b.source_id, b.checked_at, b.status,
 b.bytes_per_sec, b.error, 0 AS kind
 FROM bandwidth_samples b
 WHERE b.id = (SELECT id FROM bandwidth_samples x WHERE x.subject_key=b.subject_key
   ORDER BY x.checked_at DESC, x.id DESC LIMIT 1)
 UNION ALL
 SELECT b.subject_key, b.source_id, b.checked_at, b.status,
 b.bytes_per_sec, b.error, 1 AS kind
 FROM bandwidth_samples b
 WHERE b.status='ok' AND b.id = (SELECT id FROM bandwidth_samples x
   WHERE x.subject_key=b.subject_key AND x.status='ok'
   ORDER BY x.checked_at DESC, x.id DESC LIMIT 1)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.BandwidthView{}
	for rows.Next() {
		var key, srcID, status, serr string
		var at, bps, kind int64
		if err := rows.Scan(&key, &srcID, &at, &status, &bps, &serr, &kind); err != nil {
			return nil, err
		}
		v := out[key]
		t := time.Unix(at, 0).UTC()
		if kind == 0 {
			v.Status = status
			v.SourceID = srcID
			v.Error = serr
		} else {
			v.BytesPerSec = bps
			v.MeasuredAt = &t
			if v.Status == "" {
				v.Status = "ok"
			}
			if srcID != "" {
				v.SourceID = srcID
			}
		}
		if v.Status == "" {
			v.Status = "untested"
		}
		out[key] = v
	}
	return out, rows.Err()
}

func scanBandwidthSamples(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]model.BandwidthSample, error) {
	var out []model.BandwidthSample
	for rows.Next() {
		var b model.BandwidthSample
		var at int64
		var key string
		if err := rows.Scan(&key, &b.SourceID, &at, &b.Status,
			&b.BytesPerSec, &b.DurationMs, &b.Bytes, &b.TestURL, &b.Error); err != nil {
			return nil, err
		}
		b.SubjectKey = model.ProbeSubjectKey(key)
		b.CheckedAt = time.Unix(at, 0).UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

// deleteSubjectMetrics 在事务里删除一个对象(或前缀)的全部指标样本;
// 由 DeleteSubjectResults / DeleteSubjectResultsByPrefix / DeleteProbePoint 复用。
func deleteSubjectMetrics(tx *sql.Tx, where string, arg any) error {
	for _, q := range []string{
		`DELETE FROM reach_source_samples ` + where,
		`DELETE FROM bandwidth_samples ` + where,
	} {
		if _, err := tx.Exec(q, arg); err != nil {
			return err
		}
	}
	return nil
}

// PruneMetricsNotIn 删除名册之外对象的指标样本(UC-20):对象消失后不再有新
// 探测进来,靠 reachTargets 名册比对清档。不动 reach_source_checks /
// reach_subject_checks —— 那是判定与剔除的历史,另行治理。
func (s *Store) PruneMetricsNotIn(roster map[string]bool) error {
	rows, err := s.db.Query(`SELECT DISTINCT subject_key FROM reach_source_samples
 UNION SELECT DISTINCT subject_key FROM bandwidth_samples`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var orphans []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return err
		}
		if !roster[k] {
			orphans = append(orphans, k)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range orphans {
		for _, q := range []string{
			`DELETE FROM reach_source_samples WHERE subject_key=?`,
			`DELETE FROM bandwidth_samples WHERE subject_key=?`,
		} {
			if _, err := s.db.Exec(q, k); err != nil {
				return err
			}
		}
	}
	return nil
}
