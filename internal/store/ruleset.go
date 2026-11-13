package store

import (
	"database/sql"
	"errors"
	"time"
)

// 规则集镜像的持久化:每个被引用文件一行快照,见 ruleset_snapshots 表注释。
// 这里是 UC-05 私有化分发的服务端半边的「存」;「取」在 internal/ruleset。

// RulesetSnapshot 是一个上游规则集文件的镜像快照。
//
// Body 不进 JSON:状态接口只需要元数据,正文由 RulesetSnapshotBody 单独取。
type RulesetSnapshot struct {
	Key           string `json:"key"` // "clash/<name>" 或 "singbox/<tag>"
	URL           string `json:"url"`
	Body          []byte `json:"-"`
	ContentType   string `json:"-"`
	FetchedAt     int64  `json:"fetched_at"`      // 毫秒;>0 即已就绪(有 body)
	LastAttemptAt int64  `json:"last_attempt_at"` // 毫秒
	LastError     string `json:"last_error,omitempty"`
}

// ListRulesetSnapshots 返回全部快照的元数据(key → 行),不取 body:状态接口与
// 订阅渲染只需要知道「就绪没有」,一份订阅可能引用上百个文件,不该把镜像正文
// 全部读进内存。Body 恒为 nil;就绪判据用 FetchedAt > 0,与 body 非空等价
// (fetched_at 只在写入 body 的那次成功里赋值)。
func (s *Store) ListRulesetSnapshots() (map[string]*RulesetSnapshot, error) {
	rows, err := s.db.Query(`SELECT key, url, content_type, fetched_at, last_attempt_at, last_error
		FROM ruleset_snapshots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*RulesetSnapshot{}
	for rows.Next() {
		var snap RulesetSnapshot
		if err := rows.Scan(&snap.Key, &snap.URL, &snap.ContentType,
			&snap.FetchedAt, &snap.LastAttemptAt, &snap.LastError); err != nil {
			return nil, err
		}
		out[snap.Key] = &snap
	}
	return out, rows.Err()
}

// RulesetSnapshotBody 取一个文件的镜像正文与 Content-Type。没拉过或还没就绪
// 都返回 ErrNotFound —— 下载端把这两种都回 404,与「不存在」无法区分。
func (s *Store) RulesetSnapshotBody(key string) ([]byte, string, error) {
	var (
		body []byte
		ct   string
	)
	err := s.db.QueryRow(`SELECT body, content_type FROM ruleset_snapshots
		WHERE key = ? AND body IS NOT NULL`, key).Scan(&body, &ct)
	if err != nil {
		return nil, "", wrapNotFound(err, "规则集镜像")
	}
	if len(body) == 0 {
		return nil, "", wrapNotFound(sql.ErrNoRows, "规则集镜像")
	}
	return body, ct, nil
}

// CompleteRulesetRefresh 落一次刷新结果:无论成败都记 attempt 与 last_error;
// 成功(failure 为空且 body 非空)同时替换镜像正文。
//
// 失败路径刻意不动 body/fetched_at:上游一时抽风不该让已经就绪的镜像变成 404,
// 客户端照样拿得到上次的好副本。
//
// becameReady 表示这次把「原来没 body」的文件拉出了 body —— 订阅 URL 结构因此
// 变了(从上游地址换成镜像地址),调用方拿它决定要不要失效订阅缓存。
func (s *Store) CompleteRulesetRefresh(key, url string, at time.Time,
	body []byte, contentType, failure string) (becameReady bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var hadBody bool
	err = tx.QueryRow(`SELECT body IS NOT NULL AND length(body) > 0
		FROM ruleset_snapshots WHERE key = ?`, key).Scan(&hadBody)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	ok := failure == "" && len(body) > 0
	if ok {
		_, err = tx.Exec(`INSERT INTO ruleset_snapshots
			(key, url, body, content_type, fetched_at, last_attempt_at, last_error)
			VALUES (?, ?, ?, ?, ?, ?, '')
			ON CONFLICT(key) DO UPDATE SET
				url             = excluded.url,
				body            = excluded.body,
				content_type    = excluded.content_type,
				fetched_at      = excluded.fetched_at,
				last_attempt_at = excluded.last_attempt_at,
				last_error      = ''`,
			key, url, body, contentType, at.UnixMilli(), at.UnixMilli())
	} else {
		_, err = tx.Exec(`INSERT INTO ruleset_snapshots
			(key, url, last_attempt_at, last_error)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET
				url             = excluded.url,
				last_attempt_at = excluded.last_attempt_at,
				last_error      = excluded.last_error`,
			key, url, at.UnixMilli(), failure)
	}
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return ok && !hadBody, nil
}
