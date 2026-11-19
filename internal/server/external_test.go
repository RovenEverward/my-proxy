package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// fakeExtProber 回放安排好的降级探测报告;address → report。
type fakeExtProber struct {
	rep map[string]model.QualityReport
}

func (f *fakeExtProber) ProbeExternal(_ context.Context, address, _, _ string) model.QualityReport {
	if r, ok := f.rep[address]; ok {
		return r
	}
	return model.QualityReport{Error: "没安排", Steps: []model.QualityStep{{Name: "resolve", Status: "failed", Reason: "没安排"}}}
}

// extReport 拼一份 fake 探测报告。
func extReport(ip, cc, city string, score int) model.QualityReport {
	return model.QualityReport{
		EgressIP: ip, Region: cc, CityEn: city, City: model.CityZh(city),
		Flags: map[string]bool{}, Score: score,
		Steps: []model.QualityStep{{Name: "resolve", Status: "ok"}, {Name: "ipwho.is", Status: "ok"}},
	}
}

// seedExternalSource 建一个来源并直接写入快照(不经网络刷新):
// 端点是手写的小 JSON,SourceKey 用短串,测试只关心键的稳定性。
func seedExternalSource(t *testing.T, e *env, name string, eps []render.Endpoint) *model.SubscriptionSource {
	t.Helper()
	src := &model.SubscriptionSource{Name: name, URL: "https://example.test/sub", Enabled: true, IntervalSeconds: 60, GrantAll: true}
	if err := e.st.CreateSource(src); err != nil {
		t.Fatal(err)
	}
	snap, _ := json.Marshal(eps)
	if err := e.st.CompleteSourceRefresh(src.ID, src.Revision, time.Now(), snap, nil, ""); err != nil {
		t.Fatal(err)
	}
	fresh, err := e.st.GetSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fresh
}

func extEP(key, remark, address string) render.Endpoint {
	return render.Endpoint{
		Protocol:  model.ProtocolVLESS,
		Address:   address,
		Port:      443,
		UUID:      "11111111-1111-4111-8111-111111111111",
		Network:   model.NetworkTCP,
		Security:  model.SecurityTLS,
		Remark:    remark,
		SourceKey: key,
	}
}

func TestExternalSubscriptionNaming(t *testing.T) {
	e := newEnv(t)
	// 清洗记录按指纹写;快照里的上游原名是"脏名"。
	eps := []render.Endpoint{
		extEP("k-jp", "日本 AWS 01 随机刷新", "jp.example.test"),
		extEP("k-raw", "无法解析的名字 99", "raw.example.test"),
	}
	src := seedExternalSource(t, e, "星空", eps)
	if err := e.st.SaveExternalMetadataBatch([]model.ExternalMetadata{
		{SourceID: src.ID, SourceKey: "k-jp", Region: "JP", City: "东京", Letter: "A", Kind: "auto"},
	}); err != nil {
		t.Fatal(err)
	}
	user := e.createUser("lzd")
	names := subNames(t, e, user.SubToken)
	want := map[string]bool{"星空 · 🇯🇵 东京 A": true, "星空 · 无法解析的名字 99": true}
	if len(names) != len(want) {
		t.Fatalf("names %v", names)
	}
	for _, n := range names {
		if !want[n] {
			t.Fatalf("出现意外名字 %q;原名不应泄漏(%v)", n, names)
		}
	}
}

// subNames 拉 links 订阅并解出每条节点的显示名(#fragment 后的部分)。
func subNames(t *testing.T, e *env, token string) []string {
	t.Helper()
	code, _, body := e.sub("/sub/"+token+"/links", "v2rayng/1.8")
	if code != 200 {
		t.Fatalf("sub %d %s", code, body)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if i := strings.LastIndex(line, "#"); i >= 0 {
			s, _ := url.PathUnescape(line[i+1:])
			names = append(names, s)
		}
	}
	return names
}

