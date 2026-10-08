package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// seedListUsers 造 7 个用户,覆盖四种状态与三类入站范围。
//
// 覆盖点:
//   - alice    可用
//   - Bob      可用,名字大写(测 search 大小写)
//   - bobby    可用,名字是 bob 的子串(测子串匹配)
//   - carol    刚好用满上限 -> limited
//   - dave     到期时刻取"现在"(会被 CreateUser 截到整秒,因而严格早于 now)
//     -> 按 Status 的判定是 expired。要造出"正好等于 now"的边界只能绕过存储层直接
//     构造 model.User 来看(见 internal/model 的判定测试)
//   - erin     已经过期(到期时刻在 1 秒前)-> expired
//   - frank    显式停用 -> disabled
//
// 入站范围:alice 限定 [1,2] 与 [2,3](两个不同的限定各覆盖一种筛选结果);
// bob 限定 [2];carol 限定 [2];其余(bobby、dave、erin、frank)为空 = 不限入站。
// 这样入站 1 只有 alice 能用(唯一一个"受限且不含 1"之外还要看谁限定了别的入站"),
// 入站 3 则是 alice 与 bob 加上全部不限入站的用户。
//
// 返回按插入顺序(也就是 ID 升序)排列的用户。
func seedListUsers(t *testing.T, s *Store, now time.Time) []*model.User {
	t.Helper()
	expire := func(d time.Duration) *time.Time {
		v := now.Add(d)
		return &v
	}
	// 只有这两个用户限定入站 1 / 入站 3;Bob 的入站范围见下。
	specs := []*model.User{
		{Username: "alice", Enabled: true, InboundIDs: []int64{1, 3}},
		{Username: "Bob", Enabled: true, InboundIDs: []int64{3}},
		{Username: "bobby", Enabled: true},
		{Username: "carol", Enabled: true, DataLimit: 4096, UsedTraffic: 4096, InboundIDs: []int64{2}},
		{Username: "dave", Enabled: true, ExpireAt: expire(0)},
		{Username: "erin", Enabled: true, ExpireAt: expire(-time.Second)},
		{Username: "frank", Enabled: false},
	}
	users := make([]*model.User, 0, len(specs))
	for _, spec := range specs {
		u := *spec
		u.Key = "k-" + u.Username
		u.SubToken = "t-" + u.Username
		u.CreatedAt = now
		if err := s.CreateUser(&u); err != nil {
			t.Fatalf("创建用户 %s: %v", u.Username, err)
		}
		created := u
		users = append(users, &created)
	}
	return users
}

// usernames 取出列表里的用户名,方便按顺序断言。
func usernames(users []model.User) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.Username)
	}
	return out
}

// listPage 跑一次 ListUsersPage 并把 nil 与空切片都当"空"来处理。
func listPage(t *testing.T, s *Store, q UserQuery) ([]string, int) {
	t.Helper()
	users, total, err := s.ListUsersPage(q)
	if err != nil {
		t.Fatalf("ListUsersPage(%+v): %v", q, err)
	}
	return usernames(users), total
}

// equalStrings 断言两个字符串切片逐个相等(nil 与空切片视为相同)。
func equalStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// TestListUsersPageNoFilter 断言不加条件时的顺序与总数:全部 7 个、按 id 升序。
//
// 顺序是接口的一部分:分页按 offset 切片,底层顺序不稳定的话翻页就会漏人或重复。
func TestListUsersPageNoFilter(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	seedListUsers(t, s, now)

	users, total, err := s.ListUsersPage(UserQuery{})
	if err != nil {
		t.Fatalf("ListUsersPage: %v", err)
	}
	if total != 7 {
		t.Fatalf("total = %d, want 7", total)
	}
	if len(users) != 7 {
		t.Fatalf("返回 %d 个用户, want 7", len(users))
	}
	want := []string{"alice", "Bob", "bobby", "carol", "dave", "erin", "frank"}
	equalStrings(t, "不加条件的用户名顺序", usernames(users), want)
	for i := 1; i < len(users); i++ {
		if users[i-1].ID >= users[i].ID {
			t.Fatalf("第 %d 个用户的 id %d 没有大于前一个 %d:结果没有按 id 升序",
				i, users[i].ID, users[i-1].ID)
		}
	}
}

