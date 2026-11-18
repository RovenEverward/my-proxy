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

// UC-12 的服务端测试覆盖三件事:新形状结果回写 → 对象判定重算、按对象剔除
// 订阅、探测点凭据的权限边界。节点探测本身(谁通谁不通)由 reach 包的单测
// 负责,这里只关心判定怎么落、怎么变成订阅里的去留。

// addSourceWithSnapshot 建一个带快照的启用来源:snapshot 由刷新管线写入,
// 测试里直接调 CompleteSourceRefresh 摆出「上次刷新成功」的样子。
func addSourceWithSnapshot(t *testing.T, e *env, eps []render.Endpoint) model.SubscriptionSource {
	t.Helper()
	raw, err := json.Marshal(eps)
	if err != nil {
		t.Fatal(err)
	}
	src := model.SubscriptionSource{Name: "机场", URL: "https://example.test/sub",
		Enabled: true, GrantAll: true}
	if err := e.st.CreateSource(&src); err != nil {
		t.Fatal(err)
	}
	if err := e.st.CompleteSourceRefresh(src.ID, src.Revision, time.Now(), raw, nil, ""); err != nil {
		t.Fatal(err)
	}
	return src
}

// mkReachFixture 摆一台带一个入站的节点与一个活跃用户的订阅。
func mkReachFixture(t *testing.T, e *env) (model.Node, model.Inbound, model.User) {
	t.Helper()
	n := model.Node{Name: "hk-1", Address: "198.51.100.9"}
	if err := e.st.CreateNode(&n); err != nil {
		t.Fatal(err)
	}
	in := model.Inbound{NodeID: n.ID, Tag: "vless-tcp", Protocol: model.ProtocolVLESS,
		Port: 443, Network: model.NetworkTCP}
	if err := e.st.CreateInbound(&in); err != nil {
		t.Fatal(err)
	}
	u := model.User{Username: "alice", Key: "k-alice", Enabled: true}
	if err := e.st.CreateUser(&u); err != nil {
		t.Fatal(err)
	}
	return n, in, u
}

// postSubjectCheck 以新形状报一个对象的一侧结果。
func postSubjectCheck(t *testing.T, e *env, key string, res map[string]*reach.Result, scope string) {
	t.Helper()
	code, body := e.api("POST", "/api/reach/results", map[string]any{
		"checked_at": time.Now().UTC(), "scope": scope,
		"subjects": []map[string]any{{"subject_key": key, "results": res}},
	})
	if code != http.StatusOK {
		t.Fatalf("回写 %s 侧结果: %d %s", scope, code, body)
	}
}

func failResult() *reach.Result {
	return &reach.Result{TCP: &reach.Cell{Status: reach.CellTimeout, Error: "timeout"},
		UDP: &reach.Cell{Status: reach.CellTimeout}}
}
func okResult() *reach.Result {
	return &reach.Result{TCP: &reach.Cell{Status: reach.CellReachable, LatencyMs: 42},
		UDP: &reach.Cell{Status: reach.CellReachable, LatencyMs: 42}}
}

// setExclude 整体替换地改剔除开关,与 CLI 的读-改-写同一条路。
func setExclude(t *testing.T, e *env, on bool) {
	t.Helper()
	code, body := e.api("GET", "/api/settings", nil)
	if code != http.StatusOK {
		t.Fatalf("读设置: %d", code)
	}
	var st model.Settings
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	st.ReachExclude = on
	code, body = e.api("PUT", "/api/settings", st)
	if code != http.StatusOK {
		t.Fatalf("写设置: %d %s", code, body)
	}
}

