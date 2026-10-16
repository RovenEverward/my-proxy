package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// wipePlans 把所有套餐状态清掉,模拟一个还没跑套餐迁移的旧库。顺序不能反:
// user_plans 与关联表都引用 plans,先删引用再删套餐。
func wipePlans(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{
		`DELETE FROM user_plans`,
		`DELETE FROM plan_inbounds`,
		`DELETE FROM plan_nodes`,
		`DELETE FROM plans`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("清空套餐状态(%s): %v", q, err)
		}
	}
}

func userPlanID(t *testing.T, s *Store, username string) *int64 {
	t.Helper()
	u, err := s.GetUser(username)
	if err != nil {
		t.Fatalf("GetUser(%s): %v", username, err)
	}
	return u.PlanID
}

// TestPlanMigrationEquivalence 是这次迁移最重要的一条:升级前后,每个用户对每个
// 入站的 AllowsInbound 必须完全一致,用户自己的 inbound_ids 不被清空。覆盖空、
// 单个、多个、相同集合、全是已删除 id、含重复 id 这几种情况。
func TestPlanMigrationEquivalence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "control.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	defer s.Close()

	nodeA := newTestNode(t, s, "node-a")
	nodeB := newTestNode(t, s, "node-b")
	in1 := newTestInbound(t, s, nodeA.ID, "a-1")
	in2 := newTestInbound(t, s, nodeA.ID, "a-2")
	in3 := newTestInbound(t, s, nodeB.ID, "b-1")

	specs := []struct {
		name string
		ids  []int64
	}{
		{"empty", nil},
		{"single", []int64{in1.ID}},
		{"multi", []int64{in1.ID, in3.ID}},
		{"same-a", []int64{in3.ID, in1.ID}},      // 与 multi 同集合,顺序不同
		{"stale", []int64{999999, 888888}},       // 全是已删除的入站 id
		{"dup", []int64{in2.ID, in2.ID, in1.ID}}, // 去重后是 {in1,in2}
	}
	for _, sp := range specs {
		u := &model.User{Username: sp.name, Key: "k", SubToken: "t-" + sp.name, Enabled: true, InboundIDs: sp.ids}
		if err := s.CreateUser(u); err != nil {
			t.Fatalf("创建用户 %s: %v", sp.name, err)
		}
	}

	// 模拟升级前:还没有任何套餐。
	wipePlans(t, s)

	probeIDs := []int64{in1.ID, in2.ID, in3.ID, 4242}
	before := map[string]map[int64]bool{}
	for _, sp := range specs {
		u, err := s.GetUser(sp.name)
		if err != nil {
			t.Fatalf("迁移前 GetUser(%s): %v", sp.name, err)
		}
		if u.PlanID != nil {
			t.Fatalf("清空套餐后 %s 不该有套餐", sp.name)
		}
		m := map[int64]bool{}
		for _, id := range probeIDs {
			m[id] = u.AllowsInbound(id)
		}
		before[sp.name] = m
	}

	rep, err := s.MigratePlans()
	if err != nil {
		t.Fatalf("MigratePlans: %v", err)
	}
	if !rep.Executed {
		t.Fatal("第一次迁移应当执行")
	}
	if rep.DefaultUsers != 1 {
		t.Fatalf("inbound_ids 为空的用户应只有 1 个,实际 %d", rep.DefaultUsers)
	}
	defID, err := s.DefaultPlanID()
	if err != nil {
		t.Fatalf("迁移后应当有默认套餐: %v", err)
	}
	if rep.DefaultPlanID != defID {
		t.Fatalf("报告里的默认套餐 id = %d, want %d", rep.DefaultPlanID, defID)
	}

	// 升级前后逐一比较,并确认 inbound_ids 没被清空。
	for _, sp := range specs {
		u, err := s.GetUser(sp.name)
		if err != nil {
			t.Fatalf("迁移后 GetUser(%s): %v", sp.name, err)
		}
		for _, id := range probeIDs {
			if got := u.AllowsInbound(id); got != before[sp.name][id] {
				t.Fatalf("迁移前后 %s 对入站 %d 的判定不一致: before=%v after=%v",
					sp.name, id, before[sp.name][id], got)
			}
		}
		if len(sp.ids) != 0 && len(u.InboundIDs) != len(sp.ids) {
			t.Fatalf("迁移不该清空 %s 的 inbound_ids: got %v want %v", sp.name, u.InboundIDs, sp.ids)
		}
	}

	// 全是已删除入站 id 的用户,迁移前后都不能用任何现存入站(上面的逐一比较
	// 已经隐含这一点,这里再显式钉一次)。
	stale, err := s.GetUser("stale")
	if err != nil {
		t.Fatalf("GetUser(stale): %v", err)
	}
	for _, id := range []int64{in1.ID, in2.ID, in3.ID} {
		if stale.AllowsInbound(id) {
			t.Fatalf("全过期集合的用户不该能用入站 %d", id)
		}
	}

	plans, err := s.ListPlans()
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	planByID := map[int64]model.Plan{}
	for _, p := range plans {
		planByID[p.ID] = p
	}
	planOf := map[string]int64{}
	for _, sp := range specs {
		id := userPlanID(t, s, sp.name)
		if id == nil {
			t.Fatalf("%s 迁移后应当挂在某个套餐上", sp.name)
		}
		planOf[sp.name] = *id
	}

	if planOf["empty"] != defID {
		t.Fatalf("inbound_ids 为空的用户应归默认套餐,实际 %d", planOf["empty"])
	}
	if planOf["multi"] != planOf["same-a"] {
		t.Fatalf("入站集合相同的用户应共用一个迁移套餐: multi=%d same-a=%d", planOf["multi"], planOf["same-a"])
	}
	for _, other := range []string{"single", "dup", "stale"} {
		if planOf["multi"] == planOf[other] {
			t.Fatalf("集合不同的用户不该共用套餐: multi 与 %s", other)
		}
	}
	if planOf["stale"] == defID {
		t.Fatal("全是已删除入站的用户不得归默认套餐")
	}

	// plan_inbounds 只收仍存在的入站。
	if got := planByID[planOf["stale"]].InboundIDs; len(got) != 0 {
		t.Fatalf("全过期集合的迁移套餐应当为空,实际 %v", got)
	}
	if got := planByID[planOf["multi"]].InboundIDs; !sameInt64s(got, []int64{in1.ID, in3.ID}) {
		t.Fatalf("multi 的迁移套餐入站 = %v, want [%d %d]", got, in1.ID, in3.ID)
	}
	if got := planByID[planOf["dup"]].InboundIDs; !sameInt64s(got, []int64{in1.ID, in2.ID}) {
		t.Fatalf("去重后的迁移套餐入站 = %v, want [%d %d]", got, in1.ID, in2.ID)
	}

	// 名字用节点显示名(没有元数据时退回节点名),重名去重。
	if got := planByID[planOf["multi"]].Name; got != "迁移 · node-a + node-b" {
		t.Fatalf("多节点迁移套餐名 = %q", got)
	}
	if got := planByID[planOf["single"]].Name; got != "迁移 · node-a" {
		t.Fatalf("单节点迁移套餐名 = %q", got)
	}
	if got := planByID[planOf["stale"]].Name; got != "迁移套餐" {
		t.Fatalf("无存活节点的迁移套餐名 = %q", got)
	}
	seen := map[string]bool{}
	for _, p := range plans {
		if seen[p.Name] {
			t.Fatalf("套餐名重复: %q", p.Name)
		}
		seen[p.Name] = true
	}
	// 报告里的迁移套餐 → 用户名。
	if names := rep.MigratedPlans[planByID[planOf["multi"]].Name]; len(names) != 2 {
		t.Fatalf("multi 迁移套餐报告里应有 2 个用户,实际 %v", names)
	}

	// 幂等:再跑一次不执行,套餐数不变。
	beforeCount := len(plans)
	again, err := s.MigratePlans()
	if err != nil {
		t.Fatalf("再次 MigratePlans: %v", err)
	}
	if again.Executed {
		t.Fatal("第二次迁移不该执行")
	}
	if again.DefaultUsers != 1 {
		t.Fatalf("再次迁移的默认套餐人数 = %d, want 1", again.DefaultUsers)
	}
	plansAgain, err := s.ListPlans()
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plansAgain) != beforeCount {
		t.Fatalf("重复迁移改了套餐数: got %d want %d", len(plansAgain), beforeCount)
	}

	// 重新打开同一个库,也不该重复迁移。
	if err := s.Close(); err != nil {
		t.Fatalf("关库: %v", err)
	}
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("重新打开: %v", err)
	}
	defer reopened.Close()
	rep3, err := reopened.MigratePlans()
	if err != nil {
		t.Fatalf("重开后再迁移: %v", err)
	}
	if rep3.Executed {
		t.Fatal("重新打开后不该重复迁移")
	}
	plans3, err := reopened.ListPlans()
	if err != nil {
		t.Fatalf("重开后 ListPlans: %v", err)
	}
	if len(plans3) != beforeCount {
		t.Fatalf("重开后套餐数 = %d, want %d", len(plans3), beforeCount)
	}
}

