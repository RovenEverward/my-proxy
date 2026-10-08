package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// SaveUserNotice 记下"这一条用户通知已经发过了"。
//
// (user_id, kind) 是主键:同一用户同一类提醒只留一行。调用方先查后存,巡检里
// 查不到才发通知再存;这里用 INSERT OR REPLACE 兜底,免得两个进程(实际只有
// 一个,但写代码时别依赖这个)并发时各发一次。
func (s *Store) SaveUserNotice(n model.UserNotice) error {
	_, err := s.db.Exec(
		`INSERT INTO user_notices (user_id, kind, sent_at) VALUES (?, ?, ?)
		 ON CONFLICT(user_id, kind) DO UPDATE SET sent_at = excluded.sent_at`,
		n.UserID, n.Kind, normalizeTime(n.SentAt).Unix())
	if err != nil {
		return fmt.Errorf("记录用户 %d 的通知 %s: %w", n.UserID, n.Kind, err)
	}
	return nil
}

// UserNoticeSent 报告这一条通知是否已经发过。
func (s *Store) UserNoticeSent(userID int64, kind string) (bool, error) {
	var sentAt int64
	err := s.db.QueryRow(
		`SELECT sent_at FROM user_notices WHERE user_id = ? AND kind = ?`, userID, kind).Scan(&sentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查询用户 %d 的通知 %s: %w", userID, kind, err)
	}
	return true, nil
}

// DeleteUserNotice 清除一条"已通知过"记录。条件消失(续费、重置周期)后,
// 同一条提醒才有资格再发一次。
//
// 用户被删除时记录随外键级联删除;这里是给"条件恢复"用的显式删除。
func (s *Store) DeleteUserNotice(userID int64, kind string) error {
	_, err := s.db.Exec(
		`DELETE FROM user_notices WHERE user_id = ? AND kind = ?`, userID, kind)
	if err != nil {
		return fmt.Errorf("清除用户 %d 的通知 %s: %w", userID, kind, err)
	}
	return nil
}

// ListUserNotices 取回全部"已通知过"的记录,按 user_id 索引。
//
// 巡检每轮开始时读一次全表(用户是百级、记录只多不少地对着用户数),比逐用户
// 查询少一个数量级的往返。停用的用户不会再发通知,但记录留着无害:用户删掉时
// 外键会清掉。
func (s *Store) ListUserNotices() (map[int64]map[string]time.Time, error) {
	rows, err := s.db.Query(`SELECT user_id, kind, sent_at FROM user_notices`)
	if err != nil {
		return nil, fmt.Errorf("查询通知记录列表: %w", err)
	}
	defer rows.Close()

	out := map[int64]map[string]time.Time{}
	for rows.Next() {
		var (
			userID int64
			kind   string
			sentAt int64
		)
		if err := rows.Scan(&userID, &kind, &sentAt); err != nil {
			return nil, fmt.Errorf("读取通知记录: %w", err)
		}
		m := out[userID]
		if m == nil {
			m = map[string]time.Time{}
			out[userID] = m
		}
		m[kind] = time.Unix(sentAt, 0).UTC()
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历通知记录列表: %w", err)
	}
	return out, nil
}
