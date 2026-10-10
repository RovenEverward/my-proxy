package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/model"
)

func (s *Store) migratePurposes() error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS purposes (slug TEXT PRIMARY KEY, config TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS node_metadata (id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE, config TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS host_metadata (id INTEGER PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE, config TEXT NOT NULL)`,
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("用途建表: %w", err)
		}
	}
	return nil
}
func (s *Store) ListPurposes() ([]model.Purpose, error) {
	rows, err := s.db.Query(`SELECT config FROM purposes ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Purpose{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var p model.Purpose
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) GetPurpose(slug string) (*model.Purpose, error) {
	var raw string
	err := s.db.QueryRow(`SELECT config FROM purposes WHERE slug=?`, slug).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var p model.Purpose
	err = json.Unmarshal([]byte(raw), &p)
	return &p, err
}

// CreatePurpose and UpdatePurpose distinguish duplicate and missing objects; deleted URLs stay missing.
func (s *Store) CreatePurpose(p *model.Purpose) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO purposes(slug,config) VALUES(?,?)`, p.Slug, string(b))
	return err
}
func (s *Store) UpdatePurpose(p *model.Purpose) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE purposes SET config=? WHERE slug=?`, string(b), p.Slug)
	return purposeAffected(res, err)
}
func (s *Store) DeletePurpose(slug string) error {
	res, err := s.db.Exec(`DELETE FROM purposes WHERE slug=?`, slug)
	return purposeAffected(res, err)
}
func purposeAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
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
	table, err := metadataTable(kind)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,config FROM ` + table)
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
