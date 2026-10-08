package store

import (
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// trafficSampleColumns 是 traffic_samples 的列顺序,扫描时按同一顺序取值。
const trafficSampleColumns = `id, username, node_id, sampled_at, uplink, downlink`

func scanTrafficSample(sc rowScanner) (*model.TrafficSample, error) {
	var (
		sample    model.TrafficSample
		sampledAt int64
	)
	if err := sc.Scan(&sample.ID, &sample.Username, &sample.NodeID, &sampledAt,
		&sample.Uplink, &sample.Downlink); err != nil {
		return nil, err
	}
	sample.SampledAt = time.Unix(sampledAt, 0).UTC()
	return &sample, nil
}

// RecordTrafficSample 追加一条流量采样。
//
// 采样表只追加、不更新:同一用户同一节点会留下很多行,「上次累计值」由
// LatestTrafficSamples 从最近一行读出来,不另设状态字段。sampled_at 按本包惯例
// 截断到秒,写进去与读回来的时间相等。
func (s *Store) RecordTrafficSample(sample model.TrafficSample) error {
	_, err := s.db.Exec(
		`INSERT INTO traffic_samples (username, node_id, sampled_at, uplink, downlink)
		 VALUES (?, ?, ?, ?, ?)`,
		sample.Username, sample.NodeID, normalizeTime(sample.SampledAt).Unix(),
		sample.Uplink, sample.Downlink)
	if err != nil {
		return fmt.Errorf("记录用户 %q 在节点 %d 的流量采样: %w",
			sample.Username, sample.NodeID, err)
	}
	return nil
}

// LatestTrafficSamples 返回该节点上每个用户最近一条采样,按用户名索引。
//
// 这就是「上次累计值」的来源:控制面拿它和本次读数相减得到增量,不需要在别处再存
// 一份会因为重启而失真的状态。
//
// GROUP BY + MAX(id) 用的是 SQLite 的一条明确保证:聚合里只有一个 min()/max() 时,
// 同一 SELECT 里的普通列取自那一行(见 SQLite 文档「Bare columns in an aggregate
// query」)。换成对每行采样做相关子查询也能写,但那个写法在表的行数涨上去之后会
// 变成平方级;这个写法顺着 (node_id, username, sampled_at) 索引扫一遍就够。
//
// 采样按时间追加,所以 id 最大的那行就是最后一次采到的那一行,同一秒内的先后也由
// id 定,不依赖 MAX(sampled_at) 的并列行为。
func (s *Store) LatestTrafficSamples(nodeID int64) (map[string]model.TrafficSample, error) {
	rows, err := s.db.Query(
		`SELECT username, MAX(id), sampled_at, uplink, downlink
		 FROM traffic_samples WHERE node_id = ? GROUP BY username`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("查询节点 %d 的最近流量采样: %w", nodeID, err)
	}
	defer rows.Close()

	out := make(map[string]model.TrafficSample)
	for rows.Next() {
		var (
			sample    model.TrafficSample
			sampledAt int64
		)
		if err := rows.Scan(&sample.Username, &sample.ID, &sampledAt,
			&sample.Uplink, &sample.Downlink); err != nil {
			return nil, fmt.Errorf("读取节点 %d 的流量采样: %w", nodeID, err)
		}
		sample.NodeID = nodeID
		sample.SampledAt = time.Unix(sampledAt, 0).UTC()
		out[sample.Username] = sample
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历节点 %d 的流量采样: %w", nodeID, err)
	}
	return out, nil
}

// ListTrafficSamples 按时间升序返回某用户在某节点上 [from, to] 区间内的采样。
//
// 采样存的是累计值,所以区间用量由区间两端的累计值相减得到(需要区间外紧邻的
// 一条才算得准),曲线也直接取这些行。
func (s *Store) ListTrafficSamples(username string, nodeID int64, from, to time.Time) ([]model.TrafficSample, error) {
	rows, err := s.db.Query(
		`SELECT `+trafficSampleColumns+` FROM traffic_samples
		 WHERE username = ? AND node_id = ? AND sampled_at BETWEEN ? AND ?
		 ORDER BY sampled_at, id`,
		username, nodeID, normalizeTime(from).Unix(), normalizeTime(to).Unix())
	if err != nil {
		return nil, fmt.Errorf("查询用户 %q 在节点 %d 的流量采样: %w", username, nodeID, err)
	}
	defer rows.Close()

	var out []model.TrafficSample
	for rows.Next() {
		sample, err := scanTrafficSample(rows)
		if err != nil {
			return nil, fmt.Errorf("读取用户 %q 在节点 %d 的流量采样: %w", username, nodeID, err)
		}
		out = append(out, *sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历用户 %q 在节点 %d 的流量采样: %w", username, nodeID, err)
	}
	return out, nil
}
