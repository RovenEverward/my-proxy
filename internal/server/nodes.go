package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// errNoNodeOps 是"服务端没接上节点操作"时的错误。
//
// 它只该出现在用 New 造出来、又没调 SetNodeOps 的服务端里(测试、或者重启期间)。
// 说清楚比抛一个空指针好:使用者看到"没接上"会去查自己的接线,看到 panic 只会
// 以为程序坏了。
var errNoNodeOps = errors.New("这个服务端没有接上节点操作(SetNodeOps)")

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
	meta, err := s.store.ListLocationMetadata("nodes")
	if err != nil {
		return nil, err
	}
	// ISS-04:模板漂移要的入站/镜像一次批量拿回来,不再逐台查两遍。
	allInbounds, err := s.store.ListInbounds(0)
	if err != nil {
		return nil, err
	}
	allMirrors, err := s.store.ListSubMirrors(0)
	if err != nil {
		return nil, err
	}
	inboundsByNode := map[int64][]model.Inbound{}
	for _, in := range allInbounds {
		inboundsByNode[in.NodeID] = append(inboundsByNode[in.NodeID], in)
	}
	mirrorsByNode := map[int64][]model.SubMirror{}
	for _, m := range allMirrors {
		mirrorsByNode[m.NodeID] = append(mirrorsByNode[m.NodeID], m)
	}

	views := make([]model.NodeView, 0, len(nodes))
	for _, n := range nodes {
		v := model.NodeView{Node: n, Display: model.NodeDisplay(meta[n.ID])}
		s.markTemplateDrift(&v, inboundsByNode[n.ID], mirrorsByNode[n.ID])
		if st, ok := statuses[n.ID]; ok {
			// 拷一份再取地址:循环变量每轮复用,直接 &st 会让所有视图指向同一个值。
			copied := st
			v.Status = &copied
		}
		views = append(views, v)
	}
	return views, nil
}

// markTemplateDrift 算一台节点的模板漂移:按现在这版控制面的写法渲染出的模板
// 哈希,和这台节点下发时记下的不一致,就置上 TemplateDrift。
// 模板漂移:新版本改了节点配置的写法,应用一次才生效(和「有改动未应用」是两回事)。
//
// ISS-04:渲染结果按「节点+入站+镜像」的指纹缓存(templateHashCache),
// 输入指纹不变就不重渲染 —— 节点页每次刷新都过一遍纯 CPU 渲染是白跑。
// 没在这一版上下发过(存着的模板哈希是空串)不算飘移,否则每台节点升级控制面后
// 都会先亮一次;读入站或渲染失败只记日志、留 false,不该让列出节点整个失败。
func (s *Server) markTemplateDrift(v *model.NodeView, inbounds []model.Inbound, mirrors []model.SubMirror) {
	if v.DeployedTemplateHash == "" {
		return
	}
	fp := templateInputsFingerprint(v.Node, inbounds, mirrors)
	hash, err := s.cachedTemplateHash(fp, v.Node, inbounds, mirrors)
	if err != nil {
		s.logger.Warn("算节点模板漂移失败:渲染不出配置模板", "node", v.Name, "error", err)
		return
	}
	v.TemplateDrift = hash != v.DeployedTemplateHash
}

