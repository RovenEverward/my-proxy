package cli

import (
	"net/http"
	"strings"
	"testing"
)

// ruleset status 的表结构:表头两格式并列、文件名列出、状态列按
// 刷新中 > 有错误 > 就绪数 的优先级给一句话。
func TestRulesetStatusTable(t *testing.T) {
	startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.method != http.MethodGet || r.path != "/api/rulesets" {
			return false
		}
		_, _ = w.Write([]byte(`[
		  {"key":"lan","name":"局域网","private_ip":true,
		    "clash":{"files":[{"name":"lan","url":"https://u/lan.yaml","ready":true}],"ready":1,"total":1},
		    "sing_box":{"files":[{"name":"lan","url":"https://u/lan.srs","ready":true}],"ready":1,"total":1},
		    "refreshing":false,"last_attempt_at":1763203200000},
		  {"key":"ai","name":"AI 服务",
		    "clash":{"files":[{"name":"ai","url":"https://u/ai.yaml","ready":false,"last_error":"拉取超时"}],"ready":0,"total":1},
		    "sing_box":{"files":[],"ready":0,"total":0},
		    "refreshing":true,"last_attempt_at":1763213415000}
		]`))
		return true
	})

	out := captureStdout(t, func() { runSource(t, "ruleset", "status") })

	for _, want := range []string{"组 KEY", "组名", "文件", "就绪", "上次尝试", "状态",
		"lan", "clash: lan", "sing-box: lan", "clash 1/1", "就绪",
		"ai", "ai(错)", "刷新中", "11-15"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里缺少 %q:\n%s", want, out)
		}
	}
	// 出错文件要挂标记,就绪列给出两侧各自的比例。
	if strings.Contains(out, "lan.yaml") {
		t.Errorf("表格模式不该把上游 URL 打出来:\n%s", out)
	}
}

// ruleset-mode set 的 MODE 在本地拦:不在 upstream/privatized 就直接报错,
// 不发给控制面(枚举在 model.RulesetModeValid,与服务端同源)。
func TestRulesetModeSetRejectsBadMode(t *testing.T) {
	p := startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		t.Errorf("非法 MODE 不该发出任何请求,收到 %s %s", r.method, r.path)
		return false
	})

	err := runSourceErr(t, "settings", "ruleset-mode", "set", "mirror")
	if err == nil {
		t.Fatal("非法 MODE 应报错")
	}
	if !strings.Contains(err.Error(), "upstream 或 privatized") {
		t.Errorf("错误要指出合法取值: %v", err)
	}
	_ = p
}

// ruleset-mode set 的合法路径:读-改-写,ruleset_mode 落进 PUT 的整份设置里,
// 其它字段原样回传。
func TestRulesetModeSetPreservesOtherSettings(t *testing.T) {
	var putBody string
	startPlane(t, func(w http.ResponseWriter, r recordedReq) bool {
		if r.path != "/api/settings" {
			return false
		}
		switch r.method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"sub_url_prefix":"https://sub.example.com/","profile_title":"我的订阅","update_interval":24,"ruleset_mode":""}`))
		case http.MethodPut:
			putBody = r.body
			_, _ = w.Write([]byte(r.body))
		default:
			return false
		}
		return true
	})

	out := captureStdout(t, func() { runSource(t, "settings", "ruleset-mode", "set", "privatized") })

	if !strings.Contains(out, "已设置规则集分发方式: privatized") {
		t.Errorf("缺少回执:\n%s", out)
	}
	if !strings.Contains(putBody, `"ruleset_mode":"privatized"`) {
		t.Errorf("PUT 里没有新值:\n%s", putBody)
	}
	if !strings.Contains(putBody, "我的订阅") || !strings.Contains(putBody, "https://sub.example.com/") {
		t.Errorf("整体替换把别的设置弄丢了:\n%s", putBody)
	}
}
