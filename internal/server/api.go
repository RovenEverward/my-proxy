package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// errNoNodeOps 是"服务端没接上节点操作"时的错误。
//
// 它只该出现在用 New 造出来、又没调 SetNodeOps 的服务端里(测试、或者重启期间)。
// 说清楚比抛一个空指针好:使用者看到"没接上"会去查自己的接线,看到 panic 只会
// 以为程序坏了。
var errNoNodeOps = errors.New("这个服务端没有接上节点操作(SetNodeOps)")

// 这一层只做三件事:解析请求、校验、调存储。任何判断逻辑都不放在这里,
// 免得 CLI 与订阅两条路径出现两套规则。

// ---- 节点 ----

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	views, err := s.nodeViews(nodes)
	if err != nil {
		s.writeStoreError(w, err, "节点状态")
		return
	}
	if views == nil {
		views = []model.NodeView{}
	}
	writeJSON(w, http.StatusOK, views)
}

// nodeViews 把节点列表配上各自最近一次的状态。
//
// 一次取回全部状态再拼,而不是每台节点查一次:列表页每行都要状态,逐台查会变成
// N 次查询。状态可能缺(还没查过),这时 Status 是 nil —— 客户端据此显示"未知"。
func (s *Server) nodeViews(nodes []model.Node) ([]model.NodeView, error) {
	statuses, err := s.store.ListNodeStatuses()
	if err != nil {
		return nil, err
	}
	views := make([]model.NodeView, 0, len(nodes))
	for _, n := range nodes {
		v := model.NodeView{Node: n}
		if st, ok := statuses[n.ID]; ok {
			// 拷一份再取地址:循环变量每轮复用,直接 &st 会让所有视图指向同一个值。
			copied := st
			v.Status = &copied
		}
		views = append(views, v)
	}
	return views, nil
}

// freshNodeStatus 现场查一次并落库。
//
// 被 GET /api/nodes/{id}?fresh=1 与 doctor 共用 —— 两处必须走同一条路径,
// 否则 doctor 看到的东西和 node status 看到的会不一样。
func (s *Server) freshNodeStatus(ctx context.Context, n model.Node, now time.Time) (model.NodeStatus, error) {
	if s.nodeOps == nil {
		return model.NodeStatus{}, errNoNodeOps
	}
	st, err := s.nodeOps.CheckNode(ctx, n, now)
	if err != nil {
		return model.NodeStatus{}, err
	}
	if err := s.store.UpsertNodeStatus(st); err != nil {
		return model.NodeStatus{}, err
	}
	return st, nil
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var n model.Node
	if !decodeBody(w, r, &n) {
		return
	}
	if msg := validateNode(&n); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.CreateNode(&n); err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusCreated, n)
}

// handleGetNode 回答一台节点,连同它最近一次的状态。
//
// ?fresh=1 时先现场 ssh 查一次再回答:查的结果会落库,所以客户端拿到的东西和
// "下一次 node list"看到的一致,不会出现"刚查过、列表里还是旧的"。
func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}

	view := model.NodeView{Node: *n}
	if r.URL.Query().Get("fresh") == "1" {
		st, err := s.freshNodeStatus(r.Context(), *n, time.Now())
		if err != nil {
			s.writeStoreError(w, err, "节点状态")
			return
		}
		view.Status = &st
		writeJSON(w, http.StatusOK, view)
		return
	}

	// 状态读不出来(还没查过)不算错误:NodeView 的 Status 本来就是可空的,
	// 而"没查过"和"查过但连不上"是两件事,前者不该冒充后者。
	if st, err := s.store.GetNodeStatus(id); err == nil {
		view.Status = st
	} else if !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, err, "节点状态")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var n model.Node
	if !decodeBody(w, r, &n) {
		return
	}
	n.ID = id
	if msg := validateNode(&n); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateNode(&n); err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteNode(id); err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, nil)
}

// handlePreviewNodeConfig 只渲染不下发,让 `mpx node config` 能先看再推。
func (s *Server) handlePreviewNodeConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	cfg, err := s.renderNodeConfig(n)
	if err != nil {
		s.logger.Error("渲染节点配置失败", "node", n.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "渲染节点配置失败")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(cfg)
}

