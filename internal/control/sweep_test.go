package control

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// recordingAlerter 记下报过什么、销过什么。
//
// 这一组测的不是"日志里有没有那句话"(那是 LogAlerter 的事),而是巡检**该不该**
// 报、**该不该**销 —— 也就是 raiseNodeAlerts 的判断。
type recordingAlerter struct {
	mu       sync.Mutex
	alerts   []model.Alert
	resolved []string
}

func (a *recordingAlerter) Alert(al model.Alert) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.alerts = append(a.alerts, al)
}

func (a *recordingAlerter) Resolve(key string, _ time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resolved = append(a.resolved, key)
}

func (a *recordingAlerter) alertKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.alerts))
	for _, al := range a.alerts {
		out = append(out, al.Key)
	}
	return out
}

func (a *recordingAlerter) resolvedKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.resolved...)
}

// newAlertTask 造一个只用来跑 raiseNodeAlerts 的后台任务。
func newAlertTask(t *testing.T) (*Task, *recordingAlerter) {
	t.Helper()
	c, st := newTestControl(t, nil)
	task := NewTask(c, st, slog.New(slog.DiscardHandler), time.Minute)
	rec := &recordingAlerter{}
	task.SetAlerter(rec)
	return task, rec
}

// 一台下发过配置的节点:DeployedHash 有值,漂移判定才有基准。
func deployedNode() model.Node {
	return model.Node{ID: 7, Name: "hk", DeployedHash: strings.Repeat("ab", 32)}
}

func TestRaiseNodeAlertsResolvesDriftWhenItIsGone(t *testing.T) {
	task, rec := newAlertTask(t)
	n := deployedNode()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

	// 节点上的哈希和下发的不一样 = 有人直接改过配置。
	drifted := model.NodeStatus{
		NodeID: n.ID, Reachable: true, Active: true,
		ObservedHash: strings.Repeat("cd", 32),
	}
	task.raiseNodeAlerts(n, drifted, now)

	keys := rec.alertKeys()
	if len(keys) != 1 {
		t.Fatalf("只该报一条漂移,实际 %v", keys)
	}
	if !strings.HasPrefix(keys[0], string(model.KindNodeDrift)) {
		t.Fatalf("报出来的该是漂移,实际 %q", keys[0])
	}

	// 哈希对上了:漂移没了,该销掉。
	good := model.NodeStatus{
		NodeID: n.ID, Reachable: true, Active: true,
		ObservedHash: n.DeployedHash,
	}
	task.raiseNodeAlerts(n, good, now.Add(time.Minute))

	resolved := rec.resolvedKeys()
	if len(resolved) != 1 || resolved[0] != keys[0] {
		t.Fatalf("漂移消失后该销掉 %q,实际销了 %v", keys[0], resolved)
	}
}

func TestRaiseNodeAlertsResolvesCertWhenItLeavesWarnRange(t *testing.T) {
	task, rec := newAlertTask(t)
	n := deployedNode()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

	near := now.Add(5 * 24 * time.Hour)
	warn := model.NodeStatus{
		NodeID: n.ID, Reachable: true, Active: true, ObservedHash: n.DeployedHash,
		Certificates: []model.CertificateInfo{
			{InboundID: 1, InboundName: "vmess-ws", Path: "/etc/mpx/xray/hk.pem", NotAfter: &near, DaysLeft: 5},
		},
	}
	task.raiseNodeAlerts(n, warn, now)

	keys := rec.alertKeys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], string(model.KindCertExpiring)) {
		t.Fatalf("该报一条证书临期,实际 %v", keys)
	}

	// 同一张证书换成了长期的:那张纸的告警该销 —— 销的是"这张证书"而不是
	// "有证书这件事"(所以 Key 里带路径)。
	far := now.Add(100 * 24 * time.Hour)
	ok := model.NodeStatus{
		NodeID: n.ID, Reachable: true, Active: true, ObservedHash: n.DeployedHash,
		Certificates: []model.CertificateInfo{
			{InboundID: 1, InboundName: "vmess-ws", Path: "/etc/mpx/xray/hk.pem", NotAfter: &far, DaysLeft: 100},
		},
	}
	task.raiseNodeAlerts(n, ok, now.Add(time.Minute))

	// 报的时候用的是证书路径拼的 Key,销的时候也必须是同一个 —— 这条断言就是
	// 为了钉住"报"和"销"拼的是同一个字符串。
	resolved := rec.resolvedKeys()
	if len(resolved) != 1 || resolved[0] != keys[0] {
		t.Fatalf("证书不临期了该销掉 %q,实际销了 %v", keys[0], resolved)
	}
}

