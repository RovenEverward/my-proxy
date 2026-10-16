package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/singboxbuild"
	"github.com/RovenEverward/my-proxy/internal/xrayrelease"
)

// fixedUpdate 交回一份写死的控制面版本结论,把 /api/update 的回答钉住。
type fixedUpdate struct{ part model.UpdatePart }

func (f fixedUpdate) ControlUpdate() model.UpdatePart { return f.part }

// refreshUpdate 额外实现可选的 UpdateRefresher:记下被强制刷新了几次,并把结论
// 换成刷新后的那一份。
type refreshUpdate struct {
	fixedUpdate
	refreshed model.UpdatePart
	calls     *int
}

func (f refreshUpdate) RefreshControlUpdate(context.Context) model.UpdatePart {
	if f.calls != nil {
		*f.calls++
	}
	if f.refreshed.Current == "" && f.refreshed.Latest == "" {
		return f.fixedUpdate.part
	}
	return f.refreshed
}

// refresh=1 时先向上游强制检查一次再回答;不带时完全走缓存,一次检查都不触发。
func TestUpdateRefreshForcesCheck(t *testing.T) {
	e := newEnv(t)
	calls := 0
	e.s.SetUpdateSource(refreshUpdate{
		fixedUpdate: fixedUpdate{part: model.UpdatePart{Current: "v0.2.0", Latest: "v0.2.0"}},
		refreshed:   model.UpdatePart{Current: "v0.2.0", Latest: "v0.3.0", Behind: 1, Available: true},
		calls:       &calls,
	})

	// 不带 refresh:只回缓存结论,绝不触发强制检查。
	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	if calls != 0 {
		t.Fatalf("不带 refresh 不该触发检查,实际 %d 次", calls)
	}
	if got := decodeJSON[model.UpdateSnapshot](t, body).Control.Latest; got != "v0.2.0" {
		t.Fatalf("不带 refresh 该回缓存结论,实际 latest=%q", got)
	}

	// 带 refresh=1:强制检查一次,并把刷新后的结论发出去。
	code, body = e.api("GET", "/api/update?refresh=1", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update?refresh=1: got %d: %s", code, body)
	}
	if calls != 1 {
		t.Fatalf("带 refresh=1 该触发一次检查,实际 %d 次", calls)
	}
	c := decodeJSON[model.UpdateSnapshot](t, body).Control
	if c.Latest != "v0.3.0" || !c.Available {
		t.Fatalf("refresh=1 该给出刷新后的结论:%+v", c)
	}
}

// 更新源不支持刷新时,refresh=1 退回缓存结论照常回 200 —— 接口只新增不改,
// 旧实现不该因为这个参数就失败。
func TestUpdateRefreshFallsBackWithoutRefresher(t *testing.T) {
	e := newEnv(t)
	e.s.SetUpdateSource(fixedUpdate{part: model.UpdatePart{
		Current: "v0.2.0", Latest: "v0.2.0",
	}})

	code, body := e.api("GET", "/api/update?refresh=1", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update?refresh=1: got %d: %s", code, body)
	}
	if got := decodeJSON[model.UpdateSnapshot](t, body).Control.Latest; got != "v0.2.0" {
		t.Fatalf("不支持刷新时该回缓存结论,实际 latest=%q", got)
	}
}

// refresh 只认显式的真值;0/false/别的值都不触发检查。
func TestUpdateRefreshOnlyExplicitTrue(t *testing.T) {
	for _, q := range []string{"", "?refresh=0", "?refresh=false", "?refresh=", "?refresh=yes"} {
		calls := 0
		e := newEnv(t)
		e.s.SetUpdateSource(refreshUpdate{
			fixedUpdate: fixedUpdate{part: model.UpdatePart{Current: "v0.2.0"}},
			calls:       &calls,
		})
		code, body := e.api("GET", "/api/update"+q, nil)
		if code != http.StatusOK {
			t.Fatalf("GET /api/update%s: got %d: %s", q, code, body)
		}
		if calls != 0 {
			t.Errorf("%q 不该触发检查,实际 %d 次", q, calls)
		}
	}
}

