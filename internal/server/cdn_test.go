package server

import (
	"net/http"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func enableCDNOptimize(t *testing.T, e *env) {
	t.Helper()
	st, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("读设置: %v", err)
	}
	st.CDNOptimize = true
	if err := e.st.SaveSettings(st); err != nil {
		t.Fatalf("开 cdn_optimize: %v", err)
	}
}

func cdnResults(e *env, results []model.CDNResult) (int, []byte) {
	return e.api("POST", "/api/cdn-optimize/results", map[string]any{"results": results})
}

func cdnTargets(t *testing.T, e *env) map[string]any {
	t.Helper()
	code, data := e.api("GET", "/api/cdn-optimize/targets", nil)
	if code != http.StatusOK {
		t.Fatalf("领 CDN 名单: got %d %s", code, data)
	}
	return decodeJSON[map[string]any](t, data)
}

func anyStrings(t *testing.T, v map[string]any, key string) []string {
	t.Helper()
	raw, ok := v[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		out = append(out, x.(string))
	}
	return out
}

// AC-04:开关关着发空名单;开关开着发名单即记轮次,节流窗口内再领返回
// 空名单 + retry_after_s —— 探测端不回存的空轮也占配额。
func TestCDNTargetsShapeAndThrottle(t *testing.T) {
	e := newEnv(t)

	// 关着:空名单。
	v := cdnTargets(t, e)
	if ips := anyStrings(t, v, "ips"); len(ips) != 0 {
		t.Fatalf("开关关闭应发空名单,实际 %d 个", len(ips))
	}

	enableCDNOptimize(t, e)
	v = cdnTargets(t, e)
	ips := anyStrings(t, v, "ips")
	recheck := anyStrings(t, v, "recheck")
	scan := anyStrings(t, v, "scan")
	if len(scan) != 512 {
		t.Errorf("scan 应抽 512 个新样本,实际 %d", len(scan))
	}
	if len(recheck) != 0 {
		t.Errorf("首轮池空,recheck 应为空,实际 %d", len(recheck))
	}
	if len(ips) != len(recheck)+len(scan) {
		t.Errorf("ips 应为 recheck+scan 合集,实际 %d != %d+%d", len(ips), len(recheck), len(scan))
	}
	// 发名单即记节流:再领是空名单 + retry_after_s。
	v = cdnTargets(t, e)
	if ips := anyStrings(t, v, "ips"); len(ips) != 0 {
		t.Errorf("节流窗口内应发空名单,实际 %d 个", len(ips))
	}
	if _, ok := v["retry_after_s"]; !ok {
		t.Error("节流名单应带 retry_after_s")
	}
}

// AC-01/02/05:整轮语义 —— 通过的挂 Host,复检失败的 miss+1 且立刻摘下入站,
// 未通过的新 IP 不入池,旧探测端(只报通过项)缺席也计 miss。
func TestCDNResultsPoolAndHosts(t *testing.T) {
	e := newEnv(t)
	enableCDNOptimize(t, e)

	node := e.createNode("la-1", "203.0.113.7")
	in := e.createInbound(node.ID, "cdn-in", 443)
	e.createHost(in.ID, "洛杉矶 A · CDN", "cdn.example.com")

	hostAddrs := func() map[string]string {
		code, data := e.api("GET", pathf("/api/inbounds/%d/hosts", in.ID), nil)
		if code != http.StatusOK {
			t.Fatalf("列 Host: got %d %s", code, data)
		}
		hosts := decodeJSON[[]model.Host](t, data)
		out := map[string]string{}
		for _, h := range hosts {
			out[h.Address] = h.Kind
		}
		return out
	}

	// 第一轮:a、b、c 通过,挂成优选 Host(最多 3 个)。
	code, _ := cdnResults(e, []model.CDNResult{
		{IP: "1.1.1.1", Reachable: true, LatencyMs: 90, Edge: true, Colo: "SJC"},
		{IP: "2.2.2.2", Reachable: true, LatencyMs: 50, Edge: true, Colo: "LAX"},
		{IP: "3.3.3.3", Reachable: true, LatencyMs: 70, Edge: true, Colo: "NRT"},
		{IP: "4.4.4.4", Reachable: true, LatencyMs: 30, Edge: true, Colo: "FRA"},
	})
	if code != http.StatusOK {
		t.Fatalf("回存第一轮: got %d", code)
	}
	addrs := hostAddrs()
	if addrs["cdn.example.com"] != "" {
		t.Error("手工 Host 应保留")
	}
	for _, ip := range []string{"4.4.4.4", "2.2.2.2", "3.3.3.3"} {
		if addrs[ip] != "cdn" {
			t.Errorf("延迟前 3 的 %s 应挂优选 Host,实际名单 %v", ip, addrs)
		}
	}
	if _, ok := addrs["1.1.1.1"]; ok {
		t.Error("延迟第 4 的 a 不应挂 Host")
	}

	// 第二轮:b 复检失败(miss=1、延迟不再可信)—— 立刻摘下入站;
	// a 递补进前 3;失败的新 IP d 不入池。
	code, _ = cdnResults(e, []model.CDNResult{
		{IP: "4.4.4.4", Reachable: true, LatencyMs: 30, Edge: true, Colo: "FRA"},
		{IP: "2.2.2.2", Reachable: false},
		{IP: "3.3.3.3", Reachable: true, LatencyMs: 70, Edge: true, Colo: "NRT"},
		{IP: "1.1.1.1", Reachable: true, LatencyMs: 90, Edge: true, Colo: "SJC"},
		{IP: "5.5.5.5", Reachable: false},
	})
	if code != http.StatusOK {
		t.Fatalf("回存第二轮: got %d", code)
	}
	pool, err := e.st.ListCDNPool()
	if err != nil {
		t.Fatalf("读池: %v", err)
	}
	var miss2 int
	for _, p := range pool {
		if p.IP == "2.2.2.2" {
			miss2 = p.Misses
		}
		if p.IP == "5.5.5.5" {
			t.Error("edge=false 的新 IP 不应入池")
		}
	}
	if miss2 != 1 {
		t.Errorf("b 复检失败应 miss=1,实际 %d", miss2)
	}
	addrs = hostAddrs()
	if _, ok := addrs["2.2.2.2"]; ok {
		t.Error("失效的 b 应立刻摘下入站")
	}
	if addrs["1.1.1.1"] != "cdn" {
		t.Error("a 应递补进前 3")
	}

	// 5a:下一轮全员失败 —— 保留旧入口不作空。
	code, _ = cdnResults(e, []model.CDNResult{
		{IP: "4.4.4.4", Reachable: false},
		{IP: "3.3.3.3", Reachable: false},
		{IP: "1.1.1.1", Reachable: false},
	})
	if code != http.StatusOK {
		t.Fatalf("回存第三轮: got %d", code)
	}
	addrs = hostAddrs()
	if addrs["4.4.4.4"] != "cdn" || addrs["3.3.3.3"] != "cdn" || addrs["1.1.1.1"] != "cdn" {
		t.Errorf("当轮零可用应保留旧优选入口,实际 %v", addrs)
	}
}

