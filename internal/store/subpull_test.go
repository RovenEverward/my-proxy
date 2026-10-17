package store

import (
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// TestSubPullRecordAndList 覆盖插入、更新与排序:第一次写 first_at,之后只改
// last_at / format / client。
func TestSubPullRecordAndList(t *testing.T) {
	s := newTestStore(t)
	alice := newTestUser(t, s, "alice")
	bob := newTestUser(t, s, "bob")

	t1 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := s.RecordSubPull(alice.ID, "links", "v2rayng", t1); err != nil {
		t.Fatalf("RecordSubPull: %v", err)
	}
	t2 := t1.Add(10 * time.Minute)
	if err := s.RecordSubPull(alice.ID, "clash-meta", "clash-verge", t2); err != nil {
		t.Fatalf("RecordSubPull 更新: %v", err)
	}
	// bob 后插,但列表按用户名排序,alice 应在前。
	if err := s.RecordSubPull(bob.ID, "sing-box", "sing-box", t1); err != nil {
		t.Fatalf("RecordSubPull bob: %v", err)
	}

	got, err := s.ListSubPulls()
	if err != nil {
		t.Fatalf("ListSubPulls: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("记录数 = %d, want 2", len(got))
	}
	want := model.SubPull{Username: "alice", FirstPulledAt: t1, LastPulledAt: t2, Format: "clash-meta", Client: "clash-verge"}
	if got[0] != want {
		t.Fatalf("alice 记录 = %+v, want %+v", got[0], want)
	}
	if got[1].Username != "bob" || got[1].FirstPulledAt != t1 || got[1].LastPulledAt != t1 {
		t.Fatalf("bob 记录 = %+v", got[1])
	}

	if err := s.ClearSubPull(alice.ID); err != nil {
		t.Fatalf("ClearSubPull: %v", err)
	}
	got, err = s.ListSubPulls()
	if err != nil {
		t.Fatalf("ListSubPulls 清空后: %v", err)
	}
	if len(got) != 1 || got[0].Username != "bob" {
		t.Fatalf("清掉 alice 后 = %+v", got)
	}
}

// TestSubPullCascadeOnUserDelete 断言删用户时记录随外键级联删除。
func TestSubPullCascadeOnUserDelete(t *testing.T) {
	s := newTestStore(t)
	u := newTestUser(t, s, "alice")
	now := time.Now()
	if err := s.RecordSubPull(u.ID, "links", "v2rayng", now); err != nil {
		t.Fatalf("RecordSubPull: %v", err)
	}
	if err := s.DeleteUser("alice"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	got, err := s.ListSubPulls()
	if err != nil {
		t.Fatalf("ListSubPulls: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("删用户后仍剩记录: %+v", got)
	}
}
