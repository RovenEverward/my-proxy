package cli

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 一行节点要能一眼看出这台机器现在什么状况;列数必须和表头对得上,否则表格
// 会整体错位,读的人以为"漂移"那列写的是版本。
func TestNodeRowShowsCachedStatus(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	notAfter := now.Add(9 * 24 * time.Hour)
	v := model.NodeView{
		Node: model.Node{ID: 3, Name: "hk", Address: "1.2.3.4", DeployedHash: "abc"},
		Status: &model.NodeStatus{
			NodeID: 3, CheckedAt: now.Add(-90 * time.Second),
			Reachable: true, Active: true, Version: "26.3.27", ObservedHash: "abc",
			Certificates: []model.CertificateInfo{
				{InboundID: 1, InboundName: "vless-8443", Path: "/etc/mpx/c.pem", DaysLeft: 9, NotAfter: &notAfter},
			},
		},
	}

	got := nodeRow(v, now)
	want := []string{
		"3", "hk", "1.2.3.4", "22", "root", "10085",
		"/etc/mpx/xray/config.json", "mpx-xray",
		"active", "26.3.27", "否", "1 分前", "9 天",
	}

	if len(nodeHeaders) != len(got) {
		t.Fatalf("表头 %d 列,数据 %d 列", len(nodeHeaders), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 列(%s): got %q want %q", i+1, nodeHeaders[i], got[i], want[i])
		}
	}
}

// 从没查过的节点:每一列都说"不知道",而不是留空白 —— 空白看起来像这几列坏了,
// 也像"没有漂移"。
func TestNodeRowWithoutStatusSaysWhatItDoesNotKnow(t *testing.T) {
	now := time.Now()
	got := nodeRow(model.NodeView{Node: model.Node{ID: 5, Name: "us", Address: "5.6.7.8"}}, now)

	want := map[string]string{"状态": "未知", "版本": "-", "漂移": "未下发", "最后检查": "从未", "证书最短": "-"}
	for i, h := range nodeHeaders {
		if w, ok := want[h]; ok && got[i] != w {
			t.Errorf("%s: got %q want %q", h, got[i], w)
		}
	}
}

// "未下发""未知""是""否"是四件不同的事:没有基准、问不到、被改过、对得上。
// 少一种,看的人就会把"问不到"当成"没问题"。
func TestDriftCellTellsUnknownApartFromNoDrift(t *testing.T) {
	unreachable := &model.NodeStatus{Reachable: false, Error: "连不上"}
	clean := &model.NodeStatus{Reachable: true, ObservedHash: "abc"}
	changed := &model.NodeStatus{Reachable: true, ObservedHash: "def"}

	cases := []struct {
		name string
		v    model.NodeView
		want string
	}{
		{"从没下发过", model.NodeView{Node: model.Node{ID: 1}}, "未下发"},
		{"下发过但还没查过", model.NodeView{Node: model.Node{ID: 1, DeployedHash: "abc"}}, "未知"},
		{"发布过但连不上", model.NodeView{Node: model.Node{ID: 1, DeployedHash: "abc"}, Status: unreachable}, "未知"},
		{"和下发的一致", model.NodeView{Node: model.Node{ID: 1, DeployedHash: "abc"}, Status: clean}, "否"},
		{"节点上被改过", model.NodeView{Node: model.Node{ID: 1, DeployedHash: "abc"}, Status: changed}, "是"},
	}
	for _, c := range cases {
		if got := driftCell(c.v); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// 一张读不到、一张还有 30 天时,要报"读不到"而不是"30 天":后者会把问题盖住,
// 而这正是证书这一列存在的理由。
func TestCertCellPrefersUnreadable(t *testing.T) {
	notAfter := time.Now().Add(30 * 24 * time.Hour)
	readable := model.CertificateInfo{Path: "/etc/a.pem", DaysLeft: 30, NotAfter: &notAfter}
	unreadable := model.CertificateInfo{Path: "/etc/b.pem", Error: "读不到证书的到期时间(文件不是证书,或者已经损坏)"}

	cases := []struct {
		name string
		st   *model.NodeStatus
		want string
	}{
		{"没查过", nil, "-"},
		{"连不上", &model.NodeStatus{Reachable: false}, "-"},
		{"没有证书", &model.NodeStatus{Reachable: true}, "-"},
		{"一张 30 天", &model.NodeStatus{Reachable: true, Certificates: []model.CertificateInfo{readable}}, "30 天"},
		{"两张里有一张读不到", &model.NodeStatus{Reachable: true, Certificates: []model.CertificateInfo{readable, unreadable}}, "读不到"},
		{"两张里较短的那张", &model.NodeStatus{Reachable: true, Certificates: []model.CertificateInfo{readable, {DaysLeft: 3, NotAfter: &notAfter}}}, "3 天"},
	}
	for _, c := range cases {
		v := model.NodeView{Node: model.Node{ID: 1, DeployedHash: "abc"}, Status: c.st}
		if got := certCell(v); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// 自检发现故障时退出码是 3,不是 1:1 也可能是"连不上控制面""参数写错了",
// 那种情况重试一下就好;3 是"查成了,系统有毛病",健康检查脚本要能分开。
func TestDoctorExitCodeIsThreeOnFailure(t *testing.T) {
	report := model.DoctorReport{Checks: []model.DoctorCheck{
		{Name: "数据库", Status: model.DoctorOK, Detail: "能读能写"},
		{Name: "节点 hk", Status: model.DoctorFail, Detail: "连不上", Hint: "看看 ssh"},
	}}
	serveDoctor(t, report)

	rootCmd.SetArgs([]string{"doctor"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if got := Execute(); got != doctorFailExit {
		t.Errorf("退出码 = %d, want %d", got, doctorFailExit)
	}
}

// 只有 warn 时退出码是 0:warn 是"该看一眼",把它也算失败会让健康检查天天报
// 警,然后就没人看了。
func TestDoctorExitCodeIsZeroOnWarningsOnly(t *testing.T) {
	report := model.DoctorReport{Checks: []model.DoctorCheck{
		{Name: "节点 hk", Status: model.DoctorWarn, Detail: "从没下发过配置"},
	}}
	serveDoctor(t, report)

	rootCmd.SetArgs([]string{"doctor"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if got := Execute(); got != 0 {
		t.Errorf("退出码 = %d, want 0", got)
	}
}

func serveDoctor(t *testing.T, report model.DoctorReport) {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("编码报告: %v", err)
	}
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/doctor" {
			t.Errorf("意外的请求: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}
