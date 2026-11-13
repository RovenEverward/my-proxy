package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// migratePlans 建套餐相关的四张表,并跑一次升级迁移(幂等)。
func (s *Store) migratePlans() error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS plans (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT    NOT NULL UNIQUE,
			note       TEXT    NOT NULL DEFAULT '',
			all_nodes  INTEGER NOT NULL DEFAULT 0,
			is_default INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS plan_nodes (
			plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
			node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			PRIMARY KEY (plan_id, node_id)
		)`,
		`CREATE TABLE IF NOT EXISTS plan_inbounds (
			plan_id    INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
			inbound_id INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
			PRIMARY KEY (plan_id, inbound_id)
		)`,
		// user_plans.plan_id 刻意不带 ON DELETE:还有用户的套餐删不掉,由外键兜底;
		// DeletePlan 会先给出带用户数的中文错误,不用撞上底层约束。
		`CREATE TABLE IF NOT EXISTS user_plans (
			user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			plan_id INTEGER NOT NULL REFERENCES plans(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_plans_plan_id ON user_plans(plan_id)`,
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("套餐建表: %w", err)
		}
	}
	_, err := s.MigratePlans()
	return err
}

// PlanMigrationReport 是升级迁移的结果,供演练与测试使用。
type PlanMigrationReport struct {
	// Executed 表示这次调用真的做了迁移(库中原本没有默认套餐)。
	Executed bool `json:"executed"`
	// DefaultPlanID 是默认套餐的 id。
	DefaultPlanID int64 `json:"default_plan_id,omitempty"`
	// DefaultUsers 是归入默认套餐的用户数。
	DefaultUsers int `json:"default_users"`
	// MigratedPlans 是自动建出来的迁移套餐名 → 用户名。
	MigratedPlans map[string][]string `json:"migrated_plans"`
}

// MigratePlans 执行套餐升级迁移:库中还没有默认套餐时创建默认套餐,并按规则把
// 现有用户挂上去。幂等且单事务:默认套餐已存在时直接返回现状,不会重复迁移;
// 中途失败整体回滚,下次打开重来。
//

// 的入站完全一致:inbound_ids 为空的归默认套餐(全部);非空的不得归默认(否则和
// 全部节点取并集会放大权限),按去重排序后的相同集合分组,每组建一个迁移套餐,
// plan_inbounds 取该集合中仍存在的入站。刻意不清空用户的 inbound_ids:降级回
// 不认套餐的旧版本时,权限仍然和升级前一样。
func (s *Store) MigratePlans() (*PlanMigrationReport, error) {
	rep := &PlanMigrationReport{MigratedPlans: map[string][]string{}}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("开始套餐迁移事务: %w", err)
	}
	defer tx.Rollback()

	var defID int64
	err = tx.QueryRow(`SELECT id FROM plans WHERE is_default = 1`).Scan(&defID)
	switch {
	case err == nil:
		// 已经迁移过:只报现状,不重复动手。
		rep.DefaultPlanID = defID
		if err := tx.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE plan_id = ?`, defID).Scan(&rep.DefaultUsers); err != nil {
			return nil, fmt.Errorf("统计默认套餐用户数: %w", err)
		}
		return rep, tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("查询默认套餐: %w", err)
	}

	rep.Executed = true

	// 1. 默认套餐:all_nodes=1,以后新加的节点自动包含。
	res, err := tx.Exec(`INSERT INTO plans(name, note, all_nodes, is_default) VALUES(?, '', 1, 1)`, defaultPlanName)
	if err != nil {
		return nil, fmt.Errorf("创建默认套餐: %w", err)
	}
	if defID, err = res.LastInsertId(); err != nil {
		return nil, fmt.Errorf("读取默认套餐 id: %w", err)
	}
	rep.DefaultPlanID = defID

	// 2. 读用户与它们的 inbound_ids。
	users, err := listUsersForMigration(tx)
	if err != nil {
		return nil, err
	}

	// 3. 读入站→节点与节点显示名,用来给迁移套餐起名。
	inboundNode, err := listInboundNodes(tx)
	if err != nil {
		return nil, err
	}
	nodeNames, err := listNodeNames(tx)
	if err != nil {
		return nil, err
	}
	meta, err := listLocationMetadata(tx, "nodes")
	if err != nil {
		return nil, fmt.Errorf("读取节点元数据: %w", err)
	}
	display := func(nodeID int64) string {
		if d := model.NodeDisplay(meta[nodeID]); d != "" {
			return d
		}
		return nodeNames[nodeID]
	}

	// 4. inbound_ids 为空的用户归默认套餐。
	for _, up := range users {
		if len(up.ids) != 0 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO user_plans(user_id, plan_id) VALUES(?, ?)`, up.id, defID); err != nil {
			return nil, fmt.Errorf("把用户 %q 归入默认套餐: %w", up.username, err)
		}
		rep.DefaultUsers++
	}

	// 5. 非空集合按去重排序后的集合分组,每组建一个迁移套餐。
	groups, order := groupUsersByInboundSet(users)
	used := map[string]bool{defaultPlanName: true}
	for _, key := range order {
		g := groups[key]
		existing := make([]int64, 0, len(g.canon))
		nodes := map[int64]bool{}
		for _, id := range g.canon {
			if nodeID, ok := inboundNode[id]; ok {
				existing = append(existing, id)
				nodes[nodeID] = true
			}
		}
		name := uniquePlanName(migrationPlanName(nodes, display), used)
		used[name] = true

		res, err := tx.Exec(`INSERT INTO plans(name, note, all_nodes, is_default) VALUES(?, '', 0, 0)`, name)
		if err != nil {
			return nil, fmt.Errorf("创建迁移套餐 %q: %w", name, err)
		}
		planID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("读取迁移套餐 %q 的 id: %w", name, err)
		}
		for _, inboundID := range existing {
			if _, err := tx.Exec(`INSERT INTO plan_inbounds(plan_id, inbound_id) VALUES(?, ?)`, planID, inboundID); err != nil {
				return nil, fmt.Errorf("写入迁移套餐 %q 的入站: %w", name, err)
			}
		}
		names := make([]string, 0, len(g.members))
		for _, up := range g.members {
			if _, err := tx.Exec(`INSERT INTO user_plans(user_id, plan_id) VALUES(?, ?)`, up.id, planID); err != nil {
				return nil, fmt.Errorf("把用户 %q 归入迁移套餐 %q: %w", up.username, name, err)
			}
			names = append(names, up.username)
		}
		rep.MigratedPlans[name] = names
	}
	return rep, tx.Commit()
}

// migratingUser 是迁移时从一个用户身上读到的 id、用户名与入站集合。
type migratingUser struct {
	id       int64
	username string
	ids      []int64
}

// migrationGroup 是共享同一份入站集合的一批用户。
type migrationGroup struct {
	canon   []int64
	members []migratingUser
}

func listUsersForMigration(tx *sql.Tx) ([]migratingUser, error) {
	rows, err := tx.Query(`SELECT id, username, inbound_ids FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("读取用户: %w", err)
	}
	defer rows.Close()
	var users []migratingUser
	for rows.Next() {
		var (
			up  migratingUser
			raw string
		)
		if err := rows.Scan(&up.id, &up.username, &raw); err != nil {
			return nil, fmt.Errorf("读取用户记录: %w", err)
		}
		if up.ids, err = decodeSlice[int64](raw); err != nil {
			return nil, fmt.Errorf("解析用户 %q 的 inbound_ids: %w", up.username, err)
		}
		users = append(users, up)
	}
	return users, rows.Err()
}

