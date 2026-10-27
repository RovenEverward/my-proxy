package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// userColumns 是 users 表的列顺序,扫描时按同一顺序取值。
// key 是 SQL 关键字,加引号。最后一项是套餐归属:子查询保证 user_id 是主键时
// 最多一行,读回来是 NULL 表示没挂套餐。
const userColumns = `id, username, "key", sub_token, enabled, data_limit, used_traffic,
	lifetime_used_traffic, expire_at, reset_strategy, traffic_reset_at, inbound_ids,
	note, created_at, (SELECT plan_id FROM user_plans WHERE user_id = users.id)`

func scanUser(sc rowScanner) (*model.User, error) {
	var (
		u          model.User
		enabled    int
		expireAt   sql.NullInt64
		resetAt    sql.NullInt64
		inboundIDs string
		createdAt  int64
		planID     sql.NullInt64
	)
	if err := sc.Scan(&u.ID, &u.Username, &u.Key, &u.SubToken, &enabled,
		&u.DataLimit, &u.UsedTraffic, &u.LifetimeUsedTraffic, &expireAt,
		&u.ResetStrategy, &resetAt, &inboundIDs, &u.Note, &createdAt, &planID); err != nil {
		return nil, err
	}

	u.Enabled = enabled != 0
	if expireAt.Valid {
		t := time.Unix(expireAt.Int64, 0).UTC()
		u.ExpireAt = &t
	}
	if resetAt.Valid {
		t := time.Unix(resetAt.Int64, 0).UTC()
		u.TrafficResetAt = &t
	}
	ids, err := decodeSlice[int64](inboundIDs)
	if err != nil {
		return nil, fmt.Errorf("用户 %q 的 inbound_ids: %w", u.Username, err)
	}
	u.InboundIDs = ids
	if planID.Valid {
		id := planID.Int64
		u.PlanID = &id
	}
	u.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &u, nil
}

// attachUserPlanAuth 给一个刚从库里读出来的用户挂上解析后的套餐授权。没挂套餐的
// 用户也会被标记为「已解析」(授权为空),这样判定只认「解析没解析」,不再看
// plan_id 是否为空。
func (s *Store) attachUserPlanAuth(u *model.User) error {
	auth, err := s.loadPlanAuth()
	if err != nil {
		return err
	}
	attachPlanAuth(u, auth)
	return nil
}

// resolveUserPlanAuth 在写操作提交后重新解析用户的套餐授权,并同步 PlanID:
// UpdateUser 本身不改套餐归属(摘 all_nodes 套餐除外),所以以库里的现值为准,
// 不依赖调用方对象上带的 plan_id。这样 CreateUser / UpdateUser 返回/修改的对象
// 马上就能直接调 AllowsInbound。
func (s *Store) resolveUserPlanAuth(u *model.User) error {
	var planID sql.NullInt64
	err := s.db.QueryRow(
		`SELECT up.plan_id FROM users LEFT JOIN user_plans up ON up.user_id = users.id
		 WHERE users.username = ?`, u.Username).Scan(&planID)
	if err != nil {
		return fmt.Errorf("读取用户 %q 的套餐: %w", u.Username, err)
	}
	if planID.Valid {
		id := planID.Int64
		u.PlanID = &id
	} else {
		u.PlanID = nil
	}
	return s.attachUserPlanAuth(u)
}

// normalizeTime 把时间对齐到数据库能存下的精度(秒),并统一成 UTC,
// 这样刚写进去的对象与随后读回来的相等。
func normalizeTime(t time.Time) time.Time {
	return time.Unix(t.Unix(), 0).UTC()
}

// normalizeExpireAt 同 normalizeTime,处理 nil 表示不过期。
func normalizeExpireAt(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	n := normalizeTime(*t)
	return &n
}

// expireAtArg 把可选的时间转成驱动参数,NULL 表示没有这个时间。
func expireAtArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

