package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

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

// SaveReachChecks 用一次检查的结果覆盖各节点的存档:同一次检查里没出现的节点
// 保留旧记录(单台检查不该清掉别人的结果)。
func (s *Store) SaveReachChecks(checks []model.ReachCheck) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO reach_checks(node_id,checked_at,verdict,report) VALUES(?,?,?,?)
 ON CONFLICT(node_id) DO UPDATE SET checked_at=excluded.checked_at,verdict=excluded.verdict,report=excluded.report`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range checks {
		if c.NodeID <= 0 || c.CheckedAt.IsZero() || !model.ReachVerdicts[c.Verdict] {
			return fmt.Errorf("触达检查结果缺少节点、时间或判定")
		}
		if len(c.Report) > reachReportLimit || !json.Valid(c.Report) {
			return fmt.Errorf("触达检查报告无效或超过 256 KiB")
		}
		if _, err := stmt.Exec(c.NodeID, c.CheckedAt.Unix(), c.Verdict, string(c.Report)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestReachCheckAt 是最近一次检查发生的时刻(所有存档里最大的 checked_at)。
func (s *Store) LatestReachCheckAt() (time.Time, bool, error) {
	var at int64
	err := s.db.QueryRow(`SELECT MAX(checked_at) FROM reach_checks`).Scan(&at)
	if err != nil || at == 0 {
		return time.Time{}, false, err
	}
	return time.Unix(at, 0).UTC(), true, nil
}

// ListReachChecks 返回各节点最近一次检查存档,nodeID=0 表示全部。
func (s *Store) ListReachChecks(nodeID int64) ([]model.ReachCheck, error) {
	query := `SELECT node_id,checked_at,verdict,report FROM reach_checks`
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
	var out []model.ReachCheck
	for rows.Next() {
		var c model.ReachCheck
		var at int64
		var raw string
		if err := rows.Scan(&c.NodeID, &at, &c.Verdict, &raw); err != nil {
			return nil, err
		}
		c.CheckedAt = time.Unix(at, 0).UTC()
		c.Report = json.RawMessage(raw)
		out = append(out, c)
	}
	return out, rows.Err()
}
