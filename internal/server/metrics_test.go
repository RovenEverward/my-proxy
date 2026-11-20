package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/reach"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// UC-20 的集成测试:样本追加 → 聚合 → 带宽存档 → 端点下发 → 孤儿清理。

// postSourceCheck 以一个来源身份报一次境内活性结果(summary 由控制面现算)。
func postDomesticCheck(t *testing.T, e *env, key string, res map[string]*reach.Result, at time.Time) {
	t.Helper()
	code, body := e.api("POST", "/api/reach/results", map[string]any{
		"checked_at": at.UTC(), "scope": "domestic",
		"subjects": []map[string]any{{"subject_key": key, "results": res}},
	})
	if code != http.StatusOK {
		t.Fatalf("回写境内结果: %d %s", code, body)
	}
}

func TestMetricsAggregatesStability(t *testing.T) {
	e := newEnv(t)
	n, in, _ := mkReachFixture(t, e)
	key := model.NodeSubjectKey(n.ID)
	tid := "inbound:" + strconv.FormatInt(in.ID, 10)

	// 报 4 轮:3 通 1 不通,时延 40/50/60 → 成功率 .75,中位 50。
	now := time.Now()
	for i, res := range []*reach.Result{okResult(), okResult(), failResult(), okResult()} {
		if i == 1 {
			res.TCP.LatencyMs, res.UDP.LatencyMs = 50, 50
		}
		postDomesticCheck(t, e, string(key), map[string]*reach.Result{
			tid: res,
		}, now.Add(time.Duration(i)*time.Hour))
	}

	code, body := e.api("GET", "/api/metrics", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/metrics: %d %s", code, body)
	}
	var resp struct {
		Subjects []model.SubjectMetrics `json:"subjects"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	var m *model.SubjectMetrics
	for i := range resp.Subjects {
		if resp.Subjects[i].SubjectKey == key {
			m = &resp.Subjects[i]
		}
	}
	if m == nil {
		t.Fatalf("对象 %s 不在指标里", key)
	}
	if m.Stability.Rounds != 4 || m.Stability.Insufficient {
		t.Fatalf("轮数/不足判错: %+v", m.Stability)
	}
	if got := m.Stability.SuccessRate; got < 0.74 || got > 0.76 {
		t.Fatalf("成功率 %v,想要 .75", got)
	}
	if m.Bandwidth.Status != "untested" {
		t.Fatalf("没测过应是 untested: %+v", m.Bandwidth)
	}
}

func TestMetricsInsufficientBelowThreeRounds(t *testing.T) {
	e := newEnv(t)
	n, in, _ := mkReachFixture(t, e)
	key := model.NodeSubjectKey(n.ID)
	tid := "inbound:" + strconv.FormatInt(in.ID, 10)
	postDomesticCheck(t, e, string(key), map[string]*reach.Result{tid: okResult()}, time.Now())

	code, body := e.api("GET", "/api/metrics", nil)
	if code != http.StatusOK {
		t.Fatalf("%d", code)
	}
	var resp struct {
		Subjects []model.SubjectMetrics `json:"subjects"`
	}
	_ = json.Unmarshal(body, &resp)
	for _, m := range resp.Subjects {
		if m.SubjectKey == key {
			if !m.Stability.Insufficient || m.Stability.Rounds != 1 {
				t.Fatalf("1 轮样本应标不足: %+v", m.Stability)
			}
			return
		}
	}
	t.Fatalf("对象不在指标里")
}

func TestBandwidthResultRoundTrip(t *testing.T) {
	e := newEnv(t)
	n, _, _ := mkReachFixture(t, e)
	key := model.NodeSubjectKey(n.ID)

	// 1) 端点下发:node 对象应给出含凭据的端点。
	code, body := e.api("GET", "/api/bandwidth/endpoint?subject="+string(key), nil)
	if code != http.StatusOK {
		t.Fatalf("下发端点: %d %s", code, body)
	}
	var ep struct {
		Endpoints []render.EndpointConfig `json:"endpoints"`
	}
	if err := json.Unmarshal(body, &ep); err != nil {
		t.Fatal(err)
	}
	if len(ep.Endpoints) != 1 || ep.Endpoints[0].Protocol != "vless" || ep.Endpoints[0].UUID == "" {
		t.Fatalf("端点形状不对: %+v", ep.Endpoints)
	}
	// 测量用户被自动建了:可见、enabled、能进订阅口径。
	users, _ := e.st.ListUsers()
	var probe *model.User
	for i := range users {
		if users[i].Username == "mpx-probe" {
			probe = &users[i]
		}
	}
	if probe == nil || !probe.Enabled {
		t.Fatalf("测量用户没建或停用: %+v", probe)
	}

	// 2) 回写 ok → 展示值。
	post := map[string]any{"subject_key": string(key), "status": "ok", "bytes_per_sec": 25_000_000, "duration_ms": 5000, "bytes": 15_000_000, "test_url": "https://t/x"}
	code, body = e.api("POST", "/api/bandwidth/results", post)
	if code != http.StatusCreated {
		t.Fatalf("回写: %d %s", code, body)
	}
	// 3) 回写 failed → 状态是 failed,但接口视图仍展示旧 ok 值(读路径取最近 ok)。
	post["status"] = "failed"
	post["error"] = "dial timeout"
	delete(post, "bytes_per_sec")
	code, body = e.api("POST", "/api/bandwidth/results", post)
	if code != http.StatusCreated {
		t.Fatalf("回写失败样本: %d %s", code, body)
	}

	code, body = e.api("GET", "/api/metrics", nil)
	_ = code
	var resp struct {
		Subjects []model.SubjectMetrics `json:"subjects"`
	}
	_ = json.Unmarshal(body, &resp)
	for _, m := range resp.Subjects {
		if m.SubjectKey == key {
			if m.Bandwidth.Status != "failed" {
				t.Fatalf("最近状态应是 failed: %+v", m.Bandwidth)
			}
			if m.Bandwidth.BytesPerSec != 25_000_000 {
				t.Fatalf("失败不应清掉旧值: %+v", m.Bandwidth)
			}
			return
		}
	}
	t.Fatalf("对象不在指标里")
}

func TestBandwidthEndpointUnknownSubject(t *testing.T) {
	e := newEnv(t)
	code, _ := e.api("GET", "/api/bandwidth/endpoint?subject=node:999", nil)
	if code != http.StatusNotFound {
		t.Fatalf("不存在对象应 404,得 %d", code)
	}
	code, _ = e.api("GET", "/api/bandwidth/endpoint", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("缺 subject 应 400,得 %d", code)
	}
}

func TestBandwidthEndpointExternalUsesSnapshot(t *testing.T) {
	e := newEnv(t)
	ep := render.Endpoint{Protocol: "trojan", Address: "ext.example.com", Port: 443,
		Password: "ext-pw", SourceKey: "abc", SNI: "cdn.example.com"}
	src := addSourceWithSnapshot(t, e, []render.Endpoint{ep})
	ep.SourceID = src.ID
	key := string(ep.Key())

	code, body := e.api("GET", "/api/bandwidth/endpoint?subject="+key, nil)
	if code != http.StatusOK {
		t.Fatalf("外部端点下发: %d %s", code, body)
	}
	var resp struct {
		Endpoints []render.EndpointConfig `json:"endpoints"`
	}
	_ = json.Unmarshal(body, &resp)
	if len(resp.Endpoints) != 1 || resp.Endpoints[0].Password != "ext-pw" {
		t.Fatalf("外部端点凭据不对: %+v", resp.Endpoints)
	}
}

func TestPruneMetricsDropsOrphanSubject(t *testing.T) {
	e := newEnv(t)
	n, in, _ := mkReachFixture(t, e)
	key := model.NodeSubjectKey(n.ID)
	tid := "inbound:" + strconv.FormatInt(in.ID, 10)
	postDomesticCheck(t, e, string(key), map[string]*reach.Result{tid: okResult()}, time.Now())
	_, _ = e.api("POST", "/api/bandwidth/results", map[string]any{"subject_key": string(key), "status": "ok", "bytes_per_sec": 1e6})

	// 删节点 → 名册比对把样本清掉。
	code, _ := e.api("DELETE", "/api/nodes/"+itoa(n.ID), nil)
	if code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("删节点: %d", code)
	}
	// 触发一次 targets 拉取(孤儿清理走这条路)。
	e.s.pruneOrphanMetrics()
	rows, err := e.st.ListSourceSamples(key, "", time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("孤儿样本还在: %d", len(rows))
	}
	bw, _ := e.st.LatestBandwidthViews()
	if _, ok := bw[string(key)]; ok {
		t.Fatalf("孤儿带宽样本还在")
	}
}

// UC-20:外部源整批 failed 时不等 interval,立刻加刷拉新端点。
func TestBandwidthPanicRefreshAllFailedSource(t *testing.T) {
	e := newEnv(t)
	eps := []render.Endpoint{
		{Protocol: "trojan", Address: "a.example.com", Port: 443, Password: "x", SourceKey: "k1"},
		{Protocol: "trojan", Address: "b.example.com", Port: 443, Password: "x", SourceKey: "k2"},
	}
	raw, _ := json.Marshal(eps)
	src := model.SubscriptionSource{Name: "机场", URL: "https://example.test/sub",
		Enabled: true, GrantAll: true, IntervalSeconds: 21600}
	if err := e.st.CreateSource(&src); err != nil {
		t.Fatal(err)
	}
	// last_attempt_at 摆到 30 分钟前,越过 panicRefreshMinGap。
	old := time.Now().Add(-30 * time.Minute)
	if err := e.st.CompleteSourceRefresh(src.ID, src.Revision, old, raw, nil, ""); err != nil {
		t.Fatal(err)
	}

	for i := range eps {
		eps[i].SourceID = src.ID
		key := string(eps[i].Key())
		code, _ := e.api("POST", "/api/bandwidth/results",
			map[string]any{"subject_key": key, "status": "failed", "error": "timeout"})
		if code != http.StatusCreated {
			t.Fatalf("回写 %d: %d", i, code)
		}
	}
	// 全挂 → 触发加刷。fetch 是异步网络请求,不 predict 它什么时候
	// 回来;只断言「刷新任务被启动过」。用 Running 观察。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e.s.sources.Running(src.ID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("整批 failed 应触发一次加刷,source 没进入刷新")
}

// 反向:还有活的端点时不加刷。
func TestBandwidthNoPanicRefreshWhileAlive(t *testing.T) {
	e := newEnv(t)
	eps := []render.Endpoint{
		{Protocol: "trojan", Address: "a.example.com", Port: 443, Password: "x", SourceKey: "k1"},
		{Protocol: "trojan", Address: "b.example.com", Port: 443, Password: "x", SourceKey: "k2"},
	}
	raw, _ := json.Marshal(eps)
	src := model.SubscriptionSource{Name: "机场", URL: "https://example.test/sub",
		Enabled: true, GrantAll: true, IntervalSeconds: 21600}
	if err := e.st.CreateSource(&src); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Minute)
	if err := e.st.CompleteSourceRefresh(src.ID, src.Revision, old, raw, nil, ""); err != nil {
		t.Fatal(err)
	}
	for i, st := range eps {
		eps[i].SourceID = src.ID
		status := "failed"
		if i == 1 {
			status = "ok" // 一台活
		}
		body := map[string]any{"subject_key": string(st.Key()), "status": status}
		if status == "ok" {
			body["bytes_per_sec"] = 1e6
		}
		code, _ := e.api("POST", "/api/bandwidth/results", body)
		if code != http.StatusCreated {
			t.Fatalf("回写 %d: %d", i, code)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if e.s.sources.Running(src.ID) {
		t.Fatal("还有活端点不该加刷")
	}
}

// UC-23:未测 host 进 targets,测过的(无论 ok/failed)不进。
func TestBandwidthTargetsQueueUntestedOnly(t *testing.T) {
	e := newEnv(t)
	_, in, _ := mkReachFixture(t, e)
	// 造两个入口:一个测过 ok,一个新挂着的。
	h1 := model.Host{InboundID: in.ID, Address: "1.1.1.1", Port: 443, SNI: "a"}
	h2 := model.Host{InboundID: in.ID, Address: "2.2.2.2", Port: 443, SNI: "a"}
	if err := e.st.CreateHost(&h1); err != nil {
		t.Fatal(err)
	}
	if err := e.st.CreateHost(&h2); err != nil {
		t.Fatal(err)
	}
	k1 := string(model.HostSubjectKey(h1.ID))
	code, _ := e.api("POST", "/api/bandwidth/results",
		map[string]any{"subject_key": k1, "status": "ok", "bytes_per_sec": 1e6})
	if code != http.StatusCreated {
		t.Fatalf("回写: %d", code)
	}
	code, raw := e.api("GET", "/api/bandwidth/targets", nil)
	if code != http.StatusOK {
		t.Fatalf("targets: %d %s", code, raw)
	}
	var resp struct {
		Targets []struct {
			Subject string `json:"subject"`
		} `json:"targets"`
		TestURL string `json:"test_url"`
	}
	_ = json.Unmarshal(raw, &resp)
	k2 := string(model.HostSubjectKey(h2.ID))
	var hasK1, hasK2 bool
	for _, tg := range resp.Targets {
		if tg.Subject == k1 {
			hasK1 = true
		}
		if tg.Subject == k2 {
			hasK2 = true
		}
	}
	if hasK1 {
		t.Fatal("测过的 host 不该再进队列")
	}
	if !hasK2 {
		t.Fatalf("新 host %s 应该进队列: %+v", k2, resp.Targets)
	}
	// node:* 不进队列。
	for _, tg := range resp.Targets {
		if len(tg.Subject) >= 5 && tg.Subject[:5] == "node:" {
			t.Fatal("node: 对象不该进自动队列")
		}
	}
}
