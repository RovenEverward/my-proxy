package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 套餐(权限组)的存储。
//
// users 表刻意不加 plan_id 列:用户的整体 PUT 路径保持不动,归属放在
// user_plans 这张一人一行的关联表里。

// defaultPlanName 是系统自带默认套餐的名字。
const defaultPlanName = "默认套餐"

// 套餐相关的错误。
var (
	// ErrDefaultPlan 表示试图删除默认套餐。
	ErrDefaultPlan = errors.New("默认套餐不能删除")
	// ErrPlanNotFound 表示指定的套餐不存在。
	ErrPlanNotFound = errors.New("套餐不存在")
)

// PlanInUseError 是删除仍被用户挂着的套餐时的错误,带上用户数供调用方提示。
type PlanInUseError struct{ Users int }

func (e *PlanInUseError) Error() string {
	return fmt.Sprintf("还有 %d 个用户在用这个套餐,不能删除:请用 reassign 指定一个具体套餐(可以是默认套餐),先把这些用户改挂过去再删", e.Users)
}

// planColumns 是 plans 表的列顺序,扫描时按同一顺序取值。
const planColumns = `id, name, note, all_nodes, is_default`

func scanPlan(sc rowScanner) (*model.Plan, error) {
	var (
		p                   model.Plan
		allNodes, isDefault int
	)
	if err := sc.Scan(&p.ID, &p.Name, &p.Note, &allNodes, &isDefault); err != nil {
		return nil, err
	}
	p.AllNodes = allNodes != 0
	p.IsDefault = isDefault != 0
	return &p, nil
}

