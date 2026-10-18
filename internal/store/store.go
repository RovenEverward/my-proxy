// Package store 是控制面的 SQLite 持久化层。
//
// 只有控制面进程打开它:本机 CLI 是纯 HTTP 客户端,不碰数据库文件。库文件由
// 控制面独占,所以这里不做跨进程协调,只保证本进程内的并发安全。
//
// 表结构刻意保持精简:五类对象各一张表(nodes / inbounds / hosts / users /
// settings),外加一张只追加的流量采样表 traffic_samples。SubscriptionRule 属于
// Settings,存成 settings.rules 的 JSON 文本;Inbound.ALPN / Inbound.ShortIDs /
// Host.ALPN / User.InboundIDs 这些切片字段同样存 JSON 文本,不另建关联表。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动,注册的驱动名是 "sqlite"
)

// ErrNotFound 表示要查的记录不存在,用 errors.Is 判断:
//
//	u, err := s.GetUser("nobody")
//	if errors.Is(err, store.ErrNotFound) { /* 404 */ }
var ErrNotFound = errors.New("记录不存在")

// Store 是一个 SQLite 数据库句柄。
//
// 每个方法返回的 *model.X 都是**副本**,调用方改它不会影响数据库,除非再调
// 对应的 Update。
//
// 并发:底层连接池被限制为 1 条连接(理由见 Open),因此同一时刻只有一条 SQL
// 在跑。本包内部不会在遍历 *sql.Rows 期间再发起查询,否则会等自己持有的那条
// 连接而死锁;新增查询代码时也要守住这条。
type Store struct {
	db *sql.DB
}

// pragmas 是每条连接都要带上的 pragma,以 DSN 参数交给驱动。驱动在每条新连接
// 建立后执行它们,所以连接池换连接也不会丢设置。
//
//   - journal_mode=WAL:读不挡写。控制面的定时任务写流量时,订阅请求照常读。
//   - busy_timeout=5000:库被别的进程(例如人工用 sqlite3 维护)占着写锁时,
//     等 5 秒再报错,而不是立刻返回 database is locked。
//   - foreign_keys=ON:inbounds/hosts 的 ON DELETE CASCADE 才会生效。
//   - synchronous=NORMAL:WAL 下的常规选择,提交不逐次 fsync;断电最多丢最后
//     几个事务,换来写入快一个量级。流量统计丢几秒可以接受。
const pragmas = "_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=synchronous(NORMAL)"