// templateInputsFingerprint 是模板渲染输入的指纹:节点对象、它的入站、
// 它的订阅镜像三者任一变化,指纹就变,缓存自然失效 —— 不需要主动失效。
// DeployedTemplateHash 这类「结果字段」也在指纹里:下发会改它,缓存重算一次。
func templateInputsFingerprint(n model.Node, inbounds []model.Inbound, mirrors []model.SubMirror) string {
	raw, _ := json.Marshal(struct {
		N model.Node        `json:"n"`
		I []model.Inbound   `json:"i"`
		M []model.SubMirror `json:"m"`
	}{n, inbounds, mirrors})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// cachedTemplateHash 按输入指纹拿渲染出的模板哈希:命中直接用,没中才渲染。
// 渲染是纯 CPU(不用 ssh、不用查库),缓存只为省 CPU;指纹就是失效键。
func (s *Server) cachedTemplateHash(fp string, n model.Node, inbounds []model.Inbound, mirrors []model.SubMirror) (string, error) {
	s.templateHashMu.Lock()
	if s.templateHashCache == nil {
		s.templateHashCache = map[string]string{}
	}
	if h, ok := s.templateHashCache[fp]; ok {
		s.templateHashMu.Unlock()
		return h, nil
	}
	s.templateHashMu.Unlock()

	hash, err := control.TemplateHash(n, inbounds, mirrors)
	if err != nil {
		return "", err
	}
	s.templateHashMu.Lock()
	s.templateHashCache[fp] = hash
	s.templateHashMu.Unlock()
	return hash, nil
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
	// 现场确认可达就是「成功一次」:退避该清掉,别让它在旧窗口里继续被跳过。
	if st.Reachable && s.backoff != nil {
		s.backoff.ClearNodeBackoff(n.ID)
	}
	return st, nil
}

// createNodeRequest 是 POST /api/nodes 的请求体。
//
// Node 之外的字段都不是库里的东西:SSHPassword 只是一次性的钥匙,登一次就烧掉,
// 所以不进 model.Node —— 进库就等于「密码存了下来」,那是另一回事。
type createNodeRequest struct {
	model.Node
	SSHPassword string `json:"ssh_password,omitempty"`
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var req createNodeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	n := req.Node
	if msg := validateNode(&n); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if msg := numericNodeName(n.Name); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	d := model.CoreDeployment{APIPort: n.APIPortOrDefault(), ConfigPath: n.ConfigPathOrDefault(), ServiceName: n.ServiceNameOrDefault()}
	other := model.CoreDeployment{Core: model.CoreSingBox}
	other.APIPort, other.ConfigPath, other.ServiceName = other.APIPortOrDefault(), other.ConfigPathOrDefault(), other.ServiceNameOrDefault()
	if msg := validateCoreDeployment(&d, other, nil); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.SSHPassword != "" {
		// 密码来报到:先用它把控制面公钥装上节点,装不上就当这次建节点没发生。
		// 失败原样回给使用者:密码错、密码认证没开、连不上,都是从 ssh 那边
		// 原话带回来的线索。
		pubkey, err := node.EnsureSSHKey(s.sshKeyPath)
		if err != nil {
			s.logger.Error("准备控制面 SSH 密钥失败", "error", err)
			writeError(w, http.StatusInternalServerError, "准备控制面 SSH 密钥失败: "+err.Error())
			return
		}
		if err := node.BootstrapNode(r.Context(), n, s.sshKeyPath, pubkey, req.SSHPassword); err != nil {
			s.logger.Error("密码引导节点失败", "node", n.Name, "error", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		s.logger.Info("已用密码把公钥装上节点", "node", n.Name, "address", n.Address)
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
	if m, err := s.store.GetLocationMetadata("nodes", n.ID); err == nil {
		view.Display = model.NodeDisplay(m)
	}
	inbounds, err := s.store.ListInbounds(n.ID)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	mirrors, err := s.store.ListSubMirrors(n.ID)
	if err != nil {
		s.writeStoreError(w, err, "订阅镜像")
		return
	}
	s.markTemplateDrift(&view, inbounds, mirrors)
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
	// 已有的纯数字名照常能改别的字段;只拦「改成」纯数字。
	if old, err := s.store.GetNode(id); err == nil && old.Name != n.Name {
		if msg := numericNodeName(n.Name); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	other, err := s.store.GetCoreDeployment(id, model.CoreSingBox)
	if err != nil {
		s.writeStoreError(w, err, "节点核心")
		return
	}
	inbounds, err := s.store.ListInbounds(id)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	d := model.CoreDeployment{APIPort: n.APIPortOrDefault(), ConfigPath: n.ConfigPathOrDefault(), ServiceName: n.ServiceNameOrDefault()}
	if msg := validateCoreDeployment(&d, *other, inbounds); msg != "" {
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
	// 节点没了,它攒下的退避条也跟着清:不然 /api/status 会一直给一台
	// 已删除的节点报「在退避中」,直到控制面重启。
	if s.backoff != nil {
		s.backoff.ClearNodeBackoff(id)
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
	// 不用 r.Context():客户端断连不该把已经发出的下发半途杀掉 —— 那会让节点
	// 停在旧配置、运维还以为是失败。HTTP 层的 deadline 已由 deployBudget 罩住。
	if err := s.nodeOps.DeployNode(context.Background(), *n, time.Now()); err != nil {
		// 下发失败的原因原样回给使用者:那是要照着去节点上修的东西,
		// 换成一句"下发失败"等于把唯一的线索扔掉。
		s.logger.Error("下发节点失败", "node", n.Name, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.logger.Info("已下发节点", "node", n.Name, "address", n.Address)
	s.clearNodePending(n.ID)
	writeJSON(w, http.StatusOK, map[string]any{"node": n.Name, "synced": true})
}

// retryBackoffBudget 是人工重试一台退避节点的预算:采样(15s)+ 收敛(60s)+
// 刷新状态(30s)各按控制面预算取最坏,再加一点余量。客户端断连不该把已经
// 开始的收敛杀掉,所以不和请求上下文绑在一起(同 handleSyncNode)。
const retryBackoffBudget = 3 * time.Minute

// handleRetryNodeBackoff 人工重试一台退避中的节点。
//
// 后台任务对不通的节点按退避节奏跳过多轮采样与收敛;操作者把节点修好之后
// (比如欠费的恢复了),等下一个窗口到点可能还要一小时。这条接口立刻跑一轮:
// 通了就把退避清零、顺手刷新节点状态,不通就按原样记一次失败继续退避。
func (s *Server) handleRetryNodeBackoff(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if s.backoff == nil {
		writeError(w, http.StatusNotImplemented, "控制面没有接上退避重试通道")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), retryBackoffBudget)
	defer cancel()
	if err := s.backoff.RetryNodeBackoff(ctx, *n); err != nil {
		// 原因原样回给使用者(ssh 原话最有用),和 handleSyncNode 一个规矩。
		s.logger.Warn("人工重试节点未通", "node", n.Name, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.logger.Info("人工重试节点成功", "node", n.Name)
	writeJSON(w, http.StatusOK, map[string]any{"node": n.Name, "retried": true})
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
	if errors.Is(err, node.ErrOnlineStatsOff) {
		// 读不到 ≠ 没人在线:配置没开在线统计时明说,前端据此提示应用配置。
		writeError(w, http.StatusConflict, err.Error())
		return
	}
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
	mirrors, err := s.store.ListSubMirrors(n.ID)
	if err != nil {
		return nil, err
	}
	return render.ServerConfig(*n, inbounds, users, mirrors, time.Now())
}