func listInboundNodes(tx *sql.Tx) (map[int64]int64, error) {
	rows, err := tx.Query(`SELECT id, node_id FROM inbounds`)
	if err != nil {
		return nil, fmt.Errorf("读取入站: %w", err)
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, nodeID int64
		if err := rows.Scan(&id, &nodeID); err != nil {
			return nil, fmt.Errorf("读入站记录: %w", err)
		}
		out[id] = nodeID
	}
	return out, rows.Err()
}

func listNodeNames(tx *sql.Tx) (map[int64]string, error) {
	rows, err := tx.Query(`SELECT id, name FROM nodes`)
	if err != nil {
		return nil, fmt.Errorf("读取节点: %w", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("读节点记录: %w", err)
		}
		out[id] = name
	}
	return out, rows.Err()
}

// groupUsersByInboundSet 按去重排序后的入站集合分组,保持首次出现的顺序,
// 让迁移建出来的套餐顺序稳定。group 的键是集合的字符串形式。
func groupUsersByInboundSet(users []migratingUser) (map[string]*migrationGroup, []string) {
	groups := map[string]*migrationGroup{}
	var order []string
	for _, up := range users {
		if len(up.ids) == 0 {
			continue
		}
		canon := canonicalInboundIDs(up.ids)
		key := inboundSetKey(canon)
		g, ok := groups[key]
		if !ok {
			g = &migrationGroup{canon: canon}
			groups[key] = g
			order = append(order, key)
		}
		g.members = append(g.members, up)
	}
	return groups, order
}

// canonicalInboundIDs 去重并升序排列入站 id;非正数丢掉。
func canonicalInboundIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// inboundSetKey 把集合拼成可比较的键。id 是数字,用逗号分隔不会有歧义。
func inboundSetKey(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprint(id))
	}
	return strings.Join(parts, ",")
}

// migrationPlanName 用节点显示名拼迁移套餐名,形如
// 「迁移 · 🇺🇸 洛杉矶 A + 🇯🇵 东京 A」;没有任何还存在的节点时用「迁移套餐」。
func migrationPlanName(nodes map[int64]bool, display func(int64) string) string {
	ids := make([]int64, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if d := display(id); d != "" {
			names = append(names, d)
		}
	}
	if len(names) == 0 {
		return "迁移套餐"
	}
	return "迁移 · " + strings.Join(names, " + ")
}

// uniquePlanName 给重名补一个序号,如「迁移 · A（2）」。
func uniquePlanName(base string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s（%d）", base, i)
		if !used[candidate] {
			return candidate
		}
	}
}
