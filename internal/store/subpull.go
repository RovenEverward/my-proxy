package store

import (
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 订阅拉取记录:一人一行,只记"第一次"和"最近一次"。
//
// 它不是订阅本体的一部分:删掉整张表,订阅照常渲染、下发,只是"这个人拉到过
// 没有"回到未知。所以备份校验的必需表清单里同样没有它。

// RecordSubPull 记一次订阅拉取。
//
// 没有记录时插入,first_at 与 last_at 都取 at;已有记录时只更新 last_at、
// format 与 client —— first_at 是"这个人第一次拉到"的时刻,不能被后来的拉取
// 改掉。userID 必须指向一个存在的用户,否则外键报错。
func (s *Store) RecordSubPull(userID int64, format, client string, at time.Time) error {
	at = normalizeTime(at)
	_, err := s.db.Exec(
		`INSERT INTO sub_pulls (user_id, first_at, last_at, format, client) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		     last_at = excluded.last_at,
		     format = excluded.format,
		     client = excluded.client`,
		userID, at.Unix(), at.Unix(), format, client)
	if err != nil {
		return fmt.Errorf("记录用户 %d 的订阅拉取: %w", userID, err)
	}
	return nil
}

// ListSubPulls 返回全部订阅拉取记录,带用户名并按用户名排序。
func (s *Store) ListSubPulls() ([]model.SubPull, error) {
	rows, err := s.db.Query(`
		SELECT u.username, p.first_at, p.last_at, p.format, p.client
		FROM sub_pulls p JOIN users u ON u.id = p.user_id
		ORDER BY u.username`)
	if err != nil {
		return nil, fmt.Errorf("查询订阅拉取记录: %w", err)
	}
	defer rows.Close()

	out := []model.SubPull{}
	for rows.Next() {
		var (
			p           model.SubPull
			first, last int64
		)
		if err := rows.Scan(&p.Username, &first, &last, &p.Format, &p.Client); err != nil {
			return nil, fmt.Errorf("读取订阅拉取记录: %w", err)
		}
		p.FirstPulledAt = time.Unix(first, 0).UTC()
		p.LastPulledAt = time.Unix(last, 0).UTC()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历订阅拉取记录: %w", err)
	}
	return out, nil
}

// ClearSubPull 清掉一个用户的订阅拉取记录。吊销订阅 token 后调用,让新链接
// 重新等第一次拉取。记录本来就不存在时不算错误。
func (s *Store) ClearSubPull(userID int64) error {
	if _, err := s.db.Exec(`DELETE FROM sub_pulls WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("清除用户 %d 的订阅拉取记录: %w", userID, err)
	}
	return nil
}
