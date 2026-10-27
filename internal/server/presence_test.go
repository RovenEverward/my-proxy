package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// fakeOnlineOps 在 fakeNodeOps 上多接一份在线名单快照:控制面后台手里那份。
//
// 单独一个类型而不是给 fakeNodeOps 加方法:OnlineSnapshotSource 是可选的,别的
// 测试接上 fakeNodeOps 时不该顺手也有在线名单。
type fakeOnlineOps struct {
	fakeNodeOps
	snaps map[int64]model.OnlineSnapshot
}

func (f *fakeOnlineOps) OnlineSnapshots() map[int64]model.OnlineSnapshot {
	return f.snaps
}

// writeSample 写一行采样,顺带记下节点的采样时间。
//
// 活跃记录与活跃段都由采样算出来:累计值比上一条涨了的采样,那一刻就有流量;
// 同一个人同一台节点上按写入顺序取上一条。
func writeSample(t *testing.T, e *env, username string, nodeID int64, at time.Time, up, down int64) {
	t.Helper()
	sample := model.TrafficSample{Username: username, NodeID: nodeID, SampledAt: at, Uplink: up, Downlink: down}
	if err := e.st.ApplyCoreTrafficSample(sample, model.CoreXray); err != nil {
		t.Fatalf("写采样: %v", err)
	}
	if err := e.st.MarkNodeSampled(nodeID, at); err != nil {
		t.Fatalf("记采样时间: %v", err)
	}
}

// writeActivity 让用户在 at 那一刻有流量:一条从零涨到 100+100 的采样。
//
// 它排在这个用户第一条采样时没有上一条可减,整笔按增量计入,那一刻就有流量 ——
// 不用先铺一条基准。
func writeActivity(t *testing.T, e *env, username string, nodeID int64, at time.Time) {
	t.Helper()
	writeSample(t, e, username, nodeID, at, 100, 100)
}

// listPresence 调一次 GET /api/users,把每个人解成「用户名 → 在线情况」。
func listPresence(t *testing.T, e *env) (presenceSummary, map[string]userPresence) {
	t.Helper()
	code, data := e.api("GET", "/api/users", nil)
	if code != http.StatusOK {
		t.Fatalf("列表状态码 = %d, want 200(%s)", code, data)
	}
	page := decodeJSON[userPage](t, data)
	if page.Presence == nil {
		t.Fatalf("列表没有带 presence 抬头:%s", data)
	}
	out := make(map[string]userPresence, len(page.Users))
	for _, u := range page.Users {
		out[u.Username] = u.Presence
	}
	return *page.Presence, out
}

// 在线、离线、从没连过的用户各有各的样子。
func TestPresenceOnlineOfflineNever(t *testing.T) {
	// 基准时间截到秒:库里只存整秒,采样、采样时间与 now 要用同一个基准才对得上。
	now := time.Now().Truncate(time.Second)
	e := newEnv(t)
	node := e.createNode("node-1", "203.0.113.10")
	for _, name := range []string{"online", "offline", "never"} {
		e.createUser(name)
	}
	// 刚有流量:在线。三分钟前有流量:离线,但留最后活跃时间。
	writeActivity(t, e, "online", node.ID, now.Add(-10*time.Second))
	writeActivity(t, e, "offline", node.ID, now.Add(-3*time.Minute))
	// 节点的采样时间要在写采样之后记:writeActivity 自己也会记一次采样时间,
	// 而上面 writeActivity 用的是三分钟前,会把节点拖旧 —— 节点数据要新到 now。
	if err := e.st.MarkNodeSampled(node.ID, now); err != nil {
		t.Fatalf("记采样时间: %v", err)
	}

	sum, got := listPresence(t, e)

	if p := got["online"]; !p.Online || p.LastActiveAt == nil || p.LastNodeID == nil || *p.LastNodeID != node.ID {
		t.Fatalf("刚有流量的用户应当在线:%+v", p)
	} else if len(p.OnlineNodes) != 1 || p.OnlineNodes[0] != node.ID {
		t.Fatalf("在线节点 = %v, want [%d]", p.OnlineNodes, node.ID)
	}
	if p := got["offline"]; p.Online || p.LastActiveAt == nil || p.Stale || len(p.OnlineNodes) != 0 {
		t.Fatalf("三分钟前有流量的用户不应当在线:%+v", p)
	}
	if p := got["never"]; p.Online || p.LastActiveAt != nil || p.LastNodeID != nil || p.OnlineIPs != 0 || p.Stale {
		t.Fatalf("从没连过的用户应当是零值:%+v", p)
	}
	if sum.Online != 1 || sum.Active24h != 2 {
		t.Fatalf("抬头 = %+v, want 在线 1、最近一天活跃 2", sum)
	}
	if sum.UpdatedAt == nil || !sum.UpdatedAt.Equal(now.UTC()) {
		t.Fatalf("updated_at = %v, want %v", sum.UpdatedAt, now)
	}
}

