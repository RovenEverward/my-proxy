package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// listUsers 调一次 GET /api/users,返回状态码与解出来的 userPage。
//
// 一律走真实 HTTP(见本包测试文件开头的取舍):查询参数、状态码、响应形状
// 三件事只有整条链路跑一遍才看得见。
func listUsers(t *testing.T, e *env, query string) (int, userPage) {
	t.Helper()
	path := "/api/users"
	if query != "" {
		path += "?" + query
	}
	code, data := e.api("GET", path, nil)
	if code != http.StatusOK {
		return code, userPage{}
	}
	return code, decodeJSON[userPage](t, data)
}

// seedUsersForList 造 5 个用户,覆盖筛选会用到的几种情况:
//
//	alpha  可用,限定入站 [inboundID] 之外的那个(用不了该入站)
//	Beta   可用,限定入站 [inboundID](用得了)
//	gamma  停用
//	delta  用满上限 -> limited
//	epsilon 已过期 -> expired
//
// 返回建出来的入站 id,供 inbound_id 筛选用。
func seedUsersForList(t *testing.T, e *env) int64 {
	t.Helper()
	node := e.createNode("node-1", "10.0.0.1")
	in := e.createInbound(node.ID, "vless-ws", 443)

	for _, name := range []string{"alpha", "Beta", "gamma", "delta", "epsilon"} {
		e.createUser(name)
	}

	// 用满了上限 -> limited。
	e.updateUser("delta", func(u *model.User) {
		u.DataLimit = 1024
		u.UsedTraffic = 1024
	})
	// 已经过期 -> expired。
	e.updateUser("epsilon", func(u *model.User) {
		past := time.Now().Add(-24 * time.Hour)
		u.ExpireAt = &past
	})
	// 停用。
	e.updateUser("gamma", func(u *model.User) {
		u.Enabled = false
	})

	// 入站限定:只有 Beta 能用这个入站;alpha 限定在别的入站上。
	other := e.createInbound(node.ID, "vless-ws-2", 8443)
	e.updateUser("Beta", func(u *model.User) {
		u.InboundIDs = []int64{in.ID}
	})
	e.updateUser("alpha", func(u *model.User) {
		u.InboundIDs = []int64{other.ID}
	})
	return in.ID
}

// TestListUsersDefaults 断言默认返回的形状:一页 50 个、offset 0、total 是全部。
func TestListUsersDefaults(t *testing.T) {
	e := newEnv(t)
	seedUsersForList(t, e)

	code, page := listUsers(t, e, "")
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", code)
	}
	if page.Limit != defaultUserPageSize {
		t.Fatalf("默认 limit = %d, want %d", page.Limit, defaultUserPageSize)
	}
	if page.Offset != 0 {
		t.Fatalf("默认 offset = %d, want 0", page.Offset)
	}
	if page.Total != 5 {
		t.Fatalf("total = %d, want 5", page.Total)
	}
	if len(page.Users) != 5 {
		t.Fatalf("users 有 %d 个, want 5", len(page.Users))
	}
	// 顺序按 id 升序,否则翻页会漏人。
	for i := 1; i < len(page.Users); i++ {
		if page.Users[i-1].ID >= page.Users[i].ID {
			t.Fatalf("users 没有按 id 升序: %d 之后是 %d", page.Users[i-1].ID, page.Users[i].ID)
		}
	}
	// 状态是算出来的,不是存下来的。
	byName := make(map[string]model.Status, len(page.Users))
	for _, u := range page.Users {
		byName[u.Username] = u.Status
	}
	want := map[string]model.Status{
		"alpha":   model.StatusActive,
		"Beta":    model.StatusActive,
		"gamma":   model.StatusDisabled,
		"delta":   model.StatusLimited,
		"epsilon": model.StatusExpired,
	}
	for name, status := range want {
		if byName[name] != status {
			t.Fatalf("用户 %s 的 status = %q, want %q", name, byName[name], status)
		}
	}
}

// TestListUsersEmptyIsArray 断言一个用户都没有时 users 是 []，不是 null。
//
// null 会让 `jq '.users[]'` 之类直接报错;JSON 里"没有"和"没有这个字段"必须分清。
func TestListUsersEmptyIsArray(t *testing.T) {
	e := newEnv(t)

	code, data := e.api("GET", "/api/users", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", code)
	}
	// 先看原始 JSON:解成 userPage 之后 null 与 [] 都是 nil 切片,分不出来。
	if got := rawField(t, data, "users"); got != "[]" {
		t.Fatalf("空库时 users 的原始 JSON 是 %s, want []", got)
	}
	page := decodeJSON[userPage](t, data)
	if page.Users == nil {
		t.Fatal("users 是 nil")
	}
	if page.Total != 0 {
		t.Fatalf("total = %d, want 0", page.Total)
	}
}

// rawField 从一段 JSON 对象里抠出某个字段的原始文本,用来分辨 null 与 []。
func rawField(t *testing.T, data []byte, field string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("解析响应: %v(%s)", err, data)
	}
	raw, ok := m[field]
	if !ok {
		t.Fatalf("响应里没有 %q 字段: %s", field, data)
	}
	return string(bytes.TrimSpace(raw))
}

