package control

import (
	"context"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 模板哈希是「同一份节点与入站、不带用户」渲染出的配置哈希:下发后记下的基准
// 就等于现场算的这一份,用户增删再下发也不该让它变 —— 它只回答「模板改没改」。
func TestDeployRecordsTemplateHashIndependentOfUsers(t *testing.T) {
	c, st, r, n := coreFixture(t)
	addInbound(t, st, n.ID, "vless-ws", 19233)
	addUser(t, st, activeUser("alice"))

	// 和别处的下发测试一样,给假节点补上下发的备份/校验/重启这三步的答案。
	r.mu.Lock()
	r.replies = append(r.replies, reply{"cp -f", ""}, reply{"-test -config", ""}, reply{"systemctl restart", ""})
	r.mu.Unlock()

	now := time.Now()
	if err := c.DeployNode(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	inbounds, err := st.ListInbounds(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	want, err := TemplateHash(n, inbounds)
	if err != nil {
		t.Fatalf("算模板哈希: %v", err)
	}
	stored, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DeployedTemplateHash != want {
		t.Fatalf("下发后记下的模板哈希 %q,现场算是 %q", stored.DeployedTemplateHash, want)
	}
	// 模板哈希与整份配置的哈希不是一回事:后者含着用户名单。
	if stored.DeployedHash == "" || stored.DeployedHash == want || stored.DeployedAt == nil {
		t.Fatalf("整份下发哈希应单独记录且不同于模板哈希: %+v", stored)
	}

	// 用户变了:名单进了下发内容,整份哈希跟着变,模板哈希不许变。
	addUser(t, st, activeUser("bob"))
	r.mu.Lock()
	r.calls, r.stdin = nil, nil
	r.mu.Unlock()
	if err := c.DeployNode(context.Background(), n, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DeployedHash == stored.DeployedHash {
		t.Fatalf("用户名单变了整份下发哈希却没变: %q", after.DeployedHash)
	}
	if after.DeployedTemplateHash != want {
		t.Fatalf("用户变化不该改模板哈希: %q, want %q", after.DeployedTemplateHash, want)
	}
	if after.DeployedTemplateHash == "" {
		t.Fatalf("模板哈希变成空串了: %+v", after)
	}
}

// 渲染不出来的入站模板哈希也渲染不出来:TemplateHash 直接把错误交出来。
//
// 这条不经过 DeployNode:同样的坏入站会让整份下发在算模板哈希之前就失败,
// 「下发成功、只是模板哈希算不出来」走不到,所以只测 TemplateHash 本身。
func TestTemplateHashReportsRenderError(t *testing.T) {
	_, st, _, n := coreFixture(t)
	// 库不校验枚举:写一个渲染器不认的传输方式,让它报错。
	bad := model.Inbound{NodeID: n.ID, Tag: "bad", Protocol: model.ProtocolVLESS, Port: 19234, Network: model.Network("nope")}
	if err := st.CreateInbound(&bad); err != nil {
		t.Fatal(err)
	}
	inbounds, err := st.ListInbounds(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TemplateHash(n, inbounds); err == nil {
		t.Fatal("坏入站应让模板哈希报错")
	}
}
