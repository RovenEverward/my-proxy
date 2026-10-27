package control

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// webhookSink 是一个收 webhook 的假服务端:把每条事件记下来,并按 status 决定
// 怎么应答 —— 用 status 模拟"对面没接住"。
type webhookSink struct {
	mu     sync.Mutex
	events []WebhookEvent
	status int
}

func (s *webhookSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var ev WebhookEvent
	_ = json.NewDecoder(r.Body).Decode(&ev)
	s.mu.Lock()
	s.events = append(s.events, ev)
	code := s.status
	s.mu.Unlock()
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
}

func (s *webhookSink) got() []WebhookEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]WebhookEvent(nil), s.events...)
}

func (s *webhookSink) fail(code int) {
	s.mu.Lock()
	s.status = code
	s.mu.Unlock()
}

// newNotifyTask 造一个把 webhook 指向假服务端的后台任务。阈值用默认的
// 到期前 3 天 / 用量 80%。
func newNotifyTask(t *testing.T, sink *webhookSink) (*Task, *store.Store) {
	t.Helper()
	c, st := newTestControl(t, nil)
	task := NewTask(c, st, slog.New(slog.DiscardHandler), time.Minute)

	srv := httptest.NewServer(sink)
	t.Cleanup(srv.Close)
	allowWebhookIP = func(ip net.IP) bool { return ip.IsLoopback() }
	t.Cleanup(func() { allowWebhookIP = nil })

	s, err := st.GetSettings()
	if err != nil {
		t.Fatalf("读设置: %v", err)
	}
	s.WebhookURL = srv.URL
	if err := st.SaveSettings(s); err != nil {
		t.Fatalf("存设置: %v", err)
	}
	return task, st
}

func notifyNow() time.Time {
	return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
}

func TestNotifyUsersSendsExpiringNoticeOnce(t *testing.T) {
	sink := &webhookSink{}
	task, st := newNotifyTask(t, sink)
	now := notifyNow()

	exp := now.Add(2 * 24 * time.Hour)
	u := activeUser("alice")
	u.ExpireAt = &exp
	u = addUser(t, st, u)

	task.notifyUsers(context.Background(), now)

	evs := sink.got()
	if len(evs) != 1 {
		t.Fatalf("想要 1 条通知,得到 %d 条: %+v", len(evs), evs)
	}
	if evs[0].Event != EventUserNotice || evs[0].Kind != model.NoticeKindExpiring {
		t.Errorf("事件不对: %+v", evs[0])
	}
	if evs[0].Username != "alice" {
		t.Errorf("用户名不对: %q", evs[0].Username)
	}
	if sent, err := st.UserNoticeSent(u.ID, model.NoticeKindExpiring); err != nil || !sent {
		t.Errorf("库里没记下已发: sent=%v err=%v", sent, err)
	}

	// 条件没变,下一轮不该重发 —— 这正是记 user_notices 的意义。
	task.notifyUsers(context.Background(), now.Add(time.Minute))
	if got := len(sink.got()); got != 1 {
		t.Errorf("同一件事重发了,共 %d 条", got)
	}
}

func TestNotifyUsersForgetsExpiringNoticeAfterRenewal(t *testing.T) {
	sink := &webhookSink{}
	task, st := newNotifyTask(t, sink)
	now := notifyNow()

	exp := now.Add(2 * 24 * time.Hour)
	u := activeUser("alice")
	u.ExpireAt = &exp
	u = addUser(t, st, u)

	task.notifyUsers(context.Background(), now)

	// 续费一年:条件消失,记录该被销掉,否则下次临近到期再也发不出来。
	far := now.Add(365 * 24 * time.Hour)
	u.ExpireAt = &far
	if err := st.UpdateUser(&u); err != nil {
		t.Fatalf("续费: %v", err)
	}
	task.notifyUsers(context.Background(), now.Add(time.Minute))
	if sent, err := st.UserNoticeSent(u.ID, model.NoticeKindExpiring); err != nil || sent {
		t.Errorf("续费后已发记录还在: sent=%v err=%v", sent, err)
	}
	if got := len(sink.got()); got != 1 {
		t.Errorf("续费不该产生通知,共 %d 条", got)
	}

	// 又临近到期,应该重新发一条。
	near := now.Add(2 * 24 * time.Hour)
	u.ExpireAt = &near
	if err := st.UpdateUser(&u); err != nil {
		t.Fatalf("改回临近: %v", err)
	}
	task.notifyUsers(context.Background(), now.Add(2*time.Minute))
	if got := len(sink.got()); got != 2 {
		t.Errorf("续费后再临近应重发,共 %d 条", got)
	}
}