// TestDefaultPlanBehavior 覆盖默认套餐:恰好一个、all_nodes、可改不可删、有用户
// 的套餐不可删,以及新加的节点/入站自动进入默认套餐。
func TestDefaultPlanBehavior(t *testing.T) {
	s := newTestStore(t)

	defID, err := s.DefaultPlanID()
	if err != nil {
		t.Fatalf("空库应当有默认套餐: %v", err)
	}
	def, err := s.GetPlan(defID)
	if err != nil {
		t.Fatalf("GetPlan(默认): %v", err)
	}
	if def.Name != "默认套餐" || !def.IsDefault || !def.AllNodes {
		t.Fatalf("默认套餐形状不对: %+v", def)
	}
	if err := s.DeletePlan(defID); !errors.Is(err, ErrDefaultPlan) {
		t.Fatalf("默认套餐不能删,实际 %v", err)
	}

	// 新用户没指定套餐且没有入站 → 默认套餐。
	u := newTestUser(t, s, "alice")
	if u.PlanID == nil || *u.PlanID != defID {
		t.Fatalf("新用户应归默认套餐,实际 %v", u.PlanID)
	}

	// 建了新节点/入站之后,默认套餐用户自动可用。
	lateNode := newTestNode(t, s, "late")
	lateIn := newTestInbound(t, s, lateNode.ID, "late-1")
	got, err := s.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !got.AllowsInbound(lateIn.ID) {
		t.Fatalf("默认套餐应当包含后加的入站 %d", lateIn.ID)
	}

	// 默认套餐即使被写成 all_nodes=false 也要保持包含全部节点。
	def.AllNodes = false
	if err := s.UpdatePlan(def); err != nil {
		t.Fatalf("改默认套餐: %v", err)
	}
	def2, err := s.GetPlan(defID)
	if err != nil {
		t.Fatalf("GetPlan(默认): %v", err)
	}
	if !def2.AllNodes {
		t.Fatal("默认套餐必须始终保持 all_nodes")
	}

	// 有用户的套餐不能删,错误里带用户数。
	p := &model.Plan{Name: "基础", InboundIDs: []int64{lateIn.ID}}
	if err := s.CreatePlan(p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	bob := &model.User{Username: "bob", Key: "k", SubToken: "t-bob", Enabled: true, PlanID: &p.ID}
	if err := s.CreateUser(bob); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	err = s.DeletePlan(p.ID)
	var inUse *PlanInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("有用户的套餐应报 PlanInUseError,实际 %v", err)
	}
	if inUse.Users != 1 {
		t.Fatalf("PlanInUseError.Users = %d, want 1", inUse.Users)
	}
	if n, err := s.CountPlanUsers(p.ID); err != nil || n != 1 {
		t.Fatalf("CountPlanUsers = %d/%v, want 1", n, err)
	}
}

