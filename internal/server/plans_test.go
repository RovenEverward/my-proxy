package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// ---- 便捷函数,都走真实 HTTP ----

// plansOf 取全部套餐。
func plansOf(t *testing.T, e *env) []model.Plan {
	t.Helper()
	code, data := e.api("GET", "/api/plans", nil)
	if code != http.StatusOK {
		t.Fatalf("列套餐: got %d %s", code, data)
	}
	return decodeJSON[[]model.Plan](t, data)
}

// defaultPlanOf 找出系统自带的默认套餐。
func defaultPlanOf(t *testing.T, e *env) model.Plan {
	t.Helper()
	for _, p := range plansOf(t, e) {
		if p.IsDefault {
			return p
		}
	}
	t.Fatal("没有默认套餐")
	return model.Plan{}
}

// planByID 取一个套餐;id 不存在时失败。
func planByID(t *testing.T, e *env, id int64) model.Plan {
	t.Helper()
	code, data := e.api("GET", fmt.Sprintf("/api/plans/%d", id), nil)
	if code != http.StatusOK {
		t.Fatalf("读套餐 %d: got %d %s", id, code, data)
	}
	return decodeJSON[model.Plan](t, data)
}

// createPlan 建一个套餐并返回它。
func (e *env) createPlan(p model.Plan) model.Plan {
	e.t.Helper()
	code, data := e.api("POST", "/api/plans", p)
	if code != http.StatusCreated {
		e.t.Fatalf("建套餐: got %d %s", code, data)
	}
	return decodeJSON[model.Plan](e.t, data)
}

// userOf 读一个用户。
func userOf(t *testing.T, e *env, name string) userView {
	t.Helper()
	code, data := e.api("GET", "/api/users/"+name, nil)
	if code != http.StatusOK {
		t.Fatalf("读用户 %s: got %d %s", name, code, data)
	}
	return decodeJSON[userView](t, data)
}

// putUserRaw 把用户当前的 JSON 读回来,按 mutate 改掉顶层字段后原样 PUT。
//
// 用原始 JSON 而不是 userView 结构体,才能测出「没带 plan_id」和「带了 null」
// 的区别:结构体里一个 *int64 字段这两种写法都会被编成同一个东西。
func putUserRaw(t *testing.T, e *env, name string, mutate func(map[string]any)) int {
	t.Helper()
	code, data := e.api("GET", "/api/users/"+name, nil)
	if code != http.StatusOK {
		t.Fatalf("读用户 %s: got %d %s", name, code, data)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("解析用户 JSON: %v", err)
	}
	mutate(m)
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("序列化用户: %v", err)
	}
	code, _ = e.requestRaw("PUT", "/api/users/"+name, body)
	return code
}

func TestPlanCRUD(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("la-1", "la.example.com")
	in := e.createInbound(n.ID, "ws", 8443)

	// 默认套餐由迁移建出来:all_nodes、is_default。
	def := defaultPlanOf(t, e)
	if !def.AllNodes || def.Name == "" {
		t.Fatalf("默认套餐 = %+v", def)
	}

	p := e.createPlan(model.Plan{Name: "基础", Note: "只给一个节点", NodeIDs: []int64{n.ID}, InboundIDs: []int64{in.ID}})
	if p.ID == 0 || p.IsDefault {
		t.Fatalf("新建套餐 = %+v", p)
	}
	if len(p.NodeIDs) != 1 || p.NodeIDs[0] != n.ID || len(p.InboundIDs) != 1 || p.InboundIDs[0] != in.ID {
		t.Fatalf("关联没存下: %+v", p)
	}

	// 改名与备注。
	code, data := e.api("PUT", fmt.Sprintf("/api/plans/%d", p.ID), model.Plan{Name: "基础Pro", Note: "改过", NodeIDs: []int64{n.ID}})
	if code != http.StatusOK {
		t.Fatalf("改套餐: %d %s", code, data)
	}
	got := planByID(t, e, p.ID)
	if got.Name != "基础Pro" || got.Note != "改过" {
		t.Fatalf("改名没生效: %+v", got)
	}

	// 列表里能看见。
	var seen bool
	for _, x := range plansOf(t, e) {
		if x.ID == p.ID {
			seen = true
		}
	}
	if !seen {
		t.Fatal("列表里没有新建的套餐")
	}

	// 删除空套餐。
	if code, data := e.api("DELETE", fmt.Sprintf("/api/plans/%d", p.ID), nil); code != http.StatusOK {
		t.Fatalf("删空套餐: %d %s", code, data)
	}
	if code, _ := e.api("GET", fmt.Sprintf("/api/plans/%d", p.ID), nil); code != http.StatusNotFound {
		t.Fatalf("删除后读 = %d, want 404", code)
	}
}

