package store

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 用户活跃与节点采样时间。
//
// user_activity 记「某人 × 某节点」最后一次有流量的时刻,用来数最近活跃人数;
// node_sampled 记每台节点最后一次被采样的时刻,用来显示数据新鲜度。两者都随节点
// 级联删除:节点都没了,这些时间也没有意义。
func (s *Store) migrateActivity() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS user_activity (
		username       TEXT    NOT NULL,
		node_id        INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		last_active_at INTEGER NOT NULL,
		PRIMARY KEY(username, node_id)
	)`); err != nil {
		return fmt.Errorf("建 user_activity 表: %w", err)
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS node_sampled (
		node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
		at      INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("建 node_sampled 表: %w", err)
	}

	// 回填只做一次:做过就在 routing_state 的 migration_marks 里记一笔(与分流迁移
	// 用同一处落点),以后启动不再扫 traffic_samples。记号只在回填写完之后才写。
	done, err := s.migrationDone(userActivityBackfillKey)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	// 按 (用户, 节点, 核心) 用 LAG 算相邻采样的累计值差,差为正的就是那一刻有流量;
	// 重启行与零基准行的差不是正数,自然被排除。只认 users 里还有的人:已经删掉的
	// 用户留着名字的历史采样不该再长出活跃时间,否则同名用户再建出来会捡回旧时间。
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("回填 user_activity: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO user_activity(username,node_id,last_active_at)
		SELECT t.username,t.node_id,MAX(t.sampled_at) FROM (
			SELECT username,node_id,sampled_at,(uplink+downlink)-LAG(uplink+downlink) OVER (PARTITION BY username,node_id,core ORDER BY id) AS d
			FROM traffic_samples
		) t JOIN nodes n ON n.id=t.node_id JOIN users u ON u.username=t.username
		WHERE t.d > 0
		GROUP BY t.username,t.node_id`); err != nil {
		return fmt.Errorf("回填 user_activity: %w", err)
	}
	if err := markMigrationDone(tx, userActivityBackfillKey); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 user_activity 回填: %w", err)
	}
	return nil
}

// userActivityBackfillKey 是 user_activity 回填在 routing_state.migration_marks 里的名字。
const userActivityBackfillKey = "user_activity_backfill"

// markUserActive 记下用户在这台节点上刚有流量。只往前走,不会被更早的时间覆盖。
// 它与流量采样在同一个事务里写,提交后活跃时间不会落后于采样。
func markUserActive(tx *sql.Tx, username string, nodeID int64, at int64) error {
	_, err := tx.Exec(`INSERT INTO user_activity(username,node_id,last_active_at) VALUES(?,?,?)
		ON CONFLICT(username,node_id) DO UPDATE SET last_active_at=MAX(last_active_at, excluded.last_active_at)`,
		username, nodeID, at)
	if err != nil {
		return fmt.Errorf("记录用户 %q 在节点 %d 的活跃时间: %w", username, nodeID, err)
	}
	return nil
}

