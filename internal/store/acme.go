package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UC-26:ACME 证书状态。acme_state 是单行表(id 恒为 1),形状与 tls_identity
// 一个模式:PEM 明文落库,备份即整库打包 —— 恢复后证书与账号跟着库走,不用重签。
//
// 与 tls_identity 的区别:这张表还记着签发过程的状态(account key、域名、
// 错误),自签身份没有这些概念。
type AcmeState struct {
	// AccountKeyPEM 是 ACME 账户的私钥(PEM)。注册/续签都靠它认同一个账户;
	// 换 CA(staging ↔ 正式)仍可复用,账户以 onlyReturnExisting 探活、没了重建。
	AccountKeyPEM []byte
	// CertPEM / KeyPEM 是已签下的证书链与域名私钥;空表示还没签下来或已废弃。
	CertPEM []byte
	KeyPEM  []byte
	// Domain 是这张证书签给的域名。与 settings.acme_domain 对不上时证书作废
	// (换域名必须重签,旧证书留着只会让 SNI 错发)。
	Domain string
	// Staging 记这张证书是从哪个 CA 签的:false 正式 / true staging。
	// staging 证书浏览器不认,切 CA 时连同证书一起作废。
	Staging bool
	// IssuedAt / NotAfter 是签发成功时刻与证书到期时刻;没签过为零值。
	IssuedAt time.Time
	NotAfter time.Time
	// LastError / LastAttemptAt 是最近一次签发尝试的结果与时刻;last_error 在
	// 下一次成功时清空 —— 历史的失败不该一直挂着。
	LastError     string
	LastAttemptAt time.Time
	// Failures 是连续失败的次数;续期循环用它算指数退避的下次尝试时刻。
	Failures    int
	NextRetryAt time.Time
}

// migrateACME 建 ACME 状态表。
func (s *Store) migrateACME() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS acme_state (
		id              INTEGER PRIMARY KEY CHECK (id = 1),
		account_key_pem TEXT    NOT NULL DEFAULT '',
		cert_pem        TEXT    NOT NULL DEFAULT '',
		key_pem         TEXT    NOT NULL DEFAULT '',
		domain          TEXT    NOT NULL DEFAULT '',
		staging         INTEGER NOT NULL DEFAULT 0,
		issued_at       INTEGER NOT NULL DEFAULT 0,
		not_after       INTEGER NOT NULL DEFAULT 0,
		last_error      TEXT    NOT NULL DEFAULT '',
		last_attempt_at INTEGER NOT NULL DEFAULT 0,
		failures        INTEGER NOT NULL DEFAULT 0,
		next_retry_at   INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		return fmt.Errorf("建 acme_state 表: %w", err)
	}
	return nil
}

// GetACMEState 读 ACME 状态;还没写过时返回零值(没存过 ≠ 出错)。
func (s *Store) GetACMEState() (*AcmeState, error) {
	var (
		st                                   AcmeState
		ak, cp, kp, le                       string
		staging                              int
		issuedAt, notAfter, attempt, retryAt int64
	)
	err := s.db.QueryRow(
		`SELECT account_key_pem, cert_pem, key_pem, domain, staging,
		        issued_at, not_after, last_error, last_attempt_at, failures, next_retry_at
		 FROM acme_state WHERE id = 1`).
		Scan(&ak, &cp, &kp, &st.Domain, &staging,
			&issuedAt, &notAfter, &le, &attempt, &st.Failures, &retryAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &AcmeState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读 ACME 状态: %w", err)
	}
	st.AccountKeyPEM = []byte(ak)
	st.CertPEM = []byte(cp)
	st.KeyPEM = []byte(kp)
	st.Staging = staging != 0
	st.LastError = le
	st.IssuedAt = unixOrZero(issuedAt)
	st.NotAfter = unixOrZero(notAfter)
	st.LastAttemptAt = unixOrZero(attempt)
	st.NextRetryAt = unixOrZero(retryAt)
	return &st, nil
}

// unixOrZero 把库里的 unix 秒翻成 time.Time;0(从没写过/被清掉)翻成
// 真正的零值 —— 否则 1970 会被上层当成一个真实时刻吐进 JSON。
func unixOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// SaveACMEState 整体写 ACME 状态。
func (s *Store) SaveACMEState(st *AcmeState) error {
	if st == nil {
		return errors.New("写 ACME 状态: 状态对象为空")
	}
	_, err := s.db.Exec(
		`INSERT INTO acme_state (id, account_key_pem, cert_pem, key_pem, domain, staging,
		                        issued_at, not_after, last_error, last_attempt_at, failures, next_retry_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			account_key_pem = excluded.account_key_pem,
			cert_pem        = excluded.cert_pem,
			key_pem         = excluded.key_pem,
			domain          = excluded.domain,
			staging         = excluded.staging,
			issued_at       = excluded.issued_at,
			not_after       = excluded.not_after,
			last_error      = excluded.last_error,
			last_attempt_at = excluded.last_attempt_at,
			failures        = excluded.failures,
			next_retry_at   = excluded.next_retry_at`,
		string(st.AccountKeyPEM), string(st.CertPEM), string(st.KeyPEM), st.Domain,
		boolToInt(st.Staging), st.IssuedAt.Unix(), st.NotAfter.Unix(), st.LastError,
		st.LastAttemptAt.Unix(), st.Failures, st.NextRetryAt.Unix())
	if err != nil {
		return fmt.Errorf("写 ACME 状态: %w", err)
	}
	return nil
}

// ClearACMECert 作废已签下的证书(域名/CA 变了,或手动 unset)。账户 key 与
// 历史错误保留:账户还能用,错误还查得到 —— 废掉的只是「证书」这一个结果。
func (s *Store) ClearACMECert() error {
	_, err := s.db.Exec(`UPDATE acme_state SET
		cert_pem = '', key_pem = '', domain = '', issued_at = 0, not_after = 0,
		failures = 0, next_retry_at = 0
		WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("作废 ACME 证书: %w", err)
	}
	return nil
}
