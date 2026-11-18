package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

const qualityReportLimit = 64 * 1024

func (s *Store) migrateQuality() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS node_quality (
 node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 checked_at INTEGER NOT NULL,
 verdict TEXT NOT NULL,
 score INTEGER NOT NULL,
 report TEXT NOT NULL,
 PRIMARY KEY(node_id))`)
	return err
}

// SaveNodeQuality 覆盖一台节点的质量存档;别的节点的记录不受影响。
func (s *Store) SaveNodeQuality(q model.NodeQuality) error {
	if q.NodeID <= 0 || q.CheckedAt.IsZero() || !model.NodeQualityVerdicts[q.Verdict] {
		return fmt.Errorf("质量记录缺少节点、时间或判定")
	}
	if q.Score < 0 || q.Score > 100 || len(q.Report) == 0 || len(q.Report) > qualityReportLimit || !json.Valid(q.Report) {
		return fmt.Errorf("质量报告无效或超过 64 KiB")
	}
	_, err := s.db.Exec(`INSERT INTO node_quality(node_id,checked_at,verdict,score,report) VALUES(?,?,?,?,?)
 ON CONFLICT(node_id) DO UPDATE SET checked_at=excluded.checked_at,verdict=excluded.verdict,score=excluded.score,report=excluded.report`,
		q.NodeID, q.CheckedAt.Unix(), q.Verdict, q.Score, string(q.Report))
	return err
}

// ListNodeQuality 返回各节点最近一次质量记录,nodeID=0 表示全部。
func (s *Store) ListNodeQuality(nodeID int64) ([]model.NodeQuality, error) {
	query := `SELECT node_id,checked_at,verdict,score,report FROM node_quality`
	args := []any{}
	if nodeID > 0 {
		query += ` WHERE node_id=?`
		args = append(args, nodeID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NodeQuality
	for rows.Next() {
		var q model.NodeQuality
		var at int64
		var raw string
		if err := rows.Scan(&q.NodeID, &at, &q.Verdict, &q.Score, &raw); err != nil {
			return nil, err
		}
		q.CheckedAt = time.Unix(at, 0).UTC()
		q.Report = json.RawMessage(raw)
		out = append(out, q)
	}
	return out, rows.Err()
}

// LatestNodeQualityAt 是最近一次探测发生的时刻;没有记录时 ok=false。
func (s *Store) LatestNodeQualityAt() (time.Time, bool, error) {
	var at int64
	err := s.db.QueryRow(`SELECT MAX(checked_at) FROM node_quality`).Scan(&at)
	if err != nil || at == 0 {
		return time.Time{}, false, err
	}
	return time.Unix(at, 0).UTC(), true, nil
}
