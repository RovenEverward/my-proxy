package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// ---- 探测点(UC-12) ----

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
	// reach_subject_checks:对象级触达存档(自建 node:<id> 与外部 s:<源>:<键>)。
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS reach_subject_checks (
 subject_key TEXT PRIMARY KEY,
 checked_at INTEGER NOT NULL,
 verdict TEXT NOT NULL,
 report TEXT NOT NULL)`); err != nil {
		return err
	}
	// reach_source_checks:(对象 × 来源)的原始探测结果,判定从这里重算。
	// summary 与 results 都是 internal/reach 线形的 JSON,存储层不解析。
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
	// 旧版 reach_checks 按 node_id 存档:搬进对象级表,身份统一为 node:<id>。
	// 旧表保留不删,备份与回读口径不受影响。
	_, err = s.db.Exec(`INSERT OR IGNORE INTO reach_subject_checks(subject_key,checked_at,verdict,report)
 SELECT 'node:' || node_id, checked_at, verdict, report FROM reach_checks`)
	return err
}

const probeCols = `id, name, token_hash, join_token_hash, created_at, last_seen_at, last_ip`

func scanProbePoint(row interface{ Scan(...any) error }) (*model.ProbePoint, error) {
	var p model.ProbePoint
	var created int64
	var seen sql.NullInt64
	if err := row.Scan(&p.ID, &p.Name, &p.TokenHash, &p.JoinTokenHash, &created, &seen, &p.LastIP); err != nil {
		return nil, err
	}
	p.CreatedAt = time.Unix(created, 0).UTC()
	if seen.Valid {
		t := time.Unix(seen.Int64, 0).UTC()
		p.LastSeenAt = &t
	}
	return &p, nil
}

// CreateProbePoint 建一条探测点记录,返回带一次性 join token 的对象。
func (s *Store) CreateProbePoint(name, joinTokenHash string) (*model.ProbePoint, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO probe_points(name,join_token_hash,created_at) VALUES(?,?,?)`,
		name, joinTokenHash, now.Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.ProbePoint{ID: id, Name: name, CreatedAt: now}, nil
}

// ProbeByJoinToken 按一次性接入凭据找探测点。
func (s *Store) ProbeByJoinToken(hash string) (*model.ProbePoint, error) {
	p, err := scanProbePoint(s.db.QueryRow(`SELECT `+probeCols+` FROM probe_points WHERE join_token_hash = ?`, hash))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return p, err
}

// ProbeByToken 按正式凭据找探测点。
func (s *Store) ProbeByToken(hash string) (*model.ProbePoint, error) {
	p, err := scanProbePoint(s.db.QueryRow(`SELECT `+probeCols+` FROM probe_points WHERE token_hash = ?`, hash))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return p, err
}

// ActivateProbe 把一次性接入换成正式凭据:写入 token_hash、清掉 join hash、
// 记首次上线。
func (s *Store) ActivateProbe(id int64, tokenHash, ip string) error {
	_, err := s.db.Exec(`UPDATE probe_points SET token_hash=?, join_token_hash='', last_seen_at=?, last_ip=? WHERE id=?`,
		tokenHash, time.Now().UTC().Unix(), ip, id)
	return err
}

// TouchProbe 记一次探测点在线(节流在调用方做)。
func (s *Store) TouchProbe(id int64, ip string) error {
	_, err := s.db.Exec(`UPDATE probe_points SET last_seen_at=?, last_ip=? WHERE id=?`,
		time.Now().UTC().Unix(), ip, id)
	return err
}

// ListProbePoints 返回全部探测点;Online 由最近上报时间现算。
func (s *Store) ListProbePoints() ([]*model.ProbePoint, error) {
	rows, err := s.db.Query(`SELECT ` + probeCols + ` FROM probe_points ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ProbePoint
	for rows.Next() {
		p, err := scanProbePoint(rows)
		if err != nil {
			return nil, err
		}
		p.Online = p.LastSeenAt != nil && time.Since(*p.LastSeenAt) < 3*time.Hour
		out = append(out, p)
	}
	return out, rows.Err()
}

// RenameProbePoint 改名。
func (s *Store) RenameProbePoint(id int64, name string) error {
	res, err := s.db.Exec(`UPDATE probe_points SET name=? WHERE id=?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RotateProbeToken 吊销正式凭据并重发一次性接入 token(重新接入用)。
func (s *Store) RotateProbeToken(id int64, joinTokenHash string) error {
	res, err := s.db.Exec(`UPDATE probe_points SET token_hash='', join_token_hash=? WHERE id=?`, joinTokenHash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProbePoint 删除探测点及其作为来源留下的全部判定。
func (s *Store) DeleteProbePoint(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`DELETE FROM probe_points WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// 顺手清掉它作为探测来源留下的历史数据(UC-30 后表不再读写,
	// 但删除探测点的语义不变)。
	if _, err := tx.Exec(`DELETE FROM reach_source_checks WHERE source_id=?`, fmt.Sprintf("probe:%d", id)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM reach_source_samples WHERE source_id=?`, fmt.Sprintf("probe:%d", id)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM bandwidth_samples WHERE source_id=?`, fmt.Sprintf("probe:%d", id)); err != nil {
		return err
	}
	return tx.Commit()
}