func TestPlanDeleteRules(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("la-1", "la.example.com")
	e.createInbound(n.ID, "ws", 8443)

	def := defaultPlanOf(t, e)
	if code, data := e.api("DELETE", fmt.Sprintf("/api/plans/%d", def.ID), nil); code != http.StatusConflict {
		t.Fatalf("删默认套餐 = %d %s, want 409", code, data)
	}

	p1 := e.createPlan(model.Plan{Name: "基础"})
	p2 := e.createPlan(model.Plan{Name: "全球"})

	// 挂一个用户到 p1。
	if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":"alice","plan_id":%d}`, p1.ID))); code != http.StatusCreated {
		t.Fatalf("建带套餐用户: %d %s", code, data)
	}

	// 还有用户 → 409,错误里要有用户数。
	code, data := e.api("DELETE", fmt.Sprintf("/api/plans/%d", p1.ID), nil)
	if code != http.StatusConflict {
		t.Fatalf("删有用户的套餐 = %d %s, want 409", code, data)
	}
	if !strings.Contains(string(data), "1") {
		t.Fatalf("409 里没带用户数: %s", data)
	}
	if got := userOf(t, e, "alice"); got.PlanID == nil || *got.PlanID != p1.ID {
		t.Fatalf("被拒绝后用户归属变了: %+v", got)
	}

	// reassign 到 p2。
	if code, data := e.api("DELETE", fmt.Sprintf("/api/plans/%d?reassign=%d", p1.ID, p2.ID), nil); code != http.StatusOK {
		t.Fatalf("改挂后删: %d %s", code, data)
	}
	if got := userOf(t, e, "alice"); got.PlanID == nil || *got.PlanID != p2.ID {
		t.Fatalf("用户没被改挂: %+v", got)
	}

	// 再建一个用户挂 p2,然后用 reassign= 摘掉再删。
	if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":"bob","plan_id":%d}`, p2.ID))); code != http.StatusCreated {
		t.Fatalf("建 bob: %d %s", code, data)
	}
	// 空的 reassign 不能把用户摘出套餐(否则会回落到全部入站):409,归属不变。
	if code, data := e.api("DELETE", fmt.Sprintf("/api/plans/%d?reassign=", p2.ID), nil); code != http.StatusConflict ||
		!strings.Contains(string(data), "reassign") {
		t.Fatalf("空 reassign 删有用户的套餐: %d %s", code, data)
	}
	for _, name := range []string{"alice", "bob"} {
		if got := userOf(t, e, name); got.PlanID == nil || *got.PlanID != p2.ID {
			t.Fatalf("%s 的套餐被动了: %+v", name, got)
		}
	}
	// 套餐里没有用户时,空 reassign 照常删除。
	empty := e.createPlan(model.Plan{Name: "空套餐"})
	if code, data := e.api("DELETE", fmt.Sprintf("/api/plans/%d?reassign=", empty.ID), nil); code != http.StatusOK {
		t.Fatalf("空 reassign 删无用户套餐: %d %s", code, data)
	}

	// 把用户改挂到正在删除的套餐应当被拒绝。
	p3 := e.createPlan(model.Plan{Name: "临时"})
	if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":"carol","plan_id":%d}`, p3.ID))); code != http.StatusCreated {
		t.Fatalf("建 carol: %d %s", code, data)
	}
	if code, _ := e.api("DELETE", fmt.Sprintf("/api/plans/%d?reassign=%d", p3.ID, p3.ID), nil); code != http.StatusBadRequest {
		t.Fatalf("改挂到自己 = %d, want 400", code)
	}
}

