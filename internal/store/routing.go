package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 分流规则的存储(见 docs/internal/routing-rules-rfc.md)。
//
// 作用域用用户 id 表示,0 是全局。每个作用域有一个版本号:整表替换带上读到的版本,
// 两台设备同时编辑时后写的拿到 ErrVersionConflict,而不是悄悄覆盖前一个人的改动。

var (
	ErrVersionConflict = errors.New("规则已被别处修改,请刷新后重试")
	ErrUnknownGroup    = errors.New("去向指向的策略组不存在")
	ErrDuplicate       = errors.New("名字已被占用")
)

// GroupInUseError 是删除仍被规则或兜底引用的策略组时的错误。
type GroupInUseError struct{ Refs []string }

func (e *GroupInUseError) Error() string {
	return "策略组仍被使用:" + strings.Join(e.Refs, "、")
}

func (s *Store) migrateRouting() error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS proxy_groups (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, config TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS route_rules (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id  INTEGER REFERENCES users(id) ON DELETE CASCADE,
			position INTEGER NOT NULL,
			type     TEXT NOT NULL,
			value    TEXT NOT NULL,
			target   TEXT NOT NULL,
			enabled  INTEGER NOT NULL DEFAULT 1,
			note     TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_route_rules_scope ON route_rules(user_id, position)`,
		`CREATE TABLE IF NOT EXISTS route_scopes (user_id INTEGER PRIMARY KEY, final TEXT NOT NULL DEFAULT '', version INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TRIGGER IF NOT EXISTS trg_route_scopes_user_delete AFTER DELETE ON users BEGIN DELETE FROM route_scopes WHERE user_id = OLD.id; END`,
		`CREATE TABLE IF NOT EXISTS routing_state (id INTEGER PRIMARY KEY CHECK (id = 1), mode TEXT NOT NULL DEFAULT 'template', report TEXT NOT NULL DEFAULT '', migrated_at INTEGER)`,
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("分流规则建表: %w", err)
		}
	}
	return nil
}

// RoutingState 是分流的总开关与最近一次迁移的报告。
type RoutingState struct {
	Mode       model.RoutingMode `json:"mode"`
	Report     string            `json:"report,omitempty"`
	MigratedAt *time.Time        `json:"migrated_at,omitempty"`
}

func (s *Store) GetRoutingState() (*RoutingState, error) {
	var (
		st RoutingState
		at sql.NullInt64
	)
	err := s.db.QueryRow(`SELECT mode, report, migrated_at FROM routing_state WHERE id = 1`).Scan(&st.Mode, &st.Report, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return &RoutingState{Mode: model.RoutingTemplate}, nil
	}
	if err != nil {
		return nil, err
	}
	if at.Valid {
		t := time.Unix(at.Int64, 0)
		st.MigratedAt = &t
	}
	if st.Mode != model.RoutingRules {
		st.Mode = model.RoutingTemplate
	}
	return &st, nil
}

func (s *Store) SaveRoutingState(st *RoutingState) error {
	if st.Mode != model.RoutingRules && st.Mode != model.RoutingTemplate {
		return fmt.Errorf("分流模式只能是 template / rules")
	}
	var at any
	if st.MigratedAt != nil {
		at = st.MigratedAt.Unix()
	}
	_, err := s.db.Exec(`INSERT INTO routing_state(id, mode, report, migrated_at) VALUES(1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET mode = excluded.mode, report = excluded.report, migrated_at = excluded.migrated_at`, string(st.Mode), st.Report, at)
	return err
}

// RouteScope 是一个作用域的兜底与版本。Final 为空:全局表示还没设,用户表示跟随全局。
type RouteScope struct {
	Final   string `json:"final"`
	Version int64  `json:"version"`
}

type querier interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func scopeOf(q querier, userID int64) (RouteScope, error) {
	var sc RouteScope
	err := q.QueryRow(`SELECT final, version FROM route_scopes WHERE user_id = ?`, userID).Scan(&sc.Final, &sc.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return RouteScope{}, nil
	}
	return sc, err
}

func (s *Store) GetRouteScope(userID int64) (RouteScope, error) { return scopeOf(s.db, userID) }

func userArg(userID int64) any {
	if userID == 0 {
		return nil
	}
	return userID
}

// ListRules 返回一个作用域的规则,按顺序。
func (s *Store) ListRules(userID int64) ([]model.RouteRule, error) {
	return listRules(s.db, userID)
}

func listRules(q querier, userID int64) ([]model.RouteRule, error) {
	rows, err := q.Query(`SELECT id, COALESCE(user_id, 0), position, type, value, target, enabled, note FROM route_rules
		WHERE COALESCE(user_id, 0) = ? ORDER BY position, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.RouteRule{}
	for rows.Next() {
		var r model.RouteRule
		if err := rows.Scan(&r.ID, &r.UserID, &r.Position, &r.Type, &r.Value, &r.Target, &r.Enabled, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RulesUsingGroup 列出引用某个策略组的作用域(用户 id,0 为全局),供删除前检查。
func groupRefs(q querier, id int64) ([]string, error) {
	t := model.GroupTarget(id)
	refs := []string{}
	rows, err := q.Query(`SELECT COALESCE(u.username, ''), COUNT(*) FROM route_rules r LEFT JOIN users u ON u.id = r.user_id WHERE r.target = ? GROUP BY r.user_id`, t)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, fmt.Sprintf("%s %d 条规则", scopeName(name), n))
	}
	rows.Close()
	rows, err = q.Query(`SELECT COALESCE(u.username, '') FROM route_scopes s LEFT JOIN users u ON u.id = s.user_id WHERE s.final = ?`, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		refs = append(refs, scopeName(name)+"的「其他全部」")
	}
	return refs, rows.Err()
}

func scopeName(user string) string {
	if user == "" {
		return "全局"
	}
	return "用户 " + user
}

func checkTarget(tx *sql.Tx, t string) error {
	id, ok := model.TargetGroupID(t)
	if !ok {
		return nil
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM proxy_groups WHERE id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownGroup, t)
	}
	return nil
}

func bumpScope(tx *sql.Tx, userID int64) (int64, error) {
	if _, err := tx.Exec(`INSERT INTO route_scopes(user_id, final, version) VALUES(?, '', 1)
		ON CONFLICT(user_id) DO UPDATE SET version = version + 1`, userID); err != nil {
		return 0, err
	}
	sc, err := scopeOf(tx, userID)
	return sc.Version, err
}

func checkUser(tx *sql.Tx, userID int64) error {
	if userID == 0 {
		return nil
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceRules 整表替换一个作用域的规则。expect < 0 表示不检查版本;否则必须等于当前版本。
// 规则按切片顺序重新编号;返回新版本号。
func (s *Store) ReplaceRules(userID int64, rules []model.RouteRule, expect int64) (int64, error) {
	if len(rules) > model.MaxRulesPerScope {
		return 0, fmt.Errorf("每个作用域最多 %d 条规则", model.MaxRulesPerScope)
	}
	for i := range rules {
		rules[i].Normalize()
		if err := rules[i].Validate(); err != nil {
			return 0, fmt.Errorf("第 %d 条: %w", i+1, err)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := checkUser(tx, userID); err != nil {
		return 0, err
	}
	sc, err := scopeOf(tx, userID)
	if err != nil {
		return 0, err
	}
	if expect >= 0 && sc.Version != expect {
		return 0, ErrVersionConflict
	}
	for i, r := range rules {
		if err := checkTarget(tx, r.Target); err != nil {
			return 0, fmt.Errorf("第 %d 条: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM route_rules WHERE COALESCE(user_id, 0) = ?`, userID); err != nil {
		return 0, err
	}
	for i, r := range rules {
		if _, err := tx.Exec(`INSERT INTO route_rules(user_id, position, type, value, target, enabled, note) VALUES(?, ?, ?, ?, ?, ?, ?)`,
			userArg(userID), i, string(r.Type), r.Value, r.Target, r.Enabled, r.Note); err != nil {
			return 0, err
		}
	}
	v, err := bumpScope(tx, userID)
	if err != nil {
		return 0, err
	}
	return v, tx.Commit()
}

// UpdateRule 改一条规则的内容或开关,位置和作用域不变。
func (s *Store) UpdateRule(r *model.RouteRule) error {
	r.Normalize()
	if err := r.Validate(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID int64
	err = tx.QueryRow(`SELECT COALESCE(user_id, 0), position FROM route_rules WHERE id = ?`, r.ID).Scan(&userID, &r.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	r.UserID = userID
	if err := checkTarget(tx, r.Target); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE route_rules SET type = ?, value = ?, target = ?, enabled = ?, note = ? WHERE id = ?`,
		string(r.Type), r.Value, r.Target, r.Enabled, r.Note, r.ID); err != nil {
		return err
	}
	if _, err := bumpScope(tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// GetRule 读单条规则。
func (s *Store) GetRule(id int64) (*model.RouteRule, error) {
	var r model.RouteRule
	err := s.db.QueryRow(`SELECT id, COALESCE(user_id, 0), position, type, value, target, enabled, note FROM route_rules WHERE id = ?`, id).
		Scan(&r.ID, &r.UserID, &r.Position, &r.Type, &r.Value, &r.Target, &r.Enabled, &r.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// SetRouteFinal 设兜底。全局必须是合法去向;用户传空串表示跟随全局。
func (s *Store) SetRouteFinal(userID int64, target string) (int64, error) {
	if target != "" && !model.ValidTarget(target) {
		return 0, fmt.Errorf("去向只能是策略组、DIRECT 或 REJECT")
	}
	if userID == 0 && target == "" {
		return 0, fmt.Errorf("全局的「其他全部」不能为空")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := checkUser(tx, userID); err != nil {
		return 0, err
	}
	if err := checkTarget(tx, target); err != nil {
		return 0, err
	}
	v, err := bumpScope(tx, userID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE route_scopes SET final = ? WHERE user_id = ?`, target, userID); err != nil {
		return 0, err
	}
	return v, tx.Commit()
}

// ---- 策略组 ----

func (s *Store) ListProxyGroups() ([]model.ProxyGroup, error) {
	rows, err := s.db.Query(`SELECT id, name, config FROM proxy_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ProxyGroup{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanGroup(sc scanner) (*model.ProxyGroup, error) {
	var (
		g      model.ProxyGroup
		id     int64
		name   string
		config string
	)
	if err := sc.Scan(&id, &name, &config); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(config), &g); err != nil {
		return nil, fmt.Errorf("解析策略组 %s: %w", name, err)
	}
	g.ID, g.Name = id, name
	return &g, nil
}

func (s *Store) GetProxyGroup(id int64) (*model.ProxyGroup, error) {
	g, err := scanGroup(s.db.QueryRow(`SELECT id, name, config FROM proxy_groups WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func groupJSON(g *model.ProxyGroup) (string, error) {
	c := *g
	c.ID, c.Name = 0, ""
	b, err := json.Marshal(c)
	return string(b), err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Store) CreateProxyGroup(g *model.ProxyGroup) error {
	g.Normalize()
	if err := g.Validate(); err != nil {
		return err
	}
	cfg, err := groupJSON(g)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`INSERT INTO proxy_groups(name, config) VALUES(?, ?)`, g.Name, cfg)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	g.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateProxyGroup(g *model.ProxyGroup) error {
	g.Normalize()
	if err := g.Validate(); err != nil {
		return err
	}
	cfg, err := groupJSON(g)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE proxy_groups SET name = ?, config = ? WHERE id = ?`, g.Name, cfg, g.ID)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProxyGroup 删除策略组;仍被引用时返回 *GroupInUseError,什么都不删。
func (s *Store) DeleteProxyGroup(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	refs, err := groupRefs(tx, id)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		return &GroupInUseError{Refs: refs}
	}
	res, err := tx.Exec(`DELETE FROM proxy_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// ErrRoutingNotEmpty:已经有规则或策略组,迁移不覆盖(除非 replace)。
var ErrRoutingNotEmpty = errors.New("已经有分流规则或策略组")

// RoutingEmpty 报告规则表与策略组是否都还是空的。
func (s *Store) RoutingEmpty() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM route_rules) + (SELECT COUNT(*) FROM proxy_groups)`).Scan(&n)
	return n == 0, err
}

// ApplyRoutingSeed 在一个事务里写入迁移结果:策略组、全局规则、全局兜底和分流状态。
// groups 的 ID 是临时编号,rules / final 里的 "group:<临时编号>" 会换成真实 id。
// replace=false 时,已有规则或策略组就返回 ErrRoutingNotEmpty;replace=true 时先清空
// 全部规则(含各用户的)、兜底和策略组。
func (s *Store) ApplyRoutingSeed(groups []model.ProxyGroup, rules []model.RouteRule, final string, state *RoutingState, replace bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM route_rules) + (SELECT COUNT(*) FROM proxy_groups)`).Scan(&n); err != nil {
		return err
	}
	if n > 0 && !replace {
		return ErrRoutingNotEmpty
	}
	for _, q := range []string{`DELETE FROM route_rules`, `UPDATE route_scopes SET final = '', version = version + 1`, `DELETE FROM proxy_groups`} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	ids := map[int64]int64{}
	for _, g := range groups {
		tmp := g.ID
		g.Normalize()
		if err := g.Validate(); err != nil {
			return fmt.Errorf("策略组 %s: %w", g.Name, err)
		}
		cfg, err := groupJSON(&g)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO proxy_groups(name, config) VALUES(?, ?)`, g.Name, cfg)
		if isUnique(err) {
			return fmt.Errorf("策略组 %s: %w", g.Name, ErrDuplicate)
		}
		if err != nil {
			return err
		}
		if ids[tmp], err = res.LastInsertId(); err != nil {
			return err
		}
	}
	remap := func(t string) (string, error) {
		id, ok := model.TargetGroupID(t)
		if !ok {
			return t, nil
		}
		real, ok := ids[id]
		if !ok {
			return "", fmt.Errorf("去向 %s 没有对应的策略组", t)
		}
		return model.GroupTarget(real), nil
	}
	if len(rules) > model.MaxRulesPerScope {
		return fmt.Errorf("每个作用域最多 %d 条规则", model.MaxRulesPerScope)
	}
	for i, r := range rules {
		r.Normalize()
		if err := r.Validate(); err != nil {
			return fmt.Errorf("第 %d 条: %w", i+1, err)
		}
		if r.Target, err = remap(r.Target); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO route_rules(user_id, position, type, value, target, enabled, note) VALUES(NULL, ?, ?, ?, ?, ?, ?)`,
			i, string(r.Type), r.Value, r.Target, r.Enabled, r.Note); err != nil {
			return err
		}
	}
	if final, err = remap(final); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO route_scopes(user_id, final, version) VALUES(0, ?, 1)
		ON CONFLICT(user_id) DO UPDATE SET final = excluded.final, version = route_scopes.version + 1`, final); err != nil {
		return err
	}
	var at any
	if state.MigratedAt != nil {
		at = state.MigratedAt.Unix()
	}
	if _, err := tx.Exec(`INSERT INTO routing_state(id, mode, report, migrated_at) VALUES(1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET mode = excluded.mode, report = excluded.report, migrated_at = excluded.migrated_at`, string(state.Mode), state.Report, at); err != nil {
		return err
	}
	return tx.Commit()
}
