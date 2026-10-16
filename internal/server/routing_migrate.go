package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// 一次性迁移:模板 → 规则表(docs/internal/routing-rules-rfc.md「迁移与兼容」)。
// settings.templates 与节点/入口 metadata 一律不删。purposes 表在 v0.7.0 起已不再
// 读取,它的删除由 store 的 dropPurposes 按分流模式处理。

// 迁移结果的几种状态。
const (
	MigrateDone         = "migrated"      // 已写入规则表并切到 rules
	MigrateNeedsConfirm = "needs_confirm" // 存过自定义 sing-box 模板,要确认(定案 4)
	MigrateBlocked      = "blocked"       // 自定义 clash-meta 模板有转换不了的规则,整份不迁移
	MigrateSkipped      = "skipped"       // 已经有规则表,不覆盖
	MigrateDryRun       = "dry_run"       // 只演算,未写入
)

// MigrationReport 是给人看的迁移报告,也存进 routing_state.report。
// Purposes 字段保留形状只为解析历史报告:v0.7.0 起迁移不再读用途,新报告一律为空。
type MigrationReport struct {
	Status        string           `json:"status"`
	At            time.Time        `json:"at"`
	Source        string           `json:"source"`
	ClashCustom   bool             `json:"clash_template_custom"`
	SingBoxCustom bool             `json:"singbox_template_custom"`
	Groups        []string         `json:"groups"`
	Rules         int              `json:"rules"`
	Final         string           `json:"final"`
	Purposes      []PurposeOutcome `json:"purposes,omitempty"`
	Unconvertible []string         `json:"unconvertible,omitempty"`
	Notes         []string         `json:"notes,omitempty"`
}

// PurposeOutcome 是历史迁移报告里记一个用途转成了什么;v0.7.0 起只用于兼容旧报告。
type PurposeOutcome struct {
	Slug    string   `json:"slug"`
	Group   string   `json:"group"`
	Rules   int      `json:"rules"`
	Final   bool     `json:"final,omitempty"`
	Dropped []string `json:"dropped,omitempty"`
}

type migrateOptions struct {
	Confirm bool // 确认 sing-box 模板改为跟随规则表
	Replace bool // 覆盖已有规则表
	DryRun  bool
}

type migrationPlan struct {
	groups []model.ProxyGroup
	rules  []model.RouteRule
	final  string
}

func (p *migrationPlan) groupName(t string) string {
	switch t {
	case model.TargetDirect:
		return "直连"
	case model.TargetReject:
		return "拦截"
	}
	if id, ok := model.TargetGroupID(t); ok {
		for _, g := range p.groups {
			if g.ID == id {
				return g.Name
			}
		}
	}
	return t
}

// planMigration 演算迁移结果,不写库。
func (s *Server) planMigration(opt migrateOptions) (*migrationPlan, *MigrationReport, error) {
	rep := &MigrationReport{At: time.Now(), Source: "内置底稿"}
	settings, err := s.store.GetSettings()
	if err != nil {
		return nil, nil, err
	}
	tpl := func(f model.ClientFormat) string {
		if settings.Templates == nil {
			return ""
		}
		return strings.TrimSpace(settings.Templates[string(f)])
	}
	rep.SingBoxCustom = tpl(model.FormatSingBox) != ""
	if tpl(model.FormatClash) != "" {
		rep.Notes = append(rep.Notes, "自定义 clash 模板不受影响:clash 格式照旧按模板生成(定案 5)")
	}

	var seed *render.RoutingSeed
	if t := tpl(model.FormatClashMeta); t != "" {
		rep.ClashCustom, rep.Source = true, "自定义 clash-meta 模板"
		var bad []string
		seed, bad, err = render.ConvertClashTemplate([]byte(t))
		if err != nil {
			rep.Status, rep.Unconvertible = MigrateBlocked, []string{err.Error()}
			return nil, rep, nil
		}
		if len(bad) > 0 {
			rep.Status, rep.Unconvertible = MigrateBlocked, bad
			return nil, rep, nil
		}
	} else if seed, err = render.DefaultRoutingSeed(); err != nil {
		return nil, nil, err
	}
	plan := &migrationPlan{groups: seed.Groups, rules: seed.Rules, final: seed.Final}

	if len(plan.rules) > model.MaxRulesPerScope {
		rep.Status = MigrateBlocked
		rep.Unconvertible = []string{fmt.Sprintf("转换后有 %d 条规则,超过上限 %d", len(plan.rules), model.MaxRulesPerScope)}
		return nil, rep, nil
	}
	for _, g := range plan.groups {
		rep.Groups = append(rep.Groups, g.Name)
	}
	rep.Rules, rep.Final = len(plan.rules), plan.groupName(plan.final)
	if rep.SingBoxCustom {
		rep.Notes = append(rep.Notes, "存过自定义 sing-box 模板:迁移后 sing-box 的分流改为跟随规则表(模板里的其他部分照用)")
		if !opt.Confirm {
			rep.Status = MigrateNeedsConfirm
			return plan, rep, nil
		}
	}
	return plan, rep, nil
}

