package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// fakeUserAPI 是一个只认用户接口的假控制面,并把收到的请求记下来供断言。
//
// upsertUser 走的是"先 GET,404 就 POST,否则改定义字段再 PUT",所以三个动词
// 都得能回答;GET 不存在的用户名必须回 404,否则导入永远走不到新增那条路。
type fakeUserAPI struct {
	mu       sync.Mutex
	users    map[string]model.User // 按用户名存当前状态
	plans    []model.Plan          // GET /api/plans 的返回
	requests []fakeRequest         // 按时间顺序记下每个请求
	nextID   int64
	// failOn 是按用户名让读/写失败的钩子;用来测批量命令的部分失败。
	failOn map[string]int
}

type fakeRequest struct {
	Method   string
	Path     string
	Body     []byte
	Query    string
	Username string
}

func newFakeUserAPI(t *testing.T) *fakeUserAPI {
	t.Helper()
	f := &fakeUserAPI{users: map[string]model.User{}, failOn: map[string]int{}}
	fakeControlPlane(t, f.handler)
	return f
}

// add 直接往假控制面里塞一个用户,不走 HTTP。
func (f *fakeUserAPI) add(u model.User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u.ID == 0 {
		f.nextID++
		u.ID = f.nextID
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	if u.Key == "" {
		u.Key = "k-" + u.Username
	}
	if u.SubToken == "" {
		u.SubToken = "t-" + u.Username
	}
	f.users[u.Username] = u
}

// fail 让某个用户名的请求返回指定状态码。
func (f *fakeUserAPI) fail(username string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOn[username] = code
}

// addPlan 往假控制面里塞一个套餐,供 CSV 的「套餐」列解析。
func (f *fakeUserAPI) addPlan(p model.Plan) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plans = append(f.plans, p)
}

// recorded 返回记录下来的请求(副本)。
func (f *fakeUserAPI) recorded() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// get 取假控制面里当前的某个用户。
func (f *fakeUserAPI) get(username string) (model.User, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[username]
	return u, ok
}

// methodsFor 列出针对某个用户名发生过的请求方法,按顺序。
func (f *fakeUserAPI) methodsFor(username string) []string {
	var out []string
	for _, r := range f.recorded() {
		if r.Username == username {
			out = append(out, r.Method)
		}
	}
	return out
}

// listQueries 列出发生过的 GET /api/users 的查询串。
func (f *fakeUserAPI) listQueries() []string {
	var out []string
	for _, r := range f.recorded() {
		if r.Method == http.MethodGet && r.Path == "/api/users" {
			out = append(out, r.Query)
		}
	}
	return out
}

