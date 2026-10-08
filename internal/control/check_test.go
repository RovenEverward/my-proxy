package control

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
)

// 这一组测的是"一条 ssh 拿全状态":脚本长什么样、输出怎么解析、问不到的时候
// 控制面得到什么。用的都是假 runner —— 真机上的那套(openssl 的输出格式、
// systemctl 的措辞)在下一次真机验证里过,这里先把逻辑钉死。

func TestCheckNodeParsesFullOutput(t *testing.T) {
	c, st := newTestControl(t, nil)
	n := addNode(t, st, "hk", "203.0.113.10")
	in := addInbound(t, st, n.ID, "vmess-ws", 443)
	in.CertFile = "/etc/mpx/xray/certs/hk.pem"
	if err := st.UpdateInbound(&in); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	// 假 runner 按 keyword 包含匹配,而状态检查是一整条脚本,所以这里用脚本里
	// 一定出现的东西当锚(XRAY_BIN=),把整条脚本的输出一次性给出来 —— 这正是
	// 真机那条 ssh 会返回的东西。
	//
	// 证书行必须写真实入站 id(in.ID):控制面要靠它把证书认回具体入站,编一个
	// 对不上的 id 就正好把这个断言测掉了。
	c.runner = &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: strings.Join([]string{
		"k=service:active",
		"k=version:26.3.27",
		"k=hash:" + strings.Repeat("ab", 32),
		fmt.Sprintf("k=certpath:%d=/etc/mpx/xray/certs/hk.pem", in.ID),
		fmt.Sprintf("k=certend:%d=Nov  4 12:00:00 2026 GMT", in.ID),
		fmt.Sprintf("k=certok:%d=0", in.ID),
	}, "\n")}}}

	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	got, err := c.CheckNode(context.Background(), n, now)
	if err != nil {
		t.Fatalf("CheckNode: %v", err)
	}

	if !got.Reachable {
		t.Fatalf("这条命令成功了,该判为可达:%+v", got)
	}
	if !got.Active {
		t.Fatal("service=active 该判成在跑")
	}
	if got.Version != "26.3.27" {
		t.Fatalf("版本该是 26.3.27,实际 %q", got.Version)
	}
	if got.ObservedHash != strings.Repeat("ab", 32) {
		t.Fatalf("哈希没解析出来:%q", got.ObservedHash)
	}
	if got.NodeID != n.ID || got.CheckedAt.IsZero() {
		t.Fatalf("NodeID/CheckedAt 该填上:%+v", got)
	}
	if len(got.Certificates) != 1 {
		t.Fatalf("该有一条证书,实际 %d 条", len(got.Certificates))
	}

	cert := got.Certificates[0]
	if cert.InboundID != in.ID || cert.Path != "/etc/mpx/xray/certs/hk.pem" {
		t.Fatalf("证书该认到入站 %d 与它的路径:%+v", in.ID, cert)
	}
	if cert.NotAfter == nil {
		t.Fatalf("到期时间该解析出来:%+v", cert)
	}
	// 2026-11-04 减 2026-10-08 = 27 天。
	if cert.DaysLeft != 27 {
		t.Fatalf("剩余天数该是 27,实际 %d(notAfter=%v)", cert.DaysLeft, cert.NotAfter)
	}
	if cert.Error != "" {
		t.Fatalf("读成功时不该有 Error:%q", cert.Error)
	}
}

// 证书文件不存在:路径要明确说是"读不到",不能静默变成一条没问题的记录。
func TestCheckNodeReportsUnreadableCertFile(t *testing.T) {
	c, st := newTestControl(t, nil)
	n := addNode(t, st, "hk", "203.0.113.10")
	in := addInbound(t, st, n.ID, "vless-ws", 443)
	in.CertFile = "/etc/mpx/xray/certs/gone.pem"
	if err := st.UpdateInbound(&in); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	c.runner = &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: strings.Join([]string{
		"k=service:active",
		"k=version:26.3.27",
		"k=hash:deadbeef",
		fmt.Sprintf("k=certpath:%d=读不到:文件不存在或没有权限", in.ID),
		fmt.Sprintf("k=certend:%d=", in.ID),
		fmt.Sprintf("k=certok:%d=1", in.ID),
	}, "\n")}}}

	got, err := c.CheckNode(context.Background(), n, time.Now())
	if err != nil {
		t.Fatalf("CheckNode: %v", err)
	}
	if len(got.Certificates) != 1 {
		t.Fatalf("读不到的证书也要占一条,不能被丢掉:实际 %d 条", len(got.Certificates))
	}

	cert := got.Certificates[0]
	if cert.InboundID != in.ID {
		t.Fatalf("该认出是哪个入站的证书:%+v", cert)
	}
	if !strings.Contains(cert.Path, "读不到") {
		t.Fatalf("路径该说明读不到,实际 %q", cert.Path)
	}
	if cert.NotAfter != nil {
		t.Fatal("读不到时不该编一个到期时间出来")
	}
	if cert.Error == "" {
		t.Fatal("读不到时必须写明原因,不能是空值")
	}
	if cert.DaysLeft != 0 {
		t.Fatalf("读不到时剩余天数该是 0(且要靠 Error 分辨),实际 %d", cert.DaysLeft)
	}
}