// TestListUsersInvalidPageParams 断言非法分页参数回 400 且不落到默认值上。
//
// 静默把 limit=0 当默认 50 会让调用方以为自己拿到了"全部";宁可报错。
func TestListUsersInvalidPageParams(t *testing.T) {
	e := newEnv(t)
	e.createUser("alpha")

	cases := []struct {
		name  string
		query string
	}{
		{"limit 为 0", "limit=0"},
		{"limit 超过上限", "limit=1001"},
		{"limit 不是数字", "limit=abc"},
		{"limit 是小数", "limit=1.5"},
		{"offset 为负", "offset=-1"},
		{"offset 不是数字", "offset=x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, data := e.api("GET", "/api/users?"+tc.query, nil)
			if code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, want 400(响应:%s)", code, data)
			}
		})
	}
}

// TestListUsersBoundaryPageParams 断言边界值是允许的:limit=1 与 limit=1000 都行。
func TestListUsersBoundaryPageParams(t *testing.T) {
	e := newEnv(t)
	e.createUser("alpha")

	for _, q := range []string{"limit=1", "limit=1000", "offset=0"} {
		code, page := listUsers(t, e, q)
		if code != http.StatusOK {
			t.Fatalf("%s 的状态码 = %d, want 200", q, code)
		}
		if q == "limit=1" && page.Limit != 1 {
			t.Fatalf("limit=1 时回显的 limit = %d", page.Limit)
		}
		if q == "limit=1000" && page.Limit != 1000 {
			t.Fatalf("limit=1000 时回显的 limit = %d", page.Limit)
		}
	}
}

// TestListUsersInvalidStatus 断言未知状态回 400,且错误信息里列了可选值。
//
// 只回一句"参数不对"会让人去翻代码;把可选值列出来才是能用的报错。
func TestListUsersInvalidStatus(t *testing.T) {
	e := newEnv(t)
	e.createUser("alpha")

	for _, status := range []string{"nope", "Active", "ENABLED"} {
		code, data := e.api("GET", "/api/users?status="+status, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%s 的状态码 = %d, want 400(%s)", status, code, data)
		}
		msg := string(data)
		if !strings.Contains(msg, "active") {
			t.Fatalf("status=%s 的错误信息没有列出可选值: %s", status, msg)
		}
		// 四个取值都该出现。
		for _, want := range []string{"active", "expired", "limited", "disabled"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("status=%s 的错误信息里没有 %q: %s", status, want, msg)
			}
		}
	}
}