// 旧探测端只报通过项:缺席成员照常计 miss(兼容兜底)。
func TestCDNResultsAbsentMemberCountsMiss(t *testing.T) {
	e := newEnv(t)
	enableCDNOptimize(t, e)

	if code, _ := cdnResults(e, []model.CDNResult{
		{IP: "1.1.1.1", Reachable: true, LatencyMs: 90, Edge: true},
		{IP: "2.2.2.2", Reachable: true, LatencyMs: 50, Edge: true},
	}); code != http.StatusOK {
		t.Fatalf("首轮回存: got %d", code)
	}
	if code, _ := cdnResults(e, []model.CDNResult{
		{IP: "1.1.1.1", Reachable: true, LatencyMs: 90, Edge: true},
	}); code != http.StatusOK {
		t.Fatalf("次轮回存: got %d", code)
	}
	pool, err := e.st.ListCDNPool()
	if err != nil {
		t.Fatalf("读池: %v", err)
	}
	for _, p := range pool {
		if p.IP == "2.2.2.2" && p.Misses != 1 {
			t.Errorf("缺席成员应 miss=1,实际 %d", p.Misses)
		}
	}
}

// UC-31:选优按速度降序;丢包超阈的即使边缘真也不上入口。
func TestCDNHostsPreferSpeedAndLossGate(t *testing.T) {
	e := newEnv(t)
	enableCDNOptimize(t, e)

	node := e.createNode("la-1", "203.0.113.7")
	in := e.createInbound(node.ID, "cdn-in", 443)
	e.createHost(in.ID, "洛杉矶 A · CDN", "cdn.example.com")

	code, _ := cdnResults(e, []model.CDNResult{
		{IP: "1.1.1.1", Reachable: true, LatencyMs: 20, Edge: true, Colo: "FRA", Loss: 0, SpeedMbps: 5},
		{IP: "2.2.2.2", Reachable: true, LatencyMs: 90, Edge: true, Colo: "SJC", Loss: 0, SpeedMbps: 50},
		{IP: "3.3.3.3", Reachable: true, LatencyMs: 60, Edge: true, Colo: "NRT", Loss: 0, SpeedMbps: 30},
		{IP: "4.4.4.4", Reachable: true, LatencyMs: 40, Edge: true, Colo: "LAX", Loss: 0, SpeedMbps: 10},
		// 速度最快但丢包 50%:不该挂入口。
		{IP: "9.9.9.9", Reachable: true, LatencyMs: 10, Edge: true, Colo: "HKG", Loss: 0.5, SpeedMbps: 99},
	})
	if code != http.StatusOK {
		t.Fatalf("回存: got %d", code)
	}
	code, data := e.api("GET", pathf("/api/inbounds/%d/hosts", in.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("列 Host: got %d %s", code, data)
	}
	got := map[string]bool{}
	for _, h := range decodeJSON[[]model.Host](t, data) {
		if h.Kind == "cdn" {
			got[h.Address] = true
		}
	}
	for _, want := range []string{"2.2.2.2", "3.3.3.3", "4.4.4.4"} {
		if !got[want] {
			t.Errorf("速度前 3 的 %s 应挂优选 Host,实际 %v", want, got)
		}
	}
	if got["1.1.1.1"] {
		t.Error("延迟最低但速度第 4 的不该挂")
	}
	if got["9.9.9.9"] {
		t.Error("丢包 50% 不该挂入口")
	}
}
