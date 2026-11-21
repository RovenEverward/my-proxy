package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/RovenEverward/my-proxy/internal/acme"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// UC-26 批:管理台域名 + ACME 证书的设置与签发接口。
//
// 三条路径,都在 requireAuth 之内(它们本来就是管理操作):
//   - PUT /api/acme/config:改域名 / CF token / staging。副作用:域名或 CA
//     变了就把库里那张 ACME 证书作废(它签给的是旧域名/旧 CA,留着只会让
//     SNI 错发)并让 listener 立刻回自签 —— 证书没了比错了好。
//   - DELETE /api/acme/config:整个关掉 ACME(清三个 settings 键),证书
//     行保留但不再装 —— 再配回同域名时那张证书还是有效的。
//   - POST /api/acme/issue + GET /api/acme/issue?since=:单飞签发任务与
//     进度,事件流形状与节点升级一致。
//
// 「签发」不在请求里同步跑:DNS-01 要等公网 TXT 可见、等 CA 轮询,正常也要
// 十几秒,超时会拖死 HTTP 请求;任务化后 CLI 拉进度即可。
//
// errNoACME 是服务端没接 ACME Manager 的回答(测试里 New() 不带它):
// 读接口照常工作 —— 状态全在库里 —— 只有会触发签发的写接口拒绝。
var errNoACME = errors.New("这个服务端没有接上 ACME 通道(Serve 启动的实例才有)")

// SetACME 把 ACME Manager 接进来。serve.go 在建好 tls_identity 后调用。
func (s *Server) SetACME(m *acme.Manager) {
	s.acme = m
}

// handlePutACMEConfig 改 ACME 配置。字段是「给出才改」语义:空串不动现状,
// 这保住了敏感字段(CF token)不用每次重发。
func (s *Server) handlePutACMEConfig(w http.ResponseWriter, r *http.Request) {
	var req model.ACMEConfigRequest
	if !decodeBody(w, r, &req) {
		return
	}
	st, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "ACME 配置")
		return
	}
	domainChanged, caChanged := false, false
	if req.Domain != "" {
		d := model.NormalizeDomain(req.Domain)
		if !model.ValidDomain(d) {
			writeErrorField(w, http.StatusBadRequest, "不是合法域名,例如 cp.example.net", "domain")
			return
		}
		if d != st.ACMEDomain {
			st.ACMEDomain = d
			domainChanged = true
		}
	}
	if req.CFToken != "" {
		st.ACMECFToken = req.CFToken
	}
	if req.Staging != nil && *req.Staging != st.ACMEStaging {
		st.ACMEStaging = *req.Staging
		caChanged = true
	}
	if err := s.store.SaveSettings(st); err != nil {
		s.writeStoreError(w, err, "ACME 配置")
		return
	}
	if domainChanged || caChanged {
		if err := s.store.ClearACMECert(); err != nil {
			s.writeStoreError(w, err, "ACME 配置")
			return
		}
	}
	if s.acme != nil {
		s.acme.ReloadDomain(st.ACMEDomain)
	}
	writeJSON(w, http.StatusOK, model.ACMEConfig{
		Domain:     st.ACMEDomain,
		CFTokenSet: st.ACMECFToken != "",
		Staging:    st.ACMEStaging,
	})
}

// handleDeleteACMEConfig 关掉 ACME:清三个 settings 键,证书行保留 ——
// 它是库里的事实,哪天配回同域名还能直接用;listener 的 SNI 由空域名兜底
// 回自签。
func (s *Server) handleDeleteACMEConfig(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "ACME 配置")
		return
	}
	st.ACMEDomain = ""
	st.ACMECFToken = ""
	st.ACMEStaging = false
	if err := s.store.SaveSettings(st); err != nil {
		s.writeStoreError(w, err, "ACME 配置")
		return
	}
	if s.acme != nil {
		s.acme.ReloadDomain("")
	}
	writeJSON(w, http.StatusOK, model.ACMEConfig{})
}

// handleStartACMEIssue 起一轮签发。
func (s *Server) handleStartACMEIssue(w http.ResponseWriter, r *http.Request) {
	if s.acme == nil {
		writeError(w, http.StatusServiceUnavailable, errNoACME.Error())
		return
	}
	st, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "ACME 配置")
		return
	}
	if st.ACMEDomain == "" {
		writeError(w, http.StatusBadRequest, "还没配置管理台域名,先 PUT /api/acme/config")
		return
	}
	if st.ACMECFToken == "" {
		writeError(w, http.StatusBadRequest, "还没配置 Cloudflare API token")
		return
	}
	// context.Background():HTTP 请求返回后 ctx 就取消了,不能让它杀掉
	// 后台任务 —— 签发跑到一半被杀会让 TXT 留在 DNS 上、状态停在 running。
	job, ok := s.acme.StartIssue(context.Background(), st.ACMEDomain, st.ACMEStaging)
	if !ok {
		writeError(w, http.StatusConflict, "已有签发任务在跑")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

// handleACMEIssueProgress 回答签发任务进度;since 语义与节点升级一致。
func (s *Server) handleACMEIssueProgress(w http.ResponseWriter, r *http.Request) {
	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "since 必须是非负整数")
			return
		}
		since = n
	}
	if s.acme == nil {
		writeJSON(w, http.StatusOK, model.ACMEIssueProgress{State: model.ACMEIssueIdle, Events: []model.ACMEIssueEvent{}})
		return
	}
	writeJSON(w, http.StatusOK, s.acme.IssueProgress(since))
}

// handleACMEStatus 组装状态:配置来自 settings、证书与错误来自 acme_state、
// 任务状态来自内存。Manager 不在(测试/未接线)也能答 —— 只是任务恒 idle。
func (s *Server) handleACMEStatus(w http.ResponseWriter, r *http.Request) {
	if s.acme != nil {
		out, err := s.acme.Status()
		if err != nil {
			s.writeStoreError(w, err, "ACME 状态")
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	st, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "ACME 状态")
		return
	}
	as, err := s.store.GetACMEState()
	if err != nil {
		s.writeStoreError(w, err, "ACME 状态")
		return
	}
	out := model.ACMEStatus{
		ACMEConfig: model.ACMEConfig{
			Domain:     st.ACMEDomain,
			CFTokenSet: st.ACMECFToken != "",
			Staging:    st.ACMEStaging,
		},
		LastError: as.LastError,
	}
	out.CertIssued = len(as.CertPEM) > 0
	if out.CertIssued {
		out.CertNotAfter = &as.NotAfter
	}
	if !as.IssuedAt.IsZero() {
		out.CertIssuedAt = &as.IssuedAt
	}
	if !as.LastAttemptAt.IsZero() {
		out.LastAttemptAt = &as.LastAttemptAt
	}
	if !as.NextRetryAt.IsZero() {
		out.NextRetryAt = &as.NextRetryAt
	}
	out.IssueState = model.ACMEIssueIdle
	writeJSON(w, http.StatusOK, out)
}
