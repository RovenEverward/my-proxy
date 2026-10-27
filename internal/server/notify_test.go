package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// hookSink 是订阅通知用的假 webhook:记下收到的事件,按 status 应答。
type hookSink struct {
	mu     sync.Mutex
	events []control.WebhookEvent
	status int
}

func (s *hookSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var ev control.WebhookEvent
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

func (s *hookSink) got() []control.WebhookEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]control.WebhookEvent(nil), s.events...)
}

func TestSaveSettingsValidatesNotifyFields(t *testing.T) {
	e := newEnv(t)

	cases := []struct {
		name string
		set  model.Settings
	}{
		{"到期提醒天数为负", model.Settings{NotifyDaysLeft: -1}},
		{"用量百分比超 100", model.Settings{NotifyUsagePercent: 101}},
		{"用量百分比为负", model.Settings{NotifyUsagePercent: -1}},
		{"地址不是 URL", model.Settings{WebhookURL: "http://%zz"}},
		{"地址不是 http(s)", model.Settings{WebhookURL: "ftp://example.com/hook"}},
		{"地址缺主机名", model.Settings{WebhookURL: "http://"}},
		{"地址是回环", model.Settings{WebhookURL: "http://127.0.0.1/hook"}},
		{"地址是内网", model.Settings{WebhookURL: "http://10.0.0.5/hook"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, data := e.api("PUT", "/api/settings", c.set); code != http.StatusBadRequest {
				t.Fatalf("应 400,实际 %d %s", code, data)
			}
		})
	}

	t.Run("关掉提醒和留空地址都合法", func(t *testing.T) {
		if code, data := e.api("PUT", "/api/settings", model.Settings{
			WebhookURL: "", NotifyDaysLeft: 0, NotifyUsagePercent: 0,
		}); code != http.StatusOK {
			t.Fatalf("应 200,实际 %d %s", code, data)
		}
	})

	t.Run("正常值存得住", func(t *testing.T) {
		want := model.Settings{
			WebhookURL:         "https://hook.example.com/mpx",
			NotifyDaysLeft:     5,
			NotifyUsagePercent: 90,
		}
		if code, data := e.api("PUT", "/api/settings", want); code != http.StatusOK {
			t.Fatalf("存设置: got %d %s", code, data)
		}
		code, data := e.api("GET", "/api/settings", nil)
		if code != http.StatusOK {
			t.Fatalf("读设置: got %d %s", code, data)
		}
		var got model.Settings
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("解析设置: %v", err)
		}
		if got.WebhookURL != want.WebhookURL || got.NotifyDaysLeft != want.NotifyDaysLeft ||
			got.NotifyUsagePercent != want.NotifyUsagePercent {
			t.Errorf("读回来的通知设置不对: %+v", got)
		}
	})
}

func TestTestWebhookEndpoint(t *testing.T) {
	control.SetAllowWebhookIPForTest(func(ip net.IP) bool { return ip.IsLoopback() })
	t.Cleanup(func() { control.SetAllowWebhookIPForTest(nil) })

	t.Run("没配地址就直说", func(t *testing.T) {
		e := newEnv(t)
		code, data := e.api("POST", "/api/settings/test-webhook", nil)
		if code != http.StatusBadRequest {
			t.Fatalf("应 400,实际 %d %s", code, data)
		}
		if !strings.Contains(string(data), "还没有配置 webhook 地址") {
			t.Errorf("错误说明不对: %s", data)
		}
	})

	t.Run("配好了就真发一条", func(t *testing.T) {
		e := newEnv(t)
		sink := &hookSink{}
		srv := httptest.NewServer(sink)
		t.Cleanup(srv.Close)

		if code, data := e.api("PUT", "/api/settings", model.Settings{WebhookURL: srv.URL}); code != http.StatusOK {
			t.Fatalf("存设置: got %d %s", code, data)
		}
		code, data := e.api("POST", "/api/settings/test-webhook", nil)
		if code != http.StatusOK {
			t.Fatalf("应 200,实际 %d %s", code, data)
		}
		evs := sink.got()
		if len(evs) != 1 || evs[0].Event != control.EventTest {
			t.Fatalf("对面收到的事件不对: %+v", evs)
		}
	})

	t.Run("对面没接住要如实报 502", func(t *testing.T) {
		e := newEnv(t)
		sink := &hookSink{status: http.StatusBadGateway}
		srv := httptest.NewServer(sink)
		t.Cleanup(srv.Close)

		if code, data := e.api("PUT", "/api/settings", model.Settings{WebhookURL: srv.URL}); code != http.StatusOK {
			t.Fatalf("存设置: got %d %s", code, data)
		}
		code, data := e.api("POST", "/api/settings/test-webhook", nil)
		if code != http.StatusBadGateway {
			t.Fatalf("应 502,实际 %d %s", code, data)
		}
		if !strings.Contains(string(data), "webhook 没送到") {
			t.Errorf("错误说明不对: %s", data)
		}
	})
}