// 节点上没有 openssl:certend 是空的、certok 非 0,控制面要判成"读不到"。
func TestCheckNodeReportsMissingOpenSSL(t *testing.T) {
	c, st := newTestControl(t, nil)
	n := addNode(t, st, "hk", "203.0.113.10")
	in := addInbound(t, st, n.ID, "trojan", 8443)
	in.CertFile = "/etc/mpx/xray/certs/a.pem"
	if err := st.UpdateInbound(&in); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	c.runner = &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: strings.Join([]string{
		"k=service:active",
		"k=version:26.3.27",
		"k=hash:deadbeef",
		fmt.Sprintf("k=certpath:%d=/etc/mpx/xray/certs/a.pem", in.ID),
		fmt.Sprintf("k=certend:%d=", in.ID),
		fmt.Sprintf("k=certok:%d=127", in.ID),
	}, "\n")}}}

	got, err := c.CheckNode(context.Background(), n, time.Now())
	if err != nil {
		t.Fatalf("CheckNode: %v", err)
	}
	cert := got.Certificates[0]
	if cert.NotAfter != nil {
		t.Fatal("openssl 没跑成时不该有到期时间")
	}
	if cert.Error == "" {
		t.Fatal("openssl 缺失要说明,不能静默")
	}
	// 路径是读得到的(文件在),所以这里不该说"读不到路径"。
	if strings.Contains(cert.Path, "读不到") {
		t.Fatalf("文件本身在,路径那行该是真实路径:%q", cert.Path)
	}
}

// sha256sum 也没成(配置文件不在、路径配错)时,不能把空串当哈希写下去 ——
// 那会让漂移判定拿 "" 和基准比,凭空报出一个漂移。
func TestCheckNodeDoesNotInventHashWhenShaFails(t *testing.T) {
	r := &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: strings.Join([]string{
		"k=service:active",
		"k=version:26.3.27",
		"k=hash:hasherr",
	}, "\n")}}}

	c, st := newTestControl(t, r)
	n := addNode(t, st, "hk", "203.0.113.10")

	got, err := c.CheckNode(context.Background(), n, time.Now())
	if err != nil {
		t.Fatalf("CheckNode: %v", err)
	}
	if got.ObservedHash != "" {
		t.Fatalf("读不到哈希时该留空,实际 %q", got.ObservedHash)
	}
	if !got.Reachable || !got.Active {
		t.Fatalf("哈希读不到不代表节点不可达:%+v", got)
	}
}

// ssh 本身失败:这是一份**有效的状态**(Reachable=false),不是函数错误。
// 这是这一批最容易做错的一处 —— 做成 error 的话调用方只能把它丢掉,
// 而"节点失联"恰恰是最该被说出来的一件事。
func TestCheckNodeSSHFailureIsAStatusNotAnError(t *testing.T) {
	r := &fakeRunner{failOn: "ssh"}
	c, st := newTestControl(t, r)
	n := addNode(t, st, "hk", "203.0.113.10")

	got, err := c.CheckNode(context.Background(), n, time.Now())
	if err != nil {
		t.Fatalf("连不上节点不该是函数错误(状态本身就是结果),实际:%v", err)
	}
	if got.Reachable {
		t.Fatal("ssh 失败了,该判为不可达")
	}
	if got.Error == "" {
		t.Fatal("不可达时要带上 ssh 说的原因")
	}
	if got.NodeID != n.ID {
		t.Fatalf("不可达的状态也该知道是哪台节点,实际 NodeID=%d", got.NodeID)
	}
	if got.Certificates == nil {
		t.Fatal("Certificates 该是空切片而不是 nil:JSON 里 nil 会变成 null,客户端要多写一个判空")
	}
}

