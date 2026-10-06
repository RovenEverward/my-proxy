package store

import (
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// hostColumns 是 hosts 表的列顺序,扫描时按同一顺序取值。
const hostColumns = `id, inbound_id, remark, address, port, sni, host, path, alpn`

func scanHost(sc rowScanner) (*model.Host, error) {
	var (
		h    model.Host
		alpn string
	)
	if err := sc.Scan(&h.ID, &h.InboundID, &h.Remark, &h.Address, &h.Port,
		&h.SNI, &h.Host, &h.Path, &alpn); err != nil {
		return nil, err
	}
	var err error
	if h.ALPN, err = decodeSlice[string](alpn); err != nil {
		return nil, fmt.Errorf("Host %d 的 alpn: %w", h.ID, err)
	}
	return &h, nil
}

// CreateHost 新建 Host,成功后把数据库分配的 id 回填到 h.ID。
// inbound_id 不存在时由外键报错。
func (s *Store) CreateHost(h *model.Host) error {
	alpn, err := encodeSlice(h.ALPN)
	if err != nil {
		return fmt.Errorf("序列化 Host %q 的 alpn: %w", h.Remark, err)
	}

	res, err := s.db.Exec(
		`INSERT INTO hosts (inbound_id, remark, address, port, sni, host, path, alpn)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		h.InboundID, h.Remark, h.Address, h.Port, h.SNI, h.Host, h.Path, alpn)
	if err != nil {
		return fmt.Errorf("创建 Host %q: %w", h.Remark, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取新建 Host %q 的 id: %w", h.Remark, err)
	}
	h.ID = id
	return nil
}

// GetHost 按 id 取 Host。不存在时返回包装了 ErrNotFound 的错误。
func (s *Store) GetHost(id int64) (*model.Host, error) {
	h, err := scanHost(s.db.QueryRow(`SELECT `+hostColumns+` FROM hosts WHERE id = ?`, id))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("Host %d", id))
	}
	return h, nil
}

// ListHosts 返回 Host 列表,按 id 升序;inboundID 为 0 表示不限入站。
func (s *Store) ListHosts(inboundID int64) ([]model.Host, error) {
	q := `SELECT ` + hostColumns + ` FROM hosts`
	var args []any
	if inboundID != 0 {
		q += ` WHERE inbound_id = ?`
		args = append(args, inboundID)
	}
	q += ` ORDER BY id`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 Host 列表: %w", err)
	}
	defer rows.Close()

	var out []model.Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, fmt.Errorf("读取 Host 记录: %w", err)
		}
		out = append(out, *h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 Host 列表: %w", err)
	}
	return out, nil
}

// UpdateHost 按 h.ID 整体覆盖一条 Host 记录。id 不存在时返回 ErrNotFound。
func (s *Store) UpdateHost(h *model.Host) error {
	alpn, err := encodeSlice(h.ALPN)
	if err != nil {
		return fmt.Errorf("序列化 Host %q 的 alpn: %w", h.Remark, err)
	}

	res, err := s.db.Exec(
		`UPDATE hosts
		 SET inbound_id = ?, remark = ?, address = ?, port = ?, sni = ?, host = ?, path = ?, alpn = ?
		 WHERE id = ?`,
		h.InboundID, h.Remark, h.Address, h.Port, h.SNI, h.Host, h.Path, alpn, h.ID)
	if err != nil {
		return fmt.Errorf("更新 Host %d: %w", h.ID, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("Host %d", h.ID))
}

// DeleteHost 删除一条 Host。
func (s *Store) DeleteHost(id int64) error {
	res, err := s.db.Exec(`DELETE FROM hosts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除 Host %d: %w", id, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("Host %d", id))
}
