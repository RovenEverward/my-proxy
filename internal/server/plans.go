package server

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// 套餐(权限组)的管理接口,见 docs/internal/plans-rfc.md §3.6。
//
// 全部是新增路由,现有接口的字段与语义一个都不动。判定与授权集合的解释都在
// store 层的 loadPlanAuth 那一处,这里只做请求解析、校验与错误翻译。

// errInvalidPlanQuery 是 ?plan= 参数不合法时的错误,调用方据此回 400。
var errInvalidPlanQuery = errors.New("plan 必须是正整数")

func (s *Server) planRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/plans", s.handleListPlans)
	api.HandleFunc("POST /api/plans", s.handleCreatePlan)
	api.HandleFunc("GET /api/plans/{id}", s.handleGetPlan)
	api.HandleFunc("PUT /api/plans/{id}", s.handleUpdatePlan)
	api.HandleFunc("DELETE /api/plans/{id}", s.handleDeletePlan)
	api.HandleFunc("GET /api/plans/{id}/preview", s.handlePreviewPlan)
}

// planError 把套餐相关的存储错误翻成状态码:
//   - 默认套餐、重名、还有用户挂着:409(用户数在错误信息里);
//   - 找不到:404;
//   - 校验失败(空名、超长、引用不存在的节点/入站):400。
func (s *Server) planError(w http.ResponseWriter, err error, what string) {
	var inUse *store.PlanInUseError
	switch {
	case errors.Is(err, store.ErrDefaultPlan), errors.Is(err, store.ErrDuplicate), errors.As(err, &inUse):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrPlanNotFound):
		writeError(w, http.StatusNotFound, what+"不存在")
	case isValidation(err):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.writeStoreError(w, err, what)
	}
}

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListPlans()
	if err != nil {
		s.writeStoreError(w, err, "套餐")
		return
	}
	if plans == nil {
		plans = []model.Plan{}
	}
	writeJSON(w, http.StatusOK, plans)
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPlan(id)
	if err != nil {
		s.planError(w, err, "套餐")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	var p model.Plan
	if !decodeBody(w, r, &p) {
		return
	}
	// is_default 由系统维护,不接受调用方指定。
	p.ID = 0
	p.IsDefault = false
	if err := s.store.CreatePlan(&p); err != nil {
		s.planError(w, err, "套餐")
		return
	}
	s.invalidateSubCache()
	if got, err := s.store.GetPlan(p.ID); err == nil {
		writeJSON(w, http.StatusCreated, got)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var p model.Plan
	if !decodeBody(w, r, &p) {
		return
	}
	p.ID = id
	if err := s.store.UpdatePlan(&p); err != nil {
		s.planError(w, err, "套餐")
		return
	}
	s.invalidateSubCache()
	if got, err := s.store.GetPlan(id); err == nil {
		writeJSON(w, http.StatusOK, got)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleDeletePlan 删除套餐。
//
// 默认套餐 409;还有用户挂着时 409 并带上用户数;给了 ?reassign=<id> 先把用户
// 改挂到那个套餐,给了空的 ?reassign= 先把用户摘掉,改挂与删除在 store 的同一
// 个事务里完成。
func (s *Server) handleDeletePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var reassignTo *int64
	detach := false
	if q.Has("reassign") {
		raw := strings.TrimSpace(q.Get("reassign"))
		if raw == "" {
			detach = true
		} else {
			target, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || target <= 0 {
				writeError(w, http.StatusBadRequest, "reassign 必须是正整数,或者留空表示摘掉套餐")
				return
			}
			reassignTo = &target
		}
	}
	if err := s.store.DeletePlanReassign(id, reassignTo, detach); err != nil {
		s.planError(w, err, "套餐")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, nil)
}

// planPreviewNode 是套餐预览里的一个节点。
type planPreviewNode struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Display  string `json:"display,omitempty"`
	Draining bool   `json:"draining"`
	// Available 为 false 时 Reason 说明为什么它此刻不在订阅里(如「排水中」)。
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// planPreviewInbound 是套餐预览里的一个入站。Users 是此刻占着名额的可用用户数,
// MaxUsers 是上限(0 表示不限),Full 表示已满。Display 是节点显示名,订阅里看到
// 的名字以它为准;Tag 是入站自己的标识。
type planPreviewInbound struct {
	ID        int64  `json:"id"`
	NodeID    int64  `json:"node_id"`
	Node      string `json:"node"`
	Display   string `json:"display,omitempty"`
	Tag       string `json:"tag"`
	Protocol  string `json:"protocol"`
	Port      int    `json:"port"`
	MaxUsers  int    `json:"max_users,omitempty"`
	Users     int    `json:"users"`
	Full      bool   `json:"full"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// planPreview 是 GET /api/plans/{id}/preview 的响应:这个套餐此刻能用的节点与
// 入站(用节点显示名),以及因为排水中/满员而暂时不在订阅里的条目。
type planPreview struct {
	Plan     model.Plan           `json:"plan"`
	Nodes    []planPreviewNode    `json:"nodes"`
	Inbounds []planPreviewInbound `json:"inbounds"`
}

// handlePreviewPlan 回答「挂这个套餐的用户此刻会拿到哪些节点与入站」。
//
// 节点与入站的授权集合来自 store 的 PlanAuthInbounds(与 AllowsInbound 同一份
// 解释);满员计数复用订阅渲染那一处,所以预览里被标为「满员」的,订阅里也确实
// 不会出现。
func (s *Server) handlePreviewPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPlan(id)
	if err != nil {
		s.planError(w, err, "套餐")
		return
	}
	allNodes, authIDs, err := s.store.PlanAuthInbounds(id)
	if err != nil {
		s.planError(w, err, "套餐")
		return
	}
	authSet := make(map[int64]bool, len(authIDs))
	for _, i := range authIDs {
		authSet[i] = true
	}

	nodes, err := s.store.ListNodes()
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	inbounds, err := s.store.ListInbounds(0)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	users, err := s.store.ListUsers()
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	meta, err := s.store.ListPurposeMetadata("nodes")
	if err != nil {
		s.writeStoreError(w, err, "节点元数据")
		return
	}

	nodeByID := make(map[int64]model.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}
	// 纳入预览的节点:显式勾选的节点,加上授权入站所在但没被显式勾选的节点。
	nodeSet := make(map[int64]bool, len(p.NodeIDs))
	for _, nid := range p.NodeIDs {
		nodeSet[nid] = true
	}
	// all_nodes 套餐包含全部节点,即使某台节点上还没有入站也要列出来。
	if allNodes {
		for _, n := range nodes {
			nodeSet[n.ID] = true
		}
	}
	authorized := make([]model.Inbound, 0, len(inbounds))
	for _, in := range inbounds {
		if allNodes || authSet[in.ID] {
			authorized = append(authorized, in)
			nodeSet[in.NodeID] = true
		}
	}
	nodeIDs := make([]int64, 0, len(nodeSet))
	for nid := range nodeSet {
		if _, ok := nodeByID[nid]; ok {
			nodeIDs = append(nodeIDs, nid)
		}
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })

	occupancy := inboundOccupancy(users, inbounds, time.Now())

	out := planPreview{Plan: *p, Nodes: []planPreviewNode{}, Inbounds: []planPreviewInbound{}}
	for _, nid := range nodeIDs {
		n := nodeByID[nid]
		pn := planPreviewNode{
			ID:        n.ID,
			Name:      n.Name,
			Display:   model.NodeDisplay(meta[n.ID]),
			Draining:  n.Draining,
			Available: !n.Draining,
		}
		if n.Draining {
			pn.Reason = "节点排水中"
		}
		out.Nodes = append(out.Nodes, pn)
	}
	for _, in := range authorized {
		n := nodeByID[in.NodeID]
		used := occupancy[in.ID]
		full := in.MaxUsers > 0 && used >= in.MaxUsers
		pi := planPreviewInbound{
			ID:        in.ID,
			NodeID:    in.NodeID,
			Node:      n.Name,
			Display:   model.NodeDisplay(meta[in.NodeID]),
			Tag:       in.TagOrDefault(),
			Protocol:  string(in.Protocol),
			Port:      in.Port,
			MaxUsers:  in.MaxUsers,
			Users:     used,
			Full:      full,
			Available: !n.Draining && !full,
		}
		switch {
		case n.Draining:
			pi.Reason = "节点排水中"
		case full:
			pi.Reason = "入站满员"
		}
		out.Inbounds = append(out.Inbounds, pi)
	}
	writeJSON(w, http.StatusOK, out)
}

// planAuthUser 造一个「挂在该套餐上」的预览用户,供复用生产路径(端点解析、
// 策略组成员计算)时使用。它把 store 解释出来的授权直接装进用户,判定仍走
// AllowsInbound 那一处。
func (s *Server) planAuthUser(planID int64) (*model.User, error) {
	allNodes, ids, err := s.store.PlanAuthInbounds(planID)
	if err != nil {
		return nil, err
	}
	u := &model.User{Username: "preview", Key: "preview", Enabled: true, PlanID: &planID}
	u.SetPlanAuth(allNodes, ids)
	return u, nil
}

// endpointsForPlan 返回某个套餐范围内的端点,用于 ?plan= 的策略组预览。
func (s *Server) endpointsForPlan(planID int64) ([]render.Endpoint, error) {
	u, err := s.planAuthUser(planID)
	if err != nil {
		return nil, err
	}
	return s.endpointsFor(u)
}

// previewEndpointsFor 按可选的 plan 参数取预览端点:不带就用「能用全部入站的
// 用户」,带了就只看该套餐范围(见 RFC §3.5)。
func (s *Server) previewEndpointsFor(planArg string) ([]render.Endpoint, error) {
	planArg = strings.TrimSpace(planArg)
	if planArg == "" {
		return s.previewEndpoints()
	}
	id, err := strconv.ParseInt(planArg, 10, 64)
	if err != nil || id <= 0 {
		return nil, errInvalidPlanQuery
	}
	return s.endpointsForPlan(id)
}