func TestProbeExternalQuality(t *testing.T) {
	e := newEnv(t)
	e.s.extProber = &fakeExtProber{rep: map[string]model.QualityReport{
		"jp.example.test":   extReport("1.2.3.4", "JP", "Tokyo", 90),
		"fail.example.test": {Error: "域名解析失败", Steps: []model.QualityStep{{Name: "resolve", Status: "failed", Reason: "域名解析失败"}}},
	}}
	src := seedExternalSource(t, e, "星空", []render.Endpoint{
		extEP("k1", "日本 01", "jp.example.test"),
		extEP("k2", "坏域名", "fail.example.test"),
	})
	code, raw := e.api("POST", fmt.Sprintf("/api/sources/%d/endpoints/k1/quality", src.ID), nil)
	if code != 200 {
		t.Fatalf("probe k1: %d %s", code, raw)
	}
	var q model.ExternalQuality
	json.Unmarshal(raw, &q)
	if q.Verdict != "ok" || q.Score != 90 {
		t.Fatalf("k1: %+v", q)
	}
	code, raw = e.api("POST", fmt.Sprintf("/api/sources/%d/endpoints/k2/quality", src.ID), nil)
	if code != 200 {
		t.Fatalf("probe k2: %d %s", code, raw)
	}
	json.Unmarshal(raw, &q)
	if q.Verdict != "untested" {
		t.Fatalf("k2 应 untested: %+v", q)
	}
	// 单点与列表查询
	code, _ = e.api("GET", fmt.Sprintf("/api/sources/%d/endpoints/k1/quality", src.ID), nil)
	if code != 200 {
		t.Fatalf("get k1: %d", code)
	}
	code, raw = e.api("GET", fmt.Sprintf("/api/sources/%d/quality", src.ID), nil)
	var list struct {
		Endpoints []model.ExternalQuality `json:"endpoints"`
	}
	json.Unmarshal(raw, &list)
	if code != 200 || len(list.Endpoints) != 2 {
		t.Fatalf("list: %d %d", code, len(list.Endpoints))
	}
	// 快照里没有的指纹 → 404
	code, _ = e.api("POST", fmt.Sprintf("/api/sources/%d/endpoints/ghost/quality", src.ID), nil)
	if code != 404 {
		t.Fatalf("ghost: %d", code)
	}
	// 带路由身份前缀的 key → 400(格式错误显式报出,不再静默 404)
	code, raw = e.api("POST", fmt.Sprintf("/api/sources/%d/endpoints/s:%d:k1/quality", src.ID, src.ID), nil)
	if code != 400 {
		t.Fatalf("s: 前缀 key 应 400: %d %s", code, raw)
	}
}

