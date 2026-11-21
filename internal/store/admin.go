package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UC-26:网页管理台的账号与会话。
//
// admin_account 是单行表(id 恒为 1):单管理员账号,多操作者共享,不做权限
// 分级。password_hash 是 argon2id 的 PHC 字符串 —— 哈希在 server 层做(复用
// 设备密码那套),store 只存字符串。
//
// admin_sessions 是登录会话:token_hash 是 token 的 SHA-256(十六进制),与
// 设备 token 一个做法 —— token 是 32 字节随机数,熵足够,不需要慢哈希。
// kind 记这枚会话是浏览器('web')还是 CLI('cli')领的:同表同校验,kind 只
// 是来源信息,不决定能用在哪里。改密码时整表清空,单账号模型下"吊销所有会话"
// 就是最细的粒度。

// AdminSession 是一行 admin_sessions。不放进 model:它没有任何管理面 API 出参
// (登录只回明文 token),只在 server 鉴权内部流转。
type AdminSession struct {
	TokenHash string
	Kind      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// migrateAdmin 建账号与会话两张表。
func (s *Store) migrateAdmin() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS admin_account (
		id            INTEGER PRIMARY KEY CHECK (id = 1),
		username      TEXT    NOT NULL,
		password_hash TEXT    NOT NULL,
		created_at    INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("建 admin_account 表: %w", err)
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS admin_sessions (
		token_hash TEXT PRIMARY KEY,
		kind       TEXT    NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("建 admin_sessions 表: %w", err)
	}
	return nil
}

// AdminAccount 取账号的用户名与口令哈希;没建过账号时 err 是 ErrNotFound —
// 「还没初始化」与「库坏了」对调用方是两件事,分开报。
func (s *Store) AdminAccount() (username, passwordHash string, err error) {
	err = s.db.QueryRow(`SELECT username, password_hash FROM admin_account WHERE id = 1`).
		Scan(&username, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return username, passwordHash, err
}

// CreateAdminAccount 建立唯一的账号;调用方负责先查存在性(建号与改名的语义
// 在 server 层分叉),CHECK(id=1) 是兜底的第二道闸 —— 并发撞号时后写的报约束错。
func (s *Store) CreateAdminAccount(username, passwordHash string, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO admin_account (id, username, password_hash, created_at)
		VALUES (1, ?, ?, ?)`, username, passwordHash, now.Unix())
	if err != nil {
		return fmt.Errorf("建管理员账号: %w", err)
	}
	return nil
}

// SetAdminUsername 改账号名;没建过账号时返回 ErrNotFound。
func (s *Store) SetAdminUsername(username string) error {
	res, err := s.db.Exec(`UPDATE admin_account SET username = ? WHERE id = 1`, username)
	if err != nil {
		return fmt.Errorf("改管理员用户名: %w", err)
	}
	return affectedOrNotFound(res, "管理员账号")
}

// SetAdminPasswordHash 写入新的口令哈希;没建过账号时返回 ErrNotFound。
func (s *Store) SetAdminPasswordHash(hash string) error {
	res, err := s.db.Exec(`UPDATE admin_account SET password_hash = ? WHERE id = 1`, hash)
	if err != nil {
		return fmt.Errorf("改管理员口令: %w", err)
	}
	return affectedOrNotFound(res, "管理员账号")
}

// CreateAdminSession 记下一枚会话。
func (s *Store) CreateAdminSession(tokenHash, kind string, createdAt, expiresAt time.Time) error {
	_, err := s.db.Exec(`INSERT INTO admin_sessions (token_hash, kind, created_at, expires_at)
		VALUES (?, ?, ?, ?)`, tokenHash, kind, createdAt.Unix(), expiresAt.Unix())
	if err != nil {
		return fmt.Errorf("记下管理会话: %w", err)
	}
	return nil
}

// AdminSessionByTokenHash 按 token 哈希找一枚未过期的会话;没有或已过期时返回
// ErrNotFound —— 鉴权眼里「过期」与「不存在」是一回事,都该拒绝。
func (s *Store) AdminSessionByTokenHash(hash string, now time.Time) (*AdminSession, error) {
	var sess AdminSession
	var created, expires int64
	err := s.db.QueryRow(`SELECT token_hash, kind, created_at, expires_at
		FROM admin_sessions WHERE token_hash = ? AND expires_at > ?`, hash, now.Unix()).
		Scan(&sess.TokenHash, &sess.Kind, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.ExpiresAt = time.Unix(expires, 0)
	return &sess, nil
}

// DeleteAdminSession 吊销一枚会话;不存在时返回 ErrNotFound。
func (s *Store) DeleteAdminSession(tokenHash string) error {
	res, err := s.db.Exec(`DELETE FROM admin_sessions WHERE token_hash = ?`, tokenHash)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res, "管理会话")
}

// DeleteAllAdminSessions 吊销全部会话(改密码时用),返回吊销了几枚。
func (s *Store) DeleteAllAdminSessions() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM admin_sessions`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