// 数据旧了的节点:最近在那台节点上用过的人不算在线,标成 stale。
func TestPresenceStaleNode(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	e := newEnv(t)
	node := e.createNode("node-1", "203.0.113.10")
	e.createUser("alice")
	// 三分钟前有过流量,采样时间也停在那儿:按活跃时间本该算在线,但节点数据旧了。
	// 旧的门槛是 90 秒,三分钟足够越过它。
	// 采样时间必须写在写采样之后:writeActivity 自己也会记一次采样时间,先写会被它盖掉。
	since := now.Add(-3 * time.Minute)
	writeActivity(t, e, "alice", node.ID, since)
	// 把节点的采样时间也停在那一刻,节点才真的算旧。
	if err := e.st.MarkNodeSampled(node.ID, since); err != nil {
		t.Fatalf("记采样时间: %v", err)
	}

	sum, got := listPresence(t, e)

	p := got["alice"]
	if p.Online {
		t.Fatalf("旧节点上的人不该判在线:%+v", p)
	}
	if !p.Stale {
		t.Fatalf("最后用的是旧节点,应当标 stale:%+v", p)
	}
	if len(p.OnlineNodes) != 0 {
		t.Fatalf("旧节点不该算进在线节点:%v", p.OnlineNodes)
	}
	if len(sum.StaleNodes) != 1 || sum.StaleNodes[0].NodeID != node.ID || !sum.StaleNodes[0].Since.Equal(since.UTC()) {
		t.Fatalf("旧节点列表 = %+v, want 节点 %d 自 %v", sum.StaleNodes, node.ID, since)
	}
	if sum.Online != 0 {
		t.Fatalf("抬头在线人数 = %d, want 0", sum.Online)
	}
}

// 旧节点早了有一阵子、最后活跃离节点停下也过了余量:人仍然带着最后活跃时间与所在
// 节点,不在线,标 stale。
func TestPresenceStaleNodeBeyondSlackKeepsLastActive(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	e := newEnv(t)
	node := e.createNode("node-1", "203.0.113.10")
	e.createUser("alice")
	// 五分钟前有流量,节点也停在那一刻:超过旧节点门槛(90 秒),且在余量(2 分钟)
	// 之外,但不到两个余量。
	since := now.Add(-5 * time.Minute)
	writeActivity(t, e, "alice", node.ID, since)
	if err := e.st.MarkNodeSampled(node.ID, since); err != nil {
		t.Fatalf("记采样时间: %v", err)
	}

	sum, got := listPresence(t, e)

	p := got["alice"]
	if p.Online {
		t.Fatalf("旧节点上的人不该判在线:%+v", p)
	}
	// 旧节点判定只看这台节点自己的采样时间:节点停在五分钟前,这人就是在那之前
	// 结束的,仍标 stale(不论最后一次流量离节点停下的那一刻有多远)。
	if !p.Stale {
		t.Fatalf("最后用的是旧节点,应当标 stale:%+v", p)
	}
	if p.LastActiveAt == nil || !p.LastActiveAt.Equal(since.UTC()) {
		t.Fatalf("最后活跃时间 = %v, want %v", p.LastActiveAt, since)
	}
	if p.LastNodeID == nil || *p.LastNodeID != node.ID {
		t.Fatalf("最后所在节点 = %v, want %d", p.LastNodeID, node.ID)
	}
	if len(p.OnlineNodes) != 0 {
		t.Fatalf("旧节点不该算进在线节点:%v", p.OnlineNodes)
	}
	// 活跃记录的回答里也是同一份。
	code, data := e.api("GET", "/api/users/alice/activity", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d(%s)", code, data)
	}
	if act := decodeJSON[userActivity](t, data); act.Presence.LastActiveAt == nil || act.Presence.Online || !act.Presence.Stale {
		t.Fatalf("活跃记录里的 presence 应当带着最后活跃时间、不在线、标 stale:%+v", act.Presence)
	}
	if sum.Online != 0 {
		t.Fatalf("抬头在线人数 = %d, want 0", sum.Online)
	}
}

