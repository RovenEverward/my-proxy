package store

import (
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// mirrorColumns 是 sub_mirrors 表的列顺序,扫描时按同一顺序取值。
const mirrorColumns = `id, node_id, port, upstream, remark`

func scanMirror(sc rowScanner) (*model.SubMirror, error) {
	var m model.SubMirror
	if err := sc.Scan(&m.ID, &m.NodeID, &m.Port, &m.Upstream, &m.Remark); err != nil {
		return nil, err
	}
	return &m, nil
}

// CreateSubMirror 新建订阅镜像,成功后回填 id。node_id 不存在时由外键报错。
func (s *Store) CreateSubMirror(m *model.SubMirror) error {
	res, err := s.db.Exec(
		`INSERT INTO sub_mirrors (node_id, port, upstream, remark) VALUES (?, ?, ?, ?)`,
		m.NodeID, m.Port, m.Upstream, m.Remark)
	if err != nil {
		return fmt.Errorf("创建订阅镜像(节点 %d 端口 %d): %w", m.NodeID, m.Port, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取新建订阅镜像 id: %w", err)
	}
	m.ID = id
	return nil
}

// ListSubMirrors 返回镜像列表,按 id 升序;nodeID 为 0 表示不限节点。
func (s *Store) ListSubMirrors(nodeID int64) ([]model.SubMirror, error) {
	q := `SELECT ` + mirrorColumns + ` FROM sub_mirrors`
	var args []any
	if nodeID != 0 {
		q += ` WHERE node_id = ?`
		args = append(args, nodeID)
	}
	q += ` ORDER BY id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询订阅镜像列表: %w", err)
	}
	defer rows.Close()
	var out []model.SubMirror
	for rows.Next() {
		m, err := scanMirror(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// GetSubMirror 按 id 取镜像,不存在时报 ErrNotFound。
func (s *Store) GetSubMirror(id int64) (*model.SubMirror, error) {
	m, err := scanMirror(s.db.QueryRow(`SELECT `+mirrorColumns+` FROM sub_mirrors WHERE id = ?`, id))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("订阅镜像 %d", id))
	}
	return m, nil
}

// DeleteSubMirror 删除镜像,不存在时报 ErrNotFound。
func (s *Store) DeleteSubMirror(id int64) error {
	res, err := s.db.Exec(`DELETE FROM sub_mirrors WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除订阅镜像 %d: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return wrapNotFound(ErrNotFound, fmt.Sprintf("订阅镜像 %d", id))
	}
	return nil
}

// UpdateSubMirrorUpstream 更新镜像的转发目标(控制面迁移后跟随)。
func (s *Store) UpdateSubMirrorUpstream(id int64, upstream string) error {
	res, err := s.db.Exec(`UPDATE sub_mirrors SET upstream = ? WHERE id = ?`, upstream, id)
	if err != nil {
		return fmt.Errorf("更新订阅镜像 %d 的上游: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return wrapNotFound(ErrNotFound, fmt.Sprintf("订阅镜像 %d", id))
	}
	return nil
}

// UpdateSubMirrorRemark 更新镜像备注。
func (s *Store) UpdateSubMirrorRemark(id int64, remark string) error {
	res, err := s.db.Exec(`UPDATE sub_mirrors SET remark = ? WHERE id = ?`, remark, id)
	if err != nil {
		return fmt.Errorf("更新订阅镜像 %d 的备注: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return wrapNotFound(ErrNotFound, fmt.Sprintf("订阅镜像 %d", id))
	}
	return nil
}
