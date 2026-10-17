package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// ---- 订阅地址前缀:只在这次真的写了新值时才校验、规整 ----

// 库里已有的旧值(结尾带 /)在 PUT 同一个值时原样保留,订阅逐字节不变。
func TestSaveSettingsKeepsExistingSubURLPrefixOnSameValue(t *testing.T) {
	e := newEnv(t)
	_, subPath := e.createSubReadyUser("alice")

	if err := e.st.SaveSettings(&model.Settings{SubURLPrefix: "https://old.example.com/"}); err != nil {
		t.Fatalf("直接存旧前缀: %v", err)
	}

	code, _, before := e.sub(subPath+"/links", "v2rayng")
	if code != http.StatusOK {
		t.Fatalf("PUT 前取订阅: got %d %s", code, before)
	}

	// 读回当前设置再整体 PUT,和 CLI 的读-改-写一致,前缀就是那个旧值。
	code, data := e.api("GET", "/api/settings", nil)
	if code != http.StatusOK {
		t.Fatalf("取设置: got %d %s", code, data)
	}
	var st model.Settings
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("解析设置: %v", err)
	}
	if st.SubURLPrefix != "https://old.example.com/" {
		t.Fatalf("库里旧前缀读回来就不对: %q", st.SubURLPrefix)
	}
	if code, data := e.api("PUT", "/api/settings", st); code != http.StatusOK {
		t.Fatalf("PUT 同值: got %d %s", code, data)
	}

	stored, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("读回设置: %v", err)
	}
	if stored.SubURLPrefix != "https://old.example.com/" {
		t.Fatalf("旧前缀被迁移/改写了: %q", stored.SubURLPrefix)
	}

	code, _, after := e.sub(subPath+"/links", "v2rayng")
	if code != http.StatusOK {
		t.Fatalf("PUT 后取订阅: got %d %s", code, after)
	}
	if string(before) != string(after) {
		t.Fatalf("PUT 同值前后订阅正文变了:\n before %q\n after  %q", before, after)
	}
}

func TestSaveSettingsNormalizesNewSubURLPrefix(t *testing.T) {
	e := newEnv(t)

	if code, data := e.api("PUT", "/api/settings", model.Settings{SubURLPrefix: "https://new.example.com/sub/"}); code != http.StatusOK {
		t.Fatalf("应接受: got %d %s", code, data)
	}
	stored, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("读回设置: %v", err)
	}
	if stored.SubURLPrefix != "https://new.example.com/sub" {
		t.Fatalf("结尾斜杠没去掉: %q", stored.SubURLPrefix)
	}
}

func TestSaveSettingsRejectsBadNewSubURLPrefix(t *testing.T) {
	e := newEnv(t)
	if err := e.st.SaveSettings(&model.Settings{SubURLPrefix: "https://keep.example.com"}); err != nil {
		t.Fatalf("准备库值: %v", err)
	}

	code, body := e.api("PUT", "/api/settings", model.Settings{SubURLPrefix: "sub.example.com"})
	if code != http.StatusBadRequest {
		t.Fatalf("没有 scheme 的地址应 400,实际 %d %s", code, body)
	}
	if !strings.Contains(string(body), "http:// 或 https://") {
		t.Fatalf("错误信息应含「http:// 或 https://」: %s", body)
	}
	stored, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("读回设置: %v", err)
	}
	if stored.SubURLPrefix != "https://keep.example.com" {
		t.Fatalf("被拒的请求不该改动库里的值: %q", stored.SubURLPrefix)
	}
}

func TestSaveSettingsAllowsEmptySubURLPrefix(t *testing.T) {
	e := newEnv(t)
	if err := e.st.SaveSettings(&model.Settings{SubURLPrefix: "https://old.example.com"}); err != nil {
		t.Fatalf("准备库值: %v", err)
	}

	if code, data := e.api("PUT", "/api/settings", model.Settings{SubURLPrefix: ""}); code != http.StatusOK {
		t.Fatalf("空串应合法: got %d %s", code, data)
	}
	stored, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("读回设置: %v", err)
	}
	if stored.SubURLPrefix != "" {
		t.Fatalf("空串没存成空: %q", stored.SubURLPrefix)
	}
}

// ---- 规则的展示名 Label ----

func TestSettingsRulesCarryBuiltinLabels(t *testing.T) {
	e := newEnv(t)

	code, data := e.api("GET", "/api/settings", nil)
	if code != http.StatusOK {
		t.Fatalf("取设置: got %d %s", code, data)
	}
	var st model.Settings
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("解析设置: %v", err)
	}
	if len(st.Rules) == 0 {
		t.Fatal("设置里没有规则")
	}
	if st.Rules[0].Label != "Karing" {
		t.Fatalf("第 1 条规则的 Label: got %q want %q", st.Rules[0].Label, "Karing")
	}
}

func TestBuiltinRulesRoute(t *testing.T) {
	e := newEnv(t)

	code, data := e.api("GET", "/api/settings/builtin-rules", nil)
	if code != http.StatusOK {
		t.Fatalf("取内置规则: got %d %s", code, data)
	}
	var rules []model.SubscriptionRule
	if err := json.Unmarshal(data, &rules); err != nil {
		t.Fatalf("解析内置规则: %v", err)
	}
	if len(rules) != 9 {
		t.Fatalf("内置规则应有 9 条,实际 %d", len(rules))
	}
	if rules[1].Label != "Clash Verge、Mihomo Party、FlClash、Stash" {
		t.Fatalf("第 2 条 Label: got %q", rules[1].Label)
	}
}

// 给规则加/去 Label 不该动订阅正文。
func TestSubscriptionUnaffectedByRuleLabels(t *testing.T) {
	e := newEnv(t)
	_, subPath := e.createSubReadyUser("alice")

	code, _, before := e.sub(subPath+"/links", "v2rayng")
	if code != http.StatusOK {
		t.Fatalf("取订阅: got %d %s", code, before)
	}

	// 读回设置,给每条规则都补一个(可能是错的)Label 再整体 PUT:Label 只影响
	// 响应展示,匹配与渲染都不看它。
	code, data := e.api("GET", "/api/settings", nil)
	if code != http.StatusOK {
		t.Fatalf("取设置: got %d %s", code, data)
	}
	var st model.Settings
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("解析设置: %v", err)
	}
	for i := range st.Rules {
		st.Rules[i].Label = "随便一个名字"
	}
	if code, data := e.api("PUT", "/api/settings", st); code != http.StatusOK {
		t.Fatalf("写设置: got %d %s", code, data)
	}

	code, _, after := e.sub(subPath+"/links", "v2rayng")
	if code != http.StatusOK {
		t.Fatalf("再取订阅: got %d %s", code, after)
	}
	if string(before) != string(after) {
		t.Fatalf("加 Label 前后订阅正文变了:\n before %q\n after  %q", before, after)
	}
}