// TestPlanNodeAuthorizesLaterInbounds 覆盖「按节点授权包含节点上以后新加的入站」。
func TestPlanNodeAuthorizesLaterInbounds(t *testing.T) {
	s := newTestStore(t)
	n1 := newTestNode(t, s, "n1")
	n2 := newTestNode(t, s, "n2")

	p := &model.Plan{Name: "基础", NodeIDs: []int64{n1.ID}}
	if err := s.CreatePlan(p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	u := &model.User{Username: "alice", Key: "k", SubToken: "t", Enabled: true, PlanID: &p.ID}
	if err := s.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	later := newTestInbound(t, s, n1.ID, "later")
	other := newTestInbound(t, s, n2.ID, "other")
	got, err := s.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !got.AllowsInbound(later.ID) {
		t.Fatalf("plan_nodes 授权应包含节点后加的入站 %d", later.ID)
	}
	if got.AllowsInbound(other.ID) {
		t.Fatalf("不在套餐内节点的入站 %d 不该可用", other.ID)
	}
}

// TestPlanCascadeOnDelete 覆盖删除节点/入站/用户后的级联清理。
func TestPlanCascadeOnDelete(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "n1")
	in := newTestInbound(t, s, n.ID, "in1")

	byInbound := &model.Plan{Name: "按入站", InboundIDs: []int64{in.ID}}
	if err := s.CreatePlan(byInbound); err != nil {
		t.Fatalf("CreatePlan(按入站): %v", err)
	}
	byNode := &model.Plan{Name: "按节点", NodeIDs: []int64{n.ID}}
	if err := s.CreatePlan(byNode); err != nil {
		t.Fatalf("CreatePlan(按节点): %v", err)
	}

	user := &model.User{Username: "alice", Key: "k", SubToken: "t", Enabled: true, PlanID: &byInbound.ID}
	if err := s.CreateUser(user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// 删入站 → plan_inbounds 级联;用户仍挂着套餐(套餐本身没删)。
	if err := s.DeleteInbound(in.ID); err != nil {
		t.Fatalf("DeleteInbound: %v", err)
	}
	gotInbound, err := s.GetPlan(byInbound.ID)
	if err != nil {
		t.Fatalf("GetPlan(按入站): %v", err)
	}
	if len(gotInbound.InboundIDs) != 0 {
		t.Fatalf("入站删除后 plan_inbounds 应清空,实际 %v", gotInbound.InboundIDs)
	}

	// 删节点 → plan_nodes 级联。
	if err := s.DeleteNode(n.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	gotNode, err := s.GetPlan(byNode.ID)
	if err != nil {
		t.Fatalf("GetPlan(按节点): %v", err)
	}
	if len(gotNode.NodeIDs) != 0 {
		t.Fatalf("节点删除后 plan_nodes 应清空,实际 %v", gotNode.NodeIDs)
	}

	// 删用户 → user_plans 级联。
	if err := s.DeleteUser("alice"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if n, err := s.CountPlanUsers(byInbound.ID); err != nil || n != 0 {
		t.Fatalf("用户删除后套餐用户数 = %d/%v, want 0", n, err)
	}
}

// TestUserPlanAssignment 覆盖新建用户的三种套餐归属。
func TestUserPlanAssignment(t *testing.T) {
	s := newTestStore(t)
	defID, err := s.DefaultPlanID()
	if err != nil {
		t.Fatalf("DefaultPlanID: %v", err)
	}
	in := newTestInbound(t, s, newTestNode(t, s, "n1").ID, "in1")

	// (a) 没指定 + 空入站 → 默认套餐。
	a := &model.User{Username: "a", Key: "k", SubToken: "t-a", Enabled: true}
	if err := s.CreateUser(a); err != nil {
		t.Fatalf("CreateUser(a): %v", err)
	}
	if a.PlanID == nil || *a.PlanID != defID {
		t.Fatalf("(a) 应归默认套餐,实际 %v", a.PlanID)
	}
	// 关键:CreateUser 返回的同一个对象必须已经带上解析后的授权,直接能用。
	if !a.AllowsInbound(in.ID) {
		t.Fatal("(a) CreateUser 后同一个对象应当直接能用默认套餐的入站")
	}

	// (b) 没指定 + 非空入站 → 不挂套餐,按旧语义只用这些入站。
	b := &model.User{Username: "b", Key: "k", SubToken: "t-b", Enabled: true, InboundIDs: []int64{in.ID}}
	if err := s.CreateUser(b); err != nil {
		t.Fatalf("CreateUser(b): %v", err)
	}
	if b.PlanID != nil {
		t.Fatalf("(b) 带额外授权的新用户不该挂套餐,实际 %v", b.PlanID)
	}
	if !b.AllowsInbound(in.ID) || b.AllowsInbound(9999) {
		t.Fatal("(b) 同一个对象应当只放行列出的入站")
	}
	gotB, err := s.GetUser("b")
	if err != nil {
		t.Fatalf("GetUser(b): %v", err)
	}
	if !gotB.AllowsInbound(in.ID) || gotB.AllowsInbound(9999) {
		t.Fatal("(b) 应当只放行列出的入站")
	}

	// (c) 显式指定套餐 → 用它。
	p := &model.Plan{Name: "基础", InboundIDs: []int64{in.ID}}
	if err := s.CreatePlan(p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	c := &model.User{Username: "c", Key: "k", SubToken: "t-c", Enabled: true, PlanID: &p.ID}
	if err := s.CreateUser(c); err != nil {
		t.Fatalf("CreateUser(c): %v", err)
	}
	if c.PlanID == nil || *c.PlanID != p.ID {
		t.Fatalf("(c) 应挂在指定套餐,实际 %v", c.PlanID)
	}
	if !c.AllowsInbound(in.ID) {
		t.Fatalf("(c) CreateUser 后同一个对象应当直接能用指定套餐里的入站")
	}
	gotC, err := s.GetUser("c")
	if err != nil {
		t.Fatalf("GetUser(c): %v", err)
	}
	if !gotC.AllowsInbound(in.ID) {
		t.Fatalf("(c) 指定套餐里的入站应当可用")
	}
}

// TestUpdateUserAllNodesStripsPlan 覆盖 RFC 的例外:all_nodes 套餐下被写入非空
// inbound_ids 时摘掉套餐;其余情况保持套餐归属。
func TestUpdateUserAllNodesStripsPlan(t *testing.T) {
	s := newTestStore(t)
	defID, err := s.DefaultPlanID()
	if err != nil {
		t.Fatalf("DefaultPlanID: %v", err)
	}
	node := newTestNode(t, s, "n1")
	in1 := newTestInbound(t, s, node.ID, "in1")
	in2 := newTestInbound(t, s, node.ID, "in2")
	other := newTestInbound(t, s, newTestNode(t, s, "n2").ID, "other")

	// all_nodes 套餐 + 写入非空 inbound_ids → 摘掉套餐。
	alice := newTestUser(t, s, "alice")
	if alice.PlanID == nil || *alice.PlanID != defID {
		t.Fatalf("alice 应先在默认套餐上")
	}
	got, err := s.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser(alice): %v", err)
	}
	got.InboundIDs = []int64{in1.ID}
	if err := s.UpdateUser(got); err != nil {
		t.Fatalf("UpdateUser(alice): %v", err)
	}
	afterAlice, err := s.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser(alice): %v", err)
	}
	if afterAlice.PlanID != nil {
		t.Fatalf("all_nodes 下写入入站应摘掉套餐,实际 %v", afterAlice.PlanID)
	}
	if !afterAlice.AllowsInbound(in1.ID) || afterAlice.AllowsInbound(in2.ID) {
		t.Fatal("摘掉套餐后应只按写入的入站放行")
	}

	// 非 all_nodes 套餐 + 写入非空 inbound_ids → 归属不变。
	p := &model.Plan{Name: "基础", InboundIDs: []int64{in1.ID}}
	if err := s.CreatePlan(p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	bob := &model.User{Username: "bob", Key: "k", SubToken: "t-bob", Enabled: true, PlanID: &p.ID}
	if err := s.CreateUser(bob); err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}
	gotBob, err := s.GetUser("bob")
	if err != nil {
		t.Fatalf("GetUser(bob): %v", err)
	}
	gotBob.InboundIDs = []int64{other.ID}
	if err := s.UpdateUser(gotBob); err != nil {
		t.Fatalf("UpdateUser(bob): %v", err)
	}
	afterBob, err := s.GetUser("bob")
	if err != nil {
		t.Fatalf("GetUser(bob): %v", err)
	}
	if afterBob.PlanID == nil || *afterBob.PlanID != p.ID {
		t.Fatalf("非 all_nodes 套餐不该被摘掉,实际 %v", afterBob.PlanID)
	}
	if !afterBob.AllowsInbound(in1.ID) || !afterBob.AllowsInbound(other.ID) {
		t.Fatal("套餐入站与额外授权应当取并集")
	}

	// 默认套餐 + 写入空 inbound_ids → 保持默认。
	newTestUser(t, s, "carol")
	gotCarol, err := s.GetUser("carol")
	if err != nil {
		t.Fatalf("GetUser(carol): %v", err)
	}
	gotCarol.InboundIDs = nil
	if err := s.UpdateUser(gotCarol); err != nil {
		t.Fatalf("UpdateUser(carol): %v", err)
	}
	afterCarol, err := s.GetUser("carol")
	if err != nil {
		t.Fatalf("GetUser(carol): %v", err)
	}
	if afterCarol.PlanID == nil || *afterCarol.PlanID != defID {
		t.Fatalf("写空入站不该摘掉默认套餐,实际 %v", afterCarol.PlanID)
	}
}

// TestSetUserPlan 覆盖显式改挂/摘掉套餐与相关计数。
func TestSetUserPlan(t *testing.T) {
	s := newTestStore(t)
	in := newTestInbound(t, s, newTestNode(t, s, "n1").ID, "in1")
	p := &model.Plan{Name: "基础", InboundIDs: []int64{in.ID}}
	if err := s.CreatePlan(p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	newTestUser(t, s, "alice")

	// 返回值必须已经带上解析后的授权,可以直接判 AllowsInbound。
	got, err := s.SetUserPlan("alice", &p.ID)
	if err != nil {
		t.Fatalf("SetUserPlan(挂): %v", err)
	}
	if got == nil || got.PlanID == nil || *got.PlanID != p.ID {
		t.Fatalf("改挂后 PlanID = %v, want %d", got.PlanID, p.ID)
	}
	if !got.AllowsInbound(in.ID) {
		t.Fatal("改挂后返回的对象应当直接能用套餐里的入站")
	}
	if n, err := s.CountPlanUsers(p.ID); err != nil || n != 1 {
		t.Fatalf("CountPlanUsers = %d/%v, want 1", n, err)
	}

	// 摘掉后回到旧语义(空入站 = 全部)。
	got, err = s.SetUserPlan("alice", nil)
	if err != nil {
		t.Fatalf("SetUserPlan(摘): %v", err)
	}
	if got.PlanID != nil {
		t.Fatalf("摘掉后 PlanID 应为 nil,实际 %v", got.PlanID)
	}
	if !got.AllowsInbound(9999) {
		t.Fatal("摘掉套餐且入站为空时应回到全部可用")
	}

	if _, err := s.SetUserPlan("nobody", &p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的用户应报 ErrNotFound,实际 %v", err)
	}
	missing := int64(99999)
	if _, err := s.SetUserPlan("alice", &missing); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("不存在的套餐应报 ErrPlanNotFound,实际 %v", err)
	}
}

// TestUpdateUserReturnsResolvedAuth 覆盖 UpdateUser 修改的同一个对象在返回后
// 仍带正确的授权:被摘掉 all_nodes 套餐的,以及没动套餐归属的。
func TestUpdateUserReturnsResolvedAuth(t *testing.T) {
	s := newTestStore(t)
	node := newTestNode(t, s, "n1")
	in1 := newTestInbound(t, s, node.ID, "in1")
	other := newTestInbound(t, s, newTestNode(t, s, "n2").ID, "other")

	// 默认套餐用户写入额外授权 → 摘掉套餐,同一个对象按旧语义只放行写入的入站。
	alice := newTestUser(t, s, "alice")
	alice.InboundIDs = []int64{in1.ID}
	if err := s.UpdateUser(alice); err != nil {
		t.Fatalf("UpdateUser(alice): %v", err)
	}
	if alice.PlanID != nil {
		t.Fatalf("摘套餐后 PlanID 应为 nil,实际 %v", alice.PlanID)
	}
	if !alice.AllowsInbound(in1.ID) || alice.AllowsInbound(other.ID) {
		t.Fatal("UpdateUser 后同一个对象应当只放行写入的入站")
	}

	// 非 all_nodes 套餐用户写入额外授权 → 归属不变,套餐与额外授权取并集。
	p := &model.Plan{Name: "基础", InboundIDs: []int64{in1.ID}}
	if err := s.CreatePlan(p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	bob := &model.User{Username: "bob", Key: "k", SubToken: "t-bob", Enabled: true, PlanID: &p.ID}
	if err := s.CreateUser(bob); err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}
	bob.InboundIDs = []int64{other.ID}
	if err := s.UpdateUser(bob); err != nil {
		t.Fatalf("UpdateUser(bob): %v", err)
	}
	if bob.PlanID == nil || *bob.PlanID != p.ID {
		t.Fatalf("非 all_nodes 套餐不该被摘掉,实际 %v", bob.PlanID)
	}
	if !bob.AllowsInbound(in1.ID) || !bob.AllowsInbound(other.ID) || bob.AllowsInbound(9999) {
		t.Fatal("UpdateUser 后同一个对象应当是套餐 ∪ 额外授权")
	}
}

// TestPlanUserStats 覆盖「每套餐用户数与其中有额外授权的用户数」一次查询统计。
func TestPlanUserStats(t *testing.T) {
	s := newTestStore(t)
	in := newTestInbound(t, s, newTestNode(t, s, "n1").ID, "in1")

	defID, err := s.DefaultPlanID()
	if err != nil {
		t.Fatalf("DefaultPlanID: %v", err)
	}
	// 默认套餐:一个普通用户 + 一个有额外授权的用户。
	newTestUser(t, s, "plain")
	extraDefault := &model.User{Username: "extra-default", Key: "k", SubToken: "t-ed", Enabled: true, InboundIDs: []int64{in.ID}}
	extraDefault.PlanID = &defID
	if err := s.CreateUser(extraDefault); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// 另一个套餐:一个有额外授权的用户;再建一个没人挂的套餐。
	p := &model.Plan{Name: "基础", InboundIDs: []int64{in.ID}}
	if err := s.CreatePlan(p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	extraPlan := &model.User{Username: "extra-plan", Key: "k", SubToken: "t-ep", Enabled: true, InboundIDs: []int64{in.ID}, PlanID: &p.ID}
	if err := s.CreateUser(extraPlan); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	empty := &model.Plan{Name: "没人挂"}
	if err := s.CreatePlan(empty); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	stats, err := s.PlanUserStats()
	if err != nil {
		t.Fatalf("PlanUserStats: %v", err)
	}
	if got := stats[defID]; got.Users != 2 || got.ExtraAuth != 1 {
		t.Fatalf("默认套餐统计 = %+v, want {2 1}", got)
	}
	if got := stats[p.ID]; got.Users != 1 || got.ExtraAuth != 1 {
		t.Fatalf("基础套餐统计 = %+v, want {1 1}", got)
	}
	if got, ok := stats[empty.ID]; !ok || got.Users != 0 || got.ExtraAuth != 0 {
		t.Fatalf("没有用户挂着的套餐也应当以零值出现: %+v", got)
	}
}

// TestPlanCRUD 覆盖套餐的增删改查、去重排序、重名与非法引用。
func TestPlanCRUD(t *testing.T) {
	s := newTestStore(t)
	n1 := newTestNode(t, s, "n1")
	n2 := newTestNode(t, s, "n2")
	in1 := newTestInbound(t, s, n1.ID, "in1")
	in2 := newTestInbound(t, s, n2.ID, "in2")

	p := &model.Plan{
		Name:       " 全球 ",
		Note:       " 备注 ",
		NodeIDs:    []int64{n2.ID, n1.ID, n1.ID},
		InboundIDs: []int64{in2.ID},
	}
	if err := s.CreatePlan(p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("CreatePlan 没有回填 ID")
	}
	got, err := s.GetPlan(p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Name != "全球" || got.Note != "备注" {
		t.Fatalf("名字/备注没规范化: %+v", got)
	}
	if !sameInt64s(got.NodeIDs, []int64{n1.ID, n2.ID}) {
		t.Fatalf("节点 id 没去重排序: %v", got.NodeIDs)
	}
	if !sameInt64s(got.InboundIDs, []int64{in2.ID}) {
		t.Fatalf("入站 id 不对: %v", got.InboundIDs)
	}
	if got.IsDefault || got.Users != 0 {
		t.Fatalf("普通套餐不该是默认、用户数应为 0: %+v", got)
	}

	// 重名。
	if err := s.CreatePlan(&model.Plan{Name: "全球"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重名应报 ErrDuplicate,实际 %v", err)
	}
	// 引用不存在的节点/入站。
	if err := s.CreatePlan(&model.Plan{Name: "坏", NodeIDs: []int64{99999}}); err == nil {
		t.Fatal("引用不存在的节点应当报错")
	}
	if err := s.CreatePlan(&model.Plan{Name: "坏2", InboundIDs: []int64{99999}}); err == nil {
		t.Fatal("引用不存在的入站应当报错")
	}

	// 更新:换备注、清空节点、换入站。
	got.Note = "新备注"
	got.NodeIDs = nil
	got.InboundIDs = []int64{in1.ID}
	if err := s.UpdatePlan(got); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	got2, err := s.GetPlan(p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got2.Note != "新备注" || len(got2.NodeIDs) != 0 || !sameInt64s(got2.InboundIDs, []int64{in1.ID}) {
		t.Fatalf("更新没生效: %+v", got2)
	}

	if err := s.DeletePlan(p.ID); err != nil {
		t.Fatalf("DeletePlan: %v", err)
	}
	if _, err := s.GetPlan(p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 GetPlan 应报 ErrNotFound,实际 %v", err)
	}
}
