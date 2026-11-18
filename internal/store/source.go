package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func (s *Store) migrateSources() error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS subscription_sources (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, interval_seconds INTEGER NOT NULL DEFAULT 21600, region TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1, last_attempt_at INTEGER, last_error TEXT NOT NULL DEFAULT '', report BLOB NOT NULL DEFAULT '{}', grant_all INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS source_snapshots (source_id INTEGER PRIMARY KEY REFERENCES subscription_sources(id) ON DELETE CASCADE, url TEXT NOT NULL, endpoints BLOB NOT NULL, created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS user_source_grants (source_id INTEGER NOT NULL REFERENCES subscription_sources(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, PRIMARY KEY(source_id,user_id))`,
		`CREATE TABLE IF NOT EXISTS plan_source_grants (source_id INTEGER NOT NULL REFERENCES subscription_sources(id) ON DELETE CASCADE, plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE, PRIMARY KEY(source_id,plan_id))`,
		// Source selectors retain deleted IDs so deletion cannot widen a group.
		`CREATE TABLE IF NOT EXISTS group_source_filters (group_id INTEGER PRIMARY KEY REFERENCES proxy_groups(id) ON DELETE CASCADE, sources TEXT NOT NULL DEFAULT '[]')`,
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			return err
		}
	}
	// grant_all 建表之后补加:旧库靠这里升上来。这条迁移在 addMissingColumns
	// 之后跑(那里的循环跑时 subscription_sources 可能还没建),所以放这里。
	return s.addColumnIfMissing("subscription_sources", "grant_all",
		`ALTER TABLE subscription_sources ADD COLUMN grant_all INTEGER NOT NULL DEFAULT 0`)
}

const sourceSelect = `SELECT s.id,s.name,s.url,s.enabled,s.interval_seconds,s.region,s.grant_all,s.revision,s.last_attempt_at,s.last_error,s.report,p.created_at,COALESCE(p.url,''),COALESCE(p.endpoints,'') FROM subscription_sources s LEFT JOIN source_snapshots p ON p.source_id=s.id`

func scanSource(row scanner) (*model.SubscriptionSource, error) {
	var s model.SubscriptionSource
	var attempted, good sql.NullInt64
	err := row.Scan(&s.ID, &s.Name, &s.URL, &s.Enabled, &s.IntervalSeconds, &s.Region, &s.GrantAll, &s.Revision, &attempted, &s.LastError, &s.Report, &good, &s.SnapshotURL, &s.Snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if attempted.Valid {
		v := time.UnixMilli(attempted.Int64)
		s.LastAttemptAt = &v
	}
	if good.Valid {
		v := time.UnixMilli(good.Int64)
		s.LastGoodAt = &v
	}
	return &s, nil
}

func (s *Store) CreateSource(v *model.SubscriptionSource) error {
	v.Normalize()
	if err := v.Validate(); err != nil {
		return err
	}
	res, err := s.db.Exec(`INSERT INTO subscription_sources(name,url,enabled,interval_seconds,region,grant_all) VALUES(?,?,?,?,?,?)`, v.Name, v.URL, v.Enabled, v.IntervalSeconds, v.Region, v.GrantAll)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	v.ID, err = res.LastInsertId()
	v.Revision = 1
	return err
}

func (s *Store) GetSource(id int64) (*model.SubscriptionSource, error) {
	return scanSource(s.db.QueryRow(sourceSelect+` WHERE s.id=?`, id))
}

func (s *Store) ListSources() ([]model.SubscriptionSource, error) {
	return s.listSources(sourceSelect + ` ORDER BY s.id`)
}

func (s *Store) listSources(query string, args ...any) ([]model.SubscriptionSource, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.SubscriptionSource{}
	for rows.Next() {
		v, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (s *Store) ListSourcesForUser(userID int64) ([]model.SubscriptionSource, error) {
	return s.listSources(sourceSelect+` WHERE s.enabled=1 AND (s.grant_all=1 OR EXISTS(SELECT 1 FROM user_source_grants g WHERE g.source_id=s.id AND g.user_id=?) OR EXISTS(SELECT 1 FROM plan_source_grants pg JOIN user_plans up ON up.plan_id=pg.plan_id WHERE pg.source_id=s.id AND up.user_id=?)) ORDER BY s.id`, userID, userID)
}

func (s *Store) UpdateSource(v *model.SubscriptionSource) error {
	v.Normalize()
	if err := v.Validate(); err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE subscription_sources SET name=?,url=?,enabled=?,interval_seconds=?,region=?,grant_all=?,revision=revision+1 WHERE id=? AND revision=?`, v.Name, v.URL, v.Enabled, v.IntervalSeconds, v.Region, v.GrantAll, v.ID, v.Revision)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrVersionConflict
	}
	v.Revision++
	return nil
}

