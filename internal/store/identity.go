package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// 控制面自己的 TLS 身份(自签证书 + 私钥)存在库里,而不是 /var/lib/mpx 下的
// 文件:备份/恢复只搬库,放进库里它就自动跟着走,恢复到一台新机器后指纹不变,
// 已经接入的设备不用重新 connect。一行表,id 恒为 1。
func (s *Store) migrateIdentity() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS tls_identity (
		id       INTEGER PRIMARY KEY CHECK (id = 1),
		cert_pem TEXT    NOT NULL,
		key_pem  TEXT    NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("建 tls_identity 表: %w", err)
	}
	return nil
}

// GetTLSIdentity 取出证书与私钥(PEM)。还没有时返回 ErrNotFound。
func (s *Store) GetTLSIdentity() (certPEM, keyPEM []byte, err error) {
	var c, k string
	err = s.db.QueryRow(`SELECT cert_pem, key_pem FROM tls_identity WHERE id = 1`).Scan(&c, &k)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("读 TLS 身份: %w", err)
	}
	return []byte(c), []byte(k), nil
}

// PutTLSIdentity 写入(或替换)证书与私钥。
func (s *Store) PutTLSIdentity(certPEM, keyPEM []byte) error {
	_, err := s.db.Exec(`INSERT INTO tls_identity (id, cert_pem, key_pem) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET cert_pem = excluded.cert_pem, key_pem = excluded.key_pem`,
		string(certPEM), string(keyPEM))
	if err != nil {
		return fmt.Errorf("写 TLS 身份: %w", err)
	}
	return nil
}