// ListPlans 按 id 升序返回全部套餐,并带上节点、入站与用户数。
func (s *Store) ListPlans() ([]model.Plan, error) {
	rows, err := s.db.Query(`SELECT ` + planColumns + ` FROM plans ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询套餐列表: %w", err)
	}
	out := []model.Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取套餐记录: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历套餐列表: %w", err)
	}
	// 下面要查关联表,必须先放掉这条连接(本包只有一条)。
	rows.Close()
	if err := s.attachPlanDetails(out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetPlan 按 id 取套餐。不存在时返回包装了 ErrNotFound 的错误。
func (s *Store) GetPlan(id int64) (*model.Plan, error) {
	p, err := scanPlan(s.db.QueryRow(`SELECT `+planColumns+` FROM plans WHERE id = ?`, id))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("套餐 %d", id))
	}
	ps := []model.Plan{*p}
	if err := s.attachPlanDetails(ps); err != nil {
		return nil, err
	}
	return &ps[0], nil
}

// attachPlanDetails 把 plan_nodes / plan_inbounds / 用户数补进一批套餐。
func (s *Store) attachPlanDetails(plans []model.Plan) error {
	if len(plans) == 0 {
		return nil
	}
	idx := make(map[int64]int, len(plans))
	for i := range plans {
		idx[plans[i].ID] = i
	}

	rows, err := s.db.Query(`SELECT plan_id, node_id FROM plan_nodes ORDER BY plan_id, node_id`)
	if err != nil {
		return fmt.Errorf("查询套餐节点: %w", err)
	}
	for rows.Next() {
		var planID, nodeID int64
		if err := rows.Scan(&planID, &nodeID); err != nil {
			rows.Close()
			return fmt.Errorf("读取套餐节点: %w", err)
		}
		if i, ok := idx[planID]; ok {
			plans[i].NodeIDs = append(plans[i].NodeIDs, nodeID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("遍历套餐节点: %w", err)
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT plan_id, inbound_id FROM plan_inbounds ORDER BY plan_id, inbound_id`)
	if err != nil {
		return fmt.Errorf("查询套餐入站: %w", err)
	}
	for rows.Next() {
		var planID, inboundID int64
		if err := rows.Scan(&planID, &inboundID); err != nil {
			rows.Close()
			return fmt.Errorf("读取套餐入站: %w", err)
		}
		if i, ok := idx[planID]; ok {
			plans[i].InboundIDs = append(plans[i].InboundIDs, inboundID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("遍历套餐入站: %w", err)
	}
	rows.Close()

	// 用户数、活跃数与额外授权数按套餐统计。活跃数必须走 model.User.Status 的
	// 同一份判定(停用 > 过期 > 超限 > 可用),所以把状态相关的几列读回来在内存里
	// 算,而不是在 SQL 里把状态优先级再抄一遍。
	rows, err = s.db.Query(`
		SELECT up.plan_id, u.enabled, u.expire_at, u.data_limit, u.used_traffic, u.inbound_ids
		FROM user_plans up JOIN users u ON u.id = up.user_id`)
	if err != nil {
		return fmt.Errorf("统计套餐用户数: %w", err)
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var (
			planID      int64
			enabled     int
			expireAt    sql.NullInt64
			limit, used int64
			inboundIDs  string
		)
		if err := rows.Scan(&planID, &enabled, &expireAt, &limit, &used, &inboundIDs); err != nil {
			return fmt.Errorf("读取套餐用户数: %w", err)
		}
		i, ok := idx[planID]
		if !ok {
			continue
		}
		u := model.User{Enabled: enabled != 0, DataLimit: limit, UsedTraffic: used}
		if expireAt.Valid {
			t := time.Unix(expireAt.Int64, 0).UTC()
			u.ExpireAt = &t
		}
		plans[i].Users++
		if u.Status(now) == model.StatusActive {
			plans[i].ActiveUsers++
		}
		ids, err := decodeSlice[int64](inboundIDs)
		if err != nil {
			return fmt.Errorf("解析套餐 %d 用户的额外授权: %w", planID, err)
		}
		if len(ids) > 0 {
			plans[i].ExtraAuth++
		}
	}
	return rows.Err()
}

// CreatePlan 新建套餐,成功后回填 p.ID。is_default 由系统自己维护,这里忽略。
func (s *Store) CreatePlan(p *model.Plan) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("创建套餐 %q: %w", p.Name, err)
	}
	defer tx.Rollback()
	if err := checkPlanRefs(tx, p); err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO plans(name, note, all_nodes, is_default) VALUES(?, ?, ?, 0)`,
		p.Name, p.Note, boolToInt(p.AllNodes))
	if isUnique(err) {
		return fmt.Errorf("套餐名 %q: %w", p.Name, ErrDuplicate)
	}
	if err != nil {
		return fmt.Errorf("创建套餐 %q: %w", p.Name, err)
	}
	if p.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("读取新建套餐 %q 的 id: %w", p.Name, err)
	}
	if err := replacePlanNodes(tx, p.ID, p.NodeIDs); err != nil {
		return err
	}
	if err := replacePlanInbounds(tx, p.ID, p.InboundIDs); err != nil {
		return err
	}
	p.IsDefault = false
	return tx.Commit()
}

// UpdatePlan 按 p.ID 整体覆盖套餐的节点与入站。is_default 不能被改:默认套餐始终
// 包含全部节点,即使调用方把 AllNodes 传成 false。
func (s *Store) UpdatePlan(p *model.Plan) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("更新套餐 %d: %w", p.ID, err)
	}
	defer tx.Rollback()

	var isDefault int
	err = tx.QueryRow(`SELECT is_default FROM plans WHERE id = ?`, p.ID).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("未找到套餐 %d: %w", p.ID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("读取套餐 %d: %w", p.ID, err)
	}
	p.IsDefault = isDefault != 0
	if p.IsDefault {
		p.AllNodes = true
	}
	if err := checkPlanRefs(tx, p); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE plans SET name = ?, note = ?, all_nodes = ? WHERE id = ?`,
		p.Name, p.Note, boolToInt(p.AllNodes), p.ID)
	if isUnique(err) {
		return fmt.Errorf("套餐名 %q: %w", p.Name, ErrDuplicate)
	}
	if err != nil {
		return fmt.Errorf("更新套餐 %d: %w", p.ID, err)
	}
	if err := affectedOrNotFound(res, fmt.Sprintf("套餐 %d", p.ID)); err != nil {
		return err
	}
	if err := replacePlanNodes(tx, p.ID, p.NodeIDs); err != nil {
		return err
	}
	if err := replacePlanInbounds(tx, p.ID, p.InboundIDs); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePlan 删除套餐。默认套餐不能删;还有用户挂着时返回 *PlanInUseError,
