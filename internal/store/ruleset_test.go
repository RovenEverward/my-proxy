package store

import (
	"errors"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 快照表的写入/读取与 becameReady 语义。
func TestRulesetSnapshotLifecycle(t *testing.T) {
	s := newTestStore(t)

	snaps, err := s.ListRulesetSnapshots()
	if err != nil || len(snaps) != 0 {
		t.Fatalf("空库应没快照: %v %v", snaps, err)
	}
	if _, _, err := s.RulesetSnapshotBody("clash/openai"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没拉过的文件应 ErrNotFound: %v", err)
	}

	at := time.UnixMilli(1700000000000)
	ready, err := s.CompleteRulesetRefresh("clash/openai", "https://up/a.yaml", at,
		[]byte("payload: 1"), "text/yaml", "")
	if err != nil || !ready {
		t.Fatalf("第一次成功应 becameReady: ready=%v err=%v", ready, err)
	}
	body, ct, err := s.RulesetSnapshotBody("clash/openai")
	if err != nil || string(body) != "payload: 1" || ct != "text/yaml" {
		t.Fatalf("读出正文: %q %q %v", body, ct, err)
	}
	snap := mustSnap(t, s, "clash/openai")
	if snap.FetchedAt != at.UnixMilli() || snap.LastAttemptAt != at.UnixMilli() || snap.LastError != "" {
		t.Fatalf("快照元数据不对: %+v", snap)
	}
	if snap.URL != "https://up/a.yaml" {
		t.Fatalf("快照 url 不对: %q", snap.URL)
	}

	// 第二次成功:不再是「首次就绪」,正文照样换。
	later := at.Add(time.Hour)
	ready, err = s.CompleteRulesetRefresh("clash/openai", "https://up/a.yaml", later,
		[]byte("payload: 2"), "text/yaml; charset=utf-8", "")
	if err != nil || ready {
		t.Fatalf("已有 body 的成功不该报 becameReady: ready=%v err=%v", ready, err)
	}
	body, ct, _ = s.RulesetSnapshotBody("clash/openai")
	if string(body) != "payload: 2" || ct != "text/yaml; charset=utf-8" {
		t.Fatalf("成功应换正文: %q %q", body, ct)
	}

	// 失败保留旧正文与 fetched_at,只动 attempt/error。
	failAt := later.Add(time.Hour)
	ready, err = s.CompleteRulesetRefresh("clash/openai", "https://up/a.yaml", failAt,
		nil, "", "上游返回 HTTP 502")
	if err != nil || ready {
		t.Fatalf("失败不该报 becameReady: ready=%v err=%v", ready, err)
	}
	body, _, err = s.RulesetSnapshotBody("clash/openai")
	if err != nil || string(body) != "payload: 2" {
		t.Fatalf("失败应保留旧正文: %q %v", body, err)
	}
	snap = mustSnap(t, s, "clash/openai")
	if snap.LastError != "上游返回 HTTP 502" || snap.LastAttemptAt != failAt.UnixMilli() ||
		snap.FetchedAt != later.UnixMilli() {
		t.Fatalf("失败后元数据不对: %+v", snap)
	}

	// 从未成功过的文件:行存在(记了错误),但 Body 仍 404。
	ready, err = s.CompleteRulesetRefresh("singbox/geoip-cn", "https://up/geoip-cn.srs", at,
		nil, "", "拉取超时")
	if err != nil || ready {
		t.Fatalf("新 key 失败: ready=%v err=%v", ready, err)
	}
	snap = mustSnap(t, s, "singbox/geoip-cn")
	if snap.FetchedAt != 0 || snap.LastError != "拉取超时" {
		t.Fatalf("失败快照元数据不对: %+v", snap)
	}
	if _, _, err := s.RulesetSnapshotBody("singbox/geoip-cn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没正文的快照应 ErrNotFound: %v", err)
	}
}

func mustSnap(t *testing.T, s *Store, key string) *RulesetSnapshot {
	t.Helper()
	snaps, err := s.ListRulesetSnapshots()
	if err != nil {
		t.Fatalf("列快照: %v", err)
	}
	snap := snaps[key]
	if snap == nil {
		t.Fatalf("快照 %q 不存在", key)
	}
	return snap
}

// ListEnabledRules 只要启用中的、跨作用域都算。
func TestListEnabledRules(t *testing.T) {
	s := newTestStore(t)
	if rules, err := s.ListEnabledRules(); err != nil || len(rules) != 0 {
		t.Fatalf("空库应没规则: %v %v", rules, err)
	}
	_, err := s.ReplaceRules(0, []model.RouteRule{
		{Type: model.RuleSetRef, Value: "ai", Target: model.TargetReject, Enabled: true},
		{Type: model.RuleSuffix, Value: "x.com", Target: model.TargetDirect, Enabled: false},
		{Type: model.RuleGeoIP, Value: "CN", Target: model.TargetDirect, Enabled: true},
	}, -1)
	if err != nil {
		t.Fatalf("种规则: %v", err)
	}
	rules, err := s.ListEnabledRules()
	if err != nil {
		t.Fatalf("列启用规则: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("应只有 2 条启用规则: %v", rules)
	}
	seen := map[model.RuleType]bool{}
	for _, r := range rules {
		if !r.Enabled {
			t.Fatalf("停用的不该回来: %+v", r)
		}
		seen[r.Type] = true
	}
	if !seen[model.RuleSetRef] || !seen[model.RuleGeoIP] {
		t.Fatalf("类型不全: %v", rules)
	}
}
