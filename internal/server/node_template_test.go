package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// templateHashOf 现场算一台节点的模板哈希,和接口里的算法是同一个函数。
func templateHashOf(t *testing.T, e *env, n model.Node) string {
	t.Helper()
	inbounds, err := e.st.ListInbounds(n.ID)
	if err != nil {
		t.Fatalf("读入站: %v", err)
	}
	h, err := control.TemplateHash(n, inbounds)
	if err != nil {
		t.Fatalf("算模板哈希: %v", err)
	}
	return h
}

// deployWithTemplate 模拟一次成功下发后的记账:整份哈希与模板哈希都记上。
func deployWithTemplate(t *testing.T, e *env, n model.Node) {
	t.Helper()
	if err := e.st.SetNodeDeployed(n.ID, "deployed", time.Now()); err != nil {
		t.Fatalf("记下发哈希: %v", err)
	}
	if err := e.st.SetNodeTemplateHash(n.ID, templateHashOf(t, e, n)); err != nil {
		t.Fatalf("记模板哈希: %v", err)
	}
}

// 刚下发过的节点不报模板漂移;把基准改成旧值(等于新版本换了写法)才报。
func TestNodeTemplateDrift(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "203.0.113.10")
	e.createInbound(n.ID, "vless", 443)
	deployWithTemplate(t, e, n)

	read := func() (map[string]any, model.NodeView) {
		t.Helper()
		code, data := e.api("GET", pathf("/api/nodes/%d", n.ID), nil)
		if code != http.StatusOK {
			t.Fatalf("取节点: got %d %s", code, data)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("解析节点: %v\n%s", err, data)
		}
		return raw, decodeJSON[model.NodeView](t, data)
	}

	raw, dv := read()
	if _, ok := raw["template_drift"]; ok {
		t.Fatalf("刚下发过的节点不该带 template_drift: %s", raw)
	}
	if dv.DeployedTemplateHash == "" || dv.DeployedTemplateHash != templateHashOf(t, e, n) {
		t.Fatalf("读回的模板哈希不对: %+v", dv.Node)
	}

	if err := e.st.SetNodeTemplateHash(n.ID, "old"); err != nil {
		t.Fatalf("改模板基准: %v", err)
	}
	raw, _ = read()
	if raw["template_drift"] != true {
		t.Fatalf("基准是旧写法时应报 template_drift: true,实际 %s", raw)
	}

	// 列表里同样现算:同一台节点、同一个结论。
	code, data := e.api("GET", "/api/nodes", nil)
	if code != http.StatusOK {
		t.Fatalf("列节点: got %d %s", code, data)
	}
	views := decodeJSON[[]model.NodeView](t, data)
	if len(views) != 1 || !views[0].TemplateDrift {
		t.Fatalf("列表里的模板漂移和单台读到的不一致: %+v", views)
	}
}

// 没在这一版上下发过(模板哈希是空串)的节点不亮漂移,否则升级控制面后每台都先亮一次。
func TestNodeTemplateDriftNeedsBaseline(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "203.0.113.10")
	e.createInbound(n.ID, "vless", 443)

	code, data := e.api("GET", pathf("/api/nodes/%d", n.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("取节点: got %d %s", code, data)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("解析节点: %v\n%s", err, data)
	}
	if _, ok := raw["template_drift"]; ok {
		t.Fatalf("没有下发基准时不该带 template_drift: %s", raw)
	}
}

// 整体 PUT 带进来的零值不能抹掉下发基准(deployed_template_hash 与 deployed_hash)。
func TestUpdateNodeKeepsDeployedTemplateHash(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "203.0.113.10")
	deployWithTemplate(t, e, n)
	before, err := e.st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("读节点: %v", err)
	}

	// 前端把 Node 解出来再整体 PUT;这里故意不带任何一个下发字段。
	body := model.Node{Name: "hk-2", Address: "198.51.100.7", SSHUser: "root"}
	code, data := e.api("PUT", pathf("/api/nodes/%d", n.ID), body)
	if code != http.StatusOK {
		t.Fatalf("改节点: got %d %s", code, data)
	}
	after, err := e.st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("读回节点: %v", err)
	}
	if after.Name != "hk-2" || after.Address != "198.51.100.7" {
		t.Fatalf("普通字段没改上: %+v", after)
	}
	if after.DeployedTemplateHash != before.DeployedTemplateHash || after.DeployedTemplateHash == "" {
		t.Fatalf("整体 PUT 抹掉了模板基准: before %q after %q", before.DeployedTemplateHash, after.DeployedTemplateHash)
	}
	if after.DeployedHash != before.DeployedHash || after.DeployedAt == nil {
		t.Fatalf("整体 PUT 抹掉了下发基准: before %+v after %+v", before, after)
	}

	// 带着旧值原样带回来(界面会这么做)也不改它。
	code, data = e.api("PUT", pathf("/api/nodes/%d", n.ID), *after)
	if code != http.StatusOK {
		t.Fatalf("带旧值改节点: got %d %s", code, data)
	}
	again, err := e.st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("再读回节点: %v", err)
	}
	if again.DeployedTemplateHash != before.DeployedTemplateHash {
		t.Fatalf("带着旧值 PUT 改了模板基准: %q", again.DeployedTemplateHash)
	}
}

// 改了入站(等于结构变了)而没应用:模板哈希也跟着对不上,报漂移。
func TestNodeTemplateDriftFollowsInboundChange(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "203.0.113.10")
	e.createInbound(n.ID, "vless", 443)
	deployWithTemplate(t, e, n)

	e.createInbound(n.ID, "vless-2", 8443)
	code, data := e.api("GET", pathf("/api/nodes/%d", n.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("取节点: got %d %s", code, data)
	}
	if !strings.Contains(string(data), `"template_drift": true`) {
		t.Fatalf("入站变了应报模板漂移: %s", data)
	}
}
