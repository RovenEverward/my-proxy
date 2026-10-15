package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// devices 表:每台接入的设备一行。token_hash 是设备 token 的 SHA-256(十六进制);
// token 是 32 字节随机数,熵足够,不需要慢哈希。
func (s *Store) migrateDevices() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS devices (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		name         TEXT    NOT NULL,
		token_hash   TEXT    NOT NULL UNIQUE,
		created_at   INTEGER NOT NULL,
		last_used_at INTEGER,
		last_ip      TEXT    NOT NULL DEFAULT ''
	)`)
	if err != nil {
		return fmt.Errorf("建 devices 表: %w", err)
	}
	return nil
}

const deviceCols = `id, name, created_at, last_used_at, last_ip`

func scanDevice(row interface{ Scan(...any) error }) (*model.Device, error) {
	var d model.Device
	var created int64
	var used sql.NullInt64
	if err := row.Scan(&d.ID, &d.Name, &created, &used, &d.LastIP); err != nil {
		return nil, err
	}
	d.CreatedAt = time.Unix(created, 0)
	if used.Valid {
		t := time.Unix(used.Int64, 0)
		d.LastUsedAt = &t
	}
	return &d, nil
}

// CreateDevice 记下一台新设备。
func (s *Store) CreateDevice(name, tokenHash string, now time.Time) (*model.Device, error) {
	res, err := s.db.Exec(`INSERT INTO devices (name, token_hash, created_at) VALUES (?, ?, ?)`,
		name, tokenHash, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("记下设备: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &model.Device{ID: id, Name: name, CreatedAt: time.Unix(now.Unix(), 0)}, nil
}

// DeviceByTokenHash 按 token 哈希找设备;没有时返回 ErrNotFound。
func (s *Store) DeviceByTokenHash(hash string) (*model.Device, error) {
	d, err := scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// ListDevices 按接入先后列出全部设备。
func (s *Store) ListDevices() ([]model.Device, error) {
	rows, err := s.db.Query(`SELECT ` + deviceCols + ` FROM devices ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeleteDevice 吊销一台设备;不存在时返回 ErrNotFound。
func (s *Store) DeleteDevice(id int64) error {
	res, err := s.db.Exec(`DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchDevice 记下设备最近一次使用的时间与来源 IP。
func (s *Store) TouchDevice(id int64, ip string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE devices SET last_used_at = ?, last_ip = ? WHERE id = ?`, now.Unix(), ip, id)
	return err
}

// 设备密码只有一份,存它的 argon2id 哈希(PHC 字符串)。空串表示没设、设备
// 密码接入关闭。
func (s *Store) migrateDevicePassword() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS device_password (
		id   INTEGER PRIMARY KEY CHECK (id = 1),
		hash TEXT    NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("建 device_password 表: %w", err)
	}
	return nil
}

// DevicePasswordHash 取设备密码哈希;没设过返回空串。
func (s *Store) DevicePasswordHash() (string, error) {
	var h string
	err := s.db.QueryRow(`SELECT hash FROM device_password WHERE id = 1`).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return h, err
}

// SetDevicePasswordHash 写入设备密码哈希;空串 = 关闭设备密码接入。
func (s *Store) SetDevicePasswordHash(hash string) error {
	_, err := s.db.Exec(`INSERT INTO device_password (id, hash) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET hash = excluded.hash`, hash)
	return err
}

// DeleteAllDevices 吊销全部设备,返回吊销了几台。
func (s *Store) DeleteAllDevices() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM devices`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
