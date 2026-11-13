package server

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/ruleset"
)

// UC-05 规则集私有化分发:镜像文件的下载口(/sub/{token}/ruleset/...)与
// 管理面状态/刷新接口(/api/rulesets*)。文件清单与拉取都在 internal/ruleset,
// 这里只做鉴权、参数白名单与按组聚合的视图。

// rulesetNameRe 是镜像文件名的白名单:sing-box 的 tag 会带 "@"(steam@cn),
// clash provider 名是字母数字;两端的 name 都不允许出现分隔符与点号外的东西,
// 免得拼出来的 key 绕过快照表直接读到别的内容。
var rulesetNameRe = regexp.MustCompile(`^[a-zA-Z0-9@_.-]{1,128}$`)

// handleRulesetFile 把一个镜像好的规则集文件发给客户端。
//
// 鉴权与订阅一样:路径里那个不可猜的 token 本身。失败一律回 404 —— 这条路径
// 公网可达,不区分「没这个订阅」和「还没镜像好」,免得探测出功能是否存在。
func (s *Server) handleRulesetFile(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.GetUserBySubToken(r.PathValue("token")); err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	fmtParam := r.PathValue("fmt")
	if fmtParam != "clash" && fmtParam != "singbox" {
		writeError(w, http.StatusBadRequest, "不支持的规则集格式 "+fmtParam)
		return
	}
	name := r.PathValue("name")
	if !rulesetNameRe.MatchString(name) {
		writeError(w, http.StatusNotFound, "规则集不存在")
		return
	}
	if s.rulesets == nil {
		writeError(w, http.StatusNotFound, "规则集不存在")
		return
	}
	body, ct, err := s.store.RulesetSnapshotBody(fmtParam + "/" + name)
	if err != nil {
		// 未就绪与不存在同一个 404:镜像没拉好就该让客户端继续用上游地址,
		// 渲染闭包只给已就绪的文件换 URL,能走到这里的 404 都是探测或时序差。
		writeError(w, http.StatusNotFound, "规则集不存在")
		return
	}
	if ct == "" {
		ct = rulesetFileContentType(name)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// rulesetFileContentType 在上游没给 Content-Type 时按文件名后缀猜一个。
func rulesetFileContentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".yaml"):
		return "text/yaml"
	case strings.HasSuffix(name, ".txt"):
		return "text/plain"
	case strings.HasSuffix(name, ".srs"):
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}

// rulesetURLFunc 组装渲染期注入的地址改写闭包(UC-05):
// 已就绪(FetchedAt>0,等价于镜像有 body)的文件换成控制面镜像地址;没就绪的
// 原样写上游 —— 宁可客户端直连,也不让它拿到一个 404 的镜像地址。
//
// 返回 (nil, false) 表示不该注入:模式不是 privatized、拉取器没接,或拼不出
// 镜像前缀(没配订阅前缀又没有请求上下文,例如 doctor 自检)。
func (s *Server) rulesetURLFunc(user *model.User, settings *model.Settings, r *http.Request) (func(fmt, name, upstream string) string, bool) {
	if settings == nil || settings.RulesetMode != model.RulesetModePrivatized ||
		s.rulesets == nil || user == nil || user.SubToken == "" {
		return nil, false
	}
	base := subBaseURL(settings, r)
	if base == "" {
		return nil, false
	}
	prefix := base + "/sub/" + user.SubToken + "/ruleset/"
	snaps, err := s.store.ListRulesetSnapshots()
	if err != nil {
		return nil, false
	}
	return func(f, n, up string) string {
		if snap := snaps[f+"/"+n]; snap != nil && snap.FetchedAt > 0 {
			return prefix + f + "/" + url.PathEscape(n)
		}
		return up
	}, true
}

// ---- 管理接口 ----

// rulesetRoutes 注册规则集镜像的管理路由。只增不破:全部是新路径。
func (s *Server) rulesetRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/rulesets", s.handleListRulesets)
	api.HandleFunc("POST /api/rulesets/refresh", s.handleRefreshRulesets)
}

type rulesetFileView struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Ready     bool   `json:"ready"`
	LastError string `json:"last_error,omitempty"`
}

type rulesetFmtView struct {
	Files []rulesetFileView `json:"files"`
	Ready int               `json:"ready"`
	Total int               `json:"total"`
}