func TestPlanDuplicateAndBadRefs(t *testing.T) {
	e := newEnv(t)
	e.createPlan(model.Plan{Name: "重名"})
	if code, data := e.api("POST", "/api/plans", model.Plan{Name: "重名"}); code != http.StatusConflict {
		t.Fatalf("重名 = %d %s, want 409", code, data)
	}

	// 引用不存在的节点/入站 → 400(与现有校验风格一致)。
	if code, data := e.api("POST", "/api/plans", model.Plan{Name: "坏节点", NodeIDs: []int64{9999}}); code != http.StatusBadRequest {
		t.Fatalf("坏节点 = %d %s, want 400", code, data)
	}
	if code, data := e.api("POST", "/api/plans", model.Plan{Name: "坏入站", InboundIDs: []int64{9999}}); code != http.StatusBadRequest {
		t.Fatalf("坏入站 = %d %s, want 400", code, data)
	}
}

func TestPlanFieldLimits(t *testing.T) {
	e := newEnv(t)

	longName := strings.Repeat("名", model.PlanNameMaxBytes) // 每个汉字 3 字节
	if code, data := e.api("POST", "/api/plans", model.Plan{Name: longName}); code != http.StatusBadRequest {
		t.Fatalf("超长名字 = %d %s, want 400", code, data)
	}
	if code, data := e.api("POST", "/api/plans", model.Plan{Name: "ok", Note: strings.Repeat("a", model.PlanNoteMaxBytes+1)}); code != http.StatusBadRequest {
		t.Fatalf("超长备注 = %d %s, want 400", code, data)
	}

	nodes := make([]int64, model.PlanMaxNodes+1)
	for i := range nodes {
		nodes[i] = int64(i + 1)
	}
	if code, data := e.api("POST", "/api/plans", model.Plan{Name: "太多节点", NodeIDs: nodes}); code != http.StatusBadRequest {
		t.Fatalf("超多节点 = %d %s, want 400", code, data)
	}

	inbounds := make([]int64, model.PlanMaxInbounds+1)
	for i := range inbounds {
		inbounds[i] = int64(i + 1)
	}
	if code, data := e.api("POST", "/api/plans", model.Plan{Name: "太多入站", InboundIDs: inbounds}); code != http.StatusBadRequest {
		t.Fatalf("超多入站 = %d %s, want 400", code, data)
	}

	if code, data := e.api("POST", "/api/plans", model.Plan{}); code != http.StatusBadRequest {
		t.Fatalf("空名字 = %d %s, want 400", code, data)
	}
}

// TestPlanResponseCountsExtraAuth 断言套餐响应里带着「用户数」和「有额外授权的
// 用户数」:前者是列表页的人数,后者提示这个套餐的设置没有对这些用户完全生效。
func TestPlanResponseCountsExtraAuth(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("la-1", "la.example.com")
	in := e.createInbound(n.ID, "ws", 8443)
	p := e.createPlan(model.Plan{Name: "基础", NodeIDs: []int64{n.ID}})

	if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":"plain","plan_id":%d}`, p.ID))); code != http.StatusCreated {
		t.Fatalf("建 plain: %d %s", code, data)
	}
	if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":"extra","plan_id":%d,"inbound_ids":[%d]}`, p.ID, in.ID))); code != http.StatusCreated {
		t.Fatalf("建 extra: %d %s", code, data)
	}

	got := planByID(t, e, p.ID)
	if got.Users != 2 || got.ExtraAuth != 1 {
		t.Fatalf("套餐统计 = users %d extra %d, want 2/1", got.Users, got.ExtraAuth)
	}
}