// 版本接口是管理接口,和其它 /api 一样要 token —— 它虽然只读,但确实在说
// 控制面与每台节点跑的是什么版本,不该公网可达。
func TestUpdateRequiresToken(t *testing.T) {
	e := newEnv(t)

	if code, _ := e.request("GET", "/api/update", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("不带 token 应当 401,实际 %d", code)
	}
	code, _ := e.request("GET", "/api/update", nil, map[string]string{"Authorization": "Bearer wrong"})
	if code != http.StatusUnauthorized {
		t.Fatalf("token 不对应当 401,实际 %d", code)
	}
	if code, _ := e.api("GET", "/api/update", nil); code != http.StatusOK {
		t.Fatalf("带对 token 应当 200,实际 %d", code)
	}
}

// 从源码编出来的控制面不知道自己是谁,这时不能声称"有新版"。
//
// 这里的更新源故意报 available=true 来试探:接口必须在边界上再兜一次,不能把
// "当前版本未知却说有新版"这种自相矛盾的结论发出去。
func TestUpdateSourceBuildReportsNoUpdate(t *testing.T) {
	e := newEnv(t)
	// 收到 0.2.0 但当前版本为空 —— 源码编译版的典型样子。
	e.s.SetUpdateSource(fixedUpdate{part: model.UpdatePart{
		Current: "", Latest: "v0.2.0", Behind: 1, Available: true,
		ReleaseURL: "https://example.test/r",
	}})

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	snap := decodeJSON[model.UpdateSnapshot](t, body)
	if snap.Control.Available {
		t.Errorf("当前版本未知时不该说有新版:%+v", snap.Control)
	}
	if snap.Control.Current != "" {
		t.Errorf("当前版本该是空:%q", snap.Control.Current)
	}
	if snap.Control.Latest != "v0.2.0" {
		t.Errorf("最新版本该照给:%q", snap.Control.Latest)
	}
}

// 确实落后时,字段要一一对上 —— 尤其是 available 与 release_url,CLI 靠它们
// 决定说"可升级"并给出发布页。
func TestUpdateReportsNewerRelease(t *testing.T) {
	e := newEnv(t)
	published := time.Date(2026, 5, 1, 3, 2, 0, 0, time.UTC)
	checked := published.Add(time.Hour)
	e.s.SetUpdateSource(fixedUpdate{part: model.UpdatePart{
		Current:     "v0.2.0",
		Latest:      "v0.3.0",
		Behind:      3,
		Available:   true,
		ReleaseURL:  "https://example.test/releases/v0.3.0",
		PublishedAt: &published,
		CheckedAt:   &checked,
	}})

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	snap := decodeJSON[model.UpdateSnapshot](t, body)
	c := snap.Control
	if !c.Available || c.Current != "v0.2.0" || c.Latest != "v0.3.0" || c.Behind != 3 {
		t.Fatalf("控制面这一行不对:%+v", c)
	}
	if c.ReleaseURL != "https://example.test/releases/v0.3.0" {
		t.Errorf("发布页地址不对:%q", c.ReleaseURL)
	}
	if c.PublishedAt == nil || !c.PublishedAt.Equal(published) {
		t.Errorf("发布时间不对:%v", c.PublishedAt)
	}
	if c.CheckedAt == nil || !c.CheckedAt.Equal(checked) {
		t.Errorf("检查时间不对:%v", c.CheckedAt)
	}

	// 键名是接口契约:CLI 与 UI 按它们取值,改个名字就是接口变了。
	flat := strings.ReplaceAll(string(body), " ", "")
	for _, key := range []string{`"current"`, `"latest"`, `"behind"`, `"available"`, `"release_url"`, `"published_at"`, `"checked_at"`, `"error"`, `"control"`, `"nodes"`} {
		if !strings.Contains(flat, key) {
			t.Errorf("响应里缺键 %s:%s", key, body)
		}
	}
	// notes 是 /api/status 的东西,mpx update 用不到,不该顺手带出去。
	if strings.Contains(flat, `"notes"`) {
		t.Errorf("不该出现 notes:%s", body)
	}
}

