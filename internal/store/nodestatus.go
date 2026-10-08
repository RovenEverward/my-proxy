package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// nodeStatusColumns 是 node_status 表的列顺序,扫描时按同一顺序取值。
const nodeStatusColumns = `node_id, checked_at, reachable, active, version, observed_hash, certificates, error`

func scanNodeStatus(sc rowScanner) (*model.NodeStatus, error) {
	var (
		s       model.NodeStatus
		checked int64
		reach   int
		active  int
		certs   string
	)
	if err := sc.Scan(&s.NodeID, &checked, &reach, &active, &s.Version,
		&s.ObservedHash, &certs, &s.Error); err != nil {
		return nil, err
	}
	s.CheckedAt = time.Unix(checked, 0).UTC()
	s.Reachable = reach != 0
	s.Active = active != 0
	list, err := decodeSlice[model.CertificateInfo](certs)
	if err != nil {
		return nil, fmt.Errorf("解析节点 %d 的证书列表: %w", s.NodeID, err)
	}
	s.Certificates = list
	return &s, nil
}

// UpsertNodeStatus 写入一台节点的观察结果,每节点只留最近一次。
//
// 是 upsert 而不是追加:这张表回答的是"现在怎么样",历史没有意义 —— 要历史
// 就得再加一张表和一个保留策略,而真要看趋势时人问的是某一刻的结论,那个结论
// 在告警日志里有。
//
// 注意这里不碰 nodes 表:deployed_hash 是"我们最后一次推上去的是什么",属于
// 节点自己的属性。让状态写入顺手更新它,等于用观察值覆盖基准值,漂移就永远
// 看不出来了。
func (s *Store) UpsertNodeStatus(st model.NodeStatus) error {
	certs, err := encodeSlice(st.Certificates)
	if err != nil {
		return fmt.Errorf("序列化节点 %d 的证书列表: %w", st.NodeID, err)
	}
	_, err = s.db.Exec(
		`INSERT INTO node_status (node_id, checked_at, reachable, active, version, observed_hash, certificates, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE SET
			checked_at    = excluded.checked_at,
			reachable     = excluded.reachable,
			active        = excluded.active,
			version       = excluded.version,
			observed_hash = excluded.observed_hash,
			certificates  = excluded.certificates,
			error         = excluded.error`,
		st.NodeID, normalizeTime(st.CheckedAt).Unix(), boolToInt(st.Reachable), boolToInt(st.Active),
		st.Version, st.ObservedHash, certs, st.Error)
	if err != nil {
		return fmt.Errorf("写入节点 %d 的状态: %w", st.NodeID, err)
	}
	return nil
}

// GetNodeStatus 取一台节点最近一次的观察结果。
//
// 从没检查过时返回包装了 ErrNotFound 的错误 —— "还没查过"和"查过但连不上"
// 是两件事,调用方要能分开。
func (s *Store) GetNodeStatus(nodeID int64) (*model.NodeStatus, error) {
	st, err := scanNodeStatus(s.db.QueryRow(
		`SELECT `+nodeStatusColumns+` FROM node_status WHERE node_id = ?`, nodeID))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("节点 %d 的状态", nodeID))
	}
	return st, nil
}

// ListNodeStatuses 一次取回所有节点的观察结果,按 node_id 索引。
//
// 列表页要对每台节点显示状态,逐台查会变成 N 次查询;一次取回来的代价是一次
// 全表扫描,而节点数是十几台的量级。
func (s *Store) ListNodeStatuses() (map[int64]model.NodeStatus, error) {
	rows, err := s.db.Query(`SELECT ` + nodeStatusColumns + ` FROM node_status`)
	if err != nil {
		return nil, fmt.Errorf("查询节点状态列表: %w", err)
	}
	defer rows.Close()

	out := map[int64]model.NodeStatus{}
	for rows.Next() {
		st, err := scanNodeStatus(rows)
		if err != nil {
			return nil, fmt.Errorf("读取节点状态记录: %w", err)
		}
		out[st.NodeID] = *st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历节点状态列表: %w", err)
	}
	return out, nil
}

// SetNodeDeployed 记下"我们刚刚把这台节点的配置推上去了"。
//
// 存的是渲染出来那份内容的 sha256,不是节点上读回来的哈希:基准是"我们推了
// 什么",节点上现在是什么由下一次检查去观察。没下发过时 deployed_hash 是空串,
// 漂移判定据此报"未下发",而不是把什么都算成漂移。
func (s *Store) SetNodeDeployed(nodeID int64, hash string, at time.Time) error {
	res, err := s.db.Exec(
		`UPDATE nodes SET deployed_hash = ?, deployed_at = ? WHERE id = ?`,
		hash, normalizeTime(at).Unix(), nodeID)
	if err != nil {
		return fmt.Errorf("记录节点 %d 的下发哈希: %w", nodeID, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("节点 %d", nodeID))
}

// TrafficSampleStats 是采样表的两项统计,给自检用。
type TrafficSampleStats struct {
	// Count 是 traffic_samples 的总行数。
	Count int64
	// LatestAt 是最近一条采样的时间;一行都没有时为 nil,这不是故障。
	LatestAt *time.Time
}

// TrafficSampleStats 报采样表有多少行、最近一条是什么时候。
//
// 只做信息:没有流量就不会有采样行(批 1 的设计是"只在计数变了才写一行"),
// 所以"最近没有采样"不能判成故障,只能报出来让人自己看。
func (s *Store) TrafficSampleStats() (TrafficSampleStats, error) {
	var out TrafficSampleStats
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM traffic_samples`).Scan(&out.Count); err != nil {
		return out, fmt.Errorf("统计流量采样行数: %w", err)
	}

	var latest sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(sampled_at) FROM traffic_samples`).Scan(&latest); err != nil {
		return out, fmt.Errorf("查询最近一条流量采样: %w", err)
	}
	if latest.Valid {
		at := time.Unix(latest.Int64, 0).UTC()
		out.LatestAt = &at
	}
	return out, nil
}
