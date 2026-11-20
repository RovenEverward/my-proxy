package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/reach"
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
	if _, err := tx.Exec(`DELETE FROM reach_source_checks WHERE source_id=?`, fmt.Sprintf("probe:%d", id)); err != nil {
		return err
	}
	if err := deleteSubjectMetrics(tx, "WHERE source_id=?", fmt.Sprintf("probe:%d", id)); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- 对象级触达存档(UC-12) ----

// SaveSubjectCheck 覆盖一个对象的触达存档;同一对象后来写的覆盖先写的。
func (s *Store) SaveSubjectCheck(c model.ReachSubjectCheck) error {
	if c.SubjectKey == "" || c.CheckedAt.IsZero() || !model.ReachVerdicts[c.Verdict] {
		return fmt.Errorf("触达存档缺少对象、时间或判定")
	}
	if len(c.Report) > reachReportLimit || !json.Valid(c.Report) {
		return fmt.Errorf("触达检查报告无效或超过 256 KiB")
	}
	_, err := s.db.Exec(`INSERT INTO reach_subject_checks(subject_key,checked_at,verdict,report) VALUES(?,?,?,?)
 ON CONFLICT(subject_key) DO UPDATE SET checked_at=excluded.checked_at,verdict=excluded.verdict,report=excluded.report`,
		string(c.SubjectKey), c.CheckedAt.Unix(), c.Verdict, string(c.Report))
	return err
}

// ListSubjectChecks 返回全部对象级存档。
func (s *Store) ListSubjectChecks() ([]model.ReachSubjectCheck, error) {
	rows, err := s.db.Query(`SELECT subject_key,checked_at,verdict,report FROM reach_subject_checks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ReachSubjectCheck
	for rows.Next() {
		var c model.ReachSubjectCheck
		var at int64
		var key, raw string
		if err := rows.Scan(&key, &at, &c.Verdict, &raw); err != nil {
			return nil, err
		}
		c.SubjectKey = model.ProbeSubjectKey(key)
		c.CheckedAt = time.Unix(at, 0).UTC()
		c.Report = json.RawMessage(raw)
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteSubjectResults 清掉一个对象的存档与来源结果(节点删除时调用)。
func (s *Store) DeleteSubjectResults(key model.ProbeSubjectKey) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM reach_subject_checks WHERE subject_key=?`,
		`DELETE FROM reach_source_checks WHERE subject_key=?`,
	} {
		if _, err := tx.Exec(q, string(key)); err != nil {
			return err
		}
	}
	if err := deleteSubjectMetrics(tx, "WHERE subject_key=?", string(key)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSubjectResultsByPrefix 按身份前缀清存档与来源结果(来源删除时清掉
// "s:<id>:%" 的全部端点)。
func (s *Store) DeleteSubjectResultsByPrefix(prefix string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM reach_subject_checks WHERE subject_key LIKE ?`,
		`DELETE FROM reach_source_checks WHERE subject_key LIKE ?`,
	} {
		if _, err := tx.Exec(q, prefix+"%"); err != nil {
			return err
		}
	}
	if err := deleteSubjectMetrics(tx, "WHERE subject_key LIKE ?", prefix+"%"); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- (对象 × 来源)原始探测结果 ----

// SaveSourceCheck 覆盖一条(对象,来源)的探测结果;summary 与 results 由
// 上层按 internal/reach 线形编码成 JSON,这里只存不解析。
func (s *Store) SaveSourceCheck(subjectKey model.ProbeSubjectKey, sourceID, scope string, checkedAt time.Time, summary, results json.RawMessage) error {
	if subjectKey == "" || sourceID == "" || checkedAt.IsZero() {
		return fmt.Errorf("触达来源结果缺少对象、来源或时间")
	}
	if len(summary) > reachReportLimit || !json.Valid(summary) {
		return fmt.Errorf("触达来源摘要无效或超过 256 KiB")
	}
	if len(results) > reachReportLimit || (len(results) > 0 && !json.Valid(results)) {
		return fmt.Errorf("触达来源明细无效或超过 256 KiB")
	}
	var res sql.NullString
	if len(results) > 0 {
		res = sql.NullString{String: string(results), Valid: true}
	}
	if _, err := s.db.Exec(`INSERT INTO reach_source_checks(subject_key,source_id,scope,checked_at,summary,results) VALUES(?,?,?,?,?,?)
 ON CONFLICT(subject_key,source_id) DO UPDATE SET scope=excluded.scope,checked_at=excluded.checked_at,summary=excluded.summary,results=excluded.results`,
		string(subjectKey), sourceID, scope, checkedAt.Unix(), string(summary), res); err != nil {
		return err
	}
	// UC-20:同一摘要留成稳定度样本,reach_source_checks 仍是判定的最新行。
	var sum reach.SourceSummary
	if err := json.Unmarshal(summary, &sum); err != nil {
		// 摘要本就校验过 json.Valid;坏形状时样本不追加但不阻断存档。
		return nil
	}
	return s.AppendSourceSample(subjectKey, sourceID, scope, checkedAt, sum)
}

// ListSourceChecks 返回某对象的全部来源结果(subject 空串返回全表)。
func (s *Store) ListSourceChecks(subject model.ProbeSubjectKey) ([]model.ReachSourceCheck, error) {
	query := `SELECT subject_key,source_id,scope,checked_at,summary,results FROM reach_source_checks`
	args := []any{}
	if subject != "" {
		query += ` WHERE subject_key=?`
		args = append(args, string(subject))
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ReachSourceCheck
	for rows.Next() {
		var c model.ReachSourceCheck
		var at int64
		var key, sum string
		var results sql.NullString
		if err := rows.Scan(&key, &c.SourceID, &c.Scope, &at, &sum, &results); err != nil {
			return nil, err
		}
		c.SubjectKey = model.ProbeSubjectKey(key)
		c.CheckedAt = time.Unix(at, 0).UTC()
		c.Summary = json.RawMessage(sum)
		if results.Valid {
			c.Results = json.RawMessage(results.String)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