func TestReachTargetsListsNodeAndExt(t *testing.T) {
	e := newEnv(t)
	n, in, _ := mkReachFixture(t, e)

	src := addSourceWithSnapshot(t, e, []render.Endpoint{{Address: "ext.example.com", Port: 443,
		Protocol: model.ProtocolVLESS, SourceKey: "abc123", Remark: "快连"}})

	code, body := e.api("GET", "/api/reach/targets", nil)
	if code != http.StatusOK {
		t.Fatalf("targets: %d %s", code, body)
	}
	var rt struct {
		Interval  int             `json:"interval_minutes"`
		Targets   []reach.Target  `json:"targets"`
		Freshness map[string]any  `json:"freshness"`
		Exclude   bool            `json:"exclude_enabled"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatal(err)
	}
	if rt.Interval != model.DefaultReachIntervalMinutes {
		t.Fatalf("默认周期应为 %d,得到 %d", model.DefaultReachIntervalMinutes, rt.Interval)
	}
	var managed, ext int
	for _, tg := range rt.Targets {
		if tg.Subject == string(model.NodeSubjectKey(n.ID)) {
			managed++
			if tg.TID != "inbound:"+strconv.FormatInt(in.ID, 10) {
				t.Fatalf("端点身份应为 inbound:%d,得到 %s", in.ID, tg.TID)
			}
		}
		if tg.Subject == "s:"+strconv.FormatInt(src.ID, 10)+":abc123" {
			ext++
			if tg.Address != "ext.example.com" {
				t.Fatalf("外部目标地址不对: %s", tg.Address)
			}
		}
	}
	if managed != 1 || ext != 1 {
		t.Fatalf("目标清单:节点 %d 外部 %d,各应为 1", managed, ext)
	}
}

func TestExclusionDropsBlockedNodeAndKeepsOnOverride(t *testing.T) {
	e := newEnv(t)
	n, in, u := mkReachFixture(t, e)
	key := string(model.NodeSubjectKey(n.ID))
	tid := "inbound:" + strconv.FormatInt(in.ID, 10)

	setExclude(t, e, true)
	// 开关开着但没有判定时,订阅里必须有节点 —— 判定缺失不是剔除理由。
	eps, err := e.s.subscriptionEndpointsFor(&u, model.FormatLinks)
	if err != nil || len(eps.Endpoints) == 0 {
		t.Fatalf("没判定时节点应在订阅里: %v %d", err, len(eps.Endpoints))
	}

	// 境内不通 + 控制面通 = blocked:剔除。
	postSubjectCheck(t, e, key, map[string]*reach.Result{tid: failResult()}, "domestic")
	postSubjectCheck(t, e, key, map[string]*reach.Result{tid: okResult()}, "cp")

	checks, err := e.st.ListSubjectChecks()
	if err != nil || len(checks) != 1 {
		t.Fatalf("对象存档应有一条: %v %d", err, len(checks))
	}
	if checks[0].Verdict != "blocked" {
		t.Fatalf("境内不通+控制面通应判 blocked,得到 %s", checks[0].Verdict)
	}
	eps, err = e.s.subscriptionEndpointsFor(&u, model.FormatLinks)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps.Endpoints) != 0 {
		t.Fatalf("blocked 节点应被剔除,订阅里还有 %d 个端点", len(eps.Endpoints))
	}

	// keep 豁免:判定再差也回来。
	code, body := e.api("PUT", "/api/reach/keep", map[string]any{"subject": key, "keep": true})
	if code != http.StatusOK {
		t.Fatalf("keep: %d %s", code, body)
	}
	eps, _ = e.s.subscriptionEndpointsFor(&u, model.FormatLinks)
	if len(eps.Endpoints) == 0 {
		t.Fatal("keep 之后节点应回到订阅里")
	}

	// 开关关掉:订阅恢复全集(且不再查判定)。
	code, body = e.api("PUT", "/api/reach/keep", map[string]any{"subject": key, "keep": false})
	if code != http.StatusOK {
		t.Fatalf("unkeep: %d %s", code, body)
	}
	setExclude(t, e, false)
	eps, _ = e.s.subscriptionEndpointsFor(&u, model.FormatLinks)
	if len(eps.Endpoints) == 0 {
		t.Fatal("关掉剔除后节点应回到订阅里")
	}
}

func TestExclusionDropsExternalEndpoint(t *testing.T) {
	e := newEnv(t)
	_, _, u := mkReachFixture(t, e)

	ep := render.Endpoint{Address: "ext.example.com", Port: 443,
		Protocol: model.ProtocolVLESS, SourceKey: "dead01", Remark: "快连-1"}
	live := render.Endpoint{Address: "live.example.com", Port: 443,
		Protocol: model.ProtocolVLESS, SourceKey: "live02", Remark: "快连-2"}
	src := addSourceWithSnapshot(t, e, []render.Endpoint{ep, live})
	ep.SourceID, live.SourceID = src.ID, src.ID

	setExclude(t, e, true)
	deadKey := string(ep.Key())
	liveKey := string(live.Key())
	postSubjectCheck(t, e, deadKey, map[string]*reach.Result{deadKey: failResult()}, "domestic")
	postSubjectCheck(t, e, deadKey, map[string]*reach.Result{deadKey: okResult()}, "cp")
	postSubjectCheck(t, e, liveKey, map[string]*reach.Result{liveKey: okResult()}, "domestic")

	eps, err := e.s.subscriptionEndpointsFor(&u, model.FormatLinks)
	if err != nil {
		t.Fatal(err)
	}
	addrs := map[string]bool{}
	for _, got := range eps.Endpoints {
		addrs[got.Address] = true
	}
	if addrs["ext.example.com"] {
		t.Fatal("blocked 的外部端点应被剔除")
	}
	if !addrs["live.example.com"] {
		t.Fatal("双通的外部端点应留在订阅里")
	}
}

func TestStaleVerdictDoesNotExclude(t *testing.T) {
	e := newEnv(t)
	n, _, u := mkReachFixture(t, e)
	key := model.NodeSubjectKey(n.ID)

	// 直接摆一份 48 小时前的 blocked 判定:新鲜度窗口 24h,它必须失效。
	report, _ := json.Marshal(reach.NodeCheck{NodeID: n.ID, NodeName: n.Name,
		CheckedAt: time.Now().Add(-48 * time.Hour), Verdict: "blocked"})
	if err := e.st.SaveSubjectCheck(model.ReachSubjectCheck{
		SubjectKey: model.ProbeSubjectKey(key),
		CheckedAt:  time.Now().Add(-48 * time.Hour), Verdict: "blocked", Report: report,
	}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	sum, _ := json.Marshal(reach.SourceSummary{Probed: true, Failed: true})
	if err := e.st.SaveSourceCheck(model.ProbeSubjectKey(key), "probe:1", "domestic", old, sum, nil); err != nil {
		t.Fatal(err)
	}
	setExclude(t, e, true)
	eps, err := e.s.subscriptionEndpointsFor(&u, model.FormatLinks)
	if err != nil || len(eps.Endpoints) == 0 {
		t.Fatalf("过期判定不应剔除节点: %v %d", err, len(eps.Endpoints))
	}
}

func TestLegacyResultsShapeStillWorks(t *testing.T) {
	e := newEnv(t)
	n, _, _ := mkReachFixture(t, e)

	code, body := e.api("POST", "/api/reach/results", map[string]any{
		"checked_at": time.Now().UTC(),
		"results": []map[string]any{{
			"node_id": n.ID, "node_name": n.Name, "verdict": "ok",
			"local": map[string]any{"probed": true, "reachable": true, "latency_ms": 30},
			"cp":    map[string]any{"probed": true, "reachable": true, "latency_ms": 90},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("旧形状回写: %d %s", code, body)
	}
	code, body = e.api("GET", "/api/reach", nil)
	if code != http.StatusOK {
		t.Fatalf("列表: %d", code)
	}
	var out struct {
		Nodes []struct {
			NodeID  int64  `json:"node_id"`
			Verdict string `json:"verdict"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 1 || out.Nodes[0].NodeID != n.ID || out.Nodes[0].Verdict != "ok" {
		t.Fatalf("旧形状存档应出现在列表里: %+v", out.Nodes)
	}
}

func TestProbeTokenScope(t *testing.T) {
	e := newEnv(t)
	_, _, _ = mkReachFixture(t, e)

	code, body := e.api("POST", "/api/probes", map[string]string{"name": "mac-mini"})
	if code != http.StatusCreated {
		t.Fatalf("登记探测点: %d %s", code, body)
	}
	var created struct {
		ID        int64  `json:"id"`
		JoinToken string `json:"join_token"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.JoinToken == "" {
		t.Fatal("接入 token 不该为空")
	}

	// join 换正式 token:hello 是未鉴权的(凭据就是 join token 本身)。
	code, body = e.request("POST", "/api/probes/hello", map[string]string{
		"join_token": created.JoinToken}, nil)
	if code != http.StatusOK {
		t.Fatalf("hello: %d %s", code, body)
	}
	var hi struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &hi); err != nil || hi.Token == "" {
		t.Fatalf("hello 应回 token: %s %v", body, err)
	}
	// 一次性:第二次 hello 该 401。
	code, _ = e.request("POST", "/api/probes/hello", map[string]string{
		"join_token": created.JoinToken}, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("join token 应是一次性的,第二次用得到 %d", code)
	}

	auth := map[string]string{"Authorization": "Bearer " + hi.Token}
	// 放行面:targets、results、probe 代探。
	for _, tc := range [][2]string{
		{"GET", "/api/reach/targets"},
		{"POST", "/api/reach/results"},
		{"POST", "/api/reach/probe"},
	} {
		code, _ := e.request(tc[0], tc[1], map[string]any{}, auth)
		// results/probe 带空 body 会 400,但不能再是 401。
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Fatalf("探测 token 应能用 %s %s,得到 %d", tc[0], tc[1], code)
		}
	}
	// 越界:探测 token 不是管理 token。
	code, _ = e.request("GET", "/api/users", nil, auth)
	if code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("探测 token 不应能读用户列表,得到 %d", code)
	}
	code, _ = e.request("POST", "/api/probes", map[string]string{"name": "x"}, auth)
	if code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("探测 token 不应能登记探测点,得到 %d", code)
	}
}

func TestDeleteNodeClearsSubjectResults(t *testing.T) {
	e := newEnv(t)
	n, in, _ := mkReachFixture(t, e)
	key := string(model.NodeSubjectKey(n.ID))
	tid := "inbound:" + strconv.FormatInt(in.ID, 10)
	postSubjectCheck(t, e, key, map[string]*reach.Result{tid: okResult()}, "domestic")

	code, _ := e.api("DELETE", "/api/nodes/"+strconv.FormatInt(n.ID, 10), nil)
	if code != http.StatusOK {
		t.Fatalf("删节点: %d", code)
	}
	checks, err := e.st.ListSubjectChecks()
	if err != nil || len(checks) != 0 {
		t.Fatalf("删节点后存档应清空: %v %d", err, len(checks))
	}
}

func TestDeleteSourceClearsExtResults(t *testing.T) {
	e := newEnv(t)
	ep := render.Endpoint{Address: "ext.example.com", Port: 443,
		Protocol: model.ProtocolVLESS, SourceKey: "dead01", Remark: "快连"}
	src := addSourceWithSnapshot(t, e, []render.Endpoint{ep})
	ep.SourceID = src.ID
	key := string(ep.Key())
	postSubjectCheck(t, e, key, map[string]*reach.Result{key: okResult()}, "domestic")

	code, _ := e.api("DELETE", "/api/sources/"+strconv.FormatInt(src.ID, 10), nil)
	if code != http.StatusNoContent {
		t.Fatalf("删来源: %d", code)
	}
	checks, err := e.st.ListSubjectChecks()
	if err != nil || len(checks) != 0 {
		t.Fatalf("删来源后该来源的端点存档应清空: %v %d", err, len(checks))
	}
}

// TestInterleavedResultShapesKeepBothSides 复现 e2e 里见过的「本机列变 —」:
// 手动检查用旧形状写回(带 local/cp 摘要),随后轮转探测用新形状补 cp 侧,
// 对象报告两侧都得保住。
func TestInterleavedResultShapesKeepBothSides(t *testing.T) {
	e := newEnv(t)
	n, in, _ := mkReachFixture(t, e)

	// 旧形状:mpx ui 手动检查报节点级结果,两侧都可达。
	code, body := e.api("POST", "/api/reach/results", map[string]any{
		"checked_at": time.Now().UTC(),
		"results": []reach.NodeCheck{{
			NodeID: n.ID, NodeName: n.Name, CheckedAt: time.Now().UTC(), Verdict: "ok",
			Local: reach.SourceSummary{Probed: true, Reachable: true, LatencyMs: 42},
			CP:    reach.SourceSummary{Probed: true, Reachable: true, LatencyMs: 10},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("旧形状回写: %d %s", code, body)
	}

	// 新形状:探测端按 cp 侧补一条明细(与 cpRound 同一条路)。
	key := "inbound:" + strconv.FormatInt(in.ID, 10)
	postSubjectCheck(t, e, "node:"+strconv.FormatInt(n.ID, 10),
		map[string]*reach.Result{key: okResult()}, reach.ScopeCP)

	// 对象报告:本机侧摘要从来源行折回来,不能因为重算丢成未探。
	code, body = e.api("GET", "/api/reach", nil)
	if code != http.StatusOK {
		t.Fatalf("读触达列表: %d", code)
	}
	var list struct {
		Nodes []struct {
			NodeID  int64               `json:"node_id"`
			Verdict string              `json:"verdict"`
			Local   reach.SourceSummary `json:"local"`
			CP      reach.SourceSummary `json:"cp"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Nodes) != 1 {
		t.Fatalf("节点数 = %d,想要 1", len(list.Nodes))
	}
	got := list.Nodes[0]
	if !got.Local.Probed || !got.Local.Reachable {
		t.Errorf("本机侧丢了: %+v", got.Local)
	}
	if !got.CP.Probed || !got.CP.Reachable {
		t.Errorf("控制面侧丢了: %+v", got.CP)
	}
	if got.Verdict != "ok" {
		t.Errorf("判定 = %q,想要 ok", got.Verdict)
	}
}
