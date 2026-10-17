package store

import (
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 「有改动未应用」:入站的增删、结构性修改(端口、协议、传输、安全层、证书……)
// 只改了控制面的记录,要等使用者点「应用到节点」(sync / install)才推到节点上。
// 这里记下每台节点从什么时候起有这样的改动,应用成功后清掉。用户的增删停用由
// 后台循环自动收敛,不算在内。
func (s *Store) migrateNodePending() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS node_pending (
		node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
		since   INTEGER NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("建 node_pending 表: %w", err)
	}
	return nil
}

// MarkNodePending 记下节点上有未应用的改动;已经记过的保留最早的时间。
func (s *Store) MarkNodePending(nodeID int64, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO node_pending(node_id, since) VALUES(?, ?) ON CONFLICT(node_id) DO NOTHING`,
		nodeID, normalizeTime(at).Unix())
	if err != nil {
		return fmt.Errorf("记录节点 %d 有未应用的改动: %w", nodeID, err)
	}
	return nil
}

// ClearNodePending 在配置成功应用到节点后调用。
func (s *Store) ClearNodePending(nodeID int64) error {
	if _, err := s.db.Exec(`DELETE FROM node_pending WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("清除节点 %d 的未应用标记: %w", nodeID, err)
	}
	return nil
}

// ListNodePending 列出有未应用改动的节点,按节点 id 排序。
func (s *Store) ListNodePending() ([]model.NodePending, error) {
	rows, err := s.db.Query(`SELECT node_id, since FROM node_pending ORDER BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("读取未应用改动: %w", err)
	}
	defer rows.Close()
	out := []model.NodePending{}
	for rows.Next() {
		var p model.NodePending
		var since int64
		if err := rows.Scan(&p.NodeID, &since); err != nil {
			return nil, fmt.Errorf("读取未应用改动: %w", err)
		}
		p.Since = time.Unix(since, 0).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}