// 没接更新源时接口照常回 200,只是除版本外什么都说不出来 —— 不该整个失败,
// 也不该凭空报一个"有新版"。
func TestUpdateWithoutSourceStillAnswers(t *testing.T) {
	e := newEnv(t)

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	snap := decodeJSON[model.UpdateSnapshot](t, body)
	if snap.Control.Available || snap.Control.Latest != "" {
		t.Errorf("没接更新源时不该说有新版:%+v", snap.Control)
	}
	if snap.Nodes == nil {
		t.Errorf("没有节点时该给空数组,而不是 null:%s", body)
	}
}

// 一个节点装了哪些核心就出哪几行,版本取自最近一次观察,不现场连节点。
func TestUpdateReportsNodeCoreRows(t *testing.T) {
	e := newEnv(t)

	// 一台纯 xray 节点:老部署路径,没有 core_deployments 记录。
	xray := e.createNode("hk-1", "hk.example.com")
	if err := e.st.SetNodeDeployed(xray.ID, "xray-hash", time.Now()); err != nil {
		t.Fatalf("记 xray 下发哈希: %v", err)
	}
	if err := e.st.UpsertNodeStatus(model.NodeStatus{
		NodeID: xray.ID, Reachable: true, Active: true, Version: "26.3.27",
	}); err != nil {
		t.Fatalf("存 xray 节点状态: %v", err)
	}

	// 一台混合节点:两种核心都装,而且 NodeStatus.Version 这一格被 sing-box
	// 占了(QUIC 节点的老约定),不能拿它当 xray 的版本。
	mixed := e.createNode("jp-2", "jp.example.com")
	in := model.Inbound{NodeID: mixed.ID, Tag: "hy2", Protocol: model.ProtocolHysteria2, Port: 443}
	if err := e.st.CreateInbound(&in); err != nil {
		t.Fatalf("建 sing-box 入站: %v", err)
	}
	// 两个核心都下发过:xray 的基准记在节点上,sing-box 的记在独立那一行。
	if err := e.st.SetNodeDeployed(mixed.ID, "xray-hash", time.Now()); err != nil {
		t.Fatalf("记混合节点的 xray 下发哈希: %v", err)
	}
	if err := e.st.SetCoreDeployed(mixed.ID, model.CoreSingBox, "sb-hash", time.Now()); err != nil {
		t.Fatalf("记混合节点的 sing-box 下发哈希: %v", err)
	}
	// 巡检脚本把 xray 版本写进 Version,sing-box 单独进 Cores —— 这是真实形状:
	// 混合节点因为有 xray 下发记录,Version 不会被镜像成 sing-box 的版本。
	if err := e.st.UpsertNodeStatus(model.NodeStatus{
		NodeID: mixed.ID, Reachable: true, Active: true, Version: "26.3.27",
		Cores: []model.CoreStatus{
			{Core: model.CoreSingBox, Active: true, Version: "1.12.0", ObservedHash: "sb-hash"},
		},
	}); err != nil {
		t.Fatalf("存混合节点状态: %v", err)
	}

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	snap := decodeJSON[model.UpdateSnapshot](t, body)

	rows := map[string]model.UpdateNodeCore{}
	for _, r := range snap.Nodes {
		rows[r.Name+"/"+string(r.Core)] = r
	}
	if len(rows) != 3 {
		t.Fatalf("该有三行(纯 xray 一台一行、混合一台两行),实际 %d:%+v", len(rows), snap.Nodes)
	}

	hk := rows["hk-1/xray"]
	if hk.Current != "26.3.27" {
		t.Errorf("hk-1 的 xray 版本不对:%+v", hk)
	}
	// xray 的推荐版本跟着 MPX 发布钉死,目标就是那个常量。
	if hk.Target != xrayrelease.RecommendedVersion {
		t.Errorf("xray 的目标该是推荐版本 %s,实际 %q", xrayrelease.RecommendedVersion, hk.Target)
	}

	jpX := rows["jp-2/xray"]
	if jpX.Current != "26.3.27" {
		t.Errorf("混合节点上 xray 的版本不对:%+v", jpX)
	}
	if jpX.Target != xrayrelease.RecommendedVersion {
		t.Errorf("xray 的目标该是推荐版本 %s,实际 %q", xrayrelease.RecommendedVersion, jpX.Target)
	}
	jpS := rows["jp-2/sing-box"]
	if jpS.Current != "1.12.0" {
		t.Errorf("sing-box 版本不对:%+v", jpS)
	}
	// sing-box 的版本由控制面钉死,目标就是那个常数。
	if jpS.Target != singboxbuild.DefaultVersion {
		t.Errorf("sing-box 的目标该是钉死版本 %s,实际 %q", singboxbuild.DefaultVersion, jpS.Target)
	}

	// 显示名没设时为空,不该编一个。
	if hk.Display != "" {
		t.Errorf("没有地区信息时显示名该为空:%+v", hk)
	}

	// 节点核心那一行的键名同样是契约。
	flat := strings.ReplaceAll(string(body), " ", "")
	for _, key := range []string{`"id"`, `"name"`, `"display"`, `"core"`, `"target"`} {
		if !strings.Contains(flat, key) {
			t.Errorf("节点行里缺键 %s:%s", key, body)
		}
	}
}

