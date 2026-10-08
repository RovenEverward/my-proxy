package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// settingsShow 打出来的三行通知设置,值要过得去 —— 空值和 0 都得说人话,
// 不能把"关"显示成"0 天"。
func TestSettingsShowPrintsNotifyRows(t *testing.T) {
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/settings" {
			t.Errorf("意外的请求: %s %s", r.Method, r.URL.Path)
		}
		writeSettings(t, w, model.Settings{
			SubURLPrefix:       "https://sub.example.com/",
			WebhookURL:         "https://hook.example.com/mpx",
			NotifyDaysLeft:     5,
			NotifyUsagePercent: 90,
		})
	})

	saved := opts.output
	opts.output = outputTable
	t.Cleanup(func() { opts.output = saved })

	out := captureStdout(t, func() { runRoot(t, "settings", "show") })

	for _, want := range []string{
		"通知地址", "https://hook.example.com/mpx",
		"到期提醒", "到期前 5 天",
		"用量提醒", "用量达到 90%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里缺少 %q:\n%s", want, out)
		}
	}
}

func TestSettingsShowSaysOffWhenUnset(t *testing.T) {
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		writeSettings(t, w, model.Settings{})
	})

	saved := opts.output
	opts.output = outputTable
	t.Cleanup(func() { opts.output = saved })

	out := captureStdout(t, func() { runRoot(t, "settings", "show") })

	for _, want := range []string{"未配置(不发通知)", "关"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里缺少 %q:\n%s", want, out)
		}
	}
}

func TestSettingsTestCommandPosts(t *testing.T) {
	var gotMethod, gotPath string
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`{"webhook":"https://hook.example.com/mpx","sent":true}`))
	})

	saved := opts.output
	opts.output = outputTable
	t.Cleanup(func() { opts.output = saved })

	out := captureStdout(t, func() { runRoot(t, "settings", "test") })

	if gotMethod != http.MethodPost || gotPath != "/api/settings/test-webhook" {
		t.Errorf("请求打到了 %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(out, "测试通知已送到") {
		t.Errorf("输出里没有回执:\n%s", out)
	}
}

func TestSettingsTestCommandReportsServerError(t *testing.T) {
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"还没有配置 webhook 地址"}`))
	})

	rootCmd.SetArgs([]string{"settings", "test"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("控制面报错时命令应该失败")
	}
	if !strings.Contains(err.Error(), "还没有配置 webhook 地址") {
		t.Errorf("错误信息应原样带上服务端的说明,得到 %v", err)
	}
}

// writeSettings 把设置按控制面的格式写回去。
func writeSettings(t *testing.T, w http.ResponseWriter, s model.Settings) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(s); err != nil {
		t.Errorf("写设置响应: %v", err)
	}
}
