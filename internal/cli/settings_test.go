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

// ---- settings rules ----

// fakeSettingsStore 是一个记得住设置的假控制面:GET 回当前那份,PUT 收下新的。
// 规则的子命令都是读-改-写,所以必须两边都接才测得出真正发出去的是什么。
func fakeSettingsStore(t *testing.T, initial model.Settings) *model.Settings {
	t.Helper()
	saved := initial
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/settings" {
			t.Errorf("意外的请求: %s %s", r.Method, r.URL.Path)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeSettings(t, w, saved)
		case http.MethodPut:
			var got model.Settings
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("解请求体: %v", err)
				return
			}
			saved = got
			writeSettings(t, w, saved)
		default:
			t.Errorf("意外的方法: %s", r.Method)
		}
	})
	return &saved
}

// 这一条盯住最要紧的约定:改规则是整表 PUT,所以必须先把当前设置拉下来、
// 只改规则那一段,否则模板和通知配置会一起被清掉。
func TestSettingsRulesAddKeepsOtherSettings(t *testing.T) {
	saved := fakeSettingsStore(t, model.Settings{
		SubURLPrefix:       "https://sub.example.com",
		ProfileTitle:       "我的订阅",
		WebhookURL:         "https://hook.example.com/mpx",
		NotifyDaysLeft:     7,
		NotifyUsagePercent: 55,
		Templates:          map[string]string{"clash-meta": "proxies: []\n"},
		Rules:              []model.SubscriptionRule{{Pattern: "旧的", Result: model.FormatXray}},
	})

	useTableOutput(t)
	runRoot(t, "settings", "rules", "add", `(?i)我的客户端`, "--result", "sing-box")

	if len(saved.Rules) != 2 {
		t.Fatalf("规则应当追加成 2 条,实际 %#v", saved.Rules)
	}
	if saved.Rules[1].Pattern != `(?i)我的客户端` || saved.Rules[1].Result != model.FormatSingBox {
		t.Errorf("追加的规则不对: %#v", saved.Rules[1])
	}
	// 其余字段一个都不能丢。
	if saved.SubURLPrefix != "https://sub.example.com" || saved.ProfileTitle != "我的订阅" {
		t.Errorf("订阅前缀/标题被改掉了: %+v", saved)
	}
	if saved.WebhookURL != "https://hook.example.com/mpx" {
		t.Errorf("通知地址被清掉了: %q", saved.WebhookURL)
	}
	if saved.NotifyDaysLeft != 7 || saved.NotifyUsagePercent != 55 {
		t.Errorf("通知阈值被改掉了: %d / %d", saved.NotifyDaysLeft, saved.NotifyUsagePercent)
	}
	if saved.Templates["clash-meta"] != "proxies: []\n" {
		t.Errorf("模板被清掉了: %#v", saved.Templates)
	}
}

func TestSettingsRulesAddAtIndex(t *testing.T) {
	saved := fakeSettingsStore(t, model.Settings{
		Rules: []model.SubscriptionRule{
			{Pattern: "a", Result: model.FormatClash},
			{Pattern: "b", Result: model.FormatClash},
		},
	})

	useTableOutput(t)
	runRoot(t, "settings", "rules", "add", "z", "-r", "xray", "--index", "1")

	want := []string{"z", "a", "b"}
	if len(saved.Rules) != len(want) {
		t.Fatalf("规则条数: got %d want %d", len(saved.Rules), len(want))
	}
	for i, p := range want {
		if saved.Rules[i].Pattern != p {
			t.Fatalf("第 %d 条应为 %q,实际 %q (%+v)", i+1, p, saved.Rules[i].Pattern, saved.Rules)
		}
	}
}

// 没有 --index 时是追加,而不是插到第 0 位 —— 用空串表示"没给"就是为了这个。
func TestSettingsRulesAddWithoutIndexAppends(t *testing.T) {
	saved := fakeSettingsStore(t, model.Settings{
		Rules: []model.SubscriptionRule{{Pattern: "a", Result: model.FormatClash}},
	})

	useTableOutput(t)
	runRoot(t, "settings", "rules", "add", "最后", "-r", "links")

	if len(saved.Rules) != 2 || saved.Rules[1].Pattern != "最后" {
		t.Fatalf("应当追加到最后,实际 %+v", saved.Rules)
	}
}

func TestSettingsRulesAddRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"没给 result", []string{"settings", "rules", "add", "x"}, "必须给 --result"},
		{"result 不认识", []string{"settings", "rules", "add", "x", "-r", "nonsense"}, "不支持的结果"},
		{"正则编不过", []string{"settings", "rules", "add", "([", "-r", "clash"}, "正则不合法"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeSettingsStore(t, model.Settings{})
			useTableOutput(t)

			err := runRootErr(t, tc.args...)
			if err == nil {
				t.Fatal("应当报错")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误信息里应有 %q,得到 %v", tc.want, err)
			}
		})
	}
}