func (s *Store) DeleteSource(id int64) error {
	res, err := s.db.Exec(`DELETE FROM subscription_sources WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CompleteSourceRefresh preserves the last good snapshot on all failed attempts.
// The revision test and snapshot replacement share the same transaction.
func (s *Store) CompleteSourceRefresh(id, revision int64, at time.Time, snapshot, report []byte, failure string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var reportValue any
	if len(report) > 0 {
		reportValue = report
	}
	res, err := tx.Exec(`UPDATE subscription_sources SET last_attempt_at=?,last_error=?,report=COALESCE(?,report) WHERE id=? AND revision=? AND enabled=1`, at.UnixMilli(), failure, reportValue, id, revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrVersionConflict
	}
	if failure == "" {
		if len(snapshot) == 0 {
			return errors.New("有效快照不能为空")
		}
		_, err = tx.Exec(`INSERT INTO source_snapshots(source_id,url,endpoints,created_at) SELECT id,url,?,? FROM subscription_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET url=excluded.url,endpoints=excluded.endpoints,created_at=excluded.created_at`, snapshot, at.UnixMilli(), id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SourceGrants(id int64) ([]string, error) {
	if _, err := s.GetSource(id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT u.username FROM user_source_grants g JOIN users u ON u.id=g.user_id WHERE g.source_id=? ORDER BY u.username`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (s *Store) SetSourceGrant(id int64, username string, allow bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sid, uid int64
	if err := tx.QueryRow(`SELECT id FROM subscription_sources WHERE id=?`, id).Scan(&sid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := tx.QueryRow(`SELECT id FROM users WHERE username=?`, username).Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if allow {
		_, err = tx.Exec(`INSERT INTO user_source_grants(source_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING`, id, uid)
	} else {
		_, err = tx.Exec(`DELETE FROM user_source_grants WHERE source_id=? AND user_id=?`, id, uid)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SourcePlanGrants 列出授权给某个来源的套餐(按 id 升序返回 id+name)。
func (s *Store) SourcePlanGrants(id int64) ([]model.Plan, error) {
	if _, err := s.GetSource(id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT p.id,p.name FROM plan_source_grants g JOIN plans p ON p.id=g.plan_id WHERE g.source_id=? ORDER BY p.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Plan{}
	for rows.Next() {
		var p model.Plan
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetSourcePlanGrant 把来源授权或撤回给一个套餐(套餐名唯一,按名字定位)。
func (s *Store) SetSourcePlanGrant(id int64, planName string, allow bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sid, pid int64
	if err := tx.QueryRow(`SELECT id FROM subscription_sources WHERE id=?`, id).Scan(&sid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := tx.QueryRow(`SELECT id FROM plans WHERE name=?`, planName).Scan(&pid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if allow {
		_, err = tx.Exec(`INSERT INTO plan_source_grants(source_id,plan_id) VALUES(?,?) ON CONFLICT DO NOTHING`, id, pid)
	} else {
		_, err = tx.Exec(`DELETE FROM plan_source_grants WHERE source_id=? AND plan_id=?`, id, pid)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceSourcePlanGrants 整组替换某来源的套餐授权(给套餐 id;不存在的 id 报 ErrNotFound,事务不落)。
func (s *Store) ReplaceSourcePlanGrants(id int64, planIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM subscription_sources WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	for _, pid := range planIDs {
		var ok int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM plans WHERE id=?`, pid).Scan(&ok); err != nil {
			return err
		}
		if ok == 0 {
			return ErrNotFound
		}
	}
	if _, err = tx.Exec(`DELETE FROM plan_source_grants WHERE source_id=?`, id); err != nil {
		return err
	}
	for _, pid := range planIDs {
		if _, err = tx.Exec(`INSERT INTO plan_source_grants(source_id,plan_id) VALUES(?,?) ON CONFLICT DO NOTHING`, id, pid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ReplaceSourceGrants(id int64, names []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM subscription_sources WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	ids := []int64{}
	for _, name := range names {
		var uid int64
		if err := tx.QueryRow(`SELECT id FROM users WHERE username=?`, name).Scan(&uid); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		ids = append(ids, uid)
	}
	if _, err = tx.Exec(`DELETE FROM user_source_grants WHERE source_id=?`, id); err != nil {
		return err
	}
	for _, uid := range ids {
		if _, err = tx.Exec(`INSERT INTO user_source_grants(source_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING`, id, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetGroupSources(id int64) ([]string, error) {
	if _, err := s.GetProxyGroup(id); err != nil {
		return nil, err
	}
	var raw string
	err := s.db.QueryRow(`SELECT sources FROM group_source_filters WHERE group_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

func (s *Store) ListGroupSources() (map[int64][]string, error) {
	rows, err := s.db.Query(`SELECT group_id,sources FROM group_source_filters`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, err
		}
		out[id] = values
	}
	return out, rows.Err()
}

func (s *Store) SetGroupSources(id int64, values []string) error {
	if err := model.ValidateSourceOrigins(values); err != nil {
		return err
	}
	if _, err := s.GetProxyGroup(id); err != nil {
		return err
	}
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO group_source_filters(group_id,sources) VALUES(?,?) ON CONFLICT(group_id) DO UPDATE SET sources=excluded.sources`, id, string(raw))
	return err
}
