package control

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func newWebhookServer(t *testing.T, sink *webhookSink) string {
	t.Helper()
	srv := httptest.NewServer(sink)
	t.Cleanup(srv.Close)
	// httptest 挂回环;生产会拒,测试放开。
	allowWebhookIP = func(ip net.IP) bool { return ip.IsLoopback() }
	t.Cleanup(func() { allowWebhookIP = nil })
	return srv.URL
}

func TestPostWebhookReportsNon2xxAndEmptyURL(t *testing.T) {
	sink := &webhookSink{}
	url := newWebhookServer(t, sink)

	if err := PostWebhook(context.Background(), url, WebhookEvent{Event: EventTest}, nil); err != nil {
		t.Fatalf("2xx 应该算成功: %v", err)
	}
	if got := len(sink.got()); got != 1 {
		t.Fatalf("对面该收到 1 条,收到 %d 条", got)
	}

	sink.fail(http.StatusBadGateway)
	err := PostWebhook(context.Background(), url, WebhookEvent{Event: EventTest}, nil)
	if err == nil || !strings.Contains(err.Error(), "webhook 返回 502") {
		t.Errorf("非 2xx 应该报错并带上状态码,得到 %v", err)
	}

	if err := PostWebhook(context.Background(), "", WebhookEvent{Event: EventTest}, nil); err == nil {
		t.Error("空地址应该报错")
	}
}

func TestWebhookAlerterCoolsDownAndResolves(t *testing.T) {
	sink := &webhookSink{}
	url := newWebhookServer(t, sink)
	a := NewWebhookAlerter(func() string { return url }, nil)

	al := model.Alert{
		Key: "node_drift-7-abcd", Kind: model.KindNodeDrift,
		NodeID: 7, NodeName: "hk", Message: "节点 hk:配置不一样",
		At: time.Now(),
	}
	a.Alert(al)
	a.Alert(al) // 冷却期内,不该再发

	evs := sink.got()
	if len(evs) != 1 {
		t.Fatalf("冷却期内应该只发 1 条,发了 %d 条", len(evs))
	}
	if evs[0].Event != EventAlert || evs[0].Kind != string(model.KindNodeDrift) {
		t.Errorf("告警事件不对: %+v", evs[0])
	}
	if evs[0].Key != al.Key || evs[0].NodeName != "hk" {
		t.Errorf("告警事件缺了定位信息: %+v", evs[0])
	}

	// 恢复:一条 resolved,并且带得上当初报的是什么。
	a.Resolve(al.Key, time.Now())
	evs = sink.got()
	if len(evs) != 2 || evs[1].Event != EventResolved {
		t.Fatalf("该发 1 条恢复,得到 %+v", evs)
	}
	if evs[1].Kind != string(model.KindNodeDrift) || !strings.Contains(evs[1].Message, "已恢复") {
		t.Errorf("恢复事件没带回原告警的信息: %+v", evs[1])
	}

	// 没报过的 Key 上来恢复,什么都不做。
	a.Resolve("从未报过的-key", time.Now())
	if got := len(sink.got()); got != 2 {
		t.Errorf("没报过的 Key 不该发恢复,现在共 %d 条", got)
	}
}

func TestWebhookAlerterStaysQuietWithoutURL(t *testing.T) {
	a := NewWebhookAlerter(func() string { return "" }, nil)
	a.Alert(model.Alert{Key: "k", Kind: model.KindNodeInactive, At: time.Now()})
	a.Resolve("k", time.Now()) // 不 panic、不发,就够了
}

func TestFanoutAlerterGoesToEveryOutlet(t *testing.T) {
	first, second := &recordingAlerter{}, &recordingAlerter{}
	f := NewFanoutAlerter(first, nil, second) // nil 出口被丢掉

	al := model.Alert{Key: "k1", Kind: model.KindCertExpiring, At: time.Now()}
	f.Alert(al)
	f.Resolve("k1", time.Now())

	if got := first.alertKeys(); len(got) != 1 || got[0] != "k1" {
		t.Errorf("第一个出口没收到: %v", got)
	}
	if got := second.alertKeys(); len(got) != 1 || got[0] != "k1" {
		t.Errorf("第二个出口没收到: %v", got)
	}
	if got := first.resolvedKeys(); len(got) != 1 || got[0] != "k1" {
		t.Errorf("第一个出口没收到恢复: %v", got)
	}
	if got := second.resolvedKeys(); len(got) != 1 || got[0] != "k1" {
		t.Errorf("第二个出口没收到恢复: %v", got)
	}
}