// schema 是建表语句,全部带 IF NOT EXISTS,重复打开同一个库是幂等的。
//
// 级联删除靠外键,不靠应用层逐表删:DeleteNode / DeleteInbound 因此各只需一条
// DELETE。users.expire_at 可空,存 Unix 秒;created_at 也存 Unix 秒,不用
// SQLite 的日期文本,免得时区与格式来回转换。
var schema = []string{
	`CREATE TABLE IF NOT EXISTS core_deployments (
		node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		core TEXT NOT NULL,
		api_port INTEGER NOT NULL DEFAULT 0,
		config_path TEXT NOT NULL DEFAULT '',
		service_name TEXT NOT NULL DEFAULT '',
		deployed_hash TEXT NOT NULL DEFAULT '',
		deployed_at INTEGER,
		PRIMARY KEY(node_id, core)
	)`,
	`CREATE TABLE IF NOT EXISTS nodes (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		name         TEXT    NOT NULL UNIQUE,
		address      TEXT    NOT NULL DEFAULT '',
		ssh_port     INTEGER NOT NULL DEFAULT 0,
		ssh_user     TEXT    NOT NULL DEFAULT '',
		api_port     INTEGER NOT NULL DEFAULT 0,
		config_path  TEXT    NOT NULL DEFAULT '',
		service_name TEXT    NOT NULL DEFAULT '',
		draining     INTEGER NOT NULL DEFAULT 0,
		outbound_ip  TEXT    NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS inbounds (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		node_id      INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		tag          TEXT    NOT NULL,
		protocol     TEXT    NOT NULL,
		listen       TEXT    NOT NULL DEFAULT '',
		port         INTEGER NOT NULL DEFAULT 0,
		network      TEXT    NOT NULL DEFAULT '',
		security     TEXT    NOT NULL DEFAULT '',
		ws_path      TEXT    NOT NULL DEFAULT '',
		grpc_service TEXT    NOT NULL DEFAULT '',
		sni          TEXT    NOT NULL DEFAULT '',
		alpn         TEXT    NOT NULL DEFAULT '[]',
		fingerprint  TEXT    NOT NULL DEFAULT '',
		cert_file    TEXT    NOT NULL DEFAULT '',
		key_file     TEXT    NOT NULL DEFAULT '',
		reality_dest TEXT    NOT NULL DEFAULT '',
		reality_priv TEXT    NOT NULL DEFAULT '',
		reality_pub  TEXT    NOT NULL DEFAULT '',
		short_ids    TEXT    NOT NULL DEFAULT '[]',
		flow         TEXT    NOT NULL DEFAULT '',
		xhttp_path   TEXT    NOT NULL DEFAULT '',
		xhttp_host   TEXT    NOT NULL DEFAULT '',
		xhttp_mode   TEXT    NOT NULL DEFAULT '',
		wg_secret    TEXT    NOT NULL DEFAULT '',
		wg_address   TEXT    NOT NULL DEFAULT '',
		wg_mtu       INTEGER NOT NULL DEFAULT 0,
		mux          INTEGER NOT NULL DEFAULT 0,
		fragment     INTEGER NOT NULL DEFAULT 0,
		max_users    INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_inbounds_node_id ON inbounds(node_id)`,

	`CREATE TABLE IF NOT EXISTS hosts (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		inbound_id INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
		remark     TEXT    NOT NULL DEFAULT '',
		address    TEXT    NOT NULL DEFAULT '',
		port       INTEGER NOT NULL DEFAULT 0,
		sni        TEXT    NOT NULL DEFAULT '',
		host       TEXT    NOT NULL DEFAULT '',
		path       TEXT    NOT NULL DEFAULT '',
		alpn       TEXT    NOT NULL DEFAULT '[]'
	)`,
	`CREATE INDEX IF NOT EXISTS idx_hosts_inbound_id ON hosts(inbound_id)`,

	`CREATE TABLE IF NOT EXISTS users (
		id                    INTEGER PRIMARY KEY AUTOINCREMENT,
		username              TEXT    NOT NULL UNIQUE,
		"key"                 TEXT    NOT NULL DEFAULT '',
		sub_token             TEXT    NOT NULL DEFAULT '',
		enabled               INTEGER NOT NULL DEFAULT 1,
		data_limit            INTEGER NOT NULL DEFAULT 0,
		used_traffic          INTEGER NOT NULL DEFAULT 0,
		lifetime_used_traffic INTEGER NOT NULL DEFAULT 0,
		expire_at             INTEGER,
		reset_strategy        TEXT    NOT NULL DEFAULT 'none',
		traffic_reset_at      INTEGER,
		inbound_ids           TEXT    NOT NULL DEFAULT '[]',
		note                  TEXT    NOT NULL DEFAULT '',
		created_at            INTEGER NOT NULL DEFAULT 0
	)`,
	// sub_token 上只建普通索引:没设 token 的用户都是空串,唯一索引会互相冲突。
	`CREATE INDEX IF NOT EXISTS idx_users_sub_token ON users(sub_token)`,

	// traffic_samples 是流量的历史采样,只追加、不更新:节点上报的累计值变了就写
	// 一行。它是「上次累计值」唯一的落点 —— 每用户每节点读最近一行当基准,不另设
	// 状态字段;同一批数据也支撑按区间重算用量与画曲线。
	//
	// node_id 指向 nodes 并随节点级联删除:节点都没了,它的采样也拼不出对账,和
	// inbounds / hosts 一个做法。username 这里没有外键 —— 整个库都用用户名字符串
	// 标识用户,本来就没有指向 users 的外键。
	//
	// 索引按 (node_id, username, sampled_at):采样总是先定节点、再在该节点下遍历
	// 用户,这个顺序能让「每用户最近一行」直接顺着索引扫一遍。
	`CREATE TABLE IF NOT EXISTS traffic_samples (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		username   TEXT    NOT NULL,
		node_id    INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		sampled_at INTEGER NOT NULL,
		uplink     INTEGER NOT NULL,
		downlink   INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_traffic_samples_node_user_at
		ON traffic_samples(node_id, username, sampled_at)`,

	// node_status 是每台节点最近一次的观察结果,一台一行。
	//
	// 与 nodes 表分开的理由是两者的生命周期不同:nodes 是"我们期望这台机器是什么
	// 样",而这里只是"刚才问它,它这么回答"。检查失败也是一次回答(Reachable=0,
	// error 里写着原因),所以它同样占用这一行。
	//
	// 刻意不放 deployed_hash / deployed_template_hash:那是"我们最后一次推上去
	// 的是什么",属于节点自己的属性,在 nodes 表上。放到这里的话,每次检查写
	// 状态都会把基准覆盖成观察值,漂移就永远看不出来了。
	//
	// node_id 是主键也是外键:一台节点一行,节点删掉时它的状态跟着走(不然删了
	// 再加一台同 id 的会把旧状态捡回来)。certificates 存 JSON 文本,与 inbounds
	// 的 alpn 一个做法。
	`CREATE TABLE IF NOT EXISTS node_status (
		node_id       INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
		checked_at    INTEGER NOT NULL,
		reachable     INTEGER NOT NULL DEFAULT 0,
		active        INTEGER NOT NULL DEFAULT 0,
		version       TEXT    NOT NULL DEFAULT '',
		observed_hash TEXT    NOT NULL DEFAULT '',
		certificates  TEXT    NOT NULL DEFAULT '[]',
		error         TEXT    NOT NULL DEFAULT ''
	)`,

	// settings 是单行表:id 恒为 1。
	`CREATE TABLE IF NOT EXISTS settings (
		id              INTEGER PRIMARY KEY CHECK (id = 1),
		sub_url_prefix  TEXT    NOT NULL DEFAULT '',
		profile_title   TEXT    NOT NULL DEFAULT '{USERNAME}',
		support_link    TEXT    NOT NULL DEFAULT '',
		update_interval INTEGER NOT NULL DEFAULT 24,
		rules           TEXT    NOT NULL DEFAULT '[]',
		templates       TEXT    NOT NULL DEFAULT '{}'
	)`,

	// sub_pulls 是每个用户最近一次订阅拉取的记录:一人一行,user_id 是主键也
	// 是外键,用户删掉时记录跟着走。first_at 只在第一次拉取时写入,之后只更新
	// last_at / format / client;吊销订阅 token 时整行清掉,新链接重新等第一次。
	`CREATE TABLE IF NOT EXISTS sub_pulls (
		user_id  INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		first_at INTEGER NOT NULL,
		last_at  INTEGER NOT NULL,
		format   TEXT    NOT NULL DEFAULT '',
		client   TEXT    NOT NULL DEFAULT ''
	)`,

	// user_notices 是"这条用户通知已经发过了"的流水账,一条 (user_id, kind)
	// 一行。它让用户侧提醒精确地只发一次:巡检每轮都看用户,不记账的话一轮漏跑
	// 或窗口横跨多轮就会重复发;记下之后,同一用户同一类提醒只在条件消失(续费/
	// 重置)后才会再发。
	//
	// 与 traffic_samples 一个思路:它只是记账,不承载业务数据 —— 删掉整张表,
	// 用户、流量、订阅全都照常工作,只是"已通知过"的标记丢了,下一轮可能重发。
	// 因此备份校验的必需表清单里刻意没有它。
	`CREATE TABLE IF NOT EXISTS user_notices (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		kind    TEXT    NOT NULL,
		sent_at INTEGER NOT NULL,
		PRIMARY KEY (user_id, kind)
	)`,
}

