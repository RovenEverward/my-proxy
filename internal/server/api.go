package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// 这一层只做三件事:解析请求、校验、调存储。任何判断逻辑都不放在这里,
// 免得 CLI 与订阅两条路径出现两套规则。

// ---- 节点 ----

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if nodes == nil {
		nodes = []model.Node{}
	}
	writeJSON(w, http.StatusOK, nodes)
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
	writeJSON(w, http.StatusCreated, n)
}

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
	writeJSON(w, http.StatusOK, n)
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
	if s.deployer == nil {
		writeError(w, http.StatusNotImplemented, "控制面没有接上下发通道")
		return
	}
	if err := s.deployer.DeployNode(r.Context(), *n, time.Now()); err != nil {
		// 下发失败的原因原样回给使用者:那是要照着去节点上修的东西,
		// 换成一句"下发失败"等于把唯一的线索扔掉。
		s.logger.Error("下发节点失败", "node", n.Name, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.logger.Info("已下发节点", "node", n.Name, "address", n.Address)
	writeJSON(w, http.StatusOK, map[string]any{"node": n.Name, "synced": true})
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
	writeJSON(w, http.StatusOK, nil)
}

// ---- 对外入口 ----

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
	if hosts == nil {
		hosts = []model.Host{}
	}
	writeJSON(w, http.StatusOK, hosts)
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
	writeJSON(w, http.StatusCreated, h)
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
	writeJSON(w, http.StatusOK, h)
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
	writeJSON(w, http.StatusOK, nil)
}

// ---- 用户 ----

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	writeJSON(w, http.StatusOK, userViews(users))
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var u model.User
	if !decodeBody(w, r, &u) {
		return
	}
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
	var u model.User
	if !decodeBody(w, r, &u) {
		return
	}
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

	base := strings.TrimSuffix(settings.SubURLPrefix, "/")
	if base == "" {
		base = "http://" + r.Host
	}
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
	if err := s.store.SaveSettings(&v); err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	writeJSON(w, http.StatusOK, v)
}
