package server

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// ---- 设置 ----

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	// Label 不落库:读出来是空的,响应里按内置表补上给界面用。
	v.Rules = model.WithBuiltinLabels(v.Rules)
	writeJSON(w, http.StatusOK, v)
}

// handleBuiltinRules 返回内置 UA 识别规则(带展示名),给界面的「恢复内置」用。
func (s *Server) handleBuiltinRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.BuiltinRules())
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var v model.Settings
	if !decodeBody(w, r, &v) {
		return
	}
	// 先读回现行设置:订阅地址前缀只在这次真的写了新值时才校验与规整。库里
	// 已有的旧值(哪怕不合法、结尾带 /)绝不迁移,订阅必须逐字节不变。
	cur, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	if v.SubURLPrefix != cur.SubURLPrefix {
		p := strings.TrimSpace(v.SubURLPrefix)
		if p != "" {
			u, err := url.Parse(p)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				writeErrorField(w, http.StatusBadRequest, "链接开头要以 http:// 或 https:// 开头，例如 https://sub.example.com", "sub_url_prefix")
				return
			}
			p = strings.TrimRight(p, "/")
		}
		v.SubURLPrefix = p
	}
	for _, rule := range v.Rules {
		if !rule.Result.Valid() {
			writeErrorField(w, http.StatusBadRequest, "规则结果不合法: "+string(rule.Result), "rules")
			return
		}
		if _, err := compilePattern(rule.Pattern); err != nil {
			writeErrorField(w, http.StatusBadRequest, "规则正则不合法: "+rule.Pattern, "rules")
			return
		}
	}
	// 整体替换设置时,带进来的模板也要过一遍校验;但未出现的格式保持原样
	// (整体替换语义),已出现的格式里空串表示清空该模板。
	for format, body := range v.Templates {
		f := model.ClientFormat(format)
		if !templateFormats[f] {
			writeErrorField(w, http.StatusBadRequest, "不支持自定义模板的格式: "+format, "templates")
			return
		}
		if body != "" {
			if err := validateTemplate(f, []byte(body)); err != nil {
				writeErrorField(w, http.StatusBadRequest, err.Error(), "templates")
				return
			}
		}
	}
	// 通知阈值允许 0(表示关掉),负数没有意义 —— 放进去只会让判断永远为假,
	// 使用者却以为开着。
	if v.NotifyDaysLeft < 0 {
		writeErrorField(w, http.StatusBadRequest, "到期提醒天数不能是负数", "notify_days_left")
		return
	}
	if v.NotifyUsagePercent < 0 || v.NotifyUsagePercent > 100 {
		writeErrorField(w, http.StatusBadRequest, "用量提醒百分比要在 0 到 100 之间", "notify_usage_percent")
		return
	}
	if err := validateWebhookURL(v.WebhookURL); err != nil {
		writeErrorField(w, http.StatusBadRequest, err.Error(), "webhook_url")
		return
	}
	// ruleset_mode 空串即 upstream(老客户端不带这个字段,零值兼容),
	// 其余只认枚举值。
	if !model.RulesetModeValid(v.RulesetMode) {
		writeErrorField(w, http.StatusBadRequest, "不支持的规则集分发方式", "ruleset_mode")
		return
	}
	if err := s.store.SaveSettings(&v); err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	s.invalidateSubCache()
	v.Rules = model.WithBuiltinLabels(v.Rules)
	writeJSON(w, http.StatusOK, v)
}

// validateWebhookURL 检查通知地址(委托 control.ValidateWebhookTarget)。
//
// 空串合法;只认 http/https;本机/内网/链路本地等保留地址一律拒绝,避免认证后
// 的 SSRF。详细规则与拨号侧二次校验见 control 包。
func validateWebhookURL(raw string) error {
	return control.ValidateWebhookTarget(raw)
}

// handleTestWebhook 往当前设置里的地址发一条测试事件。
//
// 存在的理由:webhook 是一条沉默的通道 —— 配错了不会有任何反馈,要等到真出事
// 那天才发现没收到。这个接口让它当场把话说出去一次,对面收得到才算配好了。
//
// 投递失败返回 502 而不是 500:请求本身没错,是上游没接住。
func (s *Server) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	if settings.WebhookURL == "" {
		writeError(w, http.StatusBadRequest, "还没有配置 webhook 地址")
		return
	}
	ev := control.WebhookEvent{
		Event:   control.EventTest,
		Message: "这是一条来自 my-proxy 的测试通知,收到就说明地址配对了。",
		At:      time.Now(),
	}
	if err := control.PostWebhook(r.Context(), settings.WebhookURL, ev, s.logger); err != nil {
		writeError(w, http.StatusBadGateway, "webhook 没送到: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhook": settings.WebhookURL, "sent": true})
}
