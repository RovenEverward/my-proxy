package store

import (
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// inboundColumns 是 inbounds 表的列顺序,扫描时按同一顺序取值。
const inboundColumns = `id, node_id, tag, protocol, listen, port, network, security,
	ws_path, grpc_service, sni, alpn, fingerprint, cert_file, key_file,
	reality_dest, reality_priv, reality_pub, short_ids, flow,
	xhttp_path, xhttp_host, xhttp_mode, wg_secret, wg_address, wg_mtu,
	mux, fragment, max_users`

func scanInbound(sc rowScanner) (*model.Inbound, error) {
	var (
		i              model.Inbound
		alpn, shortIDs string
		mux, fragment  int
	)
	if err := sc.Scan(&i.ID, &i.NodeID, &i.Tag, &i.Protocol, &i.Listen, &i.Port,
		&i.Network, &i.Security, &i.WSPath, &i.GRPCService, &i.SNI, &alpn,
		&i.Fingerprint, &i.CertFile, &i.KeyFile, &i.RealityDest, &i.RealityPriv,
		&i.RealityPub, &shortIDs, &i.Flow,
		&i.XHTTPPath, &i.XHTTPHost, &i.XHTTPMode,
		&i.WGSecretKey, &i.WGAddress, &i.WGMTU, &mux, &fragment, &i.MaxUsers); err != nil {
		return nil, err
	}
	i.Mux = mux != 0
	i.Fragment = fragment != 0
	var err error
	if i.ALPN, err = decodeSlice[string](alpn); err != nil {
		return nil, fmt.Errorf("入站 %d 的 alpn: %w", i.ID, err)
	}
	if i.ShortIDs, err = decodeSlice[string](shortIDs); err != nil {
		return nil, fmt.Errorf("入站 %d 的 short_ids: %w", i.ID, err)
	}
	return &i, nil
}

// CreateInbound 新建入站,成功后把数据库分配的 id 回填到 i.ID。
// node_id 不存在时由外键报错。
func (s *Store) CreateInbound(i *model.Inbound) error {
	alpn, err := encodeSlice(i.ALPN)
	if err != nil {
		return fmt.Errorf("序列化入站 %q 的 alpn: %w", i.Tag, err)
	}
	shortIDs, err := encodeSlice(i.ShortIDs)
	if err != nil {
		return fmt.Errorf("序列化入站 %q 的 short_ids: %w", i.Tag, err)
	}

	res, err := s.db.Exec(
		`INSERT INTO inbounds (node_id, tag, protocol, listen, port, network, security,
			ws_path, grpc_service, sni, alpn, fingerprint, cert_file, key_file,
			reality_dest, reality_priv, reality_pub, short_ids, flow,
			xhttp_path, xhttp_host, xhttp_mode, wg_secret, wg_address, wg_mtu,
			mux, fragment, max_users)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		         ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.NodeID, i.Tag, string(i.Protocol), i.Listen, i.Port, string(i.Network),
		string(i.Security), i.WSPath, i.GRPCService, i.SNI, alpn, i.Fingerprint,
		i.CertFile, i.KeyFile, i.RealityDest, i.RealityPriv, i.RealityPub,
		shortIDs, i.Flow,
		i.XHTTPPath, i.XHTTPHost, i.XHTTPMode,
		i.WGSecretKey, i.WGAddress, i.WGMTU,
		boolToInt(i.Mux), boolToInt(i.Fragment), i.MaxUsers)
	if err != nil {
		return fmt.Errorf("创建入站 %q: %w", i.Tag, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取新建入站 %q 的 id: %w", i.Tag, err)
	}
	i.ID = id
	return nil
}

// GetInbound 按 id 取入站。不存在时返回包装了 ErrNotFound 的错误。
func (s *Store) GetInbound(id int64) (*model.Inbound, error) {
	i, err := scanInbound(s.db.QueryRow(`SELECT `+inboundColumns+` FROM inbounds WHERE id = ?`, id))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("入站 %d", id))
	}
	return i, nil
}

// ListInbounds 返回入站列表,按 id 升序;nodeID 为 0 表示不限节点。
func (s *Store) ListInbounds(nodeID int64) ([]model.Inbound, error) {
	q := `SELECT ` + inboundColumns + ` FROM inbounds`
	var args []any
	if nodeID != 0 {
		q += ` WHERE node_id = ?`
		args = append(args, nodeID)
	}
	q += ` ORDER BY id`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询入站列表: %w", err)
	}
	defer rows.Close()

	var out []model.Inbound
	for rows.Next() {
		i, err := scanInbound(rows)
		if err != nil {
			return nil, fmt.Errorf("读取入站记录: %w", err)
		}
		out = append(out, *i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历入站列表: %w", err)
	}
	return out, nil
}

// UpdateInbound 按 i.ID 整体覆盖一条入站记录。id 不存在时返回 ErrNotFound。
func (s *Store) UpdateInbound(i *model.Inbound) error {
	alpn, err := encodeSlice(i.ALPN)
	if err != nil {
		return fmt.Errorf("序列化入站 %q 的 alpn: %w", i.Tag, err)
	}
	shortIDs, err := encodeSlice(i.ShortIDs)
	if err != nil {
		return fmt.Errorf("序列化入站 %q 的 short_ids: %w", i.Tag, err)
	}

	res, err := s.db.Exec(
		`UPDATE inbounds
		 SET node_id = ?, tag = ?, protocol = ?, listen = ?, port = ?, network = ?, security = ?,
		     ws_path = ?, grpc_service = ?, sni = ?, alpn = ?, fingerprint = ?, cert_file = ?, key_file = ?,
		     reality_dest = ?, reality_priv = ?, reality_pub = ?, short_ids = ?, flow = ?,
		     xhttp_path = ?, xhttp_host = ?, xhttp_mode = ?,
		     wg_secret = ?, wg_address = ?, wg_mtu = ?, mux = ?, fragment = ?, max_users = ?
		 WHERE id = ?`,
		i.NodeID, i.Tag, string(i.Protocol), i.Listen, i.Port, string(i.Network),
		string(i.Security), i.WSPath, i.GRPCService, i.SNI, alpn, i.Fingerprint,
		i.CertFile, i.KeyFile, i.RealityDest, i.RealityPriv, i.RealityPub,
		shortIDs, i.Flow,
		i.XHTTPPath, i.XHTTPHost, i.XHTTPMode,
		i.WGSecretKey, i.WGAddress, i.WGMTU,
		boolToInt(i.Mux), boolToInt(i.Fragment), i.MaxUsers, i.ID)
	if err != nil {
		return fmt.Errorf("更新入站 %d: %w", i.ID, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("入站 %d", i.ID))
}

// DeleteInbound 删除入站。它下面的 host 由外键 ON DELETE CASCADE 一并删掉,
// 这里只发一条 DELETE。
func (s *Store) DeleteInbound(id int64) error {
	res, err := s.db.Exec(`DELETE FROM inbounds WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除入站 %d: %w", id, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("入站 %d", id))
}
