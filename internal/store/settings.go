package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// DefaultSettings 返回空库(或从没保存过设置)时的全局设置默认值。
//
// 导出是为了让调用方和测试能直接对照,不必各自再抄一份常量。每次调用返回新
// 对象,免得调用方改到共享的默认值上。
func DefaultSettings() *model.Settings {
	return &model.Settings{
		SubURLPrefix:   "", // 订阅地址前缀由部署时决定,没有能猜的默认值
		ProfileTitle:   "{USERNAME}",
		SupportLink:    "",
		UpdateInterval: 24, // 小时

		WebhookURL:         "",
		NotifyDaysLeft:     model.DefaultNotifyDaysLeft,     // 到期前 3 天
		NotifyUsagePercent: model.DefaultNotifyUsagePercent, // 用量 80%

		// 规则表种子化内置默认:订阅链接只有一条,客户端各有各的格式,没有
		// 规则就没人把请求认到对应的模板上(见 model.BuiltinRules)。
		Rules:     model.BuiltinRules(),
		Templates: nil,
	}
}

// GetSettings 读全局设置。从没保存过时返回 DefaultSettings(),不算错误。
func (s *Store) GetSettings() (*model.Settings, error) {
	var (
		st        model.Settings
		rules     string
		templates string
	)
	err := s.db.QueryRow(
		`SELECT sub_url_prefix, profile_title, support_link, update_interval,
		        webhook_url, notify_days_left, notify_usage_percent,
		        rules, templates
		 FROM settings WHERE id = 1`).
		Scan(&st.SubURLPrefix, &st.ProfileTitle, &st.SupportLink, &st.UpdateInterval,
			&st.WebhookURL, &st.NotifyDaysLeft, &st.NotifyUsagePercent, &rules, &templates)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取设置: %w", err)
	}

	var decodeErr error
	if st.Rules, decodeErr = decodeSlice[model.SubscriptionRule](rules); decodeErr != nil {
		return nil, fmt.Errorf("解析设置里的订阅规则: %w", decodeErr)
	}
	if st.Templates, decodeErr = decodeTemplates(templates); decodeErr != nil {
		return nil, fmt.Errorf("解析设置里的模板: %w", decodeErr)
	}
	// 存的是 "[]" 说明这份设置从没配过规则 —— 可能是老库升级上来的,也可能是
	// 有人整份 PUT 覆盖时把它清了。这里回落到内置默认,让「空」在新库老库上
	// 是同一个行为。真要关掉分流得显式写一条规则(见 model.RuleList)。
	if len(st.Rules) == 0 {
		st.Rules = model.BuiltinRules()
	}
	return &st, nil
}

// SaveSettings 整体替换全局设置(settings 是单行表,不存在插入、存在覆盖)。
//
// 这里只负责存取,不校验规则里的正则是否编得过:写入口(HTTP / CLI)才是该把
// 坏正则挡在外面的地方。模板同理,这里只存取不校验内容。
func (s *Store) SaveSettings(st *model.Settings) error {
	if st == nil {
		return errors.New("保存设置: 设置对象为空")
	}
	// Label 只给界面展示用,由服务端按内置表派生,不落库。这里复制一份再把
	// Label 清掉,不改调用方传进来的切片。
	rulesIn := st.Rules
	if rulesIn != nil {
		rulesIn = make([]model.SubscriptionRule, len(st.Rules))
		for i, r := range st.Rules {
			r.Label = ""
			rulesIn[i] = r
		}
	}
	rules, err := encodeSlice(rulesIn)
	if err != nil {
		return fmt.Errorf("序列化订阅规则: %w", err)
	}
	templates, err := encodeTemplates(st.Templates)
	if err != nil {
		return fmt.Errorf("序列化模板: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO settings (id, sub_url_prefix, profile_title, support_link,
		                      update_interval, webhook_url, notify_days_left,
		                      notify_usage_percent, rules, templates)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			sub_url_prefix       = excluded.sub_url_prefix,
			profile_title        = excluded.profile_title,
			support_link         = excluded.support_link,
			update_interval      = excluded.update_interval,
			webhook_url          = excluded.webhook_url,
			notify_days_left     = excluded.notify_days_left,
			notify_usage_percent = excluded.notify_usage_percent,
			rules                = excluded.rules,
			templates            = excluded.templates`,
		st.SubURLPrefix, st.ProfileTitle, st.SupportLink, st.UpdateInterval,
		st.WebhookURL, st.NotifyDaysLeft, st.NotifyUsagePercent, rules, templates)
	if err != nil {
		return fmt.Errorf("保存设置: %w", err)
	}
	return nil
}

// decodeTemplates 把 templates 列里的 JSON 对象解成 map;空串按空 map 处理,
// 兼容更早版本建的库(那列还不存在时迁移会补成默认 '{}')。
func decodeTemplates(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// encodeTemplates 把模板 map 序列化成 JSON;nil 序列化成 '{}',与列默认值一致。
func encodeTemplates(m map[string]string) (string, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