// 什么都不删 —— 不允许悄悄把这些用户变成「全部可用」。
func (s *Store) DeletePlan(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("删除套餐 %d: %w", id, err)
	}
	defer tx.Rollback()

	var isDefault int
	err = tx.QueryRow(`SELECT is_default FROM plans WHERE id = ?`, id).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("未找到套餐 %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("读取套餐 %d: %w", id, err)
	}
	if isDefault != 0 {
		return ErrDefaultPlan
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE plan_id = ?`, id).Scan(&n); err != nil {
		return fmt.Errorf("统计套餐 %d 的用户数: %w", id, err)
	}
	if n > 0 {
		return &PlanInUseError{Users: n}
	}
	res, err := tx.Exec(`DELETE FROM plans WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除套餐 %d: %w", id, err)
	}
	if err := affectedOrNotFound(res, fmt.Sprintf("套餐 %d", id)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePlanReassign 删除套餐,并与用户的改挂/摘除在同一个事务里完成:
//
//   - reassignTo 非空:先把挂着该套餐的用户整体改挂到 reassignTo;
//   - 没有 reassignTo(不论 detach)且还有用户挂着:返回 *PlanInUseError,
//     什么都不动。不允许摘掉套餐后再删:没挂套餐、也没单独勾入站的用户会按旧
//     语义回落到全部入站,权限被悄悄放大。detach 只为兼容签名保留,不再生效。
//
// 默认套餐不能删。删除与改挂必须原子:先改挂再删,中途失败整体回滚,不会出现
// 「用户改挂了、套餐还在」或「套餐删了、用户指向不存在的套餐」这两种中间态。
func (s *Store) DeletePlanReassign(id int64, reassignTo *int64, detach bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("删除套餐 %d: %w", id, err)
	}
	defer tx.Rollback()

	var isDefault int
	err = tx.QueryRow(`SELECT is_default FROM plans WHERE id = ?`, id).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("未找到套餐 %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("读取套餐 %d: %w", id, err)
	}
	if isDefault != 0 {
		return ErrDefaultPlan
	}

	switch {
	case reassignTo != nil:
		if *reassignTo == id {
			return errors.New("不能把用户改挂到正在删除的套餐")
		}
		if err := planExists(tx, *reassignTo); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE user_plans SET plan_id = ? WHERE plan_id = ?`, *reassignTo, id); err != nil {
			return fmt.Errorf("把用户从套餐 %d 改挂到 %d: %w", id, *reassignTo, err)
		}
	default:
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE plan_id = ?`, id).Scan(&n); err != nil {
			return fmt.Errorf("统计套餐 %d 的用户数: %w", id, err)
		}
		if n > 0 {
			return &PlanInUseError{Users: n}
		}
	}

	res, err := tx.Exec(`DELETE FROM plans WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除套餐 %d: %w", id, err)
	}
	if err := affectedOrNotFound(res, fmt.Sprintf("套餐 %d", id)); err != nil {
		return err
	}
	return tx.Commit()
}

// PlanAuthInbounds 返回套餐此刻的入站授权:allNodes 为真表示全部入站;否则是
// 显式授权集合(节点展开成它当前的全部入站,再加上单独勾选的入站)。
//
// 它复用 loadPlanAuth 那一份解释,调用方(比如套餐预览)不用自己再算一遍,
// 也就不会和 AllowsInbound 的判定出现分歧。
func (s *Store) PlanAuthInbounds(planID int64) (allNodes bool, ids []int64, err error) {
	auth, err := s.loadPlanAuth()
	if err != nil {
		return false, nil, err
	}
	pa, ok := auth[planID]
	if !ok {
		return false, nil, fmt.Errorf("未找到套餐 %d: %w", planID, ErrPlanNotFound)
	}
	if pa.allNodes {
		return true, nil, nil
	}
	ids = make([]int64, 0, len(pa.inbounds))
	for id := range pa.inbounds {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return false, ids, nil
}

// SetUserPlan 把用户改挂到某个套餐;planID 为 nil 表示摘掉套餐,回到旧语义。
// 提交后返回一个重新解析过授权的用户对象(等价于再 GetUser 一次),调用方拿到
// 它就能直接调 AllowsInbound,不会拿到「有 plan_id 但授权没解析」的半成品。
func (s *Store) SetUserPlan(username string, planID *int64) (*model.User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("设置用户 %q 的套餐: %w", username, err)
	}
	defer tx.Rollback()

	var userID int64
	err = tx.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("未找到用户 %q: %w", username, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("读取用户 %q: %w", username, err)
	}
	if planID == nil {
		if _, err := tx.Exec(`DELETE FROM user_plans WHERE user_id = ?`, userID); err != nil {
			return nil, fmt.Errorf("摘掉用户 %q 的套餐: %w", username, err)
		}
	} else {
		if err := planExists(tx, *planID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO user_plans(user_id, plan_id) VALUES(?, ?)
			ON CONFLICT(user_id) DO UPDATE SET plan_id = excluded.plan_id`, userID, *planID); err != nil {
			return nil, fmt.Errorf("把用户 %q 改挂套餐 %d: %w", username, *planID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUser(username)
}

// DefaultPlanID 返回默认套餐的 id;还没有时返回包装了 ErrPlanNotFound 的错误。
func (s *Store) DefaultPlanID() (int64, error) {
	return defaultPlanID(s.db)
}

// CountPlanUsers 返回挂在某个套餐上的用户数。
func (s *Store) CountPlanUsers(planID int64) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE plan_id = ?`, planID).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计套餐 %d 的用户数: %w", planID, err)
	}
	return n, nil
}

// PlanUserStats 是一个套餐的用户统计。
type PlanUserStats struct {
	// Users 是挂在该套餐上的用户总数。
	Users int `json:"users"`
	// ExtraAuth 是其中 inbound_ids 非空(套餐之外还有额外授权)的用户数。
	ExtraAuth int `json:"extra_auth"`
}

// PlanUserStats 一次查询按套餐统计用户数,以及其中有额外授权的用户数。
// 没有用户挂着的套餐返回零值;不在任何套餐上的用户不在这里体现。
func (s *Store) PlanUserStats() (map[int64]PlanUserStats, error) {
	rows, err := s.db.Query(`
		SELECT p.id,
		       COUNT(u.id),
		       COALESCE(SUM(CASE WHEN TRIM(u.inbound_ids) NOT IN ('', '[]', 'null') THEN 1 ELSE 0 END), 0)
		FROM plans p
		LEFT JOIN user_plans up ON up.plan_id = p.id
		LEFT JOIN users u ON u.id = up.user_id
		GROUP BY p.id`)
	if err != nil {
		return nil, fmt.Errorf("统计每套餐用户数: %w", err)
	}
	defer rows.Close()
	out := map[int64]PlanUserStats{}
	for rows.Next() {
		var (
			planID int64
			st     PlanUserStats
		)
		if err := rows.Scan(&planID, &st.Users, &st.ExtraAuth); err != nil {
			return nil, fmt.Errorf("读取每套餐用户数: %w", err)
		}
		out[planID] = st
	}
	return out, rows.Err()
}

// checkPlanRefs 在写入前确认引用的节点与入站都存在,给出比外键报错更好认的中文
// 提示。套餐里的 id 是外部输入,值得单独挡一道。
func checkPlanRefs(tx *sql.Tx, p *model.Plan) error {
	for _, id := range p.NodeIDs {
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM nodes WHERE id = ?`, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("节点 %d 不存在", id)
		} else if err != nil {
			return fmt.Errorf("校验套餐节点 %d: %w", id, err)
		}
	}
	for _, id := range p.InboundIDs {
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM inbounds WHERE id = ?`, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("入站 %d 不存在", id)
		} else if err != nil {
			return fmt.Errorf("校验套餐入站 %d: %w", id, err)
		}
	}
	return nil
}

// replacePlanNodes 整表替换一个套餐的节点关联。
func replacePlanNodes(tx *sql.Tx, planID int64, ids []int64) error {
	if _, err := tx.Exec(`DELETE FROM plan_nodes WHERE plan_id = ?`, planID); err != nil {
		return fmt.Errorf("清理套餐 %d 的节点: %w", planID, err)
	}
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT INTO plan_nodes(plan_id, node_id) VALUES(?, ?)`, planID, id); err != nil {
			return fmt.Errorf("写入套餐 %d 的节点 %d: %w", planID, id, err)
		}
	}
	return nil
}