// 在线名单快照:刚连上还没传数据的人也算在线,并带上他连了几个 IP。
func TestPresenceSnapshotAddsOnline(t *testing.T) {
	ops := &fakeOnlineOps{}
	e := newEnvWith(t, ops)
	node := e.createNode("node-1", "203.0.113.10")
	e.createUser("alice")
	e.createUser("bob")
	now := time.Now()
	if err := e.st.MarkNodeSampled(node.ID, now); err != nil {
		t.Fatalf("记采样时间: %v", err)
	}
	ops.snaps = map[int64]model.OnlineSnapshot{
		node.ID: {NodeID: node.ID, At: now, Users: map[string][]string{
			"alice": {"203.0.113.5", "203.0.113.6"},
			"bob":   {"198.51.100.9"},
		}},
	}

	sum, got := listPresence(t, e)

	if p := got["alice"]; !p.Online || p.OnlineIPs != 2 || len(p.OnlineNodes) != 1 {
		t.Fatalf("快照里的 alice 应当在线、两个 IP:%+v", p)
	}
	// 没有活跃记录、只在快照里的人照样在线。
	if p := got["bob"]; !p.Online || p.OnlineIPs != 1 || p.LastActiveAt != nil {
		t.Fatalf("只在快照里的 bob 应当在线:%+v", p)
	}
	if sum.Online != 2 {
		t.Fatalf("抬头在线人数 = %d, want 2", sum.Online)
	}
}

// 活跃段还没结束的,结束时间是 null;404 给不存在的用户。
func TestUserActivityEndpoint(t *testing.T) {
	ops := &fakeOnlineOps{}
	e := newEnvWith(t, ops)
	node := e.createNode("node-1", "203.0.113.10")
	e.createUser("alice")
	now := time.Now()
	if err := e.st.MarkNodeSampled(node.ID, now); err != nil {
		t.Fatalf("记采样时间: %v", err)
	}
	// 两次采样:第一次有流量,第二次在二十秒前又涨了 300 字节,两段并成一段;
	// 活跃记录与它一致,人此刻也在线,于是这一段还没结束。
	writeSample(t, e, "alice", node.ID, now.Add(-time.Minute), 100, 100)
	writeSample(t, e, "alice", node.ID, now.Add(-20*time.Second), 400, 100)
	ops.snaps = map[int64]model.OnlineSnapshot{
		node.ID: {NodeID: node.ID, At: now, Users: map[string][]string{"alice": {"203.0.113.5"}}},
	}

	code, data := e.api("GET", "/api/users/alice/activity", nil)
	if code != http.StatusOK {
		t.Fatalf("活跃记录状态码 = %d, want 200(%s)", code, data)
	}
	got := decodeJSON[userActivity](t, data)
	if len(got.Segments) != 1 {
		t.Fatalf("段数 = %d, want 1(%s)", len(got.Segments), data)
	}
	seg := got.Segments[0]
	if seg.NodeID != node.ID || seg.Bytes != 300 {
		t.Fatalf("段 = %+v, want 节点 %d 共 300 字节", seg, node.ID)
	}
	if !seg.Start.Equal(now.Add(-20 * time.Second).UTC().Truncate(time.Second)) {
		t.Fatalf("段起点 = %v", seg.Start)
	}
	// 人还在线,这段不该写结束时间。看原始 JSON:解成指针之后 nil 与"没能解"分不出来。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("解析原始响应: %v", err)
	}
	var segs []map[string]json.RawMessage
	if err := json.Unmarshal(raw["segments"], &segs); err != nil {
		t.Fatalf("解析 segments: %v", err)
	}
	if string(segs[0]["end"]) != "null" {
		t.Fatalf("还在用的那一段 end = %s, want null", segs[0]["end"])
	}
	if len(got.IPs) != 1 || got.IPs[0].IP != "203.0.113.5" || got.IPs[0].NodeID != node.ID {
		t.Fatalf("在线 IP = %+v, want 节点 %d 上的 203.0.113.5", got.IPs, node.ID)
	}
	if got.UpdatedAt == nil {
		t.Fatal("updated_at 应当是节点最后的采样时间")
	}

	// 不存在的用户:404。
	code, _ = e.api("GET", "/api/users/nobody/activity", nil)
	if code != http.StatusNotFound {
		t.Fatalf("不存在的用户状态码 = %d, want 404", code)
	}
}

