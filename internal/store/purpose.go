package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// migratePurposes 只建用途元数据表(节点/入口的 region、tags 等)。purposes 表
// 从 v0.7.0 起不再创建,老库的表由 migrate() 末尾的 dropPurposes 按分流模式处理。
func (s *Store) migratePurposes() error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS node_metadata (id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE, config TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS host_metadata (id INTEGER PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE, config TEXT NOT NULL)`,
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("用途元数据建表: %w", err)
		}
	}
	return nil
}

// dropPurposes 在分流模式已是 rules 时删掉遗留的 purposes 表。仍是 template 模式
// 的老库保留它(先按老版本迁移到 rules 再升级);新库根本没建过这张表,DROP IF
// EXISTS 是空操作。
func (s *Store) dropPurposes() error {
	st, err := s.GetRoutingState()
	if err != nil {
		return err
	}
	has, err := s.tableExists("purposes")
	if err != nil {
		return err
	}
	if st.Mode != model.RoutingRules && has {
		return nil
	}
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS purposes`); err != nil {
		return fmt.Errorf("删除 purposes 表: %w", err)
	}
	return nil
}

func (s *Store) tableExists(name string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

func metadataTable(kind string) (string, error) {
	switch kind {
	case "nodes":
		return "node_metadata", nil
	case "hosts":
		return "host_metadata", nil
	default:
		return "", fmt.Errorf("元数据只支持 nodes / hosts")
	}
}
func (s *Store) SavePurposeMetadata(kind string, id int64, m model.PurposeMetadata) error {
	table, err := metadataTable(kind)
	if err != nil {
		return err
	}
	if err = m.Validate(); err != nil {
		return err
	}
	var exists int
	if err = s.db.QueryRow(`SELECT 1 FROM `+kind+` WHERE id=?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO `+table+`(id,config) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET config=excluded.config`, id, string(b))
	return err
}
func (s *Store) ListPurposeMetadata(kind string) (map[int64]model.PurposeMetadata, error) {
	return listPurposeMetadata(s.db, kind)
}

// listPurposeMetadata 与 ListPurposeMetadata 同义,但接受任意 querier,好让迁移
// 事务内部也能读到节点元数据(*sql.Tx 与 *sql.DB 都满足 querier)。
func listPurposeMetadata(q querier, kind string) (map[int64]model.PurposeMetadata, error) {
	table, err := metadataTable(kind)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT id,config FROM ` + table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]model.PurposeMetadata{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var m model.PurposeMetadata
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, rows.Err()
}
func (s *Store) GetPurposeMetadata(kind string, id int64) (model.PurposeMetadata, error) {
	table, err := metadataTable(kind)
	if err != nil {
		return model.PurposeMetadata{}, err
	}
	var exists int
	if err = s.db.QueryRow(`SELECT 1 FROM `+kind+` WHERE id=?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return model.PurposeMetadata{}, ErrNotFound
	}
	if err != nil {
		return model.PurposeMetadata{}, err
	}
	var raw string
	err = s.db.QueryRow(`SELECT config FROM `+table+` WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PurposeMetadata{}, nil
	}
	if err != nil {
		return model.PurposeMetadata{}, err
	}
	var m model.PurposeMetadata
	err = json.Unmarshal([]byte(raw), &m)
	return m, err
}

// LegacyPurposeCount 返回遗留 purposes 表里的行数;表不存在时为 0。控制面启动时
// 用它挡住「template 模式 + 还有用途」的老库:v0.7.0 的迁移不再读用途,自动迁移
// 会把用途悄悄丢掉。
func (s *Store) LegacyPurposeCount() (int, error) {
	has, err := s.tableExists("purposes")
	if err != nil || !has {
		return 0, err
	}
	var n int
	err = s.db.QueryRow(`SELECT COUNT(*) FROM purposes`).Scan(&n)
	return n, err
}