// replacePlanInbounds 整表替换一个套餐的入站关联。
func replacePlanInbounds(tx *sql.Tx, planID int64, ids []int64) error {
	if _, err := tx.Exec(`DELETE FROM plan_inbounds WHERE plan_id = ?`, planID); err != nil {
		return fmt.Errorf("清理套餐 %d 的入站: %w", planID, err)
	}
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT INTO plan_inbounds(plan_id, inbound_id) VALUES(?, ?)`, planID, id); err != nil {
			return fmt.Errorf("写入套餐 %d 的入站 %d: %w", planID, id, err)
		}
	}
	return nil
}

// defaultPlanID 查默认套餐 id。defaultPlanID 是唯一一处解释 is_default 的地方。
func defaultPlanID(q querier) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM plans WHERE is_default = 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPlanNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("查询默认套餐: %w", err)
	}
	return id, nil
}

// planExists 确认套餐存在,不存在时报 ErrPlanNotFound。
func planExists(q querier, id int64) error {
	var one int
	err := q.QueryRow(`SELECT 1 FROM plans WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("未找到套餐 %d: %w", id, ErrPlanNotFound)
	}
	if err != nil {
		return fmt.Errorf("查询套餐 %d: %w", id, err)
	}
	return nil
}

// planAuth 是一个套餐解析后的授权。
type planAuth struct {
	allNodes bool
	inbounds map[int64]bool
}