// TestUserPlanOwnership 覆盖 POST 的三种归属与 PUT 的三态 plan_id。
func TestUserPlanOwnership(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("la-1", "la.example.com")
	in1 := e.createInbound(n.ID, "ws-1", 8443)

	def := defaultPlanOf(t, e)
	p1 := e.createPlan(model.Plan{Name: "基础", InboundIDs: []int64{in1.ID}})
	p2 := e.createPlan(model.Plan{Name: "全球"})

	// 1. 没带 plan_id、入站为空 → 归默认套餐。
	if code, data := e.requestRaw("POST", "/api/users", []byte(`{"username":"defaulted"}`)); code != http.StatusCreated {
		t.Fatalf("建 defaulted: %d %s", code, data)
	}
	if got := userOf(t, e, "defaulted"); got.PlanID == nil || *got.PlanID != def.ID {
		t.Fatalf("默认归属不对: %+v", got)
	}

	// 2. 带了 plan_id → 用该套餐。
	if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":"chosen","plan_id":%d}`, p1.ID))); code != http.StatusCreated {
		t.Fatalf("建 chosen: %d %s", code, data)
	}
	if got := userOf(t, e, "chosen"); got.PlanID == nil || *got.PlanID != p1.ID {
		t.Fatalf("按指定套餐归属不对: %+v", got)
	}

	// 3. 没带 plan_id 但带了入站(旧客户端)→ 不挂套餐。
	if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":"legacy","inbound_ids":[%d]}`, in1.ID))); code != http.StatusCreated {
		t.Fatalf("建 legacy: %d %s", code, data)
	}
	if got := userOf(t, e, "legacy"); got.PlanID != nil {
		t.Fatalf("旧写法不该挂套餐: %+v", got)
	}

	// 套餐不存在 → 4xx。
	if code, _ := e.requestRaw("POST", "/api/users", []byte(`{"username":"bogus","plan_id":99999}`)); code < 400 || code >= 500 {
		t.Fatalf("不存在的套餐 = %d, want 4xx", code)
	}
	if _, data := e.api("GET", "/api/users/bogus", nil); !strings.Contains(string(data), "不存在") {
		t.Fatalf("bogus 不该被建出来: %s", data)
	}

	// PUT 不带 plan_id(原始 JSON 里删掉这个键)→ 保持 p1。
	if code := putUserRaw(t, e, "chosen", func(m map[string]any) { delete(m, "plan_id") }); code != http.StatusOK {
		t.Fatalf("不带 plan_id 的 PUT = %d", code)
	}
	if got := userOf(t, e, "chosen"); got.PlanID == nil || *got.PlanID != p1.ID {
		t.Fatalf("老客户端 PUT 丢了套餐: %+v", got)
	}

	// PUT 带 null → 400:不能把用户移出套餐(否则会回落到全部入站),归属不变。
	if code := putUserRaw(t, e, "chosen", func(m map[string]any) {
		delete(m, "inbound_ids")
		m["plan_id"] = nil
	}); code != http.StatusBadRequest {
		t.Fatalf("带 null 的 PUT = %d, want 400", code)
	}
	if got := userOf(t, e, "chosen"); got.PlanID == nil || *got.PlanID != p1.ID {
		t.Fatalf("被拒绝的 null 动了归属: %+v", got)
	}
	// POST 带 null 同样拒绝。
	if code, _ := e.requestRaw("POST", "/api/users", []byte(`{"username":"nullplan","plan_id":null}`)); code != http.StatusBadRequest {
		t.Fatalf("POST 带 null = %d, want 400", code)
	}

	// PUT 带 id → 改挂。
	if code := putUserRaw(t, e, "chosen", func(m map[string]any) { m["plan_id"] = p2.ID }); code != http.StatusOK {
		t.Fatalf("带 id 的 PUT = %d", code)
	}
	if got := userOf(t, e, "chosen"); got.PlanID == nil || *got.PlanID != p2.ID {
		t.Fatalf("改挂没生效: %+v", got)
	}

	// PUT 带不存在的 id → 4xx,且归属不变。
	if code := putUserRaw(t, e, "chosen", func(m map[string]any) { m["plan_id"] = 99999 }); code < 400 || code >= 500 {
		t.Fatalf("改挂到不存在的套餐 = %d, want 4xx", code)
	}
	if got := userOf(t, e, "chosen"); got.PlanID == nil || *got.PlanID != p2.ID {
		t.Fatalf("失败的改挂动了归属: %+v", got)
	}

	// all_nodes 套餐被写入非空入站 → 自动摘套餐(store 规则)。
	if code := putUserRaw(t, e, "defaulted", func(m map[string]any) {
		delete(m, "plan_id")
		m["inbound_ids"] = []int64{in1.ID}
	}); code != http.StatusOK {
		t.Fatalf("默认套餐写非空入站 = %d", code)
	}
	if got := userOf(t, e, "defaulted"); got.PlanID != nil {
		t.Fatalf("all_nodes 套餐应当被摘掉: %+v", got)
	}
}