// Open 打开(不存在则创建)数据库文件并建表。建表幂等,重复调用安全。
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("打开数据库: 路径为空")
	}
	// 控制面把库放在自己的数据目录下,首次启动时该目录可能还不存在。
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录 %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("打开数据库 %s: %w", path, err)
	}

	// SQLite 同一时刻只允许一个写者。把连接池开到多条不会提高写入吞吐,只会让
	// 本进程自己的并发写互相撞 SQLITE_BUSY(两个写事务里必有一个要退避重试)。
	// 控制面的负载是少量订阅读取加定时流量写入,单连接足够,换来的是"不因自身
	// 并发而失败"。代价是一条慢查询会挡住其他查询,所以本包不在遍历 rows 时
	// 再发查询。busy_timeout 仍然保留:它防的是**别的进程**持锁。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库 %s: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	// 级联删除是 DeleteNode / DeleteInbound 正确性的前提。配置没生效就早点报
	// 错,好过删了节点却在库里留下一堆查不到的孤儿 inbound / host。
	if err := s.checkForeignKeys(); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库 %s: %w", path, err)
	}
	return s, nil
}

// Close 关闭数据库。关闭后所有方法都会报错。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("关闭数据库: %w", err)
	}
	return nil
}

// dsn 拼出 file: URI 形式的 DSN。
//
// 路径按 URI 规则转义,免得路径里出现 ? 或 # 时被当成查询串的开头;pragma 用
// 驱动认识的 _pragma 参数传递,每条新连接都会执行一次。
func dsn(path string) string {
	u := url.URL{Path: path}
	return "file:" + u.EscapedPath() + "?" + pragmas
}