// TestListUsersPageLimitOffset 断言 total 是筛选后的总数而不是本页条数,
// 且 Limit/Offset 切的是同一个序列。
func TestListUsersPageLimitOffset(t *testing.T) {
	s := newTestStore(t)
	seedListUsers(t, s, time.Now())

	all := []string{"alice", "Bob", "bobby", "carol", "dave", "erin", "frank"}

	// 第 1 页:limit 3 时给前 3 个,但 total 仍然是 7。
	got, total := listPage(t, s, UserQuery{Limit: 3})
	equalStrings(t, "limit=3 的第 1 页", got, all[:3])
	if total != 7 {
		t.Fatalf("limit=3 时 total = %d, want 7(total 是筛选后的总数,不是本页条数)", total)
	}

	// 第 2 页:offset 跳过前 3 个。
	got, total = listPage(t, s, UserQuery{Limit: 3, Offset: 3})
	equalStrings(t, "limit=3 offset=3", got, all[3:6])
	if total != 7 {
		t.Fatalf("带 offset 时 total = %d, want 7", total)
	}

	// 最后一页只有 1 个。
	got, total = listPage(t, s, UserQuery{Limit: 3, Offset: 6})
	equalStrings(t, "limit=3 offset=6", got, all[6:])
	if total != 7 {
		t.Fatalf("最后一页 total = %d, want 7", total)
	}

	// limit 比剩下的多时不该补空。
	got, _ = listPage(t, s, UserQuery{Limit: 100, Offset: 5})
	equalStrings(t, "limit 超出剩余", got, all[5:])

	// limit 为 0 表示不分页。
	got, _ = listPage(t, s, UserQuery{Offset: 2})
	equalStrings(t, "limit=0 表示到末尾", got, all[2:])
}

