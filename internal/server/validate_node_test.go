package server

import (
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 服务名和配置路径会进 systemd 单元文件和 systemctl 的命令行。留空表示用默认值,
// 要能过;写得会切坏单元文件的值,必须在入口就被拒 —— 否则错误要到节点上才现形,
// 而那时的报错("Unit mpx xray.service not found")跟真正的原因隔着好几步。
func TestValidateNodeRejectsValuesThatBreakTheUnitFile(t *testing.T) {
	ok := []struct {
		what string
		node model.Node
	}{
		{"用默认值", model.Node{Name: "hk", Address: "1.2.3.4"}},
		{"显式给两个值", model.Node{Name: "hk", Address: "1.2.3.4", ServiceName: "mpx-xray", ConfigPath: "/etc/mpx/xray/config.json"}},
	}
	for _, c := range ok {
		if got := validateNode(&c.node); got != "" {
			t.Errorf("%s 该通过,却报了 %q", c.what, got)
		}
	}

	bad := []struct {
		what string
		node model.Node
	}{
		{"服务名里有空格", model.Node{Name: "hk", Address: "a", ServiceName: "mpx xray"}},
		{"服务名里有斜杠", model.Node{Name: "hk", Address: "a", ServiceName: "x/mpx"}},
		{"服务名里有换行", model.Node{Name: "hk", Address: "a", ServiceName: "mpx\nxray"}},
		{"配置路径不是绝对路径", model.Node{Name: "hk", Address: "a", ConfigPath: "etc/xray.json"}},
		{"配置路径里有空格", model.Node{Name: "hk", Address: "a", ConfigPath: "/etc/mpx my/x.json"}},
		{"配置路径里有引号", model.Node{Name: "hk", Address: "a", ConfigPath: "/etc/mpx\"/x.json"}},
	}
	for _, c := range bad {
		if got := validateNode(&c.node); got == "" {
			t.Errorf("%s 该被拒,却通过了", c.what)
		}
	}
}

// 出口 IP 偏好只认那几个取值:创建和更新都要在入口拦下来,并且返回中文错误。
func TestNodeOutboundIPValidation(t *testing.T) {
	ok := []string{"", "v4v6", "v4", "v6", "asis"}
	for _, pref := range ok {
		if got := validateNode(&model.Node{Name: "hk", Address: "1.2.3.4", OutboundIP: pref}); got != "" {
			t.Errorf("outbound_ip=%q 该通过,却报了 %q", pref, got)
		}
	}
	for _, pref := range []string{"ipv4", "V4", "v4v6 ", "both"} {
		if got := validateNode(&model.Node{Name: "hk", Address: "1.2.3.4", OutboundIP: pref}); got == "" {
			t.Errorf("outbound_ip=%q 该被拒,却通过了", pref)
		}
	}
}

// 非法 outbound_ip 从 HTTP 入口进来时必须是 400,创建和更新两条路都要拦。
func TestNodeOutboundIPRejectedByAPI(t *testing.T) {
	e := newEnv(t)

	code, data := e.api("POST", "/api/nodes", model.Node{
		Name: "hk", Address: "1.2.3.4", OutboundIP: "ipv4",
	})
	if code != 400 {
		t.Fatalf("建节点非法 outbound_ip: got %d %s", code, data)
	}

	n := e.createNode("jp", "node.example.com")
	n.OutboundIP = "ipv4"
	code, data = e.api("PUT", pathf("/api/nodes/%d", n.ID), n)
	if code != 400 {
		t.Fatalf("改节点非法 outbound_ip: got %d %s", code, data)
	}
}