type rulesetGroupView struct {
	Key           string         `json:"key"`
	Name          string         `json:"name"`
	PrivateIP     bool           `json:"private_ip"`
	Clash         rulesetFmtView `json:"clash"`
	SingBox       rulesetFmtView `json:"sing_box"`
	Refreshing    bool           `json:"refreshing"`
	LastAttemptAt int64          `json:"last_attempt_at"` // 毫秒(与快照表一致,UnixMilli)
	// LastError 是组内最近一次非空的文件级错误:组级「刷新失败,沿用上次结果」
	// 靠它区分「正在拉」和「拉失败了」,文件级错误仍在 clash/sing_box.files 里。
	LastError string `json:"last_error,omitempty"`
}

// handleListRulesets 返回「当前被启用规则引用的」规则组的镜像状态。
// 未被引用的组不出现在响应里:镜像只拉被引用的文件,列一堆永远为空的组只是噪音。
func (s *Server) handleListRulesets(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListEnabledRules()
	if err != nil {
		s.writeStoreError(w, err, "规则集")
		return
	}
	snaps, err := s.store.ListRulesetSnapshots()
	if err != nil {
		s.writeStoreError(w, err, "规则集")
		return
	}
	byGroup := ruleset.FilesByGroup(rules)

	// 按目录顺序输出,geoip 合成组排在最后。
	keys := []string{}
	for _, rs := range model.RuleCatalog() {
		if _, ok := byGroup[rs.Key]; ok {
			keys = append(keys, rs.Key)
		}
	}
	if _, ok := byGroup[ruleset.GeoIPGroupKey]; ok {
		keys = append(keys, ruleset.GeoIPGroupKey)
	}

	out := []rulesetGroupView{}
	for _, key := range keys {
		files := byGroup[key]
		gv := rulesetGroupView{Key: key}
		if rs, ok := model.RuleSetByKey(key); ok {
			gv.Name = rs.Name
			gv.PrivateIP = rs.PrivateIP
		} else {
			gv.Name = "GeoIP" // geoip 合成组
		}
		gv.Clash.Files = []rulesetFileView{}
		gv.SingBox.Files = []rulesetFileView{}
		var lastErrAt int64
		for _, f := range files {
			fv := rulesetFileView{Name: f.Name, URL: f.Upstream}
			var attempt int64
			if snap := snaps[f.Key]; snap != nil {
				fv.Ready = snap.FetchedAt > 0
				fv.LastError = snap.LastError
				attempt = snap.LastAttemptAt
				if snap.LastError != "" && attempt >= lastErrAt {
					gv.LastError = snap.LastError
					lastErrAt = attempt
				}
			}
			if attempt > gv.LastAttemptAt {
				gv.LastAttemptAt = attempt
			}
			if s.rulesets != nil && s.rulesets.Running(f.Key) {
				gv.Refreshing = true
			}
			if f.Fmt == "clash" {
				gv.Clash.Files = append(gv.Clash.Files, fv)
				gv.Clash.Total++
				if fv.Ready {
					gv.Clash.Ready++
				}
			} else {
				gv.SingBox.Files = append(gv.SingBox.Files, fv)
				gv.SingBox.Total++
				if fv.Ready {
					gv.SingBox.Ready++
				}
			}
		}
		out = append(out, gv)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRefreshRulesets 手动触发一轮镜像刷新。body 可选 {"keys":["lan",...]},
// 值是组 key(目录组或 "geoip" 合成组);缺省对全部引用组各起一次。
// 单文件粒度由拉取器单飞合并,响应只回发起了几个文件作业。
func (s *Server) handleRefreshRulesets(w http.ResponseWriter, r *http.Request) {
	if s.rulesets == nil {
		writeError(w, http.StatusNotFound, "规则集镜像未启用")
		return
	}
	var body struct {
		Keys []string `json:"keys"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	rules, err := s.store.ListEnabledRules()
	if err != nil {
		s.writeStoreError(w, err, "规则集")
		return
	}
	byGroup := ruleset.FilesByGroup(rules)

	wanted := map[string]bool{}
	if len(body.Keys) > 0 {
		for _, k := range body.Keys {
			wanted[k] = true
		}
	} else {
		for k := range byGroup {
			wanted[k] = true
		}
	}
	started := 0
	for key := range wanted {
		for _, f := range byGroup[key] {
			if err := s.rulesets.StartRefresh(f.Key); err == nil {
				started++
			}
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": started})
}

// renderPlanWithRulesetURL 把 plan 装上镜像地址改写闭包。抽出来是因为
// renderSubscription 与后续预览路径都要在拿到 plan 后做同一件事。
func (s *Server) renderPlanWithRulesetURL(plan *render.RoutingPlan, user *model.User, settings *model.Settings, r *http.Request) {
	if fn, ok := s.rulesetURLFunc(user, settings, r); ok {
		plan.RulesetURL = fn
	}
}