// handleSyncNode 把配置下发到节点并重启它的 xray。
//
// 这是唯一会重启节点上 xray 的动作,所以只有使用者显式说了才做:重启会让该节点
// 上所有人的连接断一次。日常的增删用户不走这里(见 control.ReconcileUsers)。
func (s *Server) handleSyncNode(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if s.nodeOps == nil {
		writeError(w, http.StatusNotImplemented, "控制面没有接上下发通道")
		return
	}
	if err := s.nodeOps.DeployNode(r.Context(), *n, time.Now()); err != nil {
		// 下发失败的原因原样回给使用者:那是要照着去节点上修的东西,
		// 换成一句"下发失败"等于把唯一的线索扔掉。
		s.logger.Error("下发节点失败", "node", n.Name, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.logger.Info("已下发节点", "node", n.Name, "address", n.Address)
	writeJSON(w, http.StatusOK, map[string]any{"node": n.Name, "synced": true})
}

// handleInstallNode 在节点上装好 xray 核心并把它跑起来。
//
// 和 sync 的分工:sync 只管把配置送过去(核心得已经在节点上),install 负责把核心
// 和它的服务准备出来。install 自己会下发一次配置,所以全新机器上跑完 install 就
// 能用了,sync 只留给"只改了配置、不动核心"的场合。
func (s *Server) handleInstallNode(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if s.nodeOps == nil {
		writeError(w, http.StatusNotImplemented, "控制面没有接上下发通道")
		return
	}
	version := r.URL.Query().Get("version")
	installed, err := s.nodeOps.InstallNode(r.Context(), *n, version)
	if err != nil {
		s.logger.Error("安装节点核心失败", "node", n.Name, "error", err)
		// 版本非空说明核心确实已经推上去了,是后面的配置没让服务起来 ——
		// 这两种失败该做的事不一样,不分开说清楚,使用者会去重装一遍。
		if installed != "" {
			writeError(w, http.StatusBadGateway, "节点上的核心已经换成 "+installed+",但服务没能起来: "+err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.logger.Info("已安装节点核心", "node", n.Name, "version", installed, "address", n.Address)
	writeJSON(w, http.StatusOK, map[string]any{"node": n.Name, "version": installed})
}

// handleDrainNode 标记节点开始排水:订阅渲染不再把它的入口发给用户。
//
// 它只做订阅层的事 —— 不踢 xray 上的人(那是收敛或手动 rmu 的事,各有各的节奏)。
// 意图是「开始把人挪走」,不是「现在切断」:已连上的客户端会随下一次刷新订阅拿到
// 不含这台节点的列表、自然漂走;真要掐线,运维跑 node rm / 手动 rmu。
func (s *Server) handleDrainNode(w http.ResponseWriter, r *http.Request) {
	s.setNodeDraining(w, r, true)
}

// handleUndrainNode 取消排水。
func (s *Server) handleUndrainNode(w http.ResponseWriter, r *http.Request) {
	s.setNodeDraining(w, r, false)
}

func (s *Server) setNodeDraining(w http.ResponseWriter, r *http.Request, draining bool) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if n.Draining == draining {
		// 幂等:重复执行直接回现状。drain 是个手动运维动作,使用者不会想为它报错。
		writeJSON(w, http.StatusOK, n)
		return
	}
	n.Draining = draining
	if err := s.store.UpdateNode(n); err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	s.invalidateSubCache()
	s.logger.Info("节点排水状态已改", "node", n.Name, "draining", draining)
	writeJSON(w, http.StatusOK, n)
}

// handleNodeOnline 现场问节点:每个用户此刻连着几个 IP。答案是 xray 的统计,
// 不过时:不缓存、不落库,因为「此刻」就是问题本身;要趋势请自己轮询采样。
func (s *Server) handleNodeOnline(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if s.nodeOps == nil {
		writeError(w, http.StatusNotImplemented, "控制面没有接上下发通道")
		return
	}
	online, err := s.nodeOps.OnlineIPs(r.Context(), *n)
	if err != nil {
		s.logger.Error("读节点在线名单失败", "node", n.Name, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// 把 IP 数算出来是为了让 CLI/前端直接显示"在线"两个字 —— IP 列表本身对
	// 普通人没意义,但我们要留下它(排查共享账号要用)。
	type entry struct {
		User  string   `json:"user"`
		Count int      `json:"count"`
		IPs   []string `json:"ips"`
	}
	out := make([]entry, 0, len(online))
	for user, ips := range online {
		out = append(out, entry{User: user, Count: len(ips), IPs: ips})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User < out[j].User })
	writeJSON(w, http.StatusOK, map[string]any{"node": n.Name, "online": out})
}

// renderNodeConfig 产出某节点完整的 xray config.json。
func (s *Server) renderNodeConfig(n *model.Node) ([]byte, error) {
	inbounds, err := s.store.ListInbounds(n.ID)
	if err != nil {
		return nil, err
	}
	users, err := s.store.ListUsers()
	if err != nil {
		return nil, err
	}
	return render.ServerConfig(*n, inbounds, users, time.Now())
}

// ---- 入站 ----

func (s *Server) handleListInbounds(w http.ResponseWriter, r *http.Request) {
	var nodeID int64
	if v := r.URL.Query().Get("node_id"); v != "" {
		id, ok := parseInt(w, v)
		if !ok {
			return
		}
		nodeID = id
	}
	inbounds, err := s.store.ListInbounds(nodeID)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if inbounds == nil {
		inbounds = []model.Inbound{}
	}
	writeJSON(w, http.StatusOK, inbounds)
}

func (s *Server) handleCreateInbound(w http.ResponseWriter, r *http.Request) {
	var in model.Inbound
	if !decodeBody(w, r, &in) {
		return
	}
	if err := ensureReality(&in, nil); err != nil {
		s.writeStoreError(w, err, "REALITY 密钥")
		return
	}
	if err := ensureWireguard(&in, nil); err != nil {
		s.writeStoreError(w, err, "wireguard 密钥")
		return
	}
	if msg := validateInbound(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// 默认 tag 落库,而不是每次用它的时候现算:节点上的增删用户、日志、渲染都按
	// tag 认入站,让它们各自去推同一个默认值迟早会推出分歧。
	in.Tag = in.TagOrDefault()
	if _, err := s.store.GetNode(in.NodeID); err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if err := s.store.CreateInbound(&in); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) handleGetInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	in, err := s.store.GetInbound(id)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleUpdateInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var in model.Inbound
	if !decodeBody(w, r, &in) {
		return
	}
	in.ID = id
	old, err := s.store.GetInbound(id)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	in.NodeID = old.NodeID
	if err := ensureReality(&in, old); err != nil {
		s.writeStoreError(w, err, "REALITY 密钥")
		return
	}
	if err := ensureWireguard(&in, old); err != nil {
		s.writeStoreError(w, err, "wireguard 密钥")
		return
	}
	if msg := validateInbound(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// 没给 tag 就沿用原来的:换了 tag 等于在节点上换了一个入站,老的那个不会被删。
	if in.Tag == "" {
		in.Tag = old.TagOrDefault()
	}
	if err := s.store.UpdateInbound(&in); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleDeleteInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteInbound(id); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, nil)
}

// ---- 对外入口 ----

// hostViewOf 给一个对外入口补上生效值。合并规则在 render 里,这里只负责把它的
// 两个输入(入站、节点)取出来。
func (s *Server) hostViewOf(h model.Host) (model.HostView, error) {
	in, err := s.store.GetInbound(h.InboundID)
	if err != nil {
		return model.HostView{}, err
	}
	n, err := s.store.GetNode(in.NodeID)
	if err != nil {
		return model.HostView{}, err
	}
	return model.HostView{Host: h, Effective: render.ResolveExposure(*n, *in, h)}, nil
}

// writeHostView 把一个对外入口连同它的生效值写回去。
func (s *Server) writeHostView(w http.ResponseWriter, code int, h model.Host) {
	view, err := s.hostViewOf(h)
	if err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	writeJSON(w, code, view)
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	hosts, err := s.store.ListHosts(id)
	if err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	views := make([]model.HostView, 0, len(hosts))
	for _, h := range hosts {
		v, err := s.hostViewOf(h)
		if err != nil {
			s.writeStoreError(w, err, "对外入口")
			return
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	inboundID, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var h model.Host
	if !decodeBody(w, r, &h) {
		return
	}
	h.InboundID = inboundID
	if msg := validateHost(&h); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if _, err := s.store.GetInbound(inboundID); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if err := s.store.CreateHost(&h); err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	s.invalidateSubCache()
	s.writeHostView(w, http.StatusCreated, h)
}

func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var h model.Host
	if !decodeBody(w, r, &h) {
		return
	}
	h.ID = id
	if msg := validateHost(&h); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateHost(&h); err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	s.invalidateSubCache()
	s.writeHostView(w, http.StatusOK, h)
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteHost(id); err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, nil)
}

// ---- 用户 ----

// 用户列表的默认与最大页大小。
//
// 默认不是"全部":列表每行都要渲染,一千个用户刷屏既看不见也没法用;要一次拿完
// 就自己按 offset 翻页(每页最多 1000)。
const (
	defaultUserPageSize = 50
	maxUserPageSize     = 1000
)

// userPage 是 GET /api/users 的响应:一页用户加上匹配总数。
//
// 总数必须有:客户端靠"offset+本页条数 < total"才能说"还有 N 个",只看本页满没满
// 在整除时会猜错。它是筛选之后的总数,不是本页条数。
type userPage struct {
	Users  []userView `json:"users"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

// handleListUsers 列出用户,支持筛选与分页。
//
// 筛选参数:status(按 model.User.Status 的判定)、search(用户名子串,不分大小写)、
// inbound_id(可用该入站的用户)。判定不在这里做,一律交给 store —— 它复用 model
// 里那份唯一的判定,这里再写一遍就会有两份。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := userPage{Limit: defaultUserPageSize}

	if v := q.Get("limit"); v != "" {
		n, ok := parseInt(w, v)
		if !ok {
			return
		}
		if n < 1 || n > maxUserPageSize {
			writeError(w, http.StatusBadRequest, "limit 要在 1 到 "+strconv.Itoa(maxUserPageSize)+" 之间")
			return
		}
		page.Limit = int(n)
	}
	if v := q.Get("offset"); v != "" {
		n, ok := parseInt(w, v)
		if !ok {
			return
		}
		if n < 0 {
			writeError(w, http.StatusBadRequest, "offset 不能是负数")
			return
		}
		page.Offset = int(n)
	}

	status := model.Status(q.Get("status"))
	if status != "" && !status.Valid() {
		writeError(w, http.StatusBadRequest, "status 只能是 "+statusChoices()+"(按判定优先级排列)")
		return
	}

	var inboundID int64
	if v := q.Get("inbound_id"); v != "" {
		id, ok := parseInt(w, v)
		if !ok {
			return
		}
		inboundID = id
	}

	users, total, err := s.store.ListUsersPage(store.UserQuery{
		Status:    status,
		Search:    q.Get("search"),
		InboundID: inboundID,
		Limit:     page.Limit,
		Offset:    page.Offset,
	})
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	page.Users = userViews(users)
	page.Total = total
	writeJSON(w, http.StatusOK, page)
}

// statusChoices 拼出状态取值的提示。取值列表来自 model,不在这里另抄一份 ——
// 新加一个状态时只需要改 model。
func statusChoices() string {
	parts := make([]string, 0, len(model.UserStatuses))
	for _, s := range model.UserStatuses {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, " / ")
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if !decodeBody(w, r, &req) {
		return
	}
	u := req.User
	// 请求体里没提 enabled,就是启用 —— 新建的用户默认能用。
	u.Enabled = req.takeEnabled(false, true)
	if msg := validateUser(&u); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if _, err := s.store.GetUser(u.Username); err == nil {
		writeError(w, http.StatusConflict, "用户名已存在")
		return
	}
	if err := fillUserSecrets(&u); err != nil {
		writeError(w, http.StatusInternalServerError, "生成密钥失败")
		return
	}
	if err := s.store.CreateUser(&u); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusCreated, toUserView(u))
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUser(r.PathValue("username"))
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	writeJSON(w, http.StatusOK, toUserView(*u))
}

// handleUpdateUser 是整体替换:调用方先读、改、再写回。
//
// 留空的 Key / SubToken 会沿用原值,这样 CLI 不必把它们回传。
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("username")
	old, err := s.store.GetUser(name)
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	var req userRequest
	if !decodeBody(w, r, &req) {
		return
	}
	u := req.User
	// 改用户是整体替换,但没提 enabled 时按保持原样 —— 不然只想改流量上限
	// 的一次 PUT 会把停用的用户悄悄启用。
	u.Enabled = req.takeEnabled(old.Enabled, false)
	u.ID = old.ID
	u.Username = name
	if msg := validateUser(&u); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if u.Key == "" {
		u.Key = old.Key
	}
	if u.SubToken == "" {
		u.SubToken = old.SubToken
	}
	u.CreatedAt = old.CreatedAt
	if err := s.store.UpdateUser(&u); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, toUserView(u))
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteUser(r.PathValue("username")); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// handleResetUserTraffic 手动把用户的周期用量归零。
//
// 起算点一并推到 now,于是手动重置同时也把周期窗口重新开始 —— 用户要求"重新
// 开始算"时想的就是这个意思,只清数字却让周期照旧走到头会显得莫名其妙。
func (s *Server) handleResetUserTraffic(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ResetUserTraffic(r.PathValue("username"), time.Now()); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// handleRevokeSub 换掉订阅 token,不动用户的协议凭证。
func (s *Server) handleRevokeSub(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUser(r.PathValue("username"))
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	token, err := model.NewSubscriptionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成订阅 token 失败")
		return
	}
	u.SubToken = token
	if err := s.store.UpdateUser(u); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, toUserView(*u))
}

// handleUserLinks 给出该用户在各格式下的订阅地址。
func (s *Server) handleUserLinks(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUser(r.PathValue("username"))
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}

	base := subBaseURL(settings, r)
	sub := base + "/sub/" + u.SubToken

	out := map[string]string{"auto": sub}
	for _, f := range []model.ClientFormat{
		model.FormatLinks, model.FormatBase64, model.FormatClash, model.FormatClashMeta, model.FormatSingBox, model.FormatXray,
	} {
		out[string(f)] = sub + "/" + string(f)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- 设置 ----

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var v model.Settings
	if !decodeBody(w, r, &v) {
		return
	}
	for _, rule := range v.Rules {
		if !rule.Result.Valid() {
			writeError(w, http.StatusBadRequest, "规则结果不合法: "+string(rule.Result))
			return
		}
		if _, err := compilePattern(rule.Pattern); err != nil {
			writeError(w, http.StatusBadRequest, "规则正则不合法: "+rule.Pattern)
			return
		}
	}
	// 整体替换设置时,带进来的模板也要过一遍校验;但未出现的格式保持原样
	// (整体替换语义),已出现的格式里空串表示清空该模板。
	for format, body := range v.Templates {
		f := model.ClientFormat(format)
		if !templateFormats[f] {
			writeError(w, http.StatusBadRequest, "不支持自定义模板的格式: "+format)
			return
		}
		if body != "" {
			if err := validateTemplate(f, []byte(body)); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	// 通知阈值允许 0(表示关掉),负数没有意义 —— 放进去只会让判断永远为假,
	// 使用者却以为开着。
	if v.NotifyDaysLeft < 0 {
		writeError(w, http.StatusBadRequest, "到期提醒天数不能是负数")
		return
	}
	if v.NotifyUsagePercent < 0 || v.NotifyUsagePercent > 100 {
		writeError(w, http.StatusBadRequest, "用量提醒百分比要在 0 到 100 之间")
		return
	}
	if err := validateWebhookURL(v.WebhookURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SaveSettings(&v); err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, v)
}

// validateWebhookURL 检查通知地址是不是一个能 POST 的 http(s) 地址。
//
// 空串合法,表示不通知。只认 http/https:这个地址是我们主动发出去的,别的 scheme
// (file://、javascript:)在这里没有任何意义,放行只会把一个配置错误推迟成运行时的
// 怪错误 —— 那时候人已经忘了自己配过什么。
//
// 校验放在写入口而不是发送时:发的时候才发现地址不对,那条通知已经错过了。
func validateWebhookURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("webhook 地址不是合法 URL: %s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook 地址要以 http:// 或 https:// 开头: %s", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("webhook 地址缺少主机名: %s", raw)
	}
	return nil
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

// ---- 状态 ----

// handleStatus 回答控制面此刻怎么样:后台任务的心跳、各类对象的数量、正在退避
// 的节点。
//
// 全部来自内存与本地库,不发 SSH —— 它要在节点不通的时候也能立刻回答,「节点
// 怎么样了」是 3b 里 node status 的事。后台任务没接上时心跳给零值,数量照给。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var out model.ControlStatus
	if s.status != nil {
		out = s.status.Snapshot(time.Now())
	} else {
		out.Backoffs = []model.NodeBackoff{}
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
	hosts, err := s.store.ListHosts(0)
	if err != nil {
		s.writeStoreError(w, err, "对外入口")
		return
	}
	users, err := s.store.ListUsers()
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	out.Counts = model.Counts{
		Nodes:    len(nodes),
		Inbounds: len(inbounds),
		Hosts:    len(hosts),
		Users:    len(users),
	}
	writeJSON(w, http.StatusOK, out)
}
