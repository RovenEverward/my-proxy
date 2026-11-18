package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/selfupdate"
	"github.com/RovenEverward/my-proxy/internal/singboxbuild"
	"github.com/RovenEverward/my-proxy/internal/xrayrelease"
)

// handleUpdate 回控制面自己与各节点核心的版本,给 mpx update 用。
//
// 默认只读:不查上游、不 SSH 节点。所有结论都来自别处已经算好的东西 —— 上游检查
// 是后台任务按静默期在跑,节点版本是上一轮巡检观察到的。一个"看一下现在什么版本"
// 的操作不该顺手把每台节点连一遍,更不该因为某台连不上就整个失败。
//
// 带 ?refresh=1 时例外:先强制向上游查一次再回答,给"刚发了新版想立刻看到"的
// 手动入口用。这仍然是只读的(不碰节点),失败也不让接口失败 —— 结果里的 error
// 字段会说明为什么,其余字段照旧是上一次的结论。
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.updateNodes()
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	control := s.controlUpdate()
	if wantsRefresh(r) {
		control = s.refreshControlUpdate(r.Context())
	}
	writeJSON(w, http.StatusOK, model.UpdateSnapshot{
		Control: control,
		Nodes:   nodes,
	})
}

// wantsRefresh 报告这次请求要不要强制刷新:查询参数 refresh 为 1(或 true)。
//
// 只认显式的真值:不带、带 0、带 false 都不算。这样旧 CLI 的请求行为完全不变,
// 不认识这个参数的旧控制面也会原样忽略它。
func wantsRefresh(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh"))) {
	case "1", "true":
		return true
	default:
		return false
	}
}

// controlUpdate 说控制面自己这一份。没接更新源时只有版本,不声称有新版。
//
// 「当前版本未知就不算有新版」这条判据在接口边界上再兜一次:上游检查源将来换
// 了实现,也不该因为它的一个疏忽就让源码编译版的控制面报出"有新版本"。
func (s *Server) controlUpdate() model.UpdatePart {
	part := model.UpdatePart{}
	if s.updates != nil {
		part = s.updates.ControlUpdate()
	}
	if part.Current == "" {
		part.Available = false
	}
	part.Last = s.controlLastRun()
	return part
}

// refreshControlUpdate 强制查一次上游再给出结论。接不上刷新能力时退回缓存结论,
// 行为与不带 refresh 完全一致 —— 旧实现不该因为不认识这个参数就让请求失败。
//
// 检查失败不在这里返回错误:RefreshControlUpdate 的实现把失败记进 Error 字段,
// 上一次的结论照常带回来。接口层只负责把这份结论发出去。
func (s *Server) refreshControlUpdate(ctx context.Context) model.UpdatePart {
	part := model.UpdatePart{}
	if ref, ok := s.updates.(UpdateRefresher); ok {
		part = ref.RefreshControlUpdate(ctx)
	} else {
		part = s.controlUpdate()
	}
	if part.Current == "" {
		part.Available = false
	}
	part.Last = s.controlLastRun()
	return part
}

// controlLastRun 读一次控制面升级状态文件,翻成接口形状。
//
// 文件不存在、读不出、或者目录没配时都返回 nil:状态只是附加信息,不能因为
// 它读不到就让整个 GET /api/update 失败。
func (s *Server) controlLastRun() *model.UpdateRun {
	if s.updateDir == "" {
		return nil
	}
	st, ok, err := selfupdate.ReadControlState(filepath.Join(s.updateDir, controlStateFile))
	if err != nil || !ok {
		return nil
	}
	return &model.UpdateRun{
		Component:  st.Component,
		State:      st.State,
		From:       st.From,
		To:         st.To,
		StartedAt:  st.StartedAt,
		FinishedAt: st.FinishedAt,
		Result:     st.Result,
		Trigger:    st.Trigger,
		Error:      st.Error,
	}
}

// 控制面升级请求/状态在 update 目录下的固定文件名,与 root 侧执行器约定一致。
const (
	controlRequestFile = "request"
	controlStateFile   = "state.json"
)

// controlVersionRe 是控制面升级接口接受的版本号形状:v 加三段数字。
// 空串也是合法的,表示"最新版",由调用方另行处理。
var controlVersionRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// controlRunActions 是控制面升级接口允许的动作。
var controlRunActions = map[string]bool{"upgrade": true, "rollback": true}