// 活跃记录的回答带着这个人的在线情况:字段与列表条目里的 presence 同一个形状,
// 在线的人与从没连过的人各一份。
func TestUserActivityCarriesPresence(t *testing.T) {
	ops := &fakeOnlineOps{}
	e := newEnvWith(t, ops)
	node := e.createNode("node-1", "203.0.113.10")
	e.createUser("alice")
	e.createUser("bob")
	now := time.Now().Truncate(time.Second)
	if err := e.st.MarkNodeSampled(node.ID, now); err != nil {
		t.Fatalf("记采样时间: %v", err)
	}
	ops.snaps = map[int64]model.OnlineSnapshot{
		node.ID: {NodeID: node.ID, At: now, Users: map[string][]string{"alice": {"203.0.113.5"}}},
	}

	code, data := e.api("GET", "/api/users/alice/activity", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d(%s)", code, data)
	}
	got := decodeJSON[userActivity](t, data)
	if !got.Presence.Online || got.Presence.OnlineIPs != 1 || len(got.Presence.OnlineNodes) != 1 || got.Presence.OnlineNodes[0] != node.ID {
		t.Fatalf("alice 的 presence = %+v, want 在线、节点 %d 上一个 IP", got.Presence, node.ID)
	}
	// 与列表里同一个人的那份一致。
	_, listed := listPresence(t, e)
	if want := listed["alice"]; !reflect.DeepEqual(got.Presence, want) {
		t.Fatalf("活跃记录里的 presence %+v 与列表里的 %+v 对不上", got.Presence, want)
	}

	// 从没连过的人:零值,online_nodes 是 [] 而不是 null。
	code, data = e.api("GET", "/api/users/bob/activity", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d(%s)", code, data)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	var pres map[string]json.RawMessage
	if err := json.Unmarshal(raw["presence"], &pres); err != nil {
		t.Fatalf("解析 presence: %v(%s)", err, data)
	}
	for _, key := range []string{"online", "online_ips", "online_nodes", "last_active_at", "last_node_id"} {
		if _, ok := pres[key]; !ok {
			t.Fatalf("presence 少了 %q:%s", key, data)
		}
	}
	if string(pres["online"]) != "false" || string(pres["online_nodes"]) != "[]" || string(pres["last_active_at"]) != "null" {
		t.Fatalf("从没连过的人 presence 应当是零值:%s", data)
	}
}

// 已经结束的段写着结束时间;数组在空的时候是 [] 而不是 null。
func TestUserActivitySegmentEndedAndEmpty(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	e := newEnv(t)
	node := e.createNode("node-1", "203.0.113.10")
	e.createUser("alice")
	e.createUser("bob")
	writeSample(t, e, "alice", node.ID, now.Add(-10*time.Minute), 100, 100)
	writeSample(t, e, "alice", node.ID, now.Add(-9*time.Minute), 100, 200)

	code, data := e.api("GET", "/api/users/alice/activity", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d(%s)", code, data)
	}
	got := decodeJSON[userActivity](t, data)
	if len(got.Segments) != 1 || got.Segments[0].End == nil {
		t.Fatalf("十分钟前结束的段应当带着 end:%s", data)
	}
	if !got.Segments[0].End.Equal(now.Add(-9 * time.Minute).UTC()) {
		t.Fatalf("段终点 = %v", got.Segments[0].End)
	}

	// 没有任何流量的用户:两个数组都是 [],活跃记录照旧给整份的 updated_at。
	code, data = e.api("GET", "/api/users/bob/activity", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d(%s)", code, data)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if string(raw["segments"]) != "[]" || string(raw["ips"]) != "[]" {
		t.Fatalf("没有记录时应当是空数组,实际 %s", data)
	}
	// updated_at 是全局的「数据新到哪一刻」,不是按人算的:同一个环境里 alice 的采样
	// 也在其中,所以 bob 照旧拿到非空的值,且与列表抬头一致。
	sum, _ := listPresence(t, e)
	if sum.UpdatedAt == nil {
		t.Fatal("抬头 updated_at 不该是 null")
	}
	bob := decodeJSON[userActivity](t, data)
	if bob.UpdatedAt == nil || !bob.UpdatedAt.Equal(*sum.UpdatedAt) {
		t.Fatalf("bob 的 updated_at = %v, want 抬头 %v", bob.UpdatedAt, sum.UpdatedAt)
	}
}

// 列表的每一条都保留着原来的字段,只是多了一个 presence。
func TestListUsersKeepsExistingKeys(t *testing.T) {
	e := newEnv(t)
	e.createUser("alpha")

	code, data := e.api("GET", "/api/users", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d", code)
	}
	var raw struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(raw.Users) != 1 {
		t.Fatalf("用户数 = %d, want 1", len(raw.Users))
	}
	for _, key := range []string{"id", "username", "sub_token", "enabled", "status", "data_limit", "used_traffic", "created_at", "presence"} {
		if _, ok := raw.Users[0][key]; !ok {
			t.Fatalf("列表条目少了 %q:%s", key, data)
		}
	}
	presence, ok := raw.Users[0]["presence"].(map[string]any)
	if !ok {
		t.Fatalf("presence 不是对象:%s", data)
	}
	if nodes, ok := presence["online_nodes"].([]any); !ok || len(nodes) != 0 {
		t.Fatalf("零值的 online_nodes 应当是 [],实际 %v", presence["online_nodes"])
	}
	// 从没连过的用户没有最后活跃时间,两个字段是 null。
	if presence["last_active_at"] != nil || presence["last_node_id"] != nil {
		t.Fatalf("从没连过的用户最后活跃时间应当是 null:%v", presence)
	}

	// 单独读一个用户时不带 presence:它是 PUT 的整体替换形状。
	code, data = e.api("GET", "/api/users/alpha", nil)
	if code != http.StatusOK {
		t.Fatalf("读用户状态码 = %d", code)
	}
	var one map[string]any
	if err := json.Unmarshal(data, &one); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if _, ok := one["presence"]; ok {
		t.Fatalf("单独读一个用户不该带 presence:%s", data)
	}
}

// 没有接节点通道时照样能答:只在活跃表与节点上算,快照那一半缺席。
func TestPresenceWithoutNodeOps(t *testing.T) {
	e := newEnv(t)
	node := e.createNode("node-1", "203.0.113.10")
	e.createUser("alice")
	now := time.Now()
	if err := e.st.MarkNodeSampled(node.ID, now); err != nil {
		t.Fatalf("记采样时间: %v", err)
	}
	writeActivity(t, e, "alice", node.ID, now)

	sum, got := listPresence(t, e)
	if !got["alice"].Online || sum.Online != 1 {
		t.Fatalf("没有节点通道时按活跃时间判断 alice 应当在线:%+v / %+v", got["alice"], sum)
	}
}
