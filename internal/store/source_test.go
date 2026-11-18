package store

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestSourceSnapshotAndSeparateRelationships(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := &model.SubscriptionSource{Name: "external", URL: "https://example.test/sub/secret", Enabled: true, IntervalSeconds: 21600}
	if err := st.CreateSource(src); err != nil {
		t.Fatal(err)
	}
	u := &model.User{Username: "alice", Enabled: true}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSourceGrant(src.ID, "alice", true); err != nil {
		t.Fatal(err)
	}
	g := &model.ProxyGroup{Name: "mixed"}
	if err := st.CreateProxyGroup(g); err != nil {
		t.Fatal(err)
	}
	filter := []string{"source:1"}
	if err := st.SetGroupSources(g.ID, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateProxyGroup(g); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ListSourcesForUser(u.ID); err != nil || len(got) != 1 {
		t.Fatalf("grant lost: %v %v", got, err)
	}
	snapshot := []byte(`[{"Remark":"node"}]`)
	now := time.Now()
	if err := st.CompleteSourceRefresh(src.ID, src.Revision, now, snapshot, []byte(`{"format":"links"}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteSourceRefresh(src.ID, src.Revision, now, nil, []byte(`{}`), "timeout"); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current.Snapshot, snapshot) || current.LastError != "timeout" || current.LastGoodAt == nil {
		t.Fatalf("last good lost: %#v", current)
	}
	current.URL = "https://example.test/new"
	if err := st.UpdateSource(current); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteSourceRefresh(src.ID, src.Revision, now, []byte(`[]`), nil, ""); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale result accepted: %v", err)
	}
	if err := st.DeleteSource(src.ID); err != nil {
		t.Fatal(err)
	}
	filters, err := st.GetGroupSources(g.ID)
	if err != nil || len(filters) != 1 || filters[0] != "source:1" {
		t.Fatalf("deleted source widened group: %v %v", filters, err)
	}
	if err := st.CompleteSourceRefresh(src.ID, current.Revision, now, snapshot, nil, ""); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("deleted source resurrected: %v", err)
	}
}

func TestSourceGrantAllVisibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := &model.SubscriptionSource{Name: "external", URL: "https://example.test/sub/secret", Enabled: true, IntervalSeconds: 21600}
	if err := st.CreateSource(src); err != nil {
		t.Fatal(err)
	}
	if src.GrantAll {
		t.Fatal("新来源默认开了全员授权")
	}
	alice := &model.User{Username: "alice", Enabled: true}
	if err := st.CreateUser(alice); err != nil {
		t.Fatal(err)
	}
	bob := &model.User{Username: "bob", Enabled: true}
	if err := st.CreateUser(bob); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSourceGrant(src.ID, "alice", true); err != nil {
		t.Fatal(err)
	}
	assertVisible := func(username string, id int64, want int) {
		t.Helper()
		got, err := st.ListSourcesForUser(id)
		if err != nil {
			t.Fatalf("%s: %v", username, err)
		}
		if len(got) != want {
			t.Fatalf("%s: want %d sources, got %d", username, want, len(got))
		}
	}
	assertVisible("alice", alice.ID, 1)
	assertVisible("bob", bob.ID, 0)

	// 打开全员授权:bob 立刻可见,alice 的按人授权保留。
	src.GrantAll = true
	current, err := st.GetSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.GrantAll = true
	if err := st.UpdateSource(current); err != nil {
		t.Fatal(err)
	}
	assertVisible("alice", alice.ID, 1)
	assertVisible("bob", bob.ID, 1)

	// 新建用户不用单独授权也可见。
	carol := &model.User{Username: "carol", Enabled: true}
	if err := st.CreateUser(carol); err != nil {
		t.Fatal(err)
	}
	assertVisible("carol", carol.ID, 1)

	// 关掉开关:回到按人授权,bob 和 carol 不可见,alice 的授权仍在。
	current.GrantAll = false
	if err := st.UpdateSource(current); err != nil {
		t.Fatal(err)
	}
	assertVisible("alice", alice.ID, 1)
	assertVisible("bob", bob.ID, 0)
	assertVisible("carol", carol.ID, 0)

	// grant_all 随往返读出来,不影响旧库缺列时的零值。
	after, err := st.GetSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.GrantAll {
		t.Fatal("关闭后 grant_all 仍为真")
	}
}

func TestSourcePlanGrantsVisibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := &model.SubscriptionSource{Name: "external", URL: "https://example.test/sub/secret", Enabled: true, IntervalSeconds: 21600}
	if err := st.CreateSource(src); err != nil {
		t.Fatal(err)
	}
	vip := &model.Plan{Name: "VIP"}
	if err := st.CreatePlan(vip); err != nil {
		t.Fatal(err)
	}
	def, err := st.DefaultPlanID()
	if err != nil {
		t.Fatal(err)
	}
	// alice 挂 VIP,bob 留在默认套餐。
	alice := &model.User{Username: "alice", Enabled: true, PlanID: &vip.ID}
	if err := st.CreateUser(alice); err != nil {
		t.Fatal(err)
	}
	bob := &model.User{Username: "bob", Enabled: true, PlanID: &def}
	if err := st.CreateUser(bob); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSourcePlanGrant(src.ID, "VIP", true); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListSourcesForUser(alice.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("alice: %v %v", got, err)
	}
	got, err = st.ListSourcesForUser(bob.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("bob: %v %v", got, err)
	}
	// 套餐授权名单可读。
	plans, err := st.SourcePlanGrants(src.ID)
	if err != nil || len(plans) != 1 || plans[0].Name != "VIP" {
		t.Fatalf("plan grants: %v %v", plans, err)
	}
	// 撤回后 alice 也看不到。
	if err := st.SetSourcePlanGrant(src.ID, "VIP", false); err != nil {
		t.Fatal(err)
	}
	got, err = st.ListSourcesForUser(alice.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("alice after revoke: %v %v", got, err)
	}
	// 整组替换:给默认套餐授权,bob 可见;坏 id 不落事务。
	if err := st.ReplaceSourcePlanGrants(src.ID, []int64{def}); err != nil {
		t.Fatal(err)
	}
	got, err = st.ListSourcesForUser(bob.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("bob after replace: %v %v", got, err)
	}
	if err := st.ReplaceSourcePlanGrants(src.ID, []int64{99999}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad plan id: %v", err)
	}
	plans, err = st.SourcePlanGrants(src.ID)
	if err != nil || len(plans) != 1 {
		t.Fatalf("replace rolled back badly: %v %v", plans, err)
	}
	// 删套餐级联清授权(不复活):用一个没挂用户的套餐测。
	temp := &model.Plan{Name: "temp"}
	if err := st.CreatePlan(temp); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSourcePlanGrant(src.ID, "temp", true); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePlan(temp.ID); err != nil {
		t.Fatal(err)
	}
	plans, err = st.SourcePlanGrants(src.ID)
	if err != nil || len(plans) != 1 {
		t.Fatalf("deleted plan grant leaked: %v %v", plans, err)
	}
}