// 脚本要真的问到那四个问题,而且路径要走默认值。
func TestCheckNodeScriptAsksEverythingOnce(t *testing.T) {
	r := &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: "k=service:active"}}}
	c, st := newTestControl(t, r)
	n := addNode(t, st, "hk", "203.0.113.10")
	in := addInbound(t, st, n.ID, "vmess", 443)
	in.CertFile = "/etc/mpx/xray/certs/x.pem"
	if err := st.UpdateInbound(&in); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	if _, err := c.CheckNode(context.Background(), n, time.Now()); err != nil {
		t.Fatalf("CheckNode: %v", err)
	}

	// 只发一条:分开问会拿到四个不同瞬间的快照,拼出来的"状态"自相矛盾。
	if got := len(r.callList()); got != 1 {
		t.Fatalf("一台节点只该发一条 ssh,实际 %d 条:%v", got, r.callList())
	}
	script := r.calls[0]
	for _, want := range []string{
		"systemctl is-active 'mpx-xray'",        // 服务名走默认值
		`"$XRAY_BIN" version`,                   // 核心版本
		"sha256sum '/etc/mpx/xray/config.json'", // 配置路径走默认值
		"openssl x509 -noout -enddate -in '/etc/mpx/xray/certs/x.pem'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("脚本里少了 %q:\n%s", want, script)
		}
	}
}

// 没有证书的入站不该产生证书行 —— 那会让 node status 打出一堆空记录。
func TestCheckNodeSkipsInboundsWithoutCert(t *testing.T) {
	r := &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: "k=service:active"}}}
	c, st := newTestControl(t, r)
	n := addNode(t, st, "hk", "203.0.113.10")
	addInbound(t, st, n.ID, "vmess-plain", 80) // 没有 CertFile

	if _, err := c.CheckNode(context.Background(), n, time.Now()); err != nil {
		t.Fatalf("CheckNode: %v", err)
	}
	if strings.Contains(r.calls[0], "openssl") {
		t.Fatalf("没有证书文件的入站不该去问 openssl:\n%s", r.calls[0])
	}
}

// 脚本要把路径引起来:配置路径来自使用者输入,含空格或分号时不引就会变成
// 一条完全不同的命令 —— 而那条命令是在别人的机器上跑的。
func TestCheckScriptQuotesPaths(t *testing.T) {
	n := model.Node{Name: "hk", ConfigPath: "/tmp/a b;rm -rf /"}
	in := model.Inbound{ID: 1, CertFile: "/tmp/it's.pem"}

	script := checkScript(n, []model.Inbound{in})
	if strings.Contains(script, "sha256sum /tmp/a b;") {
		t.Fatalf("配置路径没被引起来:\n%s", script)
	}
	if !strings.Contains(script, `'/tmp/a b;rm -rf /'`) {
		t.Fatalf("配置路径该被单引号包住:\n%s", script)
	}
	if !strings.Contains(script, `'/tmp/it'\''s.pem'`) {
		t.Fatalf("证书路径里的单引号该被转义:\n%s", script)
	}
}

// 解析器只认前缀:节点上的噪音(systemd 的提示、motd、locale 警告)不能被当成
// 我们的字段,也不能因为噪音的存在整体错位。
func TestParseCheckOutputIgnoresNoise(t *testing.T) {
	var st model.NodeStatus
	parseCheckOutput(strings.Join([]string{
		"System is booting up. See pam_nologin(8)",
		"perl: warning: Setting locale failed.",
		"k=service:active",
		"some random line",
		"k=version:26.3.27",
		"k=hash:abc123",
	}, "\n"), &st)

	if !st.Active || st.Version != "26.3.27" || st.ObservedHash != "abc123" {
		t.Fatalf("噪音不该影响解析:%+v", st)
	}
}

// 剩余天数向下取整:剩 13.9 天时报 13,报 14 会让人在第 14 天以为还早。
func TestCertificateDaysLeftFloors(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var st model.NodeStatus
	st.CheckedAt = now
	parseCheckOutput(strings.Join([]string{
		"k=certpath:1=/a.pem",
		"k=certend:1=Oct 22 11:00:00 2026 GMT", // 13 天 23 小时
		"k=certok:1=0",
	}, "\n"), &st)

	if len(st.Certificates) != 1 {
		t.Fatalf("该有一条证书:%+v", st.Certificates)
	}
	if got := st.Certificates[0].DaysLeft; got != 13 {
		t.Fatalf("13 天 23 小时该报 13 天,实际 %d", got)
	}
}

// 到期时间解析不了的文本不能变成一条 0 天的记录 —— 那看起来像"今天就到期",
// 而实际是不知道。
func TestCertificateUnparsableDateKeepsError(t *testing.T) {
	var st model.NodeStatus
	st.CheckedAt = time.Now()
	parseCheckOutput(strings.Join([]string{
		"k=certpath:5=/b.pem",
		"k=certend:5=not a date at all",
		"k=certok:5=0",
	}, "\n"), &st)

	cert := st.Certificates[0]
	if cert.NotAfter != nil {
		t.Fatal("解析不了的日期不该变成时间")
	}
	if cert.Error == "" {
		t.Fatal("要留下原因")
	}
}

