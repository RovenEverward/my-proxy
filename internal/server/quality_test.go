package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// fakeQualityProber 让 POST /api/nodes/{id}/quality 回放安排好的探测输出。
type fakeQualityProber struct {
	fakeNodeOps
	out map[string]string // 节点名 → 分段输出
	err error
}

func (f *fakeQualityProber) ProbeNodeQuality(_ context.Context, n model.Node, _, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.out[n.Name], nil
}

// probeOut 拼一段 fake 的节点探测输出。
func probeOut(ip, cc, city, flags string) string {
	return fmt.Sprintf(`===MPX_IP===
%s
===MPX_GEO===
{"success":true,"country_code":"%s","city":"%s"}
===MPX_FLAGS===
%s
`, ip, cc, city, flags)
}

func TestProbeQuality(t *testing.T) {
	ops := &fakeQualityProber{out: map[string]string{
		"la":  probeOut("1.2.3.4", "US", "Los Angeles", `{"is_datacenter":true,"asn":{"asn":123,"org":"DC Inc."}}`),
		"jp":  probeOut("5.6.7.8", "JP", "Tokyo", `{"is_tor":true,"is_proxy":true}`),
		"bad": "",
	}}
	ops.err = nil
	e := newEnvWith(t, ops)
	nodes := map[string]model.Node{}
	for _, name := range []string{"la", "jp", "bad"} {
		nodes[name] = e.createNode(name, name+".example.com")
	}
	code, raw := e.api("POST", fmt.Sprintf("/api/nodes/%d/quality", nodes["la"].ID), nil)
	if code != 200 {
		t.Fatalf("probe: %d %s", code, raw)
	}
	var q model.NodeQuality
	json.Unmarshal(raw, &q)
	if q.Verdict != "ok" || q.Score != 90 {
		t.Fatalf("la 应得 90: %+v", q)
	}
	var rep model.QualityReport
	json.Unmarshal(q.Report, &rep)
	if rep.EgressIP != "1.2.3.4" || rep.Region != "US" || rep.City != "洛杉矶" || rep.ASN != "AS123" || !rep.Flags["is_datacenter"] {
		t.Fatalf("report: %+v", rep)
	}

	// Tor + 代理 → 100-50-25=25
	code, raw = e.api("POST", fmt.Sprintf("/api/nodes/%d/quality", nodes["jp"].ID), nil)
	json.Unmarshal(raw, &q)
	if q.Verdict != "ok" || q.Score != 25 {
		t.Fatalf("jp 应得 25: %+v", q)
	}

	// 空输出 → untested,不产生分数
	code, raw = e.api("POST", fmt.Sprintf("/api/nodes/%d/quality", nodes["bad"].ID), nil)
	json.Unmarshal(raw, &q)
	if q.Verdict != "untested" || q.Score != 0 {
		t.Fatalf("bad 应为 untested: %+v", q)
	}

	// GET 单台与列表
	code, _ = e.api("GET", fmt.Sprintf("/api/nodes/%d/quality", nodes["la"].ID), nil)
	if code != 200 {
		t.Fatalf("get quality: %d", code)
	}
	code, raw = e.api("GET", "/api/quality", nil)
	var list struct {
		Nodes []model.NodeQuality `json:"nodes"`
	}
	json.Unmarshal(raw, &list)
	if code != 200 || len(list.Nodes) != 3 {
		t.Fatalf("list: %d %d", code, len(list.Nodes))
	}
	code, _ = e.api("GET", "/api/quality/last", nil)
	if code != 200 {
		t.Fatalf("last: %d", code)
	}
}

