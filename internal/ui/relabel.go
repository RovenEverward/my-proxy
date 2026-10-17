package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// relabelBodyMax 是 relabel 请求体的字节上限。对照表可能几十行,给宽一点。
const relabelBodyMax = 1 << 20

// RelabelRow 是对照表的一行:旧名 → 新名,以及依据/跳过原因。
type RelabelRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"` // 内部名
	Old  string `json:"old"`
	New  string `json:"new"` // "-" 表示这行不写入
	Note string `json:"note"`
}

// RelabelPlanItem 是一条待写入计划。页面把 plan 返回的这份原样回传,apply 只写它,
// 绝不重新 locate。
type RelabelPlanItem struct {
	ID       int64                 `json:"id"`
	Name     string                `json:"name"`
	Metadata model.PurposeMetadata `json:"metadata"`
}

// RelabelResult 是 apply 里一个节点的结果。Skipped 为真时 Reason 说明原因。
type RelabelResult struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Display string `json:"display,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// RelabelRunner 由 cli 实现:对照表的计算与写入都在 cli 那一份 relabelPlan /
// relabelApply 里,CLI 与页面共用同一套行为。ui 包不 import cli,靠这个接口解耦。
//
// upstream/token/transport 由调用方传当前值(配置档切换后要跟着变):页面永远
// 打的是这个 mpx ui 进程此刻连着的那个控制面。
type RelabelRunner interface {
	Plan(ctx context.Context, up *url.URL, token string, tr http.RoundTripper, req RelabelRequest) ([]RelabelRow, []RelabelPlanItem, error)
	Apply(ctx context.Context, up *url.URL, token string, tr http.RoundTripper, plans []RelabelPlanItem) ([]RelabelResult, error)
}

// RelabelRequest 是 plan 的请求体:all 或 ids 二选一。
type RelabelRequest struct {
	All bool    `json:"all"`
	IDs []int64 `json:"ids"`
}

// serveRelabel 处理 /local/relabel/plan 与 /local/relabel/apply。只接受 POST;
// 会话与来源校验已经在 ServeHTTP 里做过,与 /local/profiles 并列。没实现
// RelabelRunner 时一律 404。
func (s *Server) serveRelabel(w http.ResponseWriter, r *http.Request) {
	if s.opts.Relabel == nil {
		writeError(w, http.StatusNotFound, "这里不支持批量识别显示名")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 POST")
		return
	}
	switch r.URL.Path {
	case "/local/relabel/plan":
		s.relabelPlan(w, r)
	case "/local/relabel/apply":
		s.relabelApply(w, r)
	default:
		writeError(w, http.StatusNotFound, "没有这个重新识别接口")
	}
}

// currentUpstream 取这个进程此刻的转发目标与凭据。还没有控制面(首次安装模式)
// 时返回 503。
func (s *Server) currentUpstream(w http.ResponseWriter) (*url.URL, string, http.RoundTripper, bool) {
	s.mu.Lock()
	up, token, tr, proxy := s.opts.Upstream, s.opts.Token, s.opts.Transport, s.proxy
	s.mu.Unlock()
	if proxy == nil || up == nil || up.Host == "" {
		writeError(w, http.StatusServiceUnavailable, "还没有控制面:先完成首次安装")
		return nil, "", nil, false
	}
	return up, token, tr, true
}

func (s *Server) relabelPlan(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, relabelBodyMax)
	var body struct {
		All bool    `json:"all"`
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return
	}
	if !body.All && len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "要么 all:true,要么给 ids")
		return
	}
	up, token, tr, ok := s.currentUpstream(w)
	if !ok {
		return
	}
	rows, plans, err := s.opts.Relabel.Plan(r.Context(), up, token, tr, RelabelRequest{All: body.All, IDs: body.IDs})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if rows == nil {
		rows = []RelabelRow{}
	}
	if plans == nil {
		plans = []RelabelPlanItem{}
	}
	writeJSON(w, map[string]any{"rows": rows, "plans": plans})
}

func (s *Server) relabelApply(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, relabelBodyMax)
	var body struct {
		Plans []RelabelPlanItem `json:"plans"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return
	}
	if len(body.Plans) == 0 {
		writeError(w, http.StatusBadRequest, "没有要写入的节点")
		return
	}
	up, token, tr, ok := s.currentUpstream(w)
	if !ok {
		return
	}
	results, err := s.opts.Relabel.Apply(r.Context(), up, token, tr, body.Plans)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if results == nil {
		results = []RelabelResult{}
	}
	writeJSON(w, map[string]any{"results": results})
}