// CreateUser 新建用户,成功后回填 u.ID 与 u.CreatedAt,并按规则决定套餐归属:
// 显式带了 PlanID 就用它;没带且 InboundIDs 为空归默认套餐;没带但 InboundIDs
// 非空则不挂套餐(旧客户端/脚本的写法,避免和默认套餐取并集后变成全部)。
//
// CreatedAt 为零值时取当前时间;用户自己指定了就沿用,方便导入既有数据。
// 时间字段只存到秒,回填与写库的值都截断到秒。username 重复会因 UNIQUE 报错。
// 用户行与套餐归属在同一个事务里写,不会出现建了用户却没挂上套餐的中间状态。
func (s *Store) CreateUser(u *model.User) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	u.CreatedAt = normalizeTime(u.CreatedAt)
	u.ExpireAt = normalizeExpireAt(u.ExpireAt)
	u.TrafficResetAt = normalizeExpireAt(u.TrafficResetAt)

	inboundIDs, err := encodeSlice(u.InboundIDs)
	if err != nil {
		return fmt.Errorf("序列化用户 %q 的 inbound_ids: %w", u.Username, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("创建用户 %q: %w", u.Username, err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO users (username, "key", sub_token, enabled, data_limit, used_traffic,
			lifetime_used_traffic, expire_at, reset_strategy, traffic_reset_at,
			inbound_ids, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.Key, u.SubToken, boolToInt(u.Enabled), u.DataLimit,
		u.UsedTraffic, u.LifetimeUsedTraffic, expireAtArg(u.ExpireAt),
		string(u.ResetStrategy), expireAtArg(u.TrafficResetAt),
		inboundIDs, u.Note, u.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("创建用户 %q: %w", u.Username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取新建用户 %q 的 id: %w", u.Username, err)
	}
	u.ID = id

	planID := u.PlanID
	if planID == nil && len(u.InboundIDs) == 0 {
		defID, err := defaultPlanID(tx)
		switch {
		case err == nil:
			planID = &defID
		case errors.Is(err, ErrPlanNotFound):
			// 库还没有默认套餐(只可能出现在迁移完成前的极早期),不挂任何套餐。
		default:
			return fmt.Errorf("读取默认套餐: %w", err)
		}
	}
	if planID != nil {
		if err := planExists(tx, *planID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO user_plans(user_id, plan_id) VALUES(?, ?)`, u.ID, *planID); err != nil {
			return fmt.Errorf("给用户 %q 挂套餐 %d: %w", u.Username, *planID, err)
		}
	}
	u.PlanID = planID
	if err := tx.Commit(); err != nil {
		return err
	}
	// 事务提交后按库中现况重新解析一次:调用方拿到的同一个对象直接调
	// AllowsInbound 也要和随后 GetUser 读出来的一致。
	return s.resolveUserPlanAuth(u)
}

// GetUser 按用户名取用户。不存在时返回包装了 ErrNotFound 的错误。
// 读出来的用户已经挂上解析后的套餐授权。
func (s *Store) GetUser(username string) (*model.User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE username = ?`, username))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("用户 %q", username))
	}
	if err := s.attachUserPlanAuth(u); err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserBySubToken 按订阅 token 取用户,走 users.sub_token 上的索引。
//
// 空 token 直接当作找不到:没设 token 的用户都存空串,拿空串去查会撞上第一个
// 这样的用户,而订阅链接里不可能出现空 token。返回值同样带上套餐授权。
func (s *Store) GetUserBySubToken(token string) (*model.User, error) {
	if token == "" {
		return nil, fmt.Errorf("未找到订阅 token 为空的用户: %w", ErrNotFound)
	}
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE sub_token = ?`, token))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("订阅 token %q 对应的用户", token))
	}
	if err := s.attachUserPlanAuth(u); err != nil {
		return nil, err
	}
	return u, nil
}

// UserQuery 是用户列表的筛选与分页条件;零值表示"全部用户、不分页"。
//
// 筛选在 Go 里按 model 的方法做(状态用 User.Status、入站用 User.AllowsInbound),
// 不把判定翻写成 SQL 的 WHERE:那两处判定是本系统唯一的一份规则,SQL 里再写一遍
// 迟早会出现"列表说 active、订阅说 limited"这种两份规则打架。代价是每次列表要
// 把用户读进内存再筛 —— 全量读用户本来就在别处做过(巡检、渲染节点配置),量级
// 在千级时这点内存和 CPU 不值得换一份第二实现;真到了十万级再考虑给 SQL 版本
// 补一份共用判定,见 decisions.md 第 24 条。
type UserQuery struct {
	// Status 为空表示不限。取值合法性由调用方先校验(非法值在这里会筛出空结果)。
	Status model.Status
	// Search 是用户名的子串,不分大小写;空表示不限。
	Search string
	// InboundID 大于 0 时只保留可用该入站的用户 —— 没限定入站的用户本来就能用
	// 全部入站,算在内。
	InboundID int64
	// PlanID 非空时只保留挂在这个套餐上的用户;nil 表示不限。
	PlanID *int64
	// Limit 是每页条数,<=0 表示不分页;Offset 是跳过的条数。
	Limit  int
	Offset int
}

// ListUsersPage 返回筛选后的一页用户,以及匹配的用户总数(不是本页条数)。
//
// 总数在筛选之后数,客户端才能说"还有 N 个"。
//
// 先把全部用户读进内存、挂上解析后的套餐授权,再做筛选:按 InboundID 筛必须走
// User.AllowsInbound,而套餐授权得在那之前解析出来。
func (s *Store) ListUsersPage(q UserQuery) ([]model.User, int, error) {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, 0, fmt.Errorf("查询用户列表: %w", err)
	}
	var all []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("读取用户记录: %w", err)
		}
		all = append(all, *u)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, fmt.Errorf("遍历用户列表: %w", err)
	}
	// 下面要查套餐表,必须先放掉这条连接(本包只有一条)。
	rows.Close()

	auth, err := s.loadPlanAuth()
	if err != nil {
		return nil, 0, err
	}
	for i := range all {
		attachPlanAuth(&all[i], auth)
	}

	now := time.Now()
	search := strings.ToLower(strings.TrimSpace(q.Search))
	var matched []model.User
	for _, u := range all {
		if search != "" && !strings.Contains(strings.ToLower(u.Username), search) {
			continue
		}
		if q.Status != "" && u.Status(now) != q.Status {
			continue
		}
		if q.InboundID > 0 && !u.AllowsInbound(q.InboundID) {
			continue
		}
		if q.PlanID != nil && (u.PlanID == nil || *u.PlanID != *q.PlanID) {
			continue
		}
		matched = append(matched, u)
	}

	total := len(matched)
	if q.Offset >= total {
		return nil, total, nil
	}
	page := matched[q.Offset:]
	if q.Limit > 0 && q.Limit < len(page) {
		page = page[:q.Limit]
	}
	return page, total, nil
}

// ListUsers 按 id 升序返回全部用户。
//
// 它是 ListUsersPage 不加任何条件的简写:全量列表与"筛出来的一页"共用同一段
// 查询与扫描,不各写一份。
func (s *Store) ListUsers() ([]model.User, error) {
	users, _, err := s.ListUsersPage(UserQuery{})
	return users, err
}

// UpdateUser 按 u.Username 整体覆盖一条用户记录,用户名本身不能通过它改。
//
// created_at 只在创建时写,这里不动它;其余字段(含 used_traffic)按 u 的值覆盖。
// 只改流量请用 AddUserTraffic,那是原子的累加。用户名不存在时返回 ErrNotFound。
//
// 套餐归属不由这里改(改归属用 SetUserPlan),只有一条例外:当前挂在 all_nodes
// 套餐下的用户被写入了非空的 inbound_ids —— 在 all_nodes 下这只可能是调用方想
// 限制这个用户,取并集没有意义,于是摘掉套餐,按旧语义生效。这条规则放在存储层,
// API、CLI、CSV 导入走的是同一条路径(见 RFC「新用户与旧客户端」)。
func (s *Store) UpdateUser(u *model.User) error {
	u.ExpireAt = normalizeExpireAt(u.ExpireAt)
	u.TrafficResetAt = normalizeExpireAt(u.TrafficResetAt)

	inboundIDs, err := encodeSlice(u.InboundIDs)
	if err != nil {
		return fmt.Errorf("序列化用户 %q 的 inbound_ids: %w", u.Username, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("更新用户 %q: %w", u.Username, err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE users
		 SET "key" = ?, sub_token = ?, enabled = ?, data_limit = ?, used_traffic = ?,
		     lifetime_used_traffic = ?, expire_at = ?, reset_strategy = ?,
		     traffic_reset_at = ?, inbound_ids = ?, note = ?
		 WHERE username = ?`,
		u.Key, u.SubToken, boolToInt(u.Enabled), u.DataLimit, u.UsedTraffic,
		u.LifetimeUsedTraffic, expireAtArg(u.ExpireAt), string(u.ResetStrategy),
		expireAtArg(u.TrafficResetAt), inboundIDs, u.Note,
		u.Username)
	if err != nil {
		return fmt.Errorf("更新用户 %q: %w", u.Username, err)
	}
	if err := affectedOrNotFound(res, fmt.Sprintf("用户 %q", u.Username)); err != nil {
		return err
	}

	if len(u.InboundIDs) > 0 {
		var allNodes int
		err := tx.QueryRow(
			`SELECT p.all_nodes FROM user_plans up JOIN plans p ON p.id = up.plan_id
			 WHERE up.user_id = (SELECT id FROM users WHERE username = ?)`,
			u.Username).Scan(&allNodes)
		switch {
		case err == nil && allNodes != 0:
			if _, err := tx.Exec(
				`DELETE FROM user_plans WHERE user_id = (SELECT id FROM users WHERE username = ?)`,
				u.Username); err != nil {
				return fmt.Errorf("摘掉用户 %q 的全节点套餐: %w", u.Username, err)
			}
			u.PlanID = nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("读取用户 %q 的套餐: %w", u.Username, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// 更新不改套餐归属,以库里现值重新解析;被摘掉套餐时 PlanID 会被清空。
	return s.resolveUserPlanAuth(u)
}

// DeleteUser 按用户名删除用户。
func (s *Store) DeleteUser(username string) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE username = ?`, username)
	if err != nil {
		return fmt.Errorf("删除用户 %q: %w", username, err)
	}
	if err := affectedOrNotFound(res, fmt.Sprintf("用户 %q", username)); err != nil {
		return err
	}
	// 活跃时间按用户名字符串记,没有指向 users 的外键,得手动清掉;不然同名用户
	// 再建出来会捡回旧时间。
	if _, err := s.db.Exec(`DELETE FROM user_activity WHERE username = ?`, username); err != nil {
		return fmt.Errorf("删除用户 %q 的活跃时间: %w", username, err)
	}
	return nil
}

// AddUserTraffic 把 up 与 down 累加到用户的流量上。
//
// used_traffic(本周期)与 lifetime_used_traffic(累计)在同一条 UPDATE 里一起加,
// 两者不会出现一个加了一个没加的状态。
//
// 控制面的定时任务会对同一个用户反复调用,所以这里是一条 UPDATE 里做
// used_traffic = used_traffic + ?,不先读再写,并发调用也不会丢计数。
func (s *Store) AddUserTraffic(username string, up, down int64) error {
	total := up + down
	res, err := s.db.Exec(
		`UPDATE users
		 SET used_traffic = used_traffic + ?,
		     lifetime_used_traffic = lifetime_used_traffic + ?
		 WHERE username = ?`,
		total, total, username)
	if err != nil {
		return fmt.Errorf("累加用户 %q 的流量: %w", username, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("用户 %q", username))
}

// ResetUserTraffic 把本周期用量归零,并把周期起算点设到 at;at 为零值表示清掉
// 起算点(周期改回从创建时间算起)。累计用量不动。
//
// 归零与设起算点是同一条 UPDATE,所以不会出现"流量清了、周期没走"或反过来的
// 中间状态。控制面的周期重置与用户手动重置都走这里,只有一种重置。
func (s *Store) ResetUserTraffic(username string, at time.Time) error {
	var arg any
	if !at.IsZero() {
		arg = normalizeTime(at).Unix()
	}
	res, err := s.db.Exec(
		`UPDATE users SET used_traffic = 0, traffic_reset_at = ? WHERE username = ?`,
		arg, username)
	if err != nil {
		return fmt.Errorf("重置用户 %q 的流量: %w", username, err)
	}
	return affectedOrNotFound(res, fmt.Sprintf("用户 %q", username))
}