func TestRegradePlan(t *testing.T) {
	ops := &fakeQualityProber{out: map[string]string{
		// la-b 分最高 → 应拿 A;la-a 第二 → B;untouched 不参与,留着 A 让位。
		"la-a":      probeOut("1.1.1.1", "US", "Los Angeles", `{"is_datacenter":true}`),
		"la-b":      probeOut("1.1.1.2", "US", "Los Angeles", `{}`),
		"conflict":  probeOut("2.2.2.2", "JP", "Tokyo", `{}`),
		"no-region": probeOut("3.3.3.3", "SG", "Singapore", `{}`),
		"untested":  "",
	}}
	e := newEnvWith(t, ops)
	names := []string{"la-a", "la-b", "conflict", "no-region", "untested"}
	nodes := map[string]model.Node{}
	for _, name := range names {
		nodes[name] = e.createNode(name, name+".example.com")
	}
	// 预先摆好元数据:la-a=B、la-b=C(同组);conflict 已有有效 US 但与探测 JP 冲突;
	// no-region 是无效地区名,应被归一化;untested 没探测记录,保留原字母。
	putMeta := func(n model.Node, m model.LocationMetadata) {
		t.Helper()
		code, raw := e.api("PUT", fmt.Sprintf("/api/nodes/%d/metadata", n.ID), m)
		if code != 200 {
			t.Fatalf("put meta %s: %d %s", n.Name, code, raw)
		}
	}
	putMeta(nodes["la-a"], model.LocationMetadata{Region: "US", City: "洛杉矶", Letter: "B"})
	putMeta(nodes["la-b"], model.LocationMetadata{Region: "US", City: "洛杉矶", Letter: "C"})
	putMeta(nodes["conflict"], model.LocationMetadata{Region: "US", Letter: "A"})
	putMeta(nodes["no-region"], model.LocationMetadata{Region: "美西", Letter: "A"})
	putMeta(nodes["untested"], model.LocationMetadata{Region: "US", City: "洛杉矶", Letter: "A"})

	// 探测:给 4 台有输出的出分;untested 留 untested。
	for _, name := range []string{"la-a", "la-b", "conflict", "no-region"} {
		code, raw := e.api("POST", fmt.Sprintf("/api/nodes/%d/quality", nodes[name].ID), nil)
		if code != 200 {
			t.Fatalf("probe %s: %d %s", name, code, raw)
		}
	}
	code, raw := e.api("POST", fmt.Sprintf("/api/nodes/%d/quality", nodes["untested"].ID), nil)
	var q model.NodeQuality
	json.Unmarshal(raw, &q)
	if q.Verdict != "untested" {
		t.Fatalf("untested: %+v", q)
	}

	// 预览:不写库
	code, raw = e.api("POST", "/api/regrade", nil)
	if code != 200 {
		t.Fatalf("regrade preview: %d %s", code, raw)
	}
	var pkt struct {
		Rows []RegradeRow `json:"rows"`
	}
	json.Unmarshal(raw, &pkt)
	byName := map[string]RegradeRow{}
	for _, r := range pkt.Rows {
		byName[r.Name] = r
	}
	// untested 占着 A:la-b 100 分拿最小空位 B,la-a 90 分顺延到 C(原 B 让给
	// 更高名次的 la-b,名次顺序比保持字母优先)。
	if !byName["la-b"].Changed || byName["la-b"].New != "🇺🇸 洛杉矶 B" {
		t.Fatalf("la-b 应改为 B: %+v", byName["la-b"])
	}
	if !byName["la-a"].Changed || byName["la-a"].New != "🇺🇸 洛杉矶 C" {
		t.Fatalf("la-a 应改为 C: %+v", byName["la-a"])
	}
	if byName["conflict"].Changed || byName["conflict"].Note == "" {
		t.Fatalf("conflict 应跳过并提示: %+v", byName["conflict"])
	}
	// no-region 美西不是国家码,探测 SG → 归一化成 🇸🇬 新加坡 A
	if !byName["no-region"].Changed || byName["no-region"].New != "🇸🇬 新加坡 A" {
		t.Fatalf("no-region 应归一化: %+v", byName["no-region"])
	}
	if byName["untested"].Changed {
		t.Fatalf("untested 不动: %+v", byName["untested"])
	}
	// 预览不写库
	m, _ := e.st.GetLocationMetadata("nodes", nodes["la-b"].ID)
	if m.Letter != "C" {
		t.Fatalf("预览不应写库: %+v", m)
	}

	// apply
	code, raw = e.api("POST", "/api/regrade", map[string]bool{"apply": true})
	if code != 200 {
		t.Fatalf("apply: %d %s", code, raw)
	}
	m, _ = e.st.GetLocationMetadata("nodes", nodes["la-b"].ID)
	if m.Letter != "B" {
		t.Fatalf("la-b 应为 B: %+v", m)
	}
	m, _ = e.st.GetLocationMetadata("nodes", nodes["la-a"].ID)
	if m.Letter != "C" {
		t.Fatalf("la-a 应为 C: %+v", m)
	}
	m, _ = e.st.GetLocationMetadata("nodes", nodes["no-region"].ID)
	if m.Region != "SG" || m.Letter != "A" {
		t.Fatalf("no-region 归一化: %+v", m)
	}
	m, _ = e.st.GetLocationMetadata("nodes", nodes["conflict"].ID)
	if m.Region != "US" || m.Letter != "A" {
		t.Fatalf("conflict 不动: %+v", m)
	}
	m, _ = e.st.GetLocationMetadata("nodes", nodes["untested"].ID)
	if m.Letter != "A" {
		t.Fatalf("untested 保持 A: %+v", m)
	}
}