func TestListUsersByPlan(t *testing.T) {
	e := newEnv(t)
	p1 := e.createPlan(model.Plan{Name: "基础"})
	p2 := e.createPlan(model.Plan{Name: "全球"})
	for _, name := range []string{"a", "b"} {
		if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":%q,"plan_id":%d}`, name, p1.ID))); code != http.StatusCreated {
			t.Fatalf("建 %s: %d %s", name, code, data)
		}
	}
	if code, data := e.requestRaw("POST", "/api/users", []byte(fmt.Sprintf(`{"username":"c","plan_id":%d}`, p2.ID))); code != http.StatusCreated {
		t.Fatalf("建 c: %d %s", code, data)
	}

	code, page := listUsers(t, e, fmt.Sprintf("plan=%d", p1.ID))
	if code != http.StatusOK || page.Total != 2 {
		t.Fatalf("按 p1 筛: code=%d total=%d", code, page.Total)
	}
	for _, u := range page.Users {
		if u.PlanID == nil || *u.PlanID != p1.ID {
			t.Fatalf("筛出来不属于 p1: %+v", u)
		}
	}
	code, page = listUsers(t, e, fmt.Sprintf("plan=%d", p2.ID))
	if code != http.StatusOK || page.Total != 1 || page.Users[0].Username != "c" {
		t.Fatalf("按 p2 筛: code=%d %+v", code, page)
	}
	if code, _ := listUsers(t, e, "plan=abc"); code != http.StatusBadRequest {
		t.Fatalf("坏 plan 参数 = %d, want 400", code)
	}
}

