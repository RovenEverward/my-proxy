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
