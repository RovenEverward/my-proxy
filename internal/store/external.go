package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// migrateExternal 建外部端点的两张清洗层表:
// external_metadata 存规范化名(地区/城市/字母),external_quality 存降级探测分。
// 身份是 (source_id, source_key)——快照刷新后源还在但端点可能换指纹,记录按键
// 清理,不挂在 subscription_sources 的行上。
func (s *Store) migrateExternal() error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS external_metadata (
		 source_id INTEGER NOT NULL REFERENCES subscription_sources(id) ON DELETE CASCADE,
		 source_key TEXT NOT NULL,
		 config TEXT NOT NULL,
		 kind TEXT NOT NULL DEFAULT 'auto',
		 updated_at INTEGER NOT NULL,
		 PRIMARY KEY(source_id, source_key))`,
		`CREATE TABLE IF NOT EXISTS external_quality (
		 source_id INTEGER NOT NULL REFERENCES subscription_sources(id) ON DELETE CASCADE,
		 source_key TEXT NOT NULL,
		 checked_at INTEGER NOT NULL,
		 verdict TEXT NOT NULL,
		 score INTEGER NOT NULL,
		 report TEXT NOT NULL,
		 PRIMARY KEY(source_id, source_key))`,
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("外部端点清洗层建表: %w", err)
		}
	}
	return nil
}

// ListExternalMetadata 返回一个来源的全部清洗记录;sourceID=0 时返回所有来源的,
// 订阅装配要一次拿全。
func (s *Store) ListExternalMetadata(sourceID int64) (map[string]model.ExternalMetadata, error) {
	return listExternalMetadata(s.db, sourceID)
}

func listExternalMetadata(q querier, sourceID int64) (map[string]model.ExternalMetadata, error) {
	query := `SELECT source_id,source_key,config,kind,updated_at FROM external_metadata`
	args := []any{}
	if sourceID > 0 {
		query += ` WHERE source_id=?`
		args = append(args, sourceID)
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.ExternalMetadata{}
	for rows.Next() {
		var m model.ExternalMetadata
		var raw, kind string
		var at int64
		if err := rows.Scan(&m.SourceID, &m.SourceKey, &raw, &kind, &at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, err
		}
		m.Kind = kind
		m.UpdatedAt = time.Unix(at, 0).UTC()
		out[extMetaKey(m.SourceID, m.SourceKey)] = m
	}
	return out, rows.Err()
}

func extMetaKey(sourceID int64, sourceKey string) string {
	return fmt.Sprintf("%d\x00%s", sourceID, sourceKey)
}

// SaveExternalMetadataBatch 在一个事务里写多条外部清洗记录。regrade 与回填
// 都走它:要么全写要么不写,不留半份排好的字母。
func (s *Store) SaveExternalMetadataBatch(items []model.ExternalMetadata) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	exists, err := tx.Prepare(`SELECT 1 FROM subscription_sources WHERE id=?`)
	if err != nil {
		return err
	}
	defer exists.Close()
	stmt, err := tx.Prepare(`INSERT INTO external_metadata(source_id,source_key,config,kind,updated_at) VALUES(?,?,?,?,?)
	 ON CONFLICT(source_id,source_key) DO UPDATE SET config=excluded.config,kind=excluded.kind,updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Unix()
	for _, m := range items {
		if err := m.Validate(); err != nil {
			return err
		}
		var one int
		if err := exists.QueryRow(m.SourceID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if m.Kind == "" {
			m.Kind = "auto"
		}
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		at := m.UpdatedAt.Unix()
		if at <= 0 {
			at = now
		}
		if _, err := stmt.Exec(m.SourceID, m.SourceKey, string(b), m.Kind, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SyncExternalMetadata 在一次快照刷新后对齐清洗层:
// 删掉不在本次快照键集合里的记录(端点消失),把调用方算好的新记录写入。
// 探测分(ExternalQuality)不删——端点可能临时消失又回来,分数可复用,
// 且删除由端点寿命决定而不是快照节奏。
func (s *Store) SyncExternalMetadata(sourceID int64, snapshotKeys []string, upserts []model.ExternalMetadata) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM subscription_sources WHERE id=?`, sourceID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if len(snapshotKeys) > 0 {
		// 占位符展开:删掉不在快照键集合里的行。
		ph := ""
		args := []any{sourceID}
		for i, k := range snapshotKeys {
			if i > 0 {
				ph += ","
			}
			ph += "?"
			args = append(args, k)
		}
		if _, err := tx.Exec(`DELETE FROM external_metadata WHERE source_id=? AND source_key NOT IN (`+ph+`)`, args...); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`DELETE FROM external_metadata WHERE source_id=?`, sourceID); err != nil {
			return err
		}
	}
	stmt, err := tx.Prepare(`INSERT INTO external_metadata(source_id,source_key,config,kind,updated_at) VALUES(?,?,?,?,?)
	 ON CONFLICT(source_id,source_key) DO UPDATE SET config=excluded.config,kind=excluded.kind,updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Unix()
	for _, m := range upserts {
		if err := m.Validate(); err != nil {
			return err
		}
		if m.Kind == "" {
			m.Kind = "auto"
		}
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		at := m.UpdatedAt.Unix()
		if at <= 0 {
			at = now
		}
		if _, err := stmt.Exec(m.SourceID, m.SourceKey, string(b), m.Kind, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveExternalQuality 覆盖一个外部端点的质量存档;别的端点记录不受影响。
func (s *Store) SaveExternalQuality(q model.ExternalQuality) error {
	if q.SourceID <= 0 || q.SourceKey == "" || q.CheckedAt.IsZero() || !model.NodeQualityVerdicts[q.Verdict] {
		return fmt.Errorf("质量记录缺来源、指纹、时间或判定")
	}
	if q.Score < 0 || q.Score > 100 || len(q.Report) == 0 || len(q.Report) > qualityReportLimit || !json.Valid(q.Report) {
		return fmt.Errorf("质量报告无效或超过 64 KiB")
	}
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM subscription_sources WHERE id=?`, q.SourceID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO external_quality(source_id,source_key,checked_at,verdict,score,report) VALUES(?,?,?,?,?,?)
	 ON CONFLICT(source_id,source_key) DO UPDATE SET checked_at=excluded.checked_at,verdict=excluded.verdict,score=excluded.score,report=excluded.report`,
		q.SourceID, q.SourceKey, q.CheckedAt.Unix(), q.Verdict, q.Score, string(q.Report))
	return err
}

// ListExternalQuality 返回外部端点的质量记录;sourceID=0 时全量返回。
func (s *Store) ListExternalQuality(sourceID int64) ([]model.ExternalQuality, error) {
	query := `SELECT source_id,source_key,checked_at,verdict,score,report FROM external_quality`
	args := []any{}
	if sourceID > 0 {
		query += ` WHERE source_id=?`
		args = append(args, sourceID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ExternalQuality
	for rows.Next() {
		var q model.ExternalQuality
		var at int64
		var raw string
		if err := rows.Scan(&q.SourceID, &q.SourceKey, &at, &q.Verdict, &q.Score, &raw); err != nil {
			return nil, err
		}
		q.CheckedAt = time.Unix(at, 0).UTC()
		q.Report = json.RawMessage(raw)
		out = append(out, q)
	}
	return out, rows.Err()
}

// DeleteExternalMetadata 删一条清洗记录;不存在时返回 ErrNotFound。
// 用于人工清空端点地区,回到「源名 · 原名」回退形态。
func (s *Store) DeleteExternalMetadata(sourceID int64, sourceKey string) error {
	res, err := s.db.Exec(`DELETE FROM external_metadata WHERE source_id=? AND source_key=?`, sourceID, sourceKey)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}