func TestNotifyUsersSendsOverUsageAndForgetsAfterReset(t *testing.T) {
	sink := &webhookSink{}
	task, st := newNotifyTask(t, sink)
	now := notifyNow()

	u := activeUser("bob")
	u.DataLimit = 1000
	u.UsedTraffic = 900 // 90% ≥ 80%
	u = addUser(t, st, u)

	task.notifyUsers(context.Background(), now)

	evs := sink.got()
	if len(evs) != 1 || evs[0].Kind != model.NoticeKindOverUsage {
		t.Fatalf("想要 1 条超限通知,得到 %+v", evs)
	}

	// 周期重置把用量打下去:条件消失,记录该销。
	u.UsedTraffic = 10
	if err := st.UpdateUser(&u); err != nil {
		t.Fatalf("重置用量: %v", err)
	}
	task.notifyUsers(context.Background(), now.Add(time.Minute))
	if sent, err := st.UserNoticeSent(u.ID, model.NoticeKindOverUsage); err != nil || sent {
		t.Errorf("重置后已发记录还在: sent=%v err=%v", sent, err)
	}

	// 新周期又用满,应该再发一条。
	u.UsedTraffic = 950
	if err := st.UpdateUser(&u); err != nil {
		t.Fatalf("再用满: %v", err)
	}
	task.notifyUsers(context.Background(), now.Add(2*time.Minute))
	if got := len(sink.got()); got != 2 {
		t.Errorf("新周期超限应重发,共 %d 条", got)
	}
}

func TestNotifyUsersDoesNothingWithoutWebhook(t *testing.T) {
	sink := &webhookSink{}
	task, st := newNotifyTask(t, sink)
	now := notifyNow()

	s, err := st.GetSettings()
	if err != nil {
		t.Fatalf("读设置: %v", err)
	}
	s.WebhookURL = ""
	if err := st.SaveSettings(s); err != nil {
		t.Fatalf("清掉地址: %v", err)
	}

	exp := now.Add(24 * time.Hour)
	u := activeUser("alice")
	u.ExpireAt = &exp
	u = addUser(t, st, u)

	task.notifyUsers(context.Background(), now)

	if got := len(sink.got()); got != 0 {
		t.Errorf("没配地址不该发,却发了 %d 条", got)
	}
	// 也不该记账:没有地址时"已发"是假话,记下来之后配好地址也发不出去。
	if sent, err := st.UserNoticeSent(u.ID, model.NoticeKindExpiring); err != nil || sent {
		t.Errorf("没配地址却记了账: sent=%v err=%v", sent, err)
	}
}

func TestNotifyUsersRetriesWhenDeliveryFails(t *testing.T) {
	sink := &webhookSink{}
	task, st := newNotifyTask(t, sink)
	now := notifyNow()

	exp := now.Add(24 * time.Hour)
	u := activeUser("alice")
	u.ExpireAt = &exp
	u = addUser(t, st, u)

	sink.fail(http.StatusInternalServerError)
	task.notifyUsers(context.Background(), now)
	if sent, err := st.UserNoticeSent(u.ID, model.NoticeKindExpiring); err != nil || sent {
		t.Errorf("投递失败不该记账: sent=%v err=%v", sent, err)
	}

	// 对面恢复:下一轮重新发,并且这次记上账。
	sink.fail(0)
	task.notifyUsers(context.Background(), now.Add(time.Minute))
	if sent, err := st.UserNoticeSent(u.ID, model.NoticeKindExpiring); err != nil || !sent {
		t.Errorf("投递成功应该记账: sent=%v err=%v", sent, err)
	}
	if got := len(sink.got()); got != 2 {
		t.Errorf("失败后应重试,共 %d 次投递", got)
	}
}

func TestNotifyUsersSkipsDisabledUsers(t *testing.T) {
	sink := &webhookSink{}
	task, st := newNotifyTask(t, sink)
	now := notifyNow()

	exp := now.Add(24 * time.Hour)
	u := activeUser("alice")
	u.ExpireAt = &exp
	u.Enabled = false
	addUser(t, st, u)

	task.notifyUsers(context.Background(), now)
	if got := len(sink.got()); got != 0 {
		t.Errorf("停用的用户不该被通知,却发了 %d 条", got)
	}
}