// TestListUsersStatusFilter 断言 status 真的筛,而且 total 跟着变。
func TestListUsersStatusFilter(t *testing.T) {
	e := newEnv(t)
	seedUsersForList(t, e)

	cases := []struct {
		status model.Status
		want   []string
	}{
		{model.StatusActive, []string{"alpha", "Beta"}},
		{model.StatusLimited, []string{"delta"}},
		{model.StatusExpired, []string{"epsilon"}},
		{model.StatusDisabled, []string{"gamma"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			code, page := listUsers(t, e, "status="+string(tc.status))
			if code != http.StatusOK {
				t.Fatalf("状态码 = %d, want 200", code)
			}
			got := make([]string, 0, len(page.Users))
			for _, u := range page.Users {
				got = append(got, u.Username)
				if u.Status != tc.status {
					t.Fatalf("筛出来的用户 %s 状态是 %q", u.Username, u.Status)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("status=%s 得到 %v, want %v", tc.status, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("status=%s 得到 %v, want %v", tc.status, got, tc.want)
				}
			}
			if page.Total != len(tc.want) {
				t.Fatalf("status=%s 的 total = %d, want %d(筛选后的总数)",
					tc.status, page.Total, len(tc.want))
			}
		})
	}
}

// TestListUsersSearchFilter 断言 search 走服务端筛选、不分大小写。
func TestListUsersSearchFilter(t *testing.T) {
	e := newEnv(t)
	seedUsersForList(t, e)

	// 小写 al 能同时匹配 alpha(Beta 里没有 al)。
	_, page := listUsers(t, e, "search="+url.QueryEscape("al"))
	if page.Total != 1 || len(page.Users) != 1 || page.Users[0].Username != "alpha" {
		t.Fatalf("search=al 得到 total=%d users=%v, want 只有 alpha", page.Total, page.Users)
	}

	// 大写同样能匹配到小写名字。
	_, page = listUsers(t, e, "search="+url.QueryEscape("EP"))
	if page.Total != 1 || page.Users[0].Username != "epsilon" {
		t.Fatalf("search=EP 得到 total=%d, want 只有 epsilon", page.Total)
	}

	// 匹配不到时 users 是 [] 而不是 null,total 为 0。
	code, data := e.api("GET", "/api/users?search="+url.QueryEscape("nobody"), nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", code)
	}
	if got := rawField(t, data, "users"); got != "[]" {
		t.Fatalf("筛不到时 users 的原始 JSON 是 %s, want []", got)
	}
	if page := decodeJSON[userPage](t, data); page.Total != 0 {
		t.Fatalf("筛不到时 total = %d, want 0", page.Total)
	}
}

// TestListUsersInboundFilter 断言 inbound_id 筛选,并且算上"不限入站"的用户。
//
// 一个刚建出来没限定入站的用户必须出现在任意入站的筛选结果里 —— 它本来就能用
// 所有入站。
func TestListUsersInboundFilter(t *testing.T) {
	e := newEnv(t)
	inboundID := seedUsersForList(t, e)

	_, page := listUsers(t, e, "inbound_id="+strconv.FormatInt(inboundID, 10))
	got := make(map[string]bool, len(page.Users))
	for _, u := range page.Users {
		got[u.Username] = true
	}
	// Beta 显式限定了这个入站;alpha 限定在别的入站上,不该出现;
	// 其余没限定入站的(含停用/过期/超限的)都该出现。
	if !got["Beta"] {
		t.Fatal("显式限定了该入站的 Beta 没出现")
	}
	if got["alpha"] {
		t.Fatal("限定在别的入站上的 alpha 不该出现")
	}
	for _, name := range []string{"gamma", "delta", "epsilon"} {
		if !got[name] {
			t.Fatalf("不限入站的 %s 没出现", name)
		}
	}
	if page.Total != 4 {
		t.Fatalf("total = %d, want 4(Beta 加三个不限入站的)", page.Total)
	}

	// 不存在的入站:只有不限入站的用户能用。
	_, page = listUsers(t, e, "inbound_id=9999")
	if page.Total != 3 {
		t.Fatalf("inbound_id=9999 时 total = %d, want 3(只有不限入站的)", page.Total)
	}
	for _, u := range page.Users {
		if len(u.InboundIDs) != 0 {
			t.Fatalf("入站 9999 不该匹配到限定了入站的 %s(%v)", u.Username, u.InboundIDs)
		}
	}
}

// TestListUsersOffsetBeyondTotal 断言 offset 越界时 users 是 []、total 照旧。
//
// CLI 靠"本页 0 条 + total 有值"告诉用户翻过头了;这里若回 null 或报错,
// 那句话就说不出来。
func TestListUsersOffsetBeyondTotal(t *testing.T) {
	e := newEnv(t)
	seedUsersForList(t, e)

	code, data := e.api("GET", "/api/users?offset=100&limit=10", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", code)
	}
	if got := rawField(t, data, "users"); got != "[]" {
		t.Fatalf("越界时 users 的原始 JSON 是 %s, want []", got)
	}
	page := decodeJSON[userPage](t, data)
	if page.Total != 5 {
		t.Fatalf("越界时 total = %d, want 5", page.Total)
	}
	if page.Offset != 100 {
		t.Fatalf("越界时回显的 offset = %d, want 100", page.Offset)
	}
}

// TestListUsersPaginationTotals 断言分页时 total 一直是筛选后的总数,不是本页条数。
func TestListUsersPaginationTotals(t *testing.T) {
	e := newEnv(t)
	seedUsersForList(t, e)

	code, first := listUsers(t, e, "limit=2")
	if code != http.StatusOK {
		t.Fatalf("第一页状态码 = %d, want 200", code)
	}
	if first.Total != 5 {
		t.Fatalf("limit=2 时 total = %d, want 5", first.Total)
	}
	if len(first.Users) != 2 {
		t.Fatalf("第一页 %d 条, want 2", len(first.Users))
	}
	if first.Offset != 0 {
		t.Fatalf("第一页 offset = %d, want 0", first.Offset)
	}

	code, second := listUsers(t, e, "limit=2&offset=2")
	if code != http.StatusOK {
		t.Fatalf("第二页状态码 = %d, want 200", code)
	}
	if second.Total != 5 {
		t.Fatalf("第二页 total = %d, want 5(仍是筛选后的总数)", second.Total)
	}
	if len(second.Users) != 2 {
		t.Fatalf("第二页 %d 条, want 2", len(second.Users))
	}
	if second.Users[0].ID == first.Users[0].ID {
		t.Fatal("第二页的第一条与第一页的第一条相同:offset 没有生效")
	}
	if second.Users[0].ID <= first.Users[len(first.Users)-1].ID {
		t.Fatal("第二页的 id 没有接在第一页之后:顺序不是按 id 升序")
	}

	_, last := listUsers(t, e, "limit=2&offset=4")
	if len(last.Users) != 1 {
		t.Fatalf("最后一页 %d 条, want 1", len(last.Users))
	}
	if last.Total != 5 {
		t.Fatalf("最后一页 total = %d, want 5", last.Total)
	}
}

// TestStatusChoicesListsEveryStatus 断言提示文字来自 model 那一份取值列表。
//
// 新加状态时只改 model 就该处处生效;这条测试把"提示"与"取值列表"绑在一起。
func TestStatusChoicesListsEveryStatus(t *testing.T) {
	got := statusChoices()
	for _, s := range model.UserStatuses {
		if !strings.Contains(got, string(s)) {
			t.Fatalf("statusChoices() = %q, 少了 %q", got, s)
		}
	}
	if !strings.Contains(got, "active") {
		t.Fatalf("statusChoices() = %q, 没有 active", got)
	}
}
