package store

import (
	"errors"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestRoutingRulesAndGroups(t *testing.T) {
	s := newTestStore(t)
	if st, err := s.GetRoutingState(); err != nil || st.Mode != model.RoutingTemplate {
		t.Fatalf("新库应是 template 模式: %+v %v", st, err)
	}
	g := &model.ProxyGroup{Name: "自动选择"}
	if err := s.CreateProxyGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProxyGroup(&model.ProxyGroup{Name: "自动选择"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重名应报 ErrDuplicate: %v", err)
	}
	auto := model.GroupTarget(g.ID)
	rules := []model.RouteRule{
		{Type: model.RuleSetRef, Value: "ads", Target: model.TargetReject, Enabled: true},
		{Type: model.RuleSuffix, Value: "Linux.Do", Target: auto, Enabled: true},
	}
	v, err := s.ReplaceRules(0, rules, 0)
	if err != nil || v != 1 {
		t.Fatalf("替换: v=%d err=%v", v, err)
	}
	if _, err := s.ReplaceRules(0, rules, 0); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("旧版本应冲突: %v", err)
	}
	got, _ := s.ListRules(0)
	if len(got) != 2 || got[1].Value != "linux.do" || got[1].Position != 1 {
		t.Fatalf("规则不对: %+v", got)
	}
	if _, err := s.ReplaceRules(0, []model.RouteRule{{Type: model.RuleSuffix, Value: "a.com", Target: "group:999"}}, -1); !errors.Is(err, ErrUnknownGroup) {
		t.Fatalf("不存在的组应拒绝: %v", err)
	}
	if v, err := s.SetRouteFinal(0, auto); err != nil || v != 2 {
		t.Fatalf("兜底: %d %v", v, err)
	}
	r := got[0]
	r.Enabled = false
	if err := s.UpdateRule(&r); err != nil {
		t.Fatal(err)
	}
	if one, _ := s.GetRule(r.ID); one.Enabled || one.Position != 0 {
		t.Fatalf("单条更新: %+v", one)
	}

	u := newTestUser(t, s, "alice")
	if _, err := s.ReplaceRules(u.ID, []model.RouteRule{{Type: model.RuleSuffix, Value: "bilibili.com", Target: auto, Enabled: true}}, -1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRouteFinal(u.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceRules(424242, nil, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的用户: %v", err)
	}
	var inUse *GroupInUseError
	if err := s.DeleteProxyGroup(g.ID); !errors.As(err, &inUse) || len(inUse.Refs) != 3 {
		t.Fatalf("被引用的组不能删: %v", err)
	}
	if err := s.DeleteUser(u.Username); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.ListRules(u.ID); len(left) != 0 {
		t.Fatal("删用户后其规则应级联删除")
	}
	if sc, _ := s.GetRouteScope(u.ID); sc.Version != 0 {
		t.Fatal("删用户后其作用域应删除")
	}
	if err := s.SaveRoutingState(&RoutingState{Mode: model.RoutingRules, Report: "ok"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.GetRoutingState(); st.Mode != model.RoutingRules || st.Report != "ok" {
		t.Fatalf("状态: %+v", st)
	}
}