func TestRegradeIncludesExternal(t *testing.T) {
	e := newEnv(t)
	e.s.extProber = &fakeExtProber{rep: map[string]model.QualityReport{
		"a.example.test": extReport("1.1.1.1", "JP", "Tokyo", 95),
		"b.example.test": extReport("1.1.1.2", "JP", "Tokyo", 60),
	}}
	src := seedExternalSource(t, e, "星空", []render.Endpoint{
		extEP("ka", "日本 A", "a.example.test"),
		extEP("kb", "日本 B", "b.example.test"),
	})
	// 预先排好的字母:kb=B,ka=C(模拟回填先按顺序排的)。
	if err := e.st.SaveExternalMetadataBatch([]model.ExternalMetadata{
		{SourceID: src.ID, SourceKey: "ka", Region: "JP", City: "东京", Letter: "C", Kind: "auto"},
		{SourceID: src.ID, SourceKey: "kb", Region: "JP", City: "东京", Letter: "B", Kind: "auto"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ka", "kb"} {
		code, raw := e.api("POST", fmt.Sprintf("/api/sources/%d/endpoints/%s/quality", src.ID, k), nil)
		if code != 200 {
			t.Fatalf("probe %s: %d %s", k, code, raw)
		}
	}
	code, raw := e.api("POST", "/api/regrade", nil)
	if code != 200 {
		t.Fatalf("preview: %d %s", code, raw)
	}
	var pkt struct {
		Rows []RegradeRow `json:"rows"`
	}
	json.Unmarshal(raw, &pkt)
	byKey := map[string]RegradeRow{}
	for _, r := range pkt.Rows {
		if r.Kind == "external" {
			byKey[r.SourceKey] = r
		}
	}
	// ka 95 分应拿 A;kb 60 分顺延 B(字母池里 A、B 都没被未测项占用)。
	if !byKey["ka"].Changed || byKey["ka"].New != "🇯🇵 东京 A" {
		t.Fatalf("ka: %+v", byKey["ka"])
	}
	// kb 已在 B:名次取最小空位,B 被 ka 让出来后它又回到原位 → 不写入。
	if byKey["kb"].Changed && byKey["kb"].New != "🇯🇵 东京 B" {
		t.Fatalf("kb: %+v", byKey["kb"])
	}
	code, raw = e.api("POST", "/api/regrade", map[string]bool{"apply": true})
	if code != 200 {
		t.Fatalf("apply: %d %s", code, raw)
	}
	metas, err := e.st.ListExternalMetadata(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if metas[externalMetaKey(src.ID, "ka")].Letter != "A" || metas[externalMetaKey(src.ID, "ka")].Kind != "graded" {
		t.Fatalf("ka apply: %+v", metas)
	}
	// 订阅里 ka 应显示 星空 · 🇯🇵 东京 A
	user := e.createUser("lzd")
	names := subNames(t, e, user.SubToken)
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["星空 · 🇯🇵 东京 A"] || !found["星空 · 🇯🇵 东京 B"] {
		t.Fatalf("apply 后订阅名不对: %v", names)
	}
}

// TestSourceRefreshBackfillsExternalMetadata:快照刷新后,能解析地区的端点
// 自动建清洗记录并出现在 sources 视图;刷新后消失的键记录被清理(AC-05)。
func TestSourceRefreshBackfillsExternalMetadata(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "vless://11111111-1111-4111-8111-111111111111@jp1.example.test:443#🇯🇵 日本 01\n")
		fmt.Fprint(w, "vless://22222222-2222-4222-8222-222222222222@jp2.example.test:443#🇯🇵 日本 02\n")
		fmt.Fprint(w, "vless://33333333-3333-4333-8333-333333333333@xx.example.test:443#无法解析\n")
	}))
	defer upstream.Close()
	v := addTestSource(t, e, upstream.URL)
	if v.LastError != "" {
		t.Fatalf("refresh failed: %s", v.LastError)
	}
	metas, err := e.st.ListExternalMetadata(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("应只有 2 条能解析地区的记录: %+v", metas)
	}
	letters := map[string]bool{}
	for _, m := range metas {
		if m.Region != "JP" || m.Letter == "" || m.Kind != "auto" {
			t.Fatalf("meta: %+v", m)
		}
		letters[m.Letter] = true
	}
	if len(letters) != 2 {
		t.Fatalf("字母应各不同: %+v", metas)
	}
	// 视图名也应是清洗形态。
	for _, n := range v.Nodes {
		if n.RawName == "无法解析" {
			if n.Name != "airport · 无法解析" {
				t.Fatalf("无法解析应回退原名: %+v", n)
			}
			continue
		}
		if !strings.HasPrefix(n.Name, "airport · 🇯🇵 ") {
			t.Fatalf("清洗名: %+v", n)
		}
	}
}

// TestSourceViewShowsCleanedName:GET /api/sources/{id} 的 nodes[] 要返回
// 清洗名,raw_name 留上游原名供回溯。
func TestSourceViewShowsCleanedName(t *testing.T) {
	e := newEnv(t)
	src := seedExternalSource(t, e, "星空", []render.Endpoint{
		extEP("k1", "日本 AWS 01", "a.example.test"),
	})
	if err := e.st.SaveExternalMetadataBatch([]model.ExternalMetadata{
		{SourceID: src.ID, SourceKey: "k1", Region: "JP", City: "东京", Letter: "A", Kind: "auto"},
	}); err != nil {
		t.Fatal(err)
	}
	code, raw := e.api("GET", fmt.Sprintf("/api/sources/%d", src.ID), nil)
	if code != 200 {
		t.Fatalf("get: %d", code)
	}
	var v struct {
		Nodes []struct {
			Name    string `json:"name"`
			RawName string `json:"raw_name"`
		} `json:"nodes"`
	}
	json.Unmarshal(raw, &v)
	if len(v.Nodes) != 1 || v.Nodes[0].Name != "星空 · 🇯🇵 东京 A" || v.Nodes[0].RawName != "日本 AWS 01" {
		t.Fatalf("nodes: %+v", v.Nodes)
	}
}