// migrate 建表,再补上后来加的列。逐条执行而不是把整段 DDL 一次交给驱动,这样
// 报错能指出是哪条。
func (s *Store) migrate() error {
	for i, ddl := range schema {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("建表(第 %d 条语句): %w", i+1, err)
		}
	}
	if err := s.addMissingColumns(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_traffic_samples_core ON traffic_samples(node_id, core, username, id)`); err != nil {
		return err
	}
	if err := s.migrateCapabilities(); err != nil {
		return err
	}
	if err := s.migrateMetadata(); err != nil {
		return err
	}
	if err := s.migrateRouting(); err != nil {
		return err
	}
	// 删 purposes 表必须在 migrateRouting 之后:它要读 routing_state 的模式。
	if err := s.dropPurposes(); err != nil {
		return err
	}
	if err := s.migrateIdentity(); err != nil {
		return err
	}
	if err := s.migrateDevices(); err != nil {
		return err
	}
	if err := s.migrateDevicePassword(); err != nil {
		return err
	}
	if err := s.migrateDiagnostics(); err != nil {
		return err
	}
	if err := s.migrateNodePending(); err != nil {
		return err
	}
	if err := s.migrateActivity(); err != nil {
		return err
	}
	if err := s.migrateJobs(); err != nil {
		return err
	}
	// 套餐迁移放最后:它要读节点元数据与入站,且必须在其它表都建好之后。
	return s.migratePlans()
}

// addedColumns 是建表语句之后补加的列。CREATE TABLE IF NOT EXISTS 对已存在的表
// 什么都不做,所以更早版本建的库要靠这里补。每条都先查一下列在不在,因此对任何
// 状态的库都是幂等的。
var addedColumns = []struct{ table, column, ddl string }{
	{"traffic_samples", "generation", `ALTER TABLE traffic_samples ADD COLUMN generation TEXT NOT NULL DEFAULT ''`},
	{"node_status", "cores", `ALTER TABLE node_status ADD COLUMN cores TEXT NOT NULL DEFAULT '[]'`},
	{"inbounds", "congestion_control", `ALTER TABLE inbounds ADD COLUMN congestion_control TEXT NOT NULL DEFAULT ''`},
	{"traffic_samples", "core", `ALTER TABLE traffic_samples ADD COLUMN core TEXT NOT NULL DEFAULT 'xray'`},
	{"users", "lifetime_used_traffic",
		`ALTER TABLE users ADD COLUMN lifetime_used_traffic INTEGER NOT NULL DEFAULT 0`},
	{"users", "traffic_reset_at",
		`ALTER TABLE users ADD COLUMN traffic_reset_at INTEGER`},
	{"settings", "templates",
		`ALTER TABLE settings ADD COLUMN templates TEXT NOT NULL DEFAULT '{}'`},
	// 下发基准放在 nodes 上而不是 node_status 里:它是节点记录自己的属性(这台
	// 机器上我们最后一次推的是什么),而观察结果才属于"一次检查"。deployed_at
	// 可空 —— 没推过就是没有那一刻,空串的 0 会让人以为 1970 年推过一次。
	{"nodes", "deployed_hash",
		`ALTER TABLE nodes ADD COLUMN deployed_hash TEXT NOT NULL DEFAULT ''`},
	{"nodes", "deployed_at",
		`ALTER TABLE nodes ADD COLUMN deployed_at INTEGER`},
	// 通知设置。列有默认值,迁移补上后旧库读设置不会报错:webhook_url 缺省空串
	// 表示不通知,两个阈值缺省用模型层的默认值(到期前 3 天 / 用量 80%)。
	{"settings", "webhook_url",
		`ALTER TABLE settings ADD COLUMN webhook_url TEXT NOT NULL DEFAULT ''`},
	{"settings", "notify_days_left",
		`ALTER TABLE settings ADD COLUMN notify_days_left INTEGER NOT NULL DEFAULT 3`},
	{"settings", "notify_usage_percent",
		`ALTER TABLE settings ADD COLUMN notify_usage_percent INTEGER NOT NULL DEFAULT 80`},
	// 批 5 协议支持:xhttp / splithttp / httpupgrade 的参数、wireguard 的服务端
	// 配置、客户端 mux / fragment 开关。wg_secret 与 reality_priv 一个待遇:
	// 只在服务端出现,客户端拿的是公钥。
	{"inbounds", "xhttp_path",
		`ALTER TABLE inbounds ADD COLUMN xhttp_path TEXT NOT NULL DEFAULT ''`},
	{"inbounds", "xhttp_host",
		`ALTER TABLE inbounds ADD COLUMN xhttp_host TEXT NOT NULL DEFAULT ''`},
	{"inbounds", "xhttp_mode",
		`ALTER TABLE inbounds ADD COLUMN xhttp_mode TEXT NOT NULL DEFAULT ''`},
	{"inbounds", "wg_secret",
		`ALTER TABLE inbounds ADD COLUMN wg_secret TEXT NOT NULL DEFAULT ''`},
	{"inbounds", "wg_address",
		`ALTER TABLE inbounds ADD COLUMN wg_address TEXT NOT NULL DEFAULT ''`},
	{"inbounds", "wg_mtu",
		`ALTER TABLE inbounds ADD COLUMN wg_mtu INTEGER NOT NULL DEFAULT 0`},
	{"inbounds", "mux",
		`ALTER TABLE inbounds ADD COLUMN mux INTEGER NOT NULL DEFAULT 0`},
	{"inbounds", "fragment",
		`ALTER TABLE inbounds ADD COLUMN fragment INTEGER NOT NULL DEFAULT 0`},
	{"inbounds", "max_users",
		`ALTER TABLE inbounds ADD COLUMN max_users INTEGER NOT NULL DEFAULT 0`},
	{"nodes", "draining",
		`ALTER TABLE nodes ADD COLUMN draining INTEGER NOT NULL DEFAULT 0`},
	{"nodes", "outbound_ip",
		`ALTER TABLE nodes ADD COLUMN outbound_ip TEXT NOT NULL DEFAULT ''`},
	// 模板哈希:下发时那份配置「不带用户」的 sha256。deployed_hash 含着用户名单,
	// 比不出模板改没改;空串表示还没在这一版上下发过,所以不参与漂移判定。
	{"nodes", "deployed_template_hash",
		`ALTER TABLE nodes ADD COLUMN deployed_template_hash TEXT NOT NULL DEFAULT ''`},
}

// addMissingColumns 把 addedColumns 里缺的列补上。
func (s *Store) addMissingColumns() error {
	for _, c := range addedColumns {
		has, err := s.hasColumn(c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := s.db.Exec(c.ddl); err != nil {
			return fmt.Errorf("给 %s 补列 %s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// hasColumn 查一张表上有没有某一列。表名只来自包内常量,不是外部输入。
func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, fmt.Errorf("读取 %s 的列: %w", table, err)
	}
	// 先收完再判断:本包只有一条连接,遍历 rows 期间再发查询会等自己而死锁。
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return false, fmt.Errorf("读取 %s 的列名: %w", table, err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("遍历 %s 的列: %w", table, err)
	}
	rows.Close()

	for _, n := range names {
		if n == column {
			return true, nil
		}
	}
	return false, nil
}

// checkForeignKeys 确认连接上真的开了外键。pragma 是每连接的,DSN 参数按理已
// 经设上;这里实测一次,失败就拒绝启动而不是默默丢级联。
func (s *Store) checkForeignKeys() error {
	var on int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
		return fmt.Errorf("检查 foreign_keys: %w", err)
	}
	if on != 1 {
		return errors.New("外键约束没有开启,级联删除不会生效")
	}
	return nil
}

// rowScanner 让 GetX 用的 *sql.Row 和 ListX 用的 *sql.Rows 共用同一段扫描代码。
type rowScanner interface {
	Scan(dest ...any) error
}

// wrapNotFound 把 sql.ErrNoRows 换成能 errors.Is(err, ErrNotFound) 判断的错误。
func wrapNotFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("未找到%s: %w", what, ErrNotFound)
	}
	return err
}

// affectedOrNotFound 在 UPDATE / DELETE 一行都没命中时返回 ErrNotFound。
// SQLite 的 changes 计数包含"值没变"的命中行,所以命中就一定非零。
func affectedOrNotFound(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取%s的受影响行数: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("未找到%s: %w", what, ErrNotFound)
	}
	return nil
}

// encodeSlice 把切片字段编码成 JSON 文本列。nil 与空切片都写成 "[]"。
func encodeSlice[T any](v []T) (string, error) {
	if len(v) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("序列化切片字段: %w", err)
	}
	return string(b), nil
}

// decodeSlice 把 JSON 文本列读回切片。空文本、"null"、"[]" 一律读成 nil,这样
// nil 和空切片往返后是同一个值,也不会读成含空字符串的切片。
func decodeSlice[T any](raw string) ([]T, error) {
	switch strings.TrimSpace(raw) {
	case "", "null", "[]":
		return nil, nil
	}
	var out []T
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("解析切片字段 %q: %w", raw, err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// boolToInt 把 bool 存成 SQLite 的 0/1。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