func (f *fakeUserAPI) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	username := strings.TrimPrefix(r.URL.Path, "/api/users")
	username = strings.TrimPrefix(username, "/")

	rec := fakeRequest{
		Method:   r.Method,
		Path:     r.URL.Path,
		Body:     body,
		Query:    r.URL.RawQuery,
		Username: username,
	}
	f.mu.Lock()
	f.requests = append(f.requests, rec)
	code, shouldFail := f.failOn[username]
	f.mu.Unlock()

	writeJSONReply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if v == nil {
			_, _ = w.Write([]byte("null\n"))
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}

	if shouldFail && username != "" {
		writeJSONReply(code, map[string]string{"error": fmt.Sprintf("假的失败:%s", username)})
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/plans":
		// CSV 有「套餐」列时导入会先拉一次套餐列表。
		f.mu.Lock()
		plans := append([]model.Plan(nil), f.plans...)
		f.mu.Unlock()
		if plans == nil {
			plans = []model.Plan{}
		}
		writeJSONReply(http.StatusOK, plans)
	case r.Method == http.MethodGet && username == "":
		// GET /api/users —— 导出/FetchAllUsers 走这里。
		q := r.URL.Query()
		limit := 50
		if v := q.Get("limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		offset := 0
		if v := q.Get("offset"); v != "" {
			fmt.Sscanf(v, "%d", &offset)
		}
		f.mu.Lock()
		names := make([]string, 0, len(f.users))
		for name := range f.users {
			names = append(names, name)
		}
		// 按 id 升序,和真实控制面一致。
		for i := 1; i < len(names); i++ {
			for j := i; j > 0; j-- {
				if f.users[names[j-1]].ID > f.users[names[j]].ID {
					names[j-1], names[j] = names[j], names[j-1]
				}
			}
		}
		total := len(names)
		users := make([]model.User, 0, limit)
		for i := offset; i < len(names) && len(users) < limit; i++ {
			users = append(users, f.users[names[i]])
		}
		f.mu.Unlock()
		if users == nil {
			users = []model.User{}
		}
		writeJSONReply(http.StatusOK, map[string]any{
			"users": users, "total": total, "limit": limit, "offset": offset,
		})

	case r.Method == http.MethodGet:
		f.mu.Lock()
		u, ok := f.users[username]
		f.mu.Unlock()
		if !ok {
			writeJSONReply(http.StatusNotFound, map[string]string{"error": "用户不存在"})
			return
		}
		writeJSONReply(http.StatusOK, u)

	case r.Method == http.MethodPost && username == "":
		var u model.User
		if err := json.Unmarshal(body, &u); err != nil {
			writeJSONReply(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		f.mu.Lock()
		f.nextID++
		u.ID = f.nextID
		u.CreatedAt = time.Now()
		f.users[u.Username] = u
		f.mu.Unlock()
		writeJSONReply(http.StatusCreated, u)

	case r.Method == http.MethodPut:
		var u model.User
		if err := json.Unmarshal(body, &u); err != nil {
			writeJSONReply(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		f.mu.Lock()
		old, ok := f.users[username]
		f.mu.Unlock()
		if !ok {
			writeJSONReply(http.StatusNotFound, map[string]string{"error": "用户不存在"})
			return
		}
		// 服务端的真实行为:留空的凭证沿用原值,运行状态不被覆盖。
		if u.Key == "" {
			u.Key = old.Key
		}
		if u.SubToken == "" {
			u.SubToken = old.SubToken
		}
		// 请求体里没带 plan_id 的键 → 保持原套餐(老客户端与空 CSV 单元格)。
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(body, &raw)
		if _, present := raw["plan_id"]; !present {
			u.PlanID = old.PlanID
		}
		u.ID = old.ID
		u.CreatedAt = old.CreatedAt
		f.mu.Lock()
		f.users[username] = u
		f.mu.Unlock()
		writeJSONReply(http.StatusOK, u)

	case r.Method == http.MethodDelete:
		f.mu.Lock()
		_, ok := f.users[username]
		delete(f.users, username)
		f.mu.Unlock()
		if !ok {
			writeJSONReply(http.StatusNotFound, map[string]string{"error": "用户不存在"})
			return
		}
		writeJSONReply(http.StatusOK, nil)

	default:
		writeJSONReply(http.StatusNotFound, map[string]string{"error": "假控制面没实现 " + r.Method + " " + r.URL.Path})
	}
}

// TestCSVExportImportRoundTrip 断言导出再导入之后,定义字段一个不差。
//
// 这是 CSV 那套空值写法("不限"/"不过期"/"全部"/"none")的往返契约:导出的人
// 改一行再导回去,不该因为某个词变了意思而静默改掉别人的配置。
func TestCSVExportImportRoundTrip(t *testing.T) {
	f := newFakeUserAPI(t)

	expire := time.Date(2027, 3, 15, 23, 59, 59, 0, time.Local)
	original := []userRow{
		{
			Username:      "alice",
			Enabled:       true,
			DataLimit:     10 << 30, // 10GB
			ExpireAt:      &expire,
			ResetStrategy: string(model.ResetMonthly),
			InboundIDs:    []int64{1, 3},
			Note:          "备注里有,逗号",
		},
		{
			Username:      "bob",
			Enabled:       false,
			DataLimit:     0, // 不限
			ExpireAt:      nil,
			ResetStrategy: string(model.ResetNone),
			InboundIDs:    nil, // 全部入站
			Note:          `带"引号"的备注`,
		},
	}

	// 导出 -> 再作为导入的输入。
	var buf bytes.Buffer
	if err := writeUsersCSV(&buf, original); err != nil {
		t.Fatalf("writeUsersCSV: %v", err)
	}
	csvText := buf.String()

	// 表头应当是那七个中文列名。
	header := strings.Split(strings.TrimRight(strings.SplitN(csvText, "\n", 2)[0], "\r"), ",")
	if len(header) != len(userCSVHeader) {
		t.Fatalf("表头有 %d 列, want %d: %v", len(header), len(userCSVHeader), header)
	}
	for i, want := range userCSVHeader {
		if header[i] != want {
			t.Fatalf("第 %d 列表头 = %q, want %q", i, header[i], want)
		}
	}

	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Failed != 0 {
		t.Fatalf("导入失败 %d 行: %+v", rep.Failed, rep.Errors)
	}
	if rep.Added != 2 {
		t.Fatalf("新增 %d 个, want 2", rep.Added)
	}
	if rep.Updated != 0 {
		t.Fatalf("更新 %d 个, want 0", rep.Updated)
	}

	for _, want := range original {
		got, ok := f.get(want.Username)
		if !ok {
			t.Fatalf("%s 没有被写进假控制面", want.Username)
		}
		if got.DataLimit != want.DataLimit {
			t.Fatalf("%s 的上限 = %d, want %d", want.Username, got.DataLimit, want.DataLimit)
		}
		if got.Enabled != want.Enabled {
			t.Fatalf("%s 的可用性 = %v, want %v", want.Username, got.Enabled, want.Enabled)
		}
		if got.ResetStrategy != model.ResetStrategy(want.ResetStrategy) {
			t.Fatalf("%s 的重置策略 = %q, want %q", want.Username, got.ResetStrategy, want.ResetStrategy)
		}
		if got.Note != want.Note {
			t.Fatalf("%s 的备注 = %q, want %q", want.Username, got.Note, want.Note)
		}
		if len(got.InboundIDs) != len(want.InboundIDs) {
			t.Fatalf("%s 的入站 = %v, want %v", want.Username, got.InboundIDs, want.InboundIDs)
		}
		for i := range got.InboundIDs {
			if got.InboundIDs[i] != want.InboundIDs[i] {
				t.Fatalf("%s 的入站 = %v, want %v", want.Username, got.InboundIDs, want.InboundIDs)
			}
		}
		// 到期日按 YYYY-MM-DD 比较(库里/接口都截到秒)。
		gotDay := ""
		if got.ExpireAt != nil {
			gotDay = got.ExpireAt.Local().Format("2006-01-02")
		}
		wantDay := ""
		if want.ExpireAt != nil {
			wantDay = want.ExpireAt.Local().Format("2006-01-02")
		}
		if gotDay != wantDay {
			t.Fatalf("%s 的到期日 = %q, want %q", want.Username, gotDay, wantDay)
		}
	}
}

// TestCSVRoundTripPreservesText 断言备注里的逗号与引号经得起 CSV 编解码。
func TestCSVRoundTripPreservesText(t *testing.T) {
	notes := []string{
		"普通备注",
		"有,逗号",
		`有"双引号"`,
		"有\n换行",
		"",
	}
	for _, note := range notes {
		t.Run(note, func(t *testing.T) {
			users := []userRow{{Username: "u1", Enabled: true, ResetStrategy: "none", Note: note}}
			var buf bytes.Buffer
			if err := writeUsersCSV(&buf, users); err != nil {
				t.Fatalf("writeUsersCSV: %v", err)
			}
			f := newFakeUserAPI(t)
			c, err := newClient()
			if err != nil {
				t.Fatalf("newClient: %v", err)
			}
			rep, err := importUsersCSV(c, strings.NewReader(buf.String()))
			if err != nil {
				t.Fatalf("importUsersCSV: %v", err)
			}
			if rep.Failed != 0 {
				t.Fatalf("导入失败: %+v", rep.Errors)
			}
			got, ok := f.get("u1")
			if !ok {
				t.Fatal("u1 没写进去")
			}
			// 空备注在往返后仍是空(空单元格表示"没有备注")。
			want := note
			if got.Note != want {
				t.Fatalf("备注 = %q, want %q", got.Note, want)
			}
		})
	}
}

// TestUpsertUserReadsBeforeWrite 断言覆盖一个已有用户时是"先 GET 再 PUT",
// 而且 PUT 出去的体里保留了运行状态与凭证。
//
// 直接 PUT 一个从 CSV 新建的结构体会把 used_traffic 清零、把用户正在用的
// UUID/密码换掉 —— 那是很难被发现的破坏。
func TestUpsertUserReadsBeforeWrite(t *testing.T) {
	f := newFakeUserAPI(t)
	f.add(model.User{
		Username:            "alice",
		Key:                 "原来的key",
		SubToken:            "原来的token",
		Enabled:             true,
		DataLimit:           100,
		UsedTraffic:         555,
		LifetimeUsedTraffic: 999,
	})

	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	expire := time.Date(2027, 1, 1, 23, 59, 59, 0, time.Local)
	created, err := upsertUser(c, userRow{
		Username:      "alice",
		Enabled:       false,
		DataLimit:     2048,
		ExpireAt:      &expire,
		ResetStrategy: "weekly",
		InboundIDs:    []int64{2},
		Note:          "改过的备注",
	})
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	if created {
		t.Fatal("已存在的用户被判成了新建")
	}

	// 请求顺序必须是 GET 在前。
	methods := f.methodsFor("alice")
	if len(methods) < 2 || methods[0] != http.MethodGet || methods[1] != http.MethodPut {
		t.Fatalf("对 alice 的请求顺序 = %v, want [GET PUT]", methods)
	}

	// 定义字段按 CSV 改了。
	got, _ := f.get("alice")
	if got.DataLimit != 2048 {
		t.Fatalf("上限 = %d, want 2048", got.DataLimit)
	}
	if got.Enabled {
		t.Fatal("可用性没有被改成停用")
	}
	if got.ResetStrategy != "weekly" {
		t.Fatalf("重置策略 = %q, want weekly", got.ResetStrategy)
	}
	if got.Note != "改过的备注" {
		t.Fatalf("备注 = %q", got.Note)
	}
	if len(got.InboundIDs) != 1 || got.InboundIDs[0] != 2 {
		t.Fatalf("入站 = %v, want [2]", got.InboundIDs)
	}

	// 运行状态与凭证没被碰。
	put := f.recorded()[len(f.recorded())-1]
	if put.Method != http.MethodPut {
		t.Fatalf("最后一个请求是 %s, want PUT", put.Method)
	}
	var sent model.User
	if err := json.Unmarshal(put.Body, &sent); err != nil {
		t.Fatalf("解析 PUT 体: %v", err)
	}
	if sent.UsedTraffic != 555 {
		t.Fatalf("PUT 出去的已用流量 = %d, want 555(不该清零)", sent.UsedTraffic)
	}
	if sent.LifetimeUsedTraffic != 999 {
		t.Fatalf("PUT 出去的累计用量 = %d, want 999", sent.LifetimeUsedTraffic)
	}
	if sent.Key != "原来的key" {
		t.Fatalf("PUT 出去的 Key = %q, want 沿用原值", sent.Key)
	}
	if sent.SubToken != "原来的token" {
		t.Fatalf("PUT 出去的 SubToken = %q, want 沿用原值", sent.SubToken)
	}
}

// TestUpsertUserCreatesWhenMissing 断言 GET 回 404 时走 POST 新增。
func TestUpsertUserCreatesWhenMissing(t *testing.T) {
	f := newFakeUserAPI(t)
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	created, err := upsertUser(c, userRow{Username: "newbie", Enabled: true, DataLimit: 4096})
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	if !created {
		t.Fatal("不存在的用户没有被判成新建")
	}
	// 新增走的是 POST /api/users(路径上没有用户名),所以要按整条记录看,
	// 不能只按 methodsFor("newbie") 过滤。
	var methods []string
	for _, r := range f.recorded() {
		switch {
		case r.Method == http.MethodGet && r.Path == "/api/users/newbie":
			methods = append(methods, "GET /api/users/newbie")
		case r.Method == http.MethodPost && r.Path == "/api/users":
			methods = append(methods, "POST /api/users")
		}
	}
	want := []string{"GET /api/users/newbie", "POST /api/users"}
	if len(methods) != len(want) {
		t.Fatalf("请求顺序 = %v, want %v", methods, want)
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Fatalf("请求顺序 = %v, want %v", methods, want)
		}
	}
	got, ok := f.get("newbie")
	if !ok {
		t.Fatal("用户没被建出来")
	}
	if got.DataLimit != 4096 {
		t.Fatalf("上限 = %d, want 4096", got.DataLimit)
	}
}

// TestUpsertUserDoesNotSwallowOtherErrors 断言除了 404 之外的错误照常往上报,
// 不能悄悄改成新增 —— 控制面挂了的时候那会往库里灌重复用户。
func TestUpsertUserDoesNotSwallowOtherErrors(t *testing.T) {
	f := newFakeUserAPI(t)
	f.fail("alice", http.StatusInternalServerError)
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if _, err := upsertUser(c, userRow{Username: "alice", Enabled: true}); err == nil {
		t.Fatal("500 被吞掉了")
	}
	for _, m := range f.methodsFor("alice") {
		if m == http.MethodPost {
			t.Fatal("GET 回 500 时不该去 POST 新增")
		}
	}
}

// TestFetchAllUsersPagesThrough 断言导出会一直翻页到 total 为止。
//
// 只取一页会静默截断导出:用户拿到手的 CSV 少了人,而且看不出来。
func TestFetchAllUsersPagesThrough(t *testing.T) {
	f := newFakeUserAPI(t)
	const n = 2300 // 超过服务端每页上限 1000,必须翻三页
	for i := 0; i < n; i++ {
		f.add(model.User{Username: fmt.Sprintf("u-%04d", i), Enabled: true})
	}
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	users, err := fetchAllUsers(c)
	if err != nil {
		t.Fatalf("fetchAllUsers: %v", err)
	}
	if len(users) != n {
		t.Fatalf("取回 %d 个用户, want %d", len(users), n)
	}
	// 每页都用服务端上限 1000。
	queries := f.listQueries()
	if len(queries) < 3 {
		t.Fatalf("只发了 %d 次列表请求, want 至少 3 次: %v", len(queries), queries)
	}
	for _, q := range queries {
		if !strings.Contains(q, "limit=1000") {
			t.Fatalf("翻页请求没有用每页上限: %q", q)
		}
	}
	// 用户名不重复(翻页没错位)。
	seen := map[string]bool{}
	for _, u := range users {
		if seen[u.Username] {
			t.Fatalf("用户 %s 出现了两次:翻页错位", u.Username)
		}
		seen[u.Username] = true
	}
}

// TestCSVRowToUserEmptyCells 断言空单元格的含义:不限 / 不过期 / 全部 / 默认策略。
func TestCSVRowToUserEmptyCells(t *testing.T) {
	cols, err := userCSVColumns(userCSVHeader)
	if err != nil {
		t.Fatalf("userCSVColumns: %v", err)
	}
	// 只有用户名,其余全空。
	rec := []string{"alice", "", "", "", "", "", "", "", ""}
	u, err := csvRowToUser(cols, rec)
	if err != nil {
		t.Fatalf("csvRowToUser: %v", err)
	}
	if u.Username != "alice" {
		t.Fatalf("用户名 = %q", u.Username)
	}
	if u.DataLimit != 0 {
		t.Fatalf("空上限应当是 0(不限), got %d", u.DataLimit)
	}
	if u.ExpireAt != nil {
		t.Fatalf("空到期时间应当不过期, got %v", u.ExpireAt)
	}
	if len(u.InboundIDs) != 0 {
		t.Fatalf("空入站范围应当是全部, got %v", u.InboundIDs)
	}
	if u.ResetStrategy != string(model.ResetNone) {
		t.Fatalf("空重置策略应当是 none, got %q", u.ResetStrategy)
	}
	// 空单元格不动"可用性" —— 默认是启用。
	if !u.Enabled {
		t.Fatal("可用性留空时应当是启用")
	}

	// 显式写"不限/不过期/全部"和留空等价。
	rec = []string{"bob", "不限", "不过期", "启用", "none", "全部", "", ""}
	u, err = csvRowToUser(cols, rec)
	if err != nil {
		t.Fatalf("csvRowToUser: %v", err)
	}
	if u.DataLimit != 0 || u.ExpireAt != nil || len(u.InboundIDs) != 0 {
		t.Fatalf("「不限/不过期/全部」应当和留空等价, got limit=%d expire=%v inbounds=%v",
			u.DataLimit, u.ExpireAt, u.InboundIDs)
	}

	// 单元格两侧的空白会被有意去掉(导出->导入不等价于原样保留空白)。
	rec = []string{"  spaced  ", " 1GB ", " 2027-01-01 ", "启用", "none", "全部", "", "  备注  ", ""}
	u, err = csvRowToUser(cols, rec)
	if err != nil {
		t.Fatalf("csvRowToUser: %v", err)
	}
	if u.Username != "spaced" || u.DataLimit != 1<<30 || u.Note != "备注" {
		t.Fatalf("两侧空白没有被去掉: username=%q limit=%d note=%q", u.Username, u.DataLimit, u.Note)
	}

	// 认不出的列名被忽略,缺用户名才报错。
	legacy, err := userCSVColumns([]string{"用户名", "以后加的列"})
	if err != nil {
		t.Fatalf("多余列不该报错: %v", err)
	}
	if _, ok := legacy["以后加的列"]; !ok {
		t.Fatal("多余列没有被记进列映射(虽然会被忽略,但下标要认得)")
	}
}

// TestImportBadRowsColumnMismatch 断言列数与表头不一致时该行报错、带行号,
// 但其余行照常导入。
func TestImportBadRowsColumnMismatch(t *testing.T) {
	f := newFakeUserAPI(t)
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	// 表头 8 列;第 3 行(数据第 2 行)只给了 4 列。
	csvText := strings.Join([]string{
		strings.Join(userCSVImportHeader, ","),
		"alice,1GB,2027-01-01,启用,none,全部,,备注一",
		"bob,2GB,2027-02-01,启用",
		"carol,3GB,2027-03-01,停用,none,全部,,备注三",
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Added != 2 {
		t.Fatalf("新增 %d 个, want 2(坏行不挡其余行)", rep.Added)
	}
	if rep.Failed != 1 {
		t.Fatalf("失败 %d 行, want 1", rep.Failed)
	}
	if len(rep.Errors) != 1 {
		t.Fatalf("错误条数 = %d, want 1: %+v", len(rep.Errors), rep.Errors)
	}
	e := rep.Errors[0]
	if e.Line != 3 {
		t.Fatalf("错误行号 = %d, want 3(表头是第 1 行)", e.Line)
	}
	wantMsg := "列数与表头不一致(表头 8 列,这行 4 列)"
	if !strings.Contains(e.Error, "列数与表头不一致") ||
		!strings.Contains(e.Error, "表头 8 列") || !strings.Contains(e.Error, "这行 4 列") {
		t.Fatalf("错误信息 = %q, want 包含 %q", e.Error, wantMsg)
	}
	// 其余两行确实写进去了。
	for _, name := range []string{"alice", "carol"} {
		if _, ok := f.get(name); !ok {
			t.Fatalf("%s 没有被导入", name)
		}
	}
	if _, ok := f.get("bob"); ok {
		t.Fatal("坏行 bob 不该被写进去")
	}
}

// TestImportBadRowsUnknownEnabled 断言「可用性」拼错时该行失败、报错提到可用性,
// 其余行照常 —— 拼错的词不能悄悄当成启用。
func TestImportBadRowsUnknownEnabled(t *testing.T) {
	f := newFakeUserAPI(t)
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	csvText := strings.Join([]string{
		strings.Join(userCSVImportHeader, ","),
		"alice,1GB,2027-01-01,启用,none,全部,,备注一",
		"bob,2GB,2027-02-01,啟用,none,全部,,备注二", // 繁体"啟用"认不出
		"carol,3GB,2027-03-01,停用,none,全部,,备注三",
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Added != 2 || rep.Failed != 1 {
		t.Fatalf("新增 %d / 失败 %d, want 2 / 1", rep.Added, rep.Failed)
	}
	e := rep.Errors[0]
	if e.Line != 3 {
		t.Fatalf("错误行号 = %d, want 3", e.Line)
	}
	if !strings.Contains(e.Error, "可用性") {
		t.Fatalf("错误信息没提到可用性: %q", e.Error)
	}
	if _, ok := f.get("bob"); ok {
		t.Fatal("拼错的可用性行不该被写进去(默认启用是危险的)")
	}
}

// TestImportMissingUsernameColumn 断言表头缺「用户名」时整份文件报错、一行不写。
func TestImportMissingUsernameColumn(t *testing.T) {
	f := newFakeUserAPI(t)
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	csvText := "流量上限,到期时间,可用性\n1GB,2027-01-01,启用\n2GB,2027-02-01,停用\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err == nil {
		t.Fatalf("表头缺用户名竟然没报错: %+v", rep)
	}
	if !strings.Contains(err.Error(), "用户名") {
		t.Fatalf("错误信息 = %q, want 提到用户名", err.Error())
	}
	// 一行都不许写。
	if len(f.recorded()) != 0 {
		t.Fatalf("表头错时仍发出了 %d 个请求: %v", len(f.recorded()), f.recorded())
	}
	if rep.Added != 0 || rep.Failed != 0 {
		t.Fatalf("表头错不该有逐行结果: %+v", rep)
	}
}

// TestImportStopsAfterThreeBadRows 断言连续三行读不出来就停下,不再继续。
//
// encoding/csv 遇到裸引号会报错并返回已解析的部分字段;下一行正常时还能恢复,
// 所以必须写连续三行都带裸引号才触发"连续多行读不出来"。
func TestImportStopsAfterThreeBadRows(t *testing.T) {
	f := newFakeUserAPI(t)
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	csvText := strings.Join([]string{
		strings.Join(userCSVImportHeader, ","),
		`alice,1GB,2027-01-01,启用,none,全部,,"坏"引号`,
		`bob,2GB,2027-02-01,停用,none,全部,,"坏"引号`,
		`carol,3GB,2027-03-01,启用,none,全部,,"坏"引号`,
		"dave,4GB,2027-04-01,启用,none,全部,,正常行不该被处理",
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Added != 0 {
		t.Fatalf("连续坏行后不该导入任何用户, got Added=%d", rep.Added)
	}
	// 第 2/3/4 行三行都读不出(表头算第 1 行);报错行号取最后一次读到的行。
	if rep.Failed != 3 {
		t.Fatalf("失败 %d 行, want 3(三行坏行各记一条)", rep.Failed)
	}
	if len(rep.Errors) != 3 {
		t.Fatalf("错误条数 = %d, want 3: %+v", len(rep.Errors), rep.Errors)
	}
	last := rep.Errors[len(rep.Errors)-1]
	if !strings.Contains(last.Error, "连续多行读不出来") {
		t.Fatalf("最后一条错误 = %q, want 含「连续多行读不出来」", last.Error)
	}
	if last.Line != 4 {
		t.Fatalf("停下来的行号 = %d, want 4(第 4 行触发>=3 次)", last.Line)
	}
	// 第四行之后的内容没有被处理。
	if _, ok := f.get("dave"); ok {
		t.Fatal("连续坏行之后不该继续导入")
	}
}

// TestImportCSVColumnCountUsesCSVRecords 断言行号按 CSV 记录序号走:
// 表头是第 1 行,数据行从第 2 行起;多值单元格(引号括起来的逗号)不算多列。
func TestImportCSVColumnCountUsesCSVRecords(t *testing.T) {
	f := newFakeUserAPI(t)
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	// 注意第二行备注用引号括起了逗号 —— CSV 眼里它仍是 7 个字段。
	csvText := strings.Join([]string{
		strings.Join(userCSVImportHeader, ","),
		`alice,1GB,2027-01-01,启用,none,全部,,"备注,有逗号"`,
		"bob,2GB",
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Added != 1 {
		t.Fatalf("新增 %d, want 1", rep.Added)
	}
	if rep.Failed != 1 || len(rep.Errors) != 1 {
		t.Fatalf("失败 %d / 错误 %d, want 1 / 1", rep.Failed, len(rep.Errors))
	}
	if rep.Errors[0].Line != 3 {
		t.Fatalf("坏行行号 = %d, want 3", rep.Errors[0].Line)
	}
	// 引号括起的逗号没有撑破列数。
	got, ok := f.get("alice")
	if !ok {
		t.Fatal("alice 没导入")
	}
	if got.Note != "备注,有逗号" {
		t.Fatalf("备注 = %q, want 备注,有逗号(引号内的逗号要保留)", got.Note)
	}
}

// TestCSVExportWritesPlanName 断言「套餐」列写出的是套餐名而不是 id。
func TestCSVExportWritesPlanName(t *testing.T) {
	id := int64(7)
	users := []userRow{{
		Username: "alice", Enabled: true, ResetStrategy: "none",
		PlanID: &id, PlanName: "全球",
	}}
	var buf bytes.Buffer
	if err := writeUsersCSV(&buf, users); err != nil {
		t.Fatalf("writeUsersCSV: %v", err)
	}
	if !strings.Contains(buf.String(), "alice,不限,不过期,启用,none,全部,全球,") {
		t.Fatalf("导出的套餐列不是套餐名:\n%s", buf.String())
	}
	// 没挂套餐的用户写空单元格,导入时表示不改。
	noPlan := []userRow{{Username: "bob", Enabled: true, ResetStrategy: "none"}}
	buf.Reset()
	if err := writeUsersCSV(&buf, noPlan); err != nil {
		t.Fatalf("writeUsersCSV: %v", err)
	}
	if !strings.Contains(buf.String(), "bob,不限,不过期,启用,none,全部,,") {
		t.Fatalf("没挂套餐应写空单元格:\n%s", buf.String())
	}
}

// TestCSVImportPlanColumn 断言「套餐」列的导入语义:
// 写名字 → 按名字解析成 id;空 → 保持原套餐;认不出的名字 → 该行失败。
func TestCSVImportPlanColumn(t *testing.T) {
	f := newFakeUserAPI(t)
	global := int64(7)
	basic := int64(8)
	f.addPlan(model.Plan{ID: global, Name: "全球"})
	f.addPlan(model.Plan{ID: basic, Name: "基础"})
	// alice 已经挂在「全球」上。
	f.add(model.User{Username: "alice", Enabled: true, PlanID: &global})

	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	csvText := strings.Join([]string{
		strings.Join(userCSVImportHeader, ","),
		"alice,不限,不过期,启用,none,全部,基础,", // 改挂「基础」
		"bob,不限,不过期,启用,none,全部,全球,",   // 新建并挂「全球」
		"carol,不限,不过期,启用,none,全部,,",   // 新建,不挂套餐
		"dave,不限,不过期,启用,none,全部,不存在,", // 名字认不出 → 失败
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Added != 2 || rep.Updated != 1 || rep.Failed != 1 {
		t.Fatalf("导入结果 = %+v, want 2 新增 / 1 更新 / 1 失败", rep)
	}
	if rep.Errors[0].Line != 5 || !strings.Contains(rep.Errors[0].Error, "套餐") {
		t.Fatalf("失败行信息 = %+v", rep.Errors[0])
	}
	if _, ok := f.get("dave"); ok {
		t.Fatal("套餐名认不出的行不该被写进去")
	}

	if got, _ := f.get("alice"); got.PlanID == nil || *got.PlanID != basic {
		t.Fatalf("alice 没改挂到「基础」: %+v", got.PlanID)
	}
	if got, _ := f.get("bob"); got.PlanID == nil || *got.PlanID != global {
		t.Fatalf("bob 没挂到「全球」: %+v", got.PlanID)
	}
	if got, _ := f.get("carol"); got.PlanID != nil {
		t.Fatalf("carol 的空套餐单元格应表示不挂: %+v", got.PlanID)
	}

	// 空「套餐」单元格覆盖已有用户时保持原套餐不动。
	keepText := strings.Join([]string{
		strings.Join(userCSVImportHeader, ","),
		"alice,2GB,不过期,启用,none,全部,,备注",
	}, "\n") + "\n"
	rep, err = importUsersCSV(c, strings.NewReader(keepText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Updated != 1 || rep.Failed != 0 {
		t.Fatalf("覆盖结果 = %+v", rep)
	}
	got, _ := f.get("alice")
	if got.PlanID == nil || *got.PlanID != basic {
		t.Fatalf("空套餐单元格覆盖时把套餐改掉了: %+v", got.PlanID)
	}
	if got.DataLimit != 2<<30 {
		t.Fatalf("其余字段没被覆盖: limit=%d", got.DataLimit)
	}
}

// TestCSVImportWithoutPlanColumnKeepsPlan 断言旧格式(没有「套餐」列)的文件
// 仍然能导入,而且不会碰用户的套餐归属。
func TestCSVImportWithoutPlanColumnKeepsPlan(t *testing.T) {
	f := newFakeUserAPI(t)
	global := int64(7)
	f.addPlan(model.Plan{ID: global, Name: "全球"})
	f.add(model.User{Username: "alice", Enabled: true, PlanID: &global})

	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	csvText := strings.Join([]string{
		"用户名,流量上限,到期时间,可用性,重置策略,入站范围,备注",
		"alice,1GB,不过期,启用,none,全部,备注",
	}, "\n") + "\n"
	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Updated != 1 || rep.Failed != 0 {
		t.Fatalf("旧格式导入结果 = %+v", rep)
	}
	got, _ := f.get("alice")
	if got.PlanID == nil || *got.PlanID != global {
		t.Fatalf("旧格式导入碰了套餐归属: %+v", got.PlanID)
	}
}

// TestCSVExportLastActiveColumn 断言「最后在线（UTC+8）」列:固定按 UTC+8 写、不看操作机
// 时区;从没连过的人(没有 presence 或 last_active_at 为空)留空。
func TestCSVExportLastActiveColumn(t *testing.T) {
	seen := time.Date(2026, 10, 17, 13, 14, 40, 0, time.UTC)
	users := []userRow{
		{Username: "alice", Enabled: true, ResetStrategy: string(model.ResetNone), Presence: &userPresence{LastActiveAt: &seen}},
		{Username: "hana", Enabled: true, ResetStrategy: string(model.ResetNone), Presence: &userPresence{}},
		{Username: "old", Enabled: true, ResetStrategy: string(model.ResetNone)}, // 老控制面不给 presence
	}

	var buf bytes.Buffer
	if err := writeUsersCSV(&buf, users); err != nil {
		t.Fatalf("writeUsersCSV: %v", err)
	}
	recs, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("读回 CSV: %v", err)
	}
	if n := len(recs[0]); n != len(userCSVHeader) {
		t.Fatalf("表头 %d 列, want %d: %v", n, len(userCSVHeader), recs[0])
	}
	// 最后一列的表头和常量是同一句话,常量本身也要是这句(界面和导出里都按它写)。
	if got := recs[0][len(recs[0])-1]; got != csvColumnLastActive {
		t.Fatalf("最后一列 = %q, want %q", got, csvColumnLastActive)
	}
	if csvColumnLastActive != "最后在线（UTC+8）" {
		t.Fatalf("csvColumnLastActive = %q, want 最后在线（UTC+8）", csvColumnLastActive)
	}
	// 13:14:40 UTC = 21:14:40 UTC+8,与浏览器/操作机时区无关。
	for _, rec := range recs[1:] {
		want := map[string]string{"alice": "2026-10-17 21:14:40", "hana": "", "old": ""}[rec[0]]
		if got := rec[len(rec)-1]; got != want {
			t.Fatalf("%s 的最后在线 = %q, want %q", rec[0], got, want)
		}
	}
}

// TestCSVImportOldEightColumnFile 断言老版本导出的文件(8 列,没有
// 「最后在线（UTC+8）」)照常能导:行宽按文件自己的表头算,不拿 9 列的导出表头去比。
func TestCSVImportOldEightColumnFile(t *testing.T) {
	f := newFakeUserAPI(t)
	global := int64(7)
	f.addPlan(model.Plan{ID: global, Name: "全球"})
	f.add(model.User{Username: "alice", Enabled: true, PlanID: &global})

	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	// 老版本的导出表头:用户名、流量上限、到期时间、可用性、重置策略、入站范围、套餐、备注。
	csvText := strings.Join([]string{
		"用户名,流量上限,到期时间,可用性,重置策略,入站范围,套餐,备注",
		"alice,2GB,不过期,启用,none,全部,,备注",
		"bob,1GB,2027-01-01,停用,none,全部,全球,备注二",
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Added != 1 || rep.Updated != 1 || rep.Failed != 0 {
		t.Fatalf("导入结果 = %+v, want 1 新增 / 1 更新 / 0 失败", rep)
	}
	got, ok := f.get("bob")
	if !ok {
		t.Fatal("bob 没导入")
	}
	if got.Enabled || got.PlanID == nil || *got.PlanID != global {
		t.Fatalf("bob = %+v, want 停用并挂「全球」", got)
	}
	if got, _ := f.get("alice"); got.DataLimit != 2<<30 || got.PlanID == nil || *got.PlanID != global {
		t.Fatalf("alice = %+v, want 2GB 且套餐不变", got)
	}
}

// TestCSVImportNineColumnFileIgnoresLastActive 断言带「最后在线（UTC+8）」列的
// 9 列文件也能导:这一列只导出、不导入,写什么都不认。
func TestCSVImportNineColumnFileIgnoresLastActive(t *testing.T) {
	f := newFakeUserAPI(t)
	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if len(userCSVHeader) != 9 || userCSVHeader[len(userCSVHeader)-1] != csvColumnLastActive {
		t.Fatalf("导出表头 = %v, want 9 列且最后一列是 %q", userCSVHeader, csvColumnLastActive)
	}
	csvText := strings.Join([]string{
		strings.Join(userCSVHeader, ","),
		"alice,1GB,2027-01-01,启用,none,全部,,备注一,2026-10-17 21:14:40",
		"bob,2GB,2027-02-01,停用,none,全部,,备注二,",
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Added != 2 || rep.Failed != 0 {
		t.Fatalf("导入结果 = %+v, want 2 新增 0 失败", rep)
	}
	got, ok := f.get("alice")
	if !ok {
		t.Fatal("alice 没导入")
	}
	if got.DataLimit != 1<<30 || got.Note != "备注一" {
		t.Fatalf("alice = %+v, want 1GB 且备注为「备注一」", got)
	}
	if b, _ := f.get("bob"); b.Enabled {
		t.Fatalf("bob 应当是停用: %+v", b)
	}
}