// TestPatchExternalMetadata:JMS 这种主机名备注解析不出地区时,人工写/清
// 地区与城市;写进去的 kind=graded,刷新回填不动它;region 置空删记录。
func TestPatchExternalMetadata(t *testing.T) {
	e := newEnv(t)
	user := e.createUser("alice")
	ep := extEP("ext-raw", "JMS-1490255@c83s2.portablesubmarines.com:443", "c83s2.portablesubmarines.com")
	src := seedExternalSource(t, e, "JMS", []render.Endpoint{ep})

	path := fmt.Sprintf("/api/sources/%d/endpoints/%s", src.ID, ep.SourceKey)
	do := func(body map[string]any) (int, *json.Decoder, []byte) {
		code, data := e.api("PATCH", path, body)
		return code, json.NewDecoder(bytes.NewReader(data)), data
	}

	// 写地区+城市:graded 记录落库,显示名带旗帜。
	code, dec, body := do(map[string]any{"region": "JP", "city": "东京"})
	if code != http.StatusOK {
		t.Fatalf("PATCH=%d body=%s", code, body)
	}
	var out struct {
		Metadata model.ExternalMetadata `json:"metadata"`
	}
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Metadata.Region != "JP" || out.Metadata.City != "东京" || out.Metadata.Letter != "A" || out.Metadata.Kind != "graded" {
		t.Fatalf("metadata=%+v", out.Metadata)
	}
	if got := subNames(t, e, user.SubToken); len(got) == 0 || !strings.Contains(got[0], "日本") && !strings.Contains(got[0], "东京") {
		t.Fatalf("subscription names after patch: %v", got)
	}

	// 中文地区写法也收。
	code, dec, body = do(map[string]any{"region": "美国"})
	if code != http.StatusOK {
		t.Fatalf("PATCH zh=%d body=%s", code, body)
	}
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Metadata.Region != "US" {
		t.Fatalf("region zh=%q", out.Metadata.Region)
	}

	// 认不出的地区给 400,不写库。
	code, _, _ = do(map[string]any{"region": "火星"})
	if code != http.StatusBadRequest {
		t.Fatalf("PATCH bad=%d", code)
	}

	// 置空地区:删记录,订阅回退原名。
	code, _, _ = do(map[string]any{"region": ""})
	if code != http.StatusOK {
		t.Fatalf("PATCH empty=%d", code)
	}
	if got := subNames(t, e, user.SubToken); len(got) == 0 || got[0] != "JMS · JMS-1490255@c83s2.portablesubmarines.com:443" {
		t.Fatalf("subscription names after delete: %v", got)
	}
}

// TestSourceDefaultRegionBackfillsMetadata:备注解析不出地区时,刷新回填
// 用来源配置的默认地区建记录(JMS 主机名备注的形状);graded 记录不动。
func TestSourceDefaultRegionBackfillsMetadata(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "vless://11111111-1111-4111-8111-111111111111@c83s2.portablesubmarines.com:443#JMS-1490255@c83s2.portablesubmarines.com:443\n")
	}))
	defer upstream.Close()
	code, data := e.api("POST", "/api/sources", map[string]any{"name": "JMS", "url": upstream.URL, "interval_seconds": 0, "region": "JP"})
	if code != 201 {
		t.Fatalf("create source: %d %s", code, data)
	}
	v := decodeJSON[sourceView](t, data)
	v = awaitSource(t, e, v.ID)
	if v.LastError != "" {
		t.Fatalf("refresh failed: %s", v.LastError)
	}
	metas, err := e.st.ListExternalMetadata(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 {
		t.Fatalf("默认地区应兜底出 1 条记录: %+v", metas)
	}
	for _, m := range metas {
		if m.Region != "JP" || m.Letter != "A" || m.Kind != "auto" {
			t.Fatalf("fallback meta: %+v", m)
		}
	}
	if len(v.Nodes) != 1 || !strings.HasPrefix(v.Nodes[0].Name, "JMS · 🇯🇵 ") {
		t.Fatalf("view node name: %+v", v.Nodes)
	}
}