// 没有任何节点的控制面不该因为节点那一块而失败;纯 sing-box 节点不该冒出一行
// 空版本的 xray。
func TestUpdateNodeRowsFollowInstalledCores(t *testing.T) {
	e := newEnv(t)

	n := e.createNode("quic-only", "quic.example.com")
	in := model.Inbound{NodeID: n.ID, Tag: "tuic", Protocol: model.ProtocolTUIC, Port: 443}
	if err := e.st.CreateInbound(&in); err != nil {
		t.Fatalf("建入站: %v", err)
	}

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	snap := decodeJSON[model.UpdateSnapshot](t, body)
	if len(snap.Nodes) != 1 || snap.Nodes[0].Core != model.CoreSingBox {
		t.Fatalf("只有 sing-box 入站时该只出一行 sing-box:%+v", snap.Nodes)
	}
	if snap.Nodes[0].Current != "" {
		t.Errorf("没查过版本时该空着:%+v", snap.Nodes[0])
	}
}

// 没有入站、也没有任何下发记录的新节点按老节点算:出一行 xray(与下发那边
// coreRequirements 的默认一致),而不是一行都不出。
func TestUpdateBareNodeCountsAsXray(t *testing.T) {
	e := newEnv(t)
	e.createNode("bare", "bare.example.com")

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	snap := decodeJSON[model.UpdateSnapshot](t, body)
	if len(snap.Nodes) != 1 || snap.Nodes[0].Core != model.CoreXray {
		t.Fatalf("没入站没下发记录的节点该按 xray 出一行:%+v", snap.Nodes)
	}
}

// 下发过 sing-box 但入站已经删掉的节点仍要出 sing-box 一行:核心还装在机器上,
// 只是不再被任何入站用着 —— 与 control.coreRequirements 用下发哈希兜底的理由
// 相同。这种节点也不该被搭上一行 xray:按那边的判据,xray 此时确实不是必须的。
func TestUpdateSingBoxRowSurvivesRemovedInbounds(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("mixed", "mixed.example.com")
	if err := e.st.SetCoreDeployed(n.ID, model.CoreSingBox, "sb-hash", time.Now()); err != nil {
		t.Fatalf("记 sing-box 下发哈希: %v", err)
	}

	code, body := e.api("GET", "/api/update", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/update: got %d: %s", code, body)
	}
	snap := decodeJSON[model.UpdateSnapshot](t, body)
	if len(snap.Nodes) != 1 || snap.Nodes[0].Core != model.CoreSingBox {
		t.Fatalf("只有 sing-box 下发过时该只出一行 sing-box:%+v", snap.Nodes)
	}
}