func TestSettingsRulesRmAndReset(t *testing.T) {
	t.Run("rm 删掉指定那条", func(t *testing.T) {
		saved := fakeSettingsStore(t, model.Settings{
			Rules: []model.SubscriptionRule{
				{Pattern: "a", Result: model.FormatClash},
				{Pattern: "b", Result: model.FormatXray},
			},
		})
		useTableOutput(t)
		runRoot(t, "settings", "rules", "rm", "1")

		if len(saved.Rules) != 1 || saved.Rules[0].Pattern != "b" {
			t.Fatalf("删错了: %+v", saved.Rules)
		}
	})

	t.Run("序号越界要报清楚", func(t *testing.T) {
		fakeSettingsStore(t, model.Settings{
			Rules: []model.SubscriptionRule{{Pattern: "a", Result: model.FormatClash}},
		})
		useTableOutput(t)

		err := runRootErr(t, "settings", "rules", "rm", "9")
		if err == nil || !strings.Contains(err.Error(), "超出范围") {
			t.Fatalf("越界应当报错,得到 %v", err)
		}
	})

	t.Run("reset 把规则清空", func(t *testing.T) {
		saved := fakeSettingsStore(t, model.Settings{
			Rules: []model.SubscriptionRule{{Pattern: "a", Result: model.FormatClash}},
		})
		useTableOutput(t)
		runRoot(t, "settings", "rules", "reset")

		if len(saved.Rules) != 0 {
			t.Fatalf("reset 后规则应为空(靠服务端回落到内置),实际 %+v", saved.Rules)
		}
	})
}

// move 的落点是"挪完之后它排第几",往前挪往后挪都是这个读法。这里把两个方向
// 都钉住 —— 之前的实现把后移的落点又减了一次,2→3 会变成原地不动。
func TestSettingsRulesMoveSemantics(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		want   []string
		reject string
	}{
		{"后移:1→4", []string{"1", "4"}, []string{"b", "c", "d", "a"}, ""},
		{"前移:4→2", []string{"4", "2"}, []string{"a", "d", "b", "c"}, ""},
		{"相邻后移:2→3", []string{"2", "3"}, []string{"a", "c", "b", "d"}, ""},
		{"相邻前移:3→2", []string{"3", "2"}, []string{"a", "c", "b", "d"}, ""},
		{"挪到自己那格", []string{"2", "2"}, nil, "已经在第 2 位了"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saved := fakeSettingsStore(t, model.Settings{
				Rules: []model.SubscriptionRule{
					{Pattern: "a", Result: model.FormatClash},
					{Pattern: "b", Result: model.FormatClash},
					{Pattern: "c", Result: model.FormatClash},
					{Pattern: "d", Result: model.FormatClash},
				},
			})
			useTableOutput(t)

			args := append([]string{"settings", "rules", "move"}, tc.args...)
			err := runRootErr(t, args...)

			if tc.reject != "" {
				if err == nil || !strings.Contains(err.Error(), tc.reject) {
					t.Fatalf("应当报 %q,得到 %v", tc.reject, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("move: %v", err)
			}
			got := make([]string, 0, len(saved.Rules))
			for _, r := range saved.Rules {
				got = append(got, r.Pattern)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("move %v 后应为 %v,实际 %v", tc.args, tc.want, got)
			}
		})
	}
}

// list 要说清规则是内置默认还是自己配的 —— 用户改没改过,决定了他该不该 reset。
func TestSettingsRulesListReportsSource(t *testing.T) {
	t.Run("内置默认", func(t *testing.T) {
		fakeSettingsStore(t, model.Settings{Rules: model.BuiltinRules()})
		useTableOutput(t)
		out := captureStdout(t, func() { runRoot(t, "settings", "rules", "list") })
		if !strings.Contains(out, "内置默认") {
			t.Errorf("应标明这是内置默认:\n%s", out)
		}
	})

	t.Run("自己配的", func(t *testing.T) {
		fakeSettingsStore(t, model.Settings{
			Rules: []model.SubscriptionRule{{Pattern: "我的", Result: model.FormatXray}},
		})
		useTableOutput(t)
		out := captureStdout(t, func() { runRoot(t, "settings", "rules", "list") })
		if !strings.Contains(out, "自定义") {
			t.Errorf("应标明这是自定义规则:\n%s", out)
		}
		if !strings.Contains(out, "我的") {
			t.Errorf("应列出规则本身:\n%s", out)
		}
	})
}

// test 子命令要和服务端算得一样:用的是同一个 MatchFormat。
func TestSettingsRulesTestCommand(t *testing.T) {
	fakeSettingsStore(t, model.Settings{Rules: []model.SubscriptionRule{
		{Pattern: `(?i)clash`, Result: model.FormatClashMeta},
	}})

	t.Run("命中", func(t *testing.T) {
		useTableOutput(t)
		out := captureStdout(t, func() {
			runRoot(t, "settings", "rules", "test", "clash-verge/1.7.7")
		})
		if !strings.Contains(out, "命中第 1 条") || !strings.Contains(out, "clash-meta") {
			t.Errorf("应报出命中的规则与格式:\n%s", out)
		}
	})

	t.Run("没命中要说这不是错误", func(t *testing.T) {
		useTableOutput(t)
		out := captureStdout(t, func() {
			runRoot(t, "settings", "rules", "test", "SomeVpn/1.0")
		})
		if !strings.Contains(out, "回落到分享链接") {
			t.Errorf("应解释回落行为:\n%s", out)
		}
	})
}

// useTableOutput 把这些测试的输出固定成表格形式(默认值,但可能被别的测试改过)。
func useTableOutput(t *testing.T) {
	t.Helper()
	saved := opts.output
	opts.output = outputTable
	t.Cleanup(func() { opts.output = saved })
}