// loadPlanAuth 读出全部套餐的授权:是否全部节点,以及入站 id 集合。节点授权要按
// 节点上「当前所有」入站展开,所以联一次 inbounds。套餐数量是个位数到两位数,
// 读用户时整张读进来,比按用户逐个查划算。
func (s *Store) loadPlanAuth() (map[int64]planAuth, error) {
	out := map[int64]planAuth{}

	rows, err := s.db.Query(`SELECT id, all_nodes FROM plans`)
	if err != nil {
		return nil, fmt.Errorf("查询套餐授权: %w", err)
	}
	for rows.Next() {
		var (
			id  int64
			all int
		)
		if err := rows.Scan(&id, &all); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取套餐授权: %w", err)
		}
		out[id] = planAuth{allNodes: all != 0, inbounds: map[int64]bool{}}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历套餐授权: %w", err)
	}
	rows.Close()

	if err := addPlanInboundAuth(out, s.db, `SELECT pn.plan_id, i.id FROM plan_nodes pn JOIN inbounds i ON i.node_id = pn.node_id`); err != nil {
		return nil, err
	}
	if err := addPlanInboundAuth(out, s.db, `SELECT plan_id, inbound_id FROM plan_inbounds`); err != nil {
		return nil, err
	}
	return out, nil
}

// addPlanInboundAuth 跑一条 (plan_id, inbound_id) 查询,把结果并进授权集合。
func addPlanInboundAuth(out map[int64]planAuth, q querier, query string) error {
	rows, err := q.Query(query)
	if err != nil {
		return fmt.Errorf("查询套餐入站授权: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var planID, inboundID int64
		if err := rows.Scan(&planID, &inboundID); err != nil {
			return fmt.Errorf("读取套餐入站授权: %w", err)
		}
		pa := out[planID]
		if pa.inbounds == nil {
			pa.inbounds = map[int64]bool{}
		}
		pa.inbounds[inboundID] = true
		out[planID] = pa
	}
	return rows.Err()
}

// attachPlanAuth 把解析后的授权挂到用户身上。套餐被人为抹掉(外键本应挡住)时按
// 「没有任何套餐授权」处理,只剩用户自己的额外授权,绝不放大权限。
func attachPlanAuth(u *model.User, auth map[int64]planAuth) {
	if u.PlanID == nil {
		u.SetPlanAuth(false, nil)
		return
	}
	pa, ok := auth[*u.PlanID]
	if !ok {
		u.SetPlanAuth(false, nil)
		return
	}
	ids := make([]int64, 0, len(pa.inbounds))
	for id := range pa.inbounds {
		ids = append(ids, id)
	}
	u.SetPlanAuth(pa.allNodes, ids)
}
