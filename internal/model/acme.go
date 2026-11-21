package model

import (
	"regexp"
	"strings"
	"time"
)

// UC-26:管理台域名 + ACME 自动证书。
//
// 这组类型是 /api/acme/* 接口的出参入参形状。持久化形状在 store 层
// (acme_state 单行表),这里只放跨层传递的部分。

// ACMEConfigRequest 是 PUT /api/acme/config 的请求体。
//
// 三个字段都是可选的「给出才改」语义,和整份 settings 的 PUT 不同 ——
// 敏感字段(CF token)不该要求调用方每次重发:空串表示保持现状,要清空
// token 走 DELETE。
type ACMEConfigRequest struct {
	// Domain 给出时替换管理台域名;空串保持现状。
	Domain string `json:"domain,omitempty"`
	// CFToken 给出时替换 Cloudflare token;空串保持现状。
	CFToken string `json:"cf_token,omitempty"`
	// Staging 给出时切换 CA(指针是为了区分「没给」与「给了 false」)。
	Staging *bool `json:"staging,omitempty"`
}

// ACMEConfig 是 /api/acme/config 的响应(也兼作 status 的一部分)。
type ACMEConfig struct {
	Domain     string `json:"domain"`
	CFTokenSet bool   `json:"cf_token_set"`
	Staging    bool   `json:"staging"`
}

// ACMEStatus 是 GET /api/acme/status 的响应:配置 + 证书 + 最近一次签发结果。
type ACMEStatus struct {
	ACMEConfig
	// CertIssued 表示库里有一张 ACME 证书(不保证未过期)。
	CertIssued bool `json:"cert_issued"`
	// CertNotAfter 是证书到期时间;没有证书时为 null。
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`
	// CertIssuedAt 是最近一次签发成功时间;没签过为 null。
	CertIssuedAt *time.Time `json:"cert_issued_at,omitempty"`
	// LastError 是最近一次签发失败的概括;没失败过或上次成功为空串。
	LastError string `json:"last_error,omitempty"`
	// LastAttemptAt 是最近一次签发尝试(无论成败)的时间。
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	// NextRetryAt 是续期循环下一次尝试续签的时间;没到退避窗口为 null。
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	// IssueRunning 表示此刻有签发任务在跑。
	IssueRunning bool `json:"issue_running"`
	// IssueState 是最近一次签发任务的状态(idle/running/done/failed)。
	IssueState string `json:"issue_state"`
}

// 签发任务的状态取值,与节点升级任务同一套词(model.UpdateNodes* 是另一组,
// 形状不同不复用)。
const (
	ACMEIssueIdle    = "idle"
	ACMEIssueRunning = "running"
	ACMEIssueDone    = "done"
	ACMEIssueFailed  = "failed"
)

// ACMEIssueEvent 是签发任务的一条进度记录。
type ACMEIssueEvent struct {
	Seq int64  `json:"seq"`
	Msg string `json:"msg"`
}

// ACMEIssueProgress 是 GET /api/acme/issue 的响应。
type ACMEIssueProgress struct {
	Job    int64            `json:"job"`
	State  string           `json:"state"`
	Seq    int64            `json:"seq"`
	Events []ACMEIssueEvent `json:"events"`
}

// domainLabelRE 是单个 DNS 标签的合法形状:字母数字开头结尾、中间可有连字符。
// 只接 ASCII 域名;punycode(xn-- 前缀)也走这条,不需要单独支持。
var domainLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidDomain 判 s 是不是一个合法的公网域名:全小写 ASCII(大小写不敏感,
// 落库前归一),至少两个标签(管理台域名不该是裸 TLD),总长不超过 253。
func ValidDomain(s string) bool {
	if s == "" || len(s) > 253 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if !domainLabelRE.MatchString(l) {
			return false
		}
	}
	return true
}

// NormalizeDomain 归一化域名:去空白、转小写、去结尾点号。配合 ValidDomain 用。
func NormalizeDomain(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}
