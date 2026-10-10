package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

const trafficSampleColumns = `id, username, node_id, sampled_at, uplink, downlink, core, generation`

func scanTrafficSample(sc rowScanner) (*model.TrafficSample, error) {
	var sample model.TrafficSample
	var at int64
	if err := sc.Scan(&sample.ID, &sample.Username, &sample.NodeID, &at, &sample.Uplink, &sample.Downlink, &sample.Core, &sample.Generation); err != nil {
		return nil, err
	}
	sample.SampledAt = time.Unix(at, 0).UTC()
	return &sample, nil
}

func validateTrafficSample(sample model.TrafficSample, core model.Core) error {
	if !core.Valid() {
		return fmt.Errorf("不支持的核心: %s", core)
	}
	if sample.Uplink < 0 || sample.Downlink < 0 {
		return errors.New("累计流量不能为负数")
	}
	return nil
}

func (s *Store) RecordTrafficSample(sample model.TrafficSample) error {
	return s.RecordCoreTrafficSample(sample, model.CoreXray)
}

// RecordCoreTrafficSample 只追加核心累计计数,不更新用户用量。
func (s *Store) RecordCoreTrafficSample(sample model.TrafficSample, core model.Core) error {
	if err := validateTrafficSample(sample, core); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO traffic_samples(username,node_id,sampled_at,uplink,downlink,core,generation) VALUES (?,?,?,?,?,?,?)`, sample.Username, sample.NodeID, normalizeTime(sample.SampledAt).Unix(), sample.Uplink, sample.Downlink, core, sample.Generation)
	return err
}

func (s *Store) LatestTrafficSamples(nodeID int64) (map[string]model.TrafficSample, error) {
	return s.LatestCoreTrafficSamples(nodeID, model.CoreXray)
}

// LatestCoreTrafficSamples 按节点、核心、用户取最大 id,同一秒多次采样仍有顺序。
func (s *Store) LatestCoreTrafficSamples(nodeID int64, core model.Core) (map[string]model.TrafficSample, error) {
	if !core.Valid() {
		return nil, fmt.Errorf("不支持的核心: %s", core)
	}
	rows, err := s.db.Query(`SELECT MAX(id),username,node_id,sampled_at,uplink,downlink,core,generation FROM traffic_samples WHERE node_id=? AND core=? GROUP BY username`, nodeID, core)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]model.TrafficSample)
	for rows.Next() {
		sample, err := scanTrafficSample(rows)
		if err != nil {
			return nil, err
		}
		out[sample.Username] = *sample
	}
	return out, rows.Err()
}

// ApplyCoreTrafficSample 原子计算增量、更新周期与累计用量并保存新基准。
// sing-box 换进程或计数回退时计入新累计值;Xray 保留原有回退规则。
func (s *Store) ApplyCoreTrafficSample(sample model.TrafficSample, core model.Core) error {
	if err := validateTrafficSample(sample, core); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prev, err := scanTrafficSample(tx.QueryRow(`SELECT `+trafficSampleColumns+` FROM traffic_samples WHERE node_id=? AND core=? AND username=? ORDER BY id DESC LIMIT 1`, sample.NodeID, core, sample.Username))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	delta := sample.DeltaFrom(prev)
	if core == model.CoreSingBox && prev != nil && ((sample.Generation != "" && sample.Generation != prev.Generation) || sample.Uplink < prev.Uplink || sample.Downlink < prev.Downlink) {
		delta.Uplink, delta.Downlink = sample.Uplink, sample.Downlink
	}
	if delta.Uplink > math.MaxInt64-delta.Downlink {
		return errors.New("流量增量溢出")
	}
	total := delta.Uplink + delta.Downlink
	res, err := tx.Exec(`UPDATE users SET used_traffic=used_traffic+?,lifetime_used_traffic=lifetime_used_traffic+? WHERE username=? AND used_traffic<=? AND lifetime_used_traffic<=?`, total, total, sample.Username, math.MaxInt64-total, math.MaxInt64-total)
	if err != nil {
		return err
	}
	if err := affectedOrNotFound(res, "用户 "+sample.Username+" 或可记账的流量范围"); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO traffic_samples(username,node_id,sampled_at,uplink,downlink,core,generation) VALUES (?,?,?,?,?,?,?)`, sample.Username, sample.NodeID, normalizeTime(sample.SampledAt).Unix(), sample.Uplink, sample.Downlink, core, sample.Generation)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ResetCoreTrafficBaselines 在已知核心重启后记录每个历史用户名的零基准。
// 单条 INSERT SELECT 原子执行,不逐用户读写,不修改已经记账的用户用量。
func (s *Store) ResetCoreTrafficBaselines(nodeID int64, core model.Core, now time.Time) error {
	if !core.Valid() {
		return fmt.Errorf("不支持的核心: %s", core)
	}
	_, err := s.db.Exec(`INSERT INTO traffic_samples(username,node_id,sampled_at,uplink,downlink,core) SELECT username,?,?,0,0,? FROM traffic_samples WHERE node_id=? AND core=? GROUP BY username`, nodeID, normalizeTime(now).Unix(), core, nodeID, core)
	return err
}

// ListTrafficSamples 保留用户在两个核心上的完整历史,每行携带 Core。
func (s *Store) ListTrafficSamples(username string, nodeID int64, from, to time.Time) ([]model.TrafficSample, error) {
	return s.listTrafficSamples(username, nodeID, from, to, "")
}

func (s *Store) ListCoreTrafficSamples(username string, nodeID int64, from, to time.Time, core model.Core) ([]model.TrafficSample, error) {
	if !core.Valid() {
		return nil, fmt.Errorf("不支持的核心: %s", core)
	}
	return s.listTrafficSamples(username, nodeID, from, to, core)
}

func (s *Store) listTrafficSamples(username string, nodeID int64, from, to time.Time, core model.Core) ([]model.TrafficSample, error) {
	q := `SELECT ` + trafficSampleColumns + ` FROM traffic_samples WHERE username=? AND node_id=? AND sampled_at BETWEEN ? AND ?`
	args := []any{username, nodeID, normalizeTime(from).Unix(), normalizeTime(to).Unix()}
	if core != "" {
		q += ` AND core=?`
		args = append(args, core)
	}
	q += ` ORDER BY sampled_at,id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TrafficSample
	for rows.Next() {
		sample, err := scanTrafficSample(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sample)
	}
	return out, rows.Err()
}