// MarkNodeSampled 记下节点最后一次被采样的时刻(同一台节点一行,后来的覆盖前面的)。
func (s *Store) MarkNodeSampled(nodeID int64, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO node_sampled(node_id, at) VALUES(?, ?)
		ON CONFLICT(node_id) DO UPDATE SET at=excluded.at`,
		nodeID, normalizeTime(at).Unix())
	if err != nil {
		return fmt.Errorf("记录节点 %d 的采样时间: %w", nodeID, err)
	}
	return nil
}

// NodeSampledTimes 返回每台节点的最后采样时刻。
func (s *Store) NodeSampledTimes() (map[int64]time.Time, error) {
	rows, err := s.db.Query(`SELECT node_id, at FROM node_sampled`)
	if err != nil {
		return nil, fmt.Errorf("读取节点采样时间: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]time.Time)
	for rows.Next() {
		var nodeID, at int64
		if err := rows.Scan(&nodeID, &at); err != nil {
			return nil, fmt.Errorf("读取节点采样时间: %w", err)
		}
		out[nodeID] = time.Unix(at, 0).UTC()
	}
	return out, rows.Err()
}

// ListUserActivity 列出每人每节点最后一次有流量的时刻,按用户名、节点 id 排序。
func (s *Store) ListUserActivity() ([]model.UserActivity, error) {
	rows, err := s.db.Query(`SELECT username, node_id, last_active_at FROM user_activity ORDER BY username, node_id`)
	if err != nil {
		return nil, fmt.Errorf("读取用户活跃时间: %w", err)
	}
	defer rows.Close()
	out := []model.UserActivity{}
	for rows.Next() {
		var a model.UserActivity
		var at int64
		if err := rows.Scan(&a.Username, &a.NodeID, &at); err != nil {
			return nil, fmt.Errorf("读取用户活跃时间: %w", err)
		}
		a.LastActiveAt = time.Unix(at, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountActiveSince 数在 t 之后(含 t)在某台节点上有过流量的人数,同一个人多台
// 节点只算一个。
func (s *Store) CountActiveSince(t time.Time) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT username) FROM user_activity WHERE last_active_at >= ?`,
		normalizeTime(t).Unix()).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计活跃人数: %w", err)
	}
	return n, nil
}

// ActivitySegments 把某人的采样按节点切成若干段连续使用:相邻两次有流量的采样
// 相隔不超过 gap 就算同一段。只算 from 之后有流量的采样,返回最新的 max 段
// (End 降序,同一时刻按节点 id 升序)。max <= 0 表示不限段数。
//
// 增量由窗口函数相对上一条采样算出,from 之后的第一条采样没有上一条可减,不计入;
// 要连同第一段从起点算起,from 应比第一段的起点再早一些。
func (s *Store) ActivitySegments(username string, from time.Time, gap time.Duration, max int) ([]model.ActivitySegment, error) {
	rows, err := s.db.Query(`SELECT node_id, sampled_at, d FROM (
			SELECT node_id, sampled_at, (uplink+downlink)-LAG(uplink+downlink) OVER (PARTITION BY node_id,core ORDER BY id) AS d
			FROM traffic_samples WHERE username=?
		) WHERE d > 0 AND sampled_at >= ? ORDER BY node_id, sampled_at`,
		username, normalizeTime(from).Unix())
	if err != nil {
		return nil, fmt.Errorf("读取用户 %q 的活跃采样: %w", username, err)
	}
	var nodeIDs []int64
	var times []int64
	var bytes []int64
	for rows.Next() {
		var nodeID, at, d int64
		if err := rows.Scan(&nodeID, &at, &d); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取用户 %q 的活跃采样: %w", username, err)
		}
		nodeIDs = append(nodeIDs, nodeID)
		times = append(times, at)
		bytes = append(bytes, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历用户 %q 的活跃采样: %w", username, err)
	}
	// 下面不再查库,连接可以放掉了。
	rows.Close()

	gapSec := int64(gap / time.Second)
	if gap > 0 && gap < time.Second {
		gapSec = 1
	}
	out := []model.ActivitySegment{}
	var cur *model.ActivitySegment
	for i := range times {
		if cur != nil && nodeIDs[i] == cur.NodeID && times[i]-times[i-1] <= gapSec {
			cur.End = time.Unix(times[i], 0).UTC()
			cur.Bytes += bytes[i]
			continue
		}
		if cur != nil {
			out = append(out, *cur)
		}
		cur = &model.ActivitySegment{
			NodeID: nodeIDs[i],
			Start:  time.Unix(times[i], 0).UTC(),
			End:    time.Unix(times[i], 0).UTC(),
			Bytes:  bytes[i],
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}

	// 最新的在前;同一结束时刻按节点 id 升序,结果稳定。
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].End.Equal(out[j].End) {
			return out[i].End.After(out[j].End)
		}
		return out[i].NodeID < out[j].NodeID
	})
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out, nil
}