// 连不上时,服务在不在跑、配置对不对、证书还有几天全都无从得知。这时只报
// "连不上",而且**不能**把上一轮报过的东西销掉 —— "看不到"不是"好了"。
func TestRaiseNodeAlertsKeepsWhatItCouldNotLookAt(t *testing.T) {
	task, rec := newAlertTask(t)
	n := deployedNode()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

	near := now.Add(2 * 24 * time.Hour)
	bad := model.NodeStatus{
		NodeID: n.ID, Reachable: true, Active: false,
		ObservedHash: strings.Repeat("cd", 32),
		Certificates: []model.CertificateInfo{
			{InboundID: 1, InboundName: "vmess-ws", Path: "/etc/mpx/xray/hk.pem", NotAfter: &near, DaysLeft: 2},
		},
	}
	task.raiseNodeAlerts(n, bad, now)
	if got := len(rec.alertKeys()); got != 3 {
		t.Fatalf("服务没跑 + 漂移 + 证书临期该报三条,实际 %d 条:%v", got, rec.alertKeys())
	}

	down := model.NodeStatus{NodeID: n.ID, Reachable: false, Error: "ssh: connect timed out"}
	task.raiseNodeAlerts(n, down, now.Add(time.Minute))

	if got := rec.resolvedKeys(); len(got) != 0 {
		t.Fatalf("连不上只是没看到,不该销任何东西,实际销了 %v", got)
	}
	keys := rec.alertKeys()
	if len(keys) != 4 || !strings.HasPrefix(keys[3], string(model.KindNodeUnreachable)) {
		t.Fatalf("该多报一条不可达,实际 %v", keys)
	}

	// 恢复之后一切正常:三条连同"不可达"一起销掉。证书在这里要给一个远期
	// 到期时间 —— 只写个空的 CertificateInfo 不是"正常",那是"读不到到期时间",
	// 本身就要报。
	far := now.Add(200 * 24 * time.Hour)
	good := model.NodeStatus{
		NodeID: n.ID, Reachable: true, Active: true, ObservedHash: n.DeployedHash,
		Certificates: []model.CertificateInfo{
			{InboundID: 1, InboundName: "vmess-ws", Path: "/etc/mpx/xray/hk.pem", NotAfter: &far, DaysLeft: 200},
		},
	}
	task.raiseNodeAlerts(n, good, now.Add(2*time.Minute))

	resolved := rec.resolvedKeys()
	if len(resolved) != 4 {
		t.Fatalf("恢复后该销掉四条,实际 %d 条:%v", len(resolved), resolved)
	}
	for _, want := range keys {
		found := false
		for _, got := range resolved {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("恢复后没销掉 %q,实际销了 %v", want, resolved)
		}
	}
}

