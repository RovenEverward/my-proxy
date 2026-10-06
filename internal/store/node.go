package store

import (
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// nodeColumns 是 nodes 表的列顺序,扫描时按同一顺序取值。
const nodeColumns = `id, name, address, ssh_port, ssh_user, api_port, config_path, service_name`

func scanNode(sc rowScanner) (*model.Node, error) {
	var n model.Node
	if err := sc.Scan(&n.ID, &n.Name, &n.Address, &n.SSHPort, &n.SSHUser,
		&n.APIPort, &n.ConfigPath, &n.ServiceName); err != nil {
		return nil, err
	}
	return &n, nil
}

// CreateNode 新建节点,成功后把数据库分配的 id 回填到 n.ID。
func (s *Store) CreateNode(n *model.Node) error {
	res, err := s.db.Exec(
		`INSERT INTO nodes (name, address, ssh_port, ssh_user, api_port, config_path, service_name)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Address, n.SSHPort, n.SSHUser, n.APIPort, n.ConfigPath, n.ServiceName)
	if err != nil {
		return fmt.Errorf("创建节点 %q: %w", n.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取新建节点 %q 的 id: %w", n.Name, err)
	}
	n.ID = id
	return nil
}

// GetNode 按 id 取节点。不存在时返回包装了 ErrNotFound 的错误。
func (s *Store) GetNode(id int64) (*model.Node, error) {
	n, err := scanNode(s.db.QueryRow(`SELECT `+nodeColumns+` FROM nodes WHERE id = ?`, id))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("节点 %d", id))
	}
	return n, nil
}

// ListNodes 按 id 升序返回全部节点。
func (s *Store) ListNodes() ([]model.Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeColumns + ` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询节点列表: %w", err)
	}
	defer rows.Close()

	var out []model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("读取节点记录: %w", err)
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历节点列表: %w", err)
	}
	return out, nil
}

// UpdateNode 按 n.ID 整体覆盖一条节点记录。id 不存在时返回 ErrNotFound。
func (s *Store) UpdateNode(n *model.Node) error {
	res, err := s.db.Exec(
		`UPDATE nodes
		 SET name = ?, address = ?, ssh_port = ?, ssh_user = ?, api_port = ?, config_path = ?, service_name = ?
		 WHERE id = ?`,
		n.Name, n.Address, n.SSHPort, n.SSHUser, n.APIPort, n.ConfigPath, n.ServiceName, n.ID)
	if err != nil {
		return fmt.Errorf("更新节点 %d: %w", n.ID, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("节点 %d", n.ID))
}

// DeleteNode 删除节点。该节点的 inbound 与它们下面的 host 由外键 ON DELETE
// CASCADE 一并删掉,这里只发一条 DELETE。
func (s *Store) DeleteNode(id int64) error {
	res, err := s.db.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除节点 %d: %w", id, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("节点 %d", id))
}