// 入站顺序稳定:每次检查的证书顺序都在变的话,输出会没法比对。
// 脚本故意倒着输出(id 2 在前),解析出来仍要按入站 id 升序 —— 顺序由内容定,
// 不由行的先后定。
func TestCheckNodeKeepsCertificateOrderStable(t *testing.T) {
	r := &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: strings.Join([]string{
		"k=certpath:2=/b.pem", "k=certend:2=Nov  4 12:00:00 2026 GMT", "k=certok:2=0",
		"k=certpath:1=/a.pem", "k=certend:1=Nov  4 12:00:00 2026 GMT", "k=certok:1=0",
	}, "\n")}}}
	c, st := newTestControl(t, r)
	n := addNode(t, st, "hk", "203.0.113.10")

	got, err := c.CheckNode(context.Background(), n, time.Now())
	if err != nil {
		t.Fatalf("CheckNode: %v", err)
	}
	// 输出里 id 2 在前,结果里该是 id 1 在前。
	if len(got.Certificates) != 2 {
		t.Fatalf("该有两条:%+v", got.Certificates)
	}
	if got.Certificates[0].InboundID != 1 || got.Certificates[1].InboundID != 2 {
		t.Fatalf("证书该按入站 ID 升序,实际 %d,%d",
			got.Certificates[0].InboundID, got.Certificates[1].InboundID)
	}
}

// 读库失败才是真的"连状态都算不出来",这时才该返回 error。
func TestCheckNodeReturnsErrorWhenStoreFails(t *testing.T) {
	c, _ := newTestControl(t, &fakeRunner{})
	st2, err := c.store.ListNodes()
	if err != nil || st2 != nil {
		t.Fatalf("前置检查:%v %v", st2, err)
	}

	// 关掉库之后连状态都算不出来。
	if err := c.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	n := model.Node{ID: 1, Name: "hk", Address: "203.0.113.10"}
	if _, err := c.CheckNode(context.Background(), n, time.Now()); err == nil {
		t.Fatal("库读不出来时该返回 error")
	}
}

// certok 要真的是 openssl 的退出码。
//
// 写成 `echo k=certend:$(openssl ...); echo k=certok:$?` 的话,后者拿到的是上一条
// echo 的退出码 —— 永远是 0。于是"节点上没装 openssl"和"文件不是证书"都退化成
// 同一句"没读成",而这两件事该做的处理不一样。
func TestCheckScriptCarriesOpenSSLExitCode(t *testing.T) {
	in := model.Inbound{ID: 3, CertFile: "/etc/a.pem"}
	script := checkScript(model.Node{Name: "hk"}, []model.Inbound{in})

	for _, want := range []string{"certrc=$?", "k=certok:3=$certrc"} {
		if !strings.Contains(script, want) {
			t.Fatalf("脚本里少了 %q:\n%s", want, script)
		}
	}
	// 紧跟 echo 的 $? 永远是 0,不能是它。
	if strings.Contains(script, "k=certok:3=$?") {
		t.Fatalf("certok 直接取 $? 拿到的是上一条 echo 的退出码:\n%s", script)
	}
}

// 哈希的哨兵值要真的会出现。
//
// `$(sha256sum X | cut ... || echo hasherr)` 里的 `||` 跟的是整条管道,而管道最后
// 一段是 cut:sha256sum 失败时 cut 照样成功退出,哨兵永远不会出现,值只是一个空串。
func TestCheckScriptHashFallbackSurvivesPipeline(t *testing.T) {
	script := checkScript(model.Node{Name: "hk"}, nil)
	if !strings.Contains(script, "${hash:-hasherr}") {
		t.Fatalf("哈希走的是管道,哨兵不会生效:\n%s", script)
	}
}

// 没装 openssl(退出码 127)和文件不是证书(别的非 0)要分开说 —— 一个要装东西,
// 一个要换文件。
func TestCheckNodeTellsMissingOpenSSLApartFromBadFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rc     string
		expect string
	}{
		{name: "没装 openssl", rc: "127", expect: "没有 openssl"},
		{name: "文件不是证书", rc: "1", expect: "不是证书"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var st model.NodeStatus
			st.CheckedAt = time.Now()
			parseCheckOutput(strings.Join([]string{
				"k=certpath:1=/a.pem",
				"k=certend:1=",
				"k=certok:1=" + tc.rc,
			}, "\n"), &st)

			if len(st.Certificates) != 1 {
				t.Fatalf("该有一条证书:%+v", st.Certificates)
			}
			if cert := st.Certificates[0]; !strings.Contains(cert.Error, tc.expect) {
				t.Fatalf("退出码 %s 的理由该包含 %q,实际 %q", tc.rc, tc.expect, cert.Error)
			}
		})
	}
}

// 假 runner 在包内已被 control_test.go 定义;这里只确认它仍然满足 Runner。
var _ node.Runner = (*fakeRunner)(nil)