// 巡检的排期:第一次(控制面刚起来)就查,之后每 sweepEvery 轮一次。
func TestSweepRunsOnTheFirstTickThenEveryTenth(t *testing.T) {
	c, st := newTestControl(t, nil)
	fake := &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: strings.Join([]string{
		"k=service:active",
		"k=version:26.3.27",
		"k=hash:" + strings.Repeat("ab", 32),
	}, "\n")}}}
	c.runner = fake
	n := addNode(t, st, "hk", "203.0.113.10")
	task := NewTask(c, st, slog.New(slog.DiscardHandler), time.Minute)
	now := time.Now()

	task.ticks = 1
	task.sweepNodeStatus(context.Background(), []model.Node{n}, now)
	if !fake.ran("XRAY_BIN=") {
		t.Fatal("第一轮就该查一次:刚起来的控制面不该让 node list 空着五分钟")
	}

	// 第二次到第九次都不该查。
	for _, ticks := range []uint64{2, 5, 9} {
		before := len(fake.callList())
		task.ticks = ticks
		task.sweepNodeStatus(context.Background(), []model.Node{n}, now)
		if got := len(fake.callList()); got != before {
			t.Fatalf("第 %d 轮不该查,实际多发了 %d 条命令", ticks, got-before)
		}
	}

	before := len(fake.callList())
	task.ticks = sweepEvery
	task.sweepNodeStatus(context.Background(), []model.Node{n}, now)
	if got := len(fake.callList()); got != before+1 {
		t.Fatalf("第 %d 轮该查一次,实际多了 %d 条", sweepEvery, got-before)
	}
}

// 整轮的预算用完时,剩下的节点留到下一轮,不再往下发命令。
func TestSweepStopsWhenTheRoundIsOver(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	fake := &fakeRunner{replies: []reply{{keyword: "XRAY_BIN=", out: "k=service:active"}}}
	c, st := newLoggingControl(t, fake, logger)
	n := addNode(t, st, "hk", "203.0.113.10")
	task := NewTask(c, st, logger, time.Minute)
	task.ticks = 1

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task.sweepNodeStatus(ctx, []model.Node{n}, time.Now())

	if fake.ran("XRAY_BIN=") {
		t.Fatal("轮次预算已经用完,不该再发命令")
	}
	if !strings.Contains(buf.String(), "这一轮巡检超时") {
		t.Fatalf("该说一句这一轮没查完,日志里没有:%s", buf.String())
	}
}

// LogAlerter 的两条规矩:同一个 Key 冷却期内只报一次;报过之后才谈得上恢复,
// 而恢复会清掉冷却 —— 条件消失之后再出现是一次新故障,该立刻报。
func TestLogAlerterCoolsDownAndResolves(t *testing.T) {
	var buf bytes.Buffer
	a := NewLogAlerter(slog.New(slog.NewTextHandler(&buf, nil)))
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	al := model.Alert{
		Key: "cert_expiring-1-abcd1234", Kind: model.KindCertExpiring,
		NodeID: 1, NodeName: "hk", Message: "节点 hk:证书还有 5 天到期",
	}

	count := func() (int, int) {
		return strings.Count(buf.String(), "节点告警:"), strings.Count(buf.String(), "节点告警已恢复")
	}

	al.At = now
	a.Alert(al)
	al.At = now.Add(time.Minute)
	a.Alert(al) // 冷却之内
	if reported, _ := count(); reported != 1 {
		t.Fatalf("冷却期内该只报一次,实际 %d 次", reported)
	}

	al.At = now.Add(alertCooldown)
	a.Alert(al)
	if reported, _ := count(); reported != 2 {
		t.Fatalf("冷却过了该再报一次,实际 %d 次", reported)
	}

	// 没报过的 Key 上来 Resolve 什么都不该打:只有真的报过才谈得上恢复。
	a.Resolve("cert_expiring-1-ffffffff", now)
	if _, recovered := count(); recovered != 0 {
		t.Fatal("没报过的 Key 不该有恢复")
	}
	a.Resolve(al.Key, now)
	if _, recovered := count(); recovered != 1 {
		t.Fatal("报过的 Key 该有一条恢复")
	}

	// 恢复清掉了冷却:同一件事马上再出现,不该被上一轮的冷却摁住。
	al.At = now
	a.Alert(al)
	if reported, _ := count(); reported != 3 {
		t.Fatalf("恢复后再出现该立刻报,实际报了 %d 次", reported)
	}
}