// TestListUsersPageOffsetBeyondTotal 断言越界时返回空切片而不是报错,
// 并且 total 照常给对 —— CLI 正是靠"本页 0 条 + total 有值"才知道翻过头了。
func TestListUsersPageOffsetBeyondTotal(t *testing.T) {
	s := newTestStore(t)
	seedListUsers(t, s, time.Now())

	users, total, err := s.ListUsersPage(UserQuery{Limit: 50, Offset: 7})
	if err != nil {
		t.Fatalf("offset 等于 total: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("offset 等于 total 时返回了 %d 个用户, want 0", len(users))
	}
	if total != 7 {
		t.Fatalf("offset 等于 total 时 total = %d, want 7", total)
	}

	users, total, err = s.ListUsersPage(UserQuery{Limit: 10, Offset: 999})
	if err != nil {
		t.Fatalf("offset 远超 total: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("offset 远超 total 时返回了 %d 个用户, want 0", len(users))
	}
	if total != 7 {
		t.Fatalf("offset 远超 total 时 total = %d, want 7", total)
	}

	// 空库上 offset=0 也是空的。
	empty := newTestStore(t)
	if users, total, err := empty.ListUsersPage(UserQuery{}); err != nil || total != 0 || len(users) != 0 {
		t.Fatalf("空库: users=%d total=%d err=%v, want 0/0/nil", len(users), total, err)
	}
}

// TestListUsersPageByStatus 逐个状态取值筛一遍。
//
// 这里的期望值是照 model.User.Status 的优先级手写出来的:无论实现怎么改,
// "谁该出现在哪个状态的列表里"都不能变。
func TestListUsersPageByStatus(t *testing.T) {
	s := newTestStore(t)
	seedListUsers(t, s, time.Now())

	cases := []struct {
		status model.Status
		want   []string
	}{
		{model.StatusActive, []string{"alice", "Bob", "bobby"}},
		{model.StatusLimited, []string{"carol"}},
		{model.StatusExpired, []string{"dave", "erin"}},
		{model.StatusDisabled, []string{"frank"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			got, total := listPage(t, s, UserQuery{Status: tc.status})
			equalStrings(t, "status="+string(tc.status), got, tc.want)
			if total != len(tc.want) {
				t.Fatalf("status=%s 的 total = %d, want %d(筛选后的总数)",
					tc.status, total, len(tc.want))
			}
		})
	}

	// 状态筛选能与分页叠加:筛选先做,total 是筛选后的数。
	got, total := listPage(t, s, UserQuery{Status: model.StatusActive, Limit: 2, Offset: 1})
	equalStrings(t, "status=active 的第 2 页", got, []string{"Bob", "bobby"})
	if total != 3 {
		t.Fatalf("status=active 分页时 total = %d, want 3", total)
	}
}

// TestListUsersPageBySearch 断言 search 是不分大小写的子串匹配,前后空白被忽略。
func TestListUsersPageBySearch(t *testing.T) {
	s := newTestStore(t)
	seedListUsers(t, s, time.Now())

	cases := []struct {
		name   string
		search string
		want   []string
	}{
		{"小写能匹配到首字母大写的用户名", "bob", []string{"Bob", "bobby"}},
		{"大写同样能匹配", "BOB", []string{"Bob", "bobby"}},
		{"混合大小写", "BoB", []string{"Bob", "bobby"}},
		{"子串而不是前缀", "obb", []string{"bobby"}},
		{"两头的空白被忽略", "  bob  ", []string{"Bob", "bobby"}},
		{"匹配不到就是空", "nobody", nil},
		{"空串等于不筛", "", []string{"alice", "Bob", "bobby", "carol", "dave", "erin", "frank"}},
		{"只有空白等于不筛", "   ", []string{"alice", "Bob", "bobby", "carol", "dave", "erin", "frank"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total := listPage(t, s, UserQuery{Search: tc.search})
			equalStrings(t, fmt.Sprintf("search=%q", tc.search), got, tc.want)
			if total != len(tc.want) {
				t.Fatalf("search=%q 的 total = %d, want %d", tc.search, total, len(tc.want))
			}
		})
	}
}

// TestListUsersPageByInbound 断言"不限入站"的用户也算能用某个入站。
//
// 这是最容易写错的一条:InboundIDs 为空在模型里表示全部入站,如果筛选只比对
// 切片里的值,不限制的用户会被整批漏掉 —— 而他们恰恰是大多数。
func TestListUsersPageByInbound(t *testing.T) {
	s := newTestStore(t)
	seedListUsers(t, s, time.Now())

	// 入站 2:显式限定且确实含 2 的只有 carol;alice 限定 [1,3] 与 Bob 限定 [3]
	// 都用不了 2,而"不限入站"的 bobby/dave/erin/frank 照样能用 —— 后半截正是
	// 这条测试要守住的:空 InboundIDs 表示全部入站。
	got, total := listPage(t, s, UserQuery{InboundID: 2})
	equalStrings(t, "inbound=2 含不限入站的用户", got,
		[]string{"bobby", "carol", "dave", "erin", "frank"})
	if total != 5 {
		t.Fatalf("inbound=2 的 total = %d, want 5(不限入站的用户要算在内)", total)
	}

	// 入站 1:alice 限定 [1,3] 含 1;没人限定入站 1 的其余用户里,
	// bob 限定 [3] 所以用不了,carol 限定 [2] 也用不了,其余"不限入站"的都能用。
	got, total = listPage(t, s, UserQuery{InboundID: 1})
	equalStrings(t, "inbound=1 含不限入站的用户", got,
		[]string{"alice", "bobby", "dave", "erin", "frank"})
	if total != 5 {
		t.Fatalf("inbound=1 的 total = %d, want 5(不限入站的用户要算在内)", total)
	}

	// 入站 3:alice([1,3]) 与 Bob([3]) 显式限定了,加上全部不限入站的用户。
	got, total = listPage(t, s, UserQuery{InboundID: 3})
	equalStrings(t, "inbound=3", got, []string{"alice", "Bob", "bobby", "dave", "erin", "frank"})
	if total != 6 {
		t.Fatalf("inbound=3 的 total = %d, want 6", total)
	}

	// 没人限定的入站:三个显式限定 [1,3]/[3]/[2] 的用户用不了,其余都能用。
	got, total = listPage(t, s, UserQuery{InboundID: 99})
	equalStrings(t, "inbound=99", got, []string{"bobby", "dave", "erin", "frank"})
	if total != 4 {
		t.Fatalf("inbound=99 的 total = %d, want 4", total)
	}

	// 0 表示不按入站筛。
	got, total = listPage(t, s, UserQuery{InboundID: 0})
	if len(got) != 7 || total != 7 {
		t.Fatalf("inbound=0 应当不筛: got %d 个, total=%d", len(got), total)
	}
}

// TestListUsersPageCombinedFilters 断言多个条件是与的关系,且 total 跟着筛选走。
func TestListUsersPageCombinedFilters(t *testing.T) {
	s := newTestStore(t)
	seedListUsers(t, s, time.Now())

	// active + 名字含 bob + 能用入站 3 -> Bob(限定 [3]);bobby 名字也含 bob,
	// 但它"不限入站"所以照样能用入站 3。两者都该在结果里。
	got, total := listPage(t, s, UserQuery{Status: model.StatusActive, Search: "bob", InboundID: 3})
	equalStrings(t, "active+bob+inbound=3", got, []string{"Bob", "bobby"})
	if total != 2 {
		t.Fatalf("组合筛选的 total = %d, want 2", total)
	}

	// 同样的名字但换个状态:没有一个是 expired,结果为空。
	got, total = listPage(t, s, UserQuery{Status: model.StatusExpired, Search: "bob"})
	if len(got) != 0 || total != 0 {
		t.Fatalf("expired+bob 应当为空, got %v total=%d", got, total)
	}

	// 组合筛选也支持分页:total 是筛选后的数,offset 作用在筛选后的序列上。
	got, total = listPage(t, s, UserQuery{Search: "bob", Limit: 1, Offset: 1})
	equalStrings(t, "search=bob 的第 2 页", got, []string{"bobby"})
	if total != 2 {
		t.Fatalf("search=bob 分页时 total = %d, want 2", total)
	}
}

// TestListUsersMatchesListUsersPage ListUsers() 必须就是 ListUsersPage(UserQuery{})。
//
// 它只是简写;如果哪天它换了实现(比如改回直接查库、忘了排序),这里会红。
func TestListUsersMatchesListUsersPage(t *testing.T) {
	s := newTestStore(t)
	seedListUsers(t, s, time.Now())

	plain, err := s.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	paged, total, err := s.ListUsersPage(UserQuery{})
	if err != nil {
		t.Fatalf("ListUsersPage: %v", err)
	}
	if total != len(paged) {
		t.Fatalf("不带条件时 total = %d 与返回条数 %d 不一致", total, len(paged))
	}
	if !sameStrings(usernames(plain), usernames(paged)) {
		t.Fatalf("ListUsers() = %v, ListUsersPage({}) = %v, 两者应当一致",
			usernames(plain), usernames(paged))
	}

	// 空库上两者也都应当是空的。
	empty := newTestStore(t)
	plainEmpty, err := empty.ListUsers()
	if err != nil {
		t.Fatalf("空库 ListUsers: %v", err)
	}
	if len(plainEmpty) != 0 {
		t.Fatalf("空库 ListUsers 返回了 %d 个用户", len(plainEmpty))
	}
}

// TestListUsersPageScalesLinearly 是性能底线,不是毫秒级指标。
//
// 筛选与分页在 Go 里做,每次列表都要把用户读进内存。这没问题,但前提是筛选本身
// 别退化成 O(n²)(比如每筛一个用户就重查一次库、或者对每个 offset 都从头扫一遍)。
// 1000 个用户下,即使 -race 加慢 CI,6 秒也远远够用;真退化成平方会直接跑飞。
func TestListUsersPageScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("性能底线在 -short 下跳过")
	}
	s := newTestStore(t)
	const n = 1000
	now := time.Now()
	expire := now.Add(24 * time.Hour)
	err := func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for i := 0; i < n; i++ {
			u := model.User{
				Username:  fmt.Sprintf("bulk-%04d", i),
				Key:       fmt.Sprintf("k-%04d", i),
				SubToken:  fmt.Sprintf("t-%04d", i),
				Enabled:   i%3 != 0,
				DataLimit: int64(i % 100),
				// 每两个用户里有一个用满上限 -> limited
				UsedTraffic: int64((i % 100) * (i % 2)),
				ExpireAt:    &expire,
				CreatedAt:   now,
			}
			if i%2 == 1 {
				u.UsedTraffic = u.DataLimit
			}
			if _, err := tx.Exec(
				`INSERT INTO users (username, "key", sub_token, enabled, data_limit, used_traffic,
					lifetime_used_traffic, expire_at, reset_strategy, traffic_reset_at,
					inbound_ids, note, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				u.Username, u.Key, u.SubToken, boolToInt(u.Enabled), u.DataLimit,
				u.UsedTraffic, 0, u.ExpireAt.Unix(), "none", nil, "[]", "", u.CreatedAt.Unix(),
			); err != nil {
				return err
			}
		}
		return tx.Commit()
	}()
	if err != nil {
		t.Fatalf("批量造用户: %v", err)
	}

	limit := time.Now().Add(6 * time.Second)

	// 一页 50:筛选照样得扫全表,但结果只切 50 条。
	page, total, err := s.ListUsersPage(UserQuery{Limit: 50})
	if err != nil {
		t.Fatalf("取一页: %v", err)
	}
	if total != n {
		t.Fatalf("total = %d, want %d", total, n)
	}
	if len(page) != 50 {
		t.Fatalf("一页返回 %d 条, want 50", len(page))
	}

	// 取全部。
	all, total, err := s.ListUsersPage(UserQuery{})
	if err != nil {
		t.Fatalf("取全部: %v", err)
	}
	if total != n || len(all) != n {
		t.Fatalf("取全部得到 %d 条 / total=%d, want %d", len(all), total, n)
	}

	// 带筛选(状态 + 搜索)也一样。
	filtered, _, err := s.ListUsersPage(UserQuery{Status: model.StatusLimited, Search: "bulk-01", Limit: 20})
	if err != nil {
		t.Fatalf("带筛选取一页: %v", err)
	}
	if len(filtered) > 20 {
		t.Fatalf("带筛选的一页有 %d 条,超过 limit", len(filtered))
	}

	if time.Now().After(limit) {
		t.Fatalf("%d 个用户上取一页与取全部超过 6 秒,筛选可能退化成了平方复杂度", n)
	}
}
