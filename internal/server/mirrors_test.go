package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 订阅镜像走 /api/mirrors:建、列、改、删,默认都触发一次 DeployNode
// (结构性变更,跟 node sync 同规矩)。下面几个用例不真碰节点,fakeNodeOps
// 的 DeployNode 直接返回 nil。

func TestMirrorCreateListDelete(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	n := e.createNode("jp", "206.237.120.219")

	code, data := e.api("POST", "/api/mirrors", map[string]any{
		"node_id": n.ID, "port": 18080, "upstream": "203.0.113.1:8081", "remark": "东京",
	})
	if code != http.StatusOK {
		t.Fatalf("建镜像: got %d %s", code, data)
	}
	var created struct {
		ID       int64  `json:"id"`
		Endpoint string `json:"endpoint"`
		Synced   bool   `json:"synced"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("解析建镜像响应: %v", err)
	}
	if created.ID == 0 || !created.Synced {
		t.Fatalf("期望已下发的镜像 id,实际 %+v", created)
	}
	if !strings.Contains(created.Endpoint, "206.237.120.219:18080") {
		t.Fatalf("入口没带节点地址与端口: %s", created.Endpoint)
	}

	// 列表里要能查到这台节点与端口。
	code, data = e.api("GET", "/api/mirrors", nil)
	if code != http.StatusOK {
		t.Fatalf("列镜像: got %d %s", code, data)
	}
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil || len(list) != 1 {
		t.Fatalf("镜像列表: %v %s", err, data)
	}
	if list[0]["node"] != "jp" {
		t.Fatalf("镜像节点名: %v", list[0])
	}

	code, data = e.api("DELETE", "/api/mirrors/"+itoa(created.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("删镜像: got %d %s", code, data)
	}
	if code, _ := e.api("GET", "/api/mirrors", nil); code != http.StatusOK {
		t.Fatalf("删除后列镜像")
	}
}

func TestMirrorPortConflict(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	n := e.createNode("hk", "203.0.113.10")
	e.createInbound(n.ID, "vless-ws", 8443)

	// 与入站撞端口。
	code, data := e.api("POST", "/api/mirrors", map[string]any{"node_id": n.ID, "port": 8443, "upstream": "203.0.113.1:8081"})
	if code != http.StatusConflict {
		t.Fatalf("期望 409,实际 %d %s", code, data)
	}
	// 与 API 端口撞。
	if code, data := e.api("POST", "/api/mirrors", map[string]any{"node_id": n.ID, "port": n.APIPortOrDefault(), "upstream": "203.0.113.1:8081"}); code != http.StatusConflict {
		t.Fatalf("API 端口冲突期望 409,实际 %d %s", code, data)
	}
	// 两个镜像撞。
	e.api("POST", "/api/mirrors", map[string]any{"node_id": n.ID, "port": 18080, "upstream": "203.0.113.1:8081"})
	if code, data := e.api("POST", "/api/mirrors", map[string]any{"node_id": n.ID, "port": 18080, "upstream": "203.0.113.1:8081"}); code != http.StatusConflict {
		t.Fatalf("镜像互相撞期望 409,实际 %d %s", code, data)
	}
}

func TestUserLinksIncludeMirror(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	n := e.createNode("jp", "206.237.120.219")
	u := e.createUser("alice")

	e.api("POST", "/api/mirrors", map[string]any{"node_id": n.ID, "port": 18080, "upstream": "203.0.113.1:8081"})

	code, data := e.api("GET", "/api/users/"+u.Username+"/links", nil)
	if code != http.StatusOK {
		t.Fatalf("user links: got %d %s", code, data)
	}
	links := decodeJSON[map[string]string](e.t, data)
	mirror, ok := links["mirror-jp"]
	if !ok {
		t.Fatalf("链接里没带镜像入口: %v", links)
	}
	if !strings.HasSuffix(mirror, "/sub/"+u.SubToken) || !strings.Contains(mirror, "206.237.120.219:18080") {
		t.Fatalf("镜像入口形状不对: %s", mirror)
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
