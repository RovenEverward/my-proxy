package server

import (
	"net/http"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/singboxbuild"
)

// handleUpdate 回控制面自己与各节点核心的版本,给 mpx update 用。
//
// 只读:不查上游、不 SSH 节点。所有结论都来自别处已经算好的东西 —— 上游检查是
// 后台任务按静默期在跑,节点版本是上一轮巡检观察到的。一个"看一下现在什么版本"
// 的操作不该顺手把每台节点连一遍,更不该因为某台连不上就整个失败。
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.updateNodes()
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	writeJSON(w, http.StatusOK, model.UpdateSnapshot{
		Control: s.controlUpdate(),
		Nodes:   nodes,
	})
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
	return part
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
	meta, err := s.store.ListPurposeMetadata("nodes")
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