// handleUpdateControl 接收一次控制面升级/回退请求,只落请求文件。
//
// 鉴权与限流已由 /api 的 mux 负责。这里不自己动二进制、不重启服务:控制面以
// 普通用户运行,换 /usr/local/bin/mpx 是 root 的一次性单元 `mpx update run
// control` 干的活,两边靠 update 目录里的请求/状态文件对接。
func (s *Server) handleUpdateControl(w http.ResponseWriter, r *http.Request) {
	if s.updateDir == "" {
		writeError(w, http.StatusServiceUnavailable, "控制面未配置库路径")
		return
	}

	var body struct {
		Version        string `json:"version"`
		AllowDowngrade bool   `json:"allow_downgrade"`
		Action         string `json:"action"`
	}
	// 空正文等价于"按默认升级到最新版",不该因为没带 body 就 400。
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}

	body.Version = strings.TrimSpace(body.Version)
	if body.Version != "" && !controlVersionRe.MatchString(body.Version) {
		writeError(w, http.StatusBadRequest, "version 必须是形如 v0.7.0 的版本号,或留空表示最新")
		return
	}
	if body.Action == "" {
		body.Action = "upgrade"
	}
	if !controlRunActions[body.Action] {
		writeError(w, http.StatusBadRequest, `action 只能是 "upgrade" 或 "rollback"`)
		return
	}

	reqPath := filepath.Join(s.updateDir, controlRequestFile)
	statePath := filepath.Join(s.updateDir, controlStateFile)
	if st, ok, err := selfupdate.ReadControlState(statePath); err != nil {
		s.logger.Error("读取控制面升级状态失败", "error", err)
		writeError(w, http.StatusInternalServerError, "读取控制面升级状态失败")
		return
	} else if ok && st.State != "" && st.State != "idle" {
		writeError(w, http.StatusConflict, "上一次升级还在进行")
		return
	}

	err := selfupdate.WriteControlRequest(reqPath, selfupdate.ControlRequest{
		Action:         body.Action,
		To:             body.Version,
		AllowDowngrade: body.AllowDowngrade,
		RequestedAt:    time.Now(),
	})
	switch {
	case errors.Is(err, selfupdate.ErrRequestPending):
		writeError(w, http.StatusConflict, "已有待执行的升级请求")
		return
	case err != nil:
		s.logger.Error("写入控制面升级请求失败", "error", err)
		writeError(w, http.StatusInternalServerError,
			s.updateDir+" 不可写,请用 install.sh 升级到支持远程升级的版本一次")
		return
	}

	to := body.Version
	if to == "" {
		to = "latest"
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"from":   buildinfo.Version(),
		"to":     to,
		"action": body.Action,
	})
}

// updateNodes 拼出每台节点装了哪些核心、各自是什么版本。
//
// 一行一个核心,而不是一行一个节点:混合节点上两个核心各自能升级,合成一行就
// 只能报其中一个的版本。装没装的判据跟下发那边一致(见 control.coreRequirements):
// 有该核心的入站、或该核心下发过基准哈希。少了这个判据,只用过 xray 的节点也会
// 冒出一行空版本的 sing-box。
func (s *Server) updateNodes() ([]model.UpdateNodeCore, error) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, err
	}
	statuses, err := s.store.ListNodeStatuses()
	if err != nil {
		return nil, err
	}
	// 一次取回所有入站,而不是每台节点查一次 —— 节点多了逐台查会变成 N 次查询。
	inbounds, err := s.store.ListInbounds(0)
	if err != nil {
		return nil, err
	}
	// sing-box 装没装看 core_deployments:「下发过基准」是它唯一的持久记录。
	// 不能只看状态里的 Cores:那要等一轮巡检,刚建好还没巡检的节点会漏报。
	deployed, err := s.store.DeployedNodesForCore(model.CoreSingBox)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.ListLocationMetadata("nodes")
	if err != nil {
		return nil, err
	}

	byNode := make(map[int64][]model.Inbound, len(nodes))
	for _, in := range inbounds {
		byNode[in.NodeID] = append(byNode[in.NodeID], in)
	}

	rows := make([]model.UpdateNodeCore, 0, len(nodes))
	for _, n := range nodes {
		mine := byNode[n.ID]
		xray, singbox := updateCorePresent(n, mine, deployed[n.ID])
		if !xray && !singbox {
			continue
		}
		display := model.NodeDisplay(meta[n.ID])
		if xray {
			rows = append(rows, model.UpdateNodeCore{
				NodeID: n.ID, Name: n.Name, Display: display, Core: model.CoreXray,
				Current: xrayVersion(statuses[n.ID]),
				// xray 的版本同样由控制面钉死:推荐版本跟着 MPX 发布走,而不是
				// 看节点上现在是什么。升级任务据此算出目标,CLI 据此显示"可升级"。
				Target: xrayrelease.RecommendedVersion,
			})
		}
		if singbox {
			rows = append(rows, model.UpdateNodeCore{
				NodeID: n.ID, Name: n.Name, Display: display, Core: model.CoreSingBox,
				Current: singBoxVersion(statuses[n.ID]),
				// sing-box 的版本是控制面钉死的,不是"看节点上现在是什么":
				// 这一行报的是"按计划该是多少",所以目标才算得出来。
				Target: singboxbuild.DefaultVersion,
			})
		}
	}
	return rows, nil
}

// updateCorePresent 报告这台节点上两种核心装没装。
//
// 判据与 control.coreRequirements 保持同一套:有该核心的入站、或该核心下发过
// 基准哈希就算装了。singboxDeployed 就是「这台节点有没有 sing-box 的下发基准」。
//
// 没有入站、两个核心都没下发过时,按 Xray 处理:老节点上的核心是手工装的,
// 库里没有它的下发记录,但它确实在跑,不该因此不出一行。
func updateCorePresent(n model.Node, inbounds []model.Inbound, singboxDeployed bool) (xray, singbox bool) {
	singbox = len(model.InboundsForCore(inbounds, model.CoreSingBox)) > 0 || singboxDeployed
	xray = len(model.InboundsForCore(inbounds, model.CoreXray)) > 0 || n.DeployedHash != "" || (len(inbounds) == 0 && !singbox)
	return xray, singbox
}

// xrayVersion 是这台节点上 Xray 的版本。
//
// 直接读 NodeStatus.Version:巡检脚本每次都先输出 xray 的版本到这一格,只有
// QUIC 单核心节点(没有任何 xray 入站、也没有 xray 下发记录)才会被镜像成
// sing-box 的版本(见 control.parseCheckOutput)。而那种节点根本不会走到这里
// —— updateCorePresent 不给它出 xray 行。
func xrayVersion(st model.NodeStatus) string { return st.Version }

// singBoxVersion 是这台节点上 sing-box 的版本,没查到过时为空。
func singBoxVersion(st model.NodeStatus) string {
	for _, c := range st.Cores {
		if c.Core == model.CoreSingBox {
			return c.Version
		}
	}
	return ""
}