// migrateRouting 演算并(除 dry run 外)写入迁移结果。只有状态为 migrated 时才写规则表
// 并切到 rules;其他状态只记报告,模式不变。
func (s *Server) migrateRouting(opt migrateOptions) (*MigrationReport, error) {
	plan, rep, err := s.planMigration(opt)
	if err != nil {
		return nil, err
	}
	if rep.Status == "" {
		empty, err := s.store.RoutingEmpty()
		if err != nil {
			return nil, err
		}
		if !empty && !opt.Replace {
			rep.Status = MigrateSkipped
			rep.Notes = append(rep.Notes, "已经有规则或策略组,没有覆盖;要按旧配置重新生成请加 replace")
		}
	}
	if opt.DryRun {
		if rep.Status == "" {
			rep.Status = MigrateDryRun
		}
		return rep, nil
	}
	st, err := s.store.GetRoutingState()
	if err != nil {
		return nil, err
	}
	if rep.Status != "" {
		raw, _ := json.Marshal(rep)
		st.Report = string(raw)
		return rep, s.store.SaveRoutingState(st)
	}
	rep.Status = MigrateDone
	raw, _ := json.Marshal(rep)
	now := time.Now()
	err = s.store.ApplyRoutingSeed(plan.groups, plan.rules, plan.final,
		&store.RoutingState{Mode: model.RoutingRules, Report: string(raw), MigratedAt: &now}, opt.Replace)
	if err != nil {
		return nil, err
	}
	s.invalidateSubCache()
	return rep, nil
}

// AutoMigrateRouting 在 serve 启动时调用:从没迁移过、规则表也是空的,就按 RFC 做一次
// 迁移。需要确认或转换不了时只记报告,继续按模板出订阅。
func (s *Server) AutoMigrateRouting() {
	st, err := s.store.GetRoutingState()
	if err != nil {
		s.logger.Error("读取分流状态失败,跳过迁移", "error", err)
		return
	}
	if st.MigratedAt != nil {
		return
	}
	if empty, err := s.store.RoutingEmpty(); err != nil || !empty {
		return
	}
	rep, err := s.migrateRouting(migrateOptions{})
	if err != nil {
		s.logger.Error("分流规则迁移失败,继续按模板出订阅", "error", err)
		return
	}
	switch rep.Status {
	case MigrateDone:
		s.logger.Info("已把模板与用途迁移为分流规则", "groups", len(rep.Groups), "rules", rep.Rules, "final", rep.Final)
	case MigrateNeedsConfirm:
		s.logger.Warn("存过自定义 sing-box 模板:sing-box 的分流将改为跟随规则表,确认后才迁移(mpx rule migrate --confirm)")
	case MigrateBlocked:
		s.logger.Warn("自定义 clash-meta 模板有无法转换的规则,继续按模板出订阅(mpx rule migrate --report 查看)", "count", len(rep.Unconvertible))
	}
}

func (s *Server) handleMigrateRouting(w http.ResponseWriter, r *http.Request) {
	var opt struct {
		DryRun  bool `json:"dry_run"`
		Confirm bool `json:"confirm"`
		Replace bool `json:"replace"`
	}
	if r.ContentLength != 0 && !decodeBody(w, r, &opt) {
		return
	}
	rep, err := s.migrateRouting(migrateOptions{DryRun: opt.DryRun, Confirm: opt.Confirm, Replace: opt.Replace})
	if err != nil {
		s.routingError(w, err, "迁移")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleMigrateRouting 演算并写入迁移结果。
func parseReport(raw string) *MigrationReport {
	if raw == "" {
		return nil
	}
	var rep MigrationReport
	if json.Unmarshal([]byte(raw), &rep) != nil {
		return nil
	}
	return &rep
}