func TestPlanPreview(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("la-1", "la.example.com")
	in := e.createInbound(n.ID, "reality", 443)
	// 入站上限 1,再让一个用户占住它,预览里该是「满员」。
	if code, data := e.api("PUT", fmt.Sprintf("/api/inbounds/%d", in.ID), model.Inbound{
		NodeID: n.ID, Tag: "reality", Protocol: model.ProtocolVLESS, Port: 443,
		Network: model.NetworkTCP, Security: model.SecurityNone, MaxUsers: 1,
	}); code != http.StatusOK {
		t.Fatalf("把入站上限设为 1: %d %s", code, data)
	}
	e.createUser("occupier") // 默认套餐(all_nodes)能用这个入站

	p := e.createPlan(model.Plan{Name: "基础", NodeIDs: []int64{n.ID}})
	code, data := e.api("GET", fmt.Sprintf("/api/plans/%d/preview", p.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("套餐预览: %d %s", code, data)
	}
	pv := decodeJSON[planPreview](t, data)
	if pv.Plan.ID != p.ID {
		t.Fatalf("预览里的套餐不对: %+v", pv.Plan)
	}
	if len(pv.Nodes) != 1 || pv.Nodes[0].ID != n.ID {
		t.Fatalf("预览节点不对: %+v", pv.Nodes)
	}
	if len(pv.Inbounds) != 1 {
		t.Fatalf("预览入站数 = %d, want 1", len(pv.Inbounds))
	}
	pi := pv.Inbounds[0]
	if !pi.Full || pi.Available || pi.Reason != "入站满员" {
		t.Fatalf("满员没标出来: %+v", pi)
	}
	if pi.ID != in.ID || pi.Tag != "reality" || pi.MaxUsers != 1 || pi.Users != 1 {
		t.Fatalf("预览入站字段不对: %+v", pi)
	}

	// 排水节点:节点暂不在订阅里。
	n2 := e.createNode("jp-1", "jp.example.com")
	e.createInbound(n2.ID, "ws", 8443)
	if code, data := e.api("POST", fmt.Sprintf("/api/nodes/%d/drain", n2.ID), nil); code != http.StatusOK {
		t.Fatalf("排水: %d %s", code, data)
	}
	p2 := e.createPlan(model.Plan{Name: "日本", NodeIDs: []int64{n2.ID}})
	code, data = e.api("GET", fmt.Sprintf("/api/plans/%d/preview", p2.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("排水套餐预览: %d %s", code, data)
	}
	pv = decodeJSON[planPreview](t, data)
	if len(pv.Nodes) != 1 || !pv.Nodes[0].Draining || pv.Nodes[0].Available || pv.Nodes[0].Reason != "节点排水中" {
		t.Fatalf("排水没标出来: %+v", pv.Nodes)
	}
	if len(pv.Inbounds) != 1 || pv.Inbounds[0].Available {
		t.Fatalf("排水的入站应不可用: %+v", pv.Inbounds)
	}
}

func TestProxyGroupPreviewByPlan(t *testing.T) {
	e := newEnv(t)
	n1 := e.createNode("la-1", "la.example.com")
	in1 := e.createInbound(n1.ID, "ws-1", 8443)
	n2 := e.createNode("jp-1", "jp.example.com")
	e.createInbound(n2.ID, "ws-2", 8444)

	p := e.createPlan(model.Plan{Name: "基础", InboundIDs: []int64{in1.ID}})

	g := model.ProxyGroup{Name: "全部"}
	code, data := e.api("POST", "/api/proxy-groups/preview", g)
	if code != http.StatusOK {
		t.Fatalf("不带 plan 预览: %d %s", code, data)
	}
	all := decodeJSON[[]previewEntry](t, data)
	if len(all) < 2 {
		t.Fatalf("不带 plan 应看到全部入口, got %d", len(all))
	}

	code, data = e.api("POST", fmt.Sprintf("/api/proxy-groups/preview?plan=%d", p.ID), g)
	if code != http.StatusOK {
		t.Fatalf("带 plan 预览: %d %s", code, data)
	}
	scoped := decodeJSON[[]previewEntry](t, data)
	if len(scoped) != 1 || scoped[0].InboundID != in1.ID {
		t.Fatalf("带 plan 应只看套餐内入口: %+v", scoped)
	}

	if code, _ := e.api("POST", "/api/proxy-groups/preview?plan=abc", g); code != http.StatusBadRequest {
		t.Fatalf("坏 plan = %d, want 400", code)
	}
	if code, _ := e.api("POST", "/api/proxy-groups/preview?plan=99999", g); code != http.StatusNotFound {
		t.Fatalf("不存在的 plan = %d, want 404", code)
	}
}

// TestPlanChangeInvalidatesSubscription 断言改套餐归属后订阅内容跟着变,
// 而不是被订阅缓存吃掉。
func TestPlanChangeInvalidatesSubscription(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("la-1", "la.example.com")
	in1 := e.createInbound(n.ID, "ws-1", 8443)
	in2 := e.createInbound(n.ID, "ws-2", 8444)
	e.createHost(in1.ID, "one.example.com", "one.example.com")
	e.createHost(in2.ID, "two.example.com", "two.example.com")

	p1 := e.createPlan(model.Plan{Name: "基础", InboundIDs: []int64{in1.ID}})
	p2 := e.createPlan(model.Plan{Name: "全球", InboundIDs: []int64{in2.ID}})

	user := e.createUser("alice")
	path := "/sub/" + user.SubToken + "/links"
	code, _, body := e.sub(path, "v2rayng/1.8")
	if code != http.StatusOK {
		t.Fatalf("订阅: %d %s", code, body)
	}
	if !strings.Contains(string(body), "one.example.com") || !strings.Contains(string(body), "two.example.com") {
		t.Fatalf("默认套餐应能看到两个入口:\n%s", body)
	}

	// 改挂 p1:缓存必须先失效。
	if code := putUserRaw(t, e, "alice", func(m map[string]any) { m["plan_id"] = p1.ID }); code != http.StatusOK {
		t.Fatalf("改挂 p1 = %d", code)
	}
	code, _, body = e.sub(path, "v2rayng/1.8")
	if code != http.StatusOK || !strings.Contains(string(body), "one.example.com") || strings.Contains(string(body), "two.example.com") {
		t.Fatalf("改挂 p1 后订阅不对:\n%s", body)
	}

	// 再改挂 p2:第二次缓存失效同样要生效。
	if code := putUserRaw(t, e, "alice", func(m map[string]any) { m["plan_id"] = p2.ID }); code != http.StatusOK {
		t.Fatalf("改挂 p2 = %d", code)
	}
	code, _, body = e.sub(path, "v2rayng/1.8")
	if code != http.StatusOK || !strings.Contains(string(body), "two.example.com") || strings.Contains(string(body), "one.example.com") {
		t.Fatalf("改挂 p2 后订阅不对:\n%s", body)
	}

	// 套餐本身被改动也要让订阅跟着变:从 p2 里去掉入站,订阅就当它没授权。
	if code, data := e.api("PUT", fmt.Sprintf("/api/plans/%d", p2.ID), model.Plan{Name: "全球", InboundIDs: nil}); code != http.StatusOK {
		t.Fatalf("改套餐范围: %d %s", code, data)
	}
	code, _, body = e.sub(path, "v2rayng/1.8")
	if code != http.StatusOK {
		t.Fatalf("改套餐后订阅: %d %s", code, body)
	}
	if strings.Contains(string(body), "two.example.com") {
		t.Fatalf("套餐去掉入站后订阅仍带旧入口:\n%s", body)
	}
}
