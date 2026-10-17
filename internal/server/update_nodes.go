package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/singboxbuild"
	"github.com/RovenEverward/my-proxy/internal/xrayrelease"
)

// 节点核心升级进度接口的几个上限。
//
// 事件只放内存、只增不删,响应要能在很长一段时间里被反复拉取:一次最多带 20 条、
// 每条消息截到 200 字节、整段响应控制在 4KB 的余量内。客户端按 seq 增量拉,拉到
// 的这批被截断了就下一轮继续,不会丢事件。
const (
	nodeUpdateMaxEvents  = 20
	nodeUpdateMsgLimit   = 200
	nodeUpdateBodyBudget = 3500
)

// errNoNodeUpgrader 是节点操作通道没接上自升级能力的错误。
//
// 和 errNoNodeOps 一样,说清楚比抛空指针好:接不上时任务会失败并把这句话写进
// 事件,使用者一眼知道是要去查接线。
var errNoNodeUpgrader = errors.New("这个服务端没有接上节点自升级通道(SetNodeOps 且实现 CoreUpgrader)")

// nodeUpdateState 是一次节点核心升级任务的内存状态。
//
// job 每次启动自增,客户端用它确认自己跟的是不是同一轮;state 是 idle/running/
// done/failed;seq 是已产生事件的总序号;events 按 seq 递增保存。全部字段由 mu
// 保护 —— 任务 goroutine 在写,进度接口在读。
type nodeUpdateState struct {
	mu     sync.Mutex
	job    int64
	state  string
	seq    int64
	events []model.UpdateNodeEvent
}

// handleUpdateNodesStart 启动一次节点核心一键升级。
//
// 同一时刻只允许一轮:已经有一轮在跑就 409,而不是排队或悄悄合并 —— 连续点两下
// 应当得到"正在跑"的明确回答。任务在后台跑,202 只回一个 job id。
func (s *Server) handleUpdateNodesStart(w http.ResponseWriter, r *http.Request) {
	job, ok := s.beginNodeUpdate()
	if !ok {
		writeError(w, http.StatusConflict, "已有节点核心升级任务在跑")
		return
	}
	go s.runNodeUpdate()
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

// handleUpdateNodesProgress 回答升级任务到此刻为止的进度。
//
// since 是客户端上次拿到的游标(事件 seq),只回比它新的事件。游标非法就 400,
// 而不是默默当成 0:那会让客户端以为拿到了从头开始的全量。
func (s *Server) handleUpdateNodesProgress(w http.ResponseWriter, r *http.Request) {
	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "since 必须是非负整数")
			return
		}
		since = n
	}
	writeJSON(w, http.StatusOK, s.nodeUpdateProgress(since))
}

// beginNodeUpdate 尝试占用任务槽。返回 false 表示已有一轮在跑。
func (s *Server) beginNodeUpdate() (int64, bool) {
	st := &s.nodeUpdate
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.state == model.UpdateNodesRunning {
		return 0, false
	}
	st.job++
	st.state = model.UpdateNodesRunning
	st.seq = 0
	st.events = nil
	return st.job, true
}

// setNodeUpdateState 落一次任务状态。
func (s *Server) setNodeUpdateState(state string) {
	st := &s.nodeUpdate
	st.mu.Lock()
	st.state = state
	st.mu.Unlock()
}

// addNodeUpdateEvent 追加一条事件。消息在这里就截断,不让一个超长的 ssh 报错
// 把整个进度响应撑爆。
func (s *Server) addNodeUpdateEvent(row model.UpdateNodeCore, status, from, to, msg string) {
	st := &s.nodeUpdate
	st.mu.Lock()
	defer st.mu.Unlock()
	st.seq++
	st.events = append(st.events, model.UpdateNodeEvent{
		Seq:    st.seq,
		Node:   row.Name,
		Core:   row.Core,
		From:   from,
		To:     to,
		Status: status,
		Msg:    truncateBytes(msg, nodeUpdateMsgLimit),
	})
}

// nodeUpdateProgress 组装一次进度响应。
//
// Seq 是任务当前的事件总数(不是本次返回的最后一条):客户端据此判断"还有没有
// 没拉完的"。Events 只带 since 之后最早的一批,最多 20 条,并按整段响应的大小
// 再收一次口 —— 每条约 200 字节的 20 条会超过 4KB,所以两个上限都要有。
func (s *Server) nodeUpdateProgress(since int64) model.UpdateNodesProgress {
	st := &s.nodeUpdate
	st.mu.Lock()
	defer st.mu.Unlock()

	out := model.UpdateNodesProgress{
		Job:    st.job,
		State:  st.state,
		Seq:    st.seq,
		Events: []model.UpdateNodeEvent{},
	}
	if out.State == "" {
		out.State = model.UpdateNodesIdle
	}
	size := 0
	for _, ev := range st.events {
		if ev.Seq <= since {
			continue
		}
		if len(out.Events) >= nodeUpdateMaxEvents {
			break
		}
		b, err := json.Marshal(ev)
		if err == nil && len(out.Events) > 0 && size+len(b) > nodeUpdateBodyBudget {
			break
		}
		out.Events = append(out.Events, ev)
		if err == nil {
			size += len(b)
		}
	}
	return out
}

// runNodeUpdate 是任务主体:按节点 id 升序逐台、逐核心串行升级。
//
// 任何一台失败都立刻收工并把整轮标成 failed,后面的节点一个都不动 —— 一台节点
// 升级失败往往说明网络、架构或权限有共性问题,继续推只会把更多机器推下水。
func (s *Server) runNodeUpdate() {
	rows, err := s.updateNodes()
	if err != nil {
		s.logger.Error("读取节点核心版本失败", "error", err)
		s.setNodeUpdateState(model.UpdateNodesFailed)
		return
	}
	// updateNodes 已按 ListNodes 的 id 序出,这里再稳排一次:同台节点的两个核心
	// 保持 xray 在前、sing-box 在后的相对次序。
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].NodeID < rows[j].NodeID })

	ctx := context.Background()
	for _, row := range rows {
		n, err := s.store.GetNode(row.NodeID)
		if err != nil {
			s.addNodeUpdateEvent(row, "failed", row.Current, row.Target, "读节点失败: "+err.Error())
			s.setNodeUpdateState(model.UpdateNodesFailed)
			return
		}
		if err := s.upgradeNodeCore(ctx, *n, row); err != nil {
			s.setNodeUpdateState(model.UpdateNodesFailed)
			return
		}
	}
	s.setNodeUpdateState(model.UpdateNodesDone)
}

// upgradeNodeCore 按核心类型分派。核心认不出来按失败处理,不假装跳过。
func (s *Server) upgradeNodeCore(ctx context.Context, n model.Node, row model.UpdateNodeCore) error {
	switch row.Core {
	case model.CoreXray:
		return s.upgradeNodeXray(ctx, n, row)
	case model.CoreSingBox:
		return s.upgradeNodeSingBox(ctx, n, row)
	default:
		err := fmt.Errorf("不认识的核心 %q", row.Core)
		s.addNodeUpdateEvent(row, "failed", row.Current, row.Target, err.Error())
		return err
	}
}

// upgradeNodeXray 走"节点自己从 GitHub 拉"的路。
//
// 控制面只负责问清节点架构、拼出资产地址、取官方 sha256,下载与替换都在节点上
// 发生(见 node.UpgradeXray)。已经是推荐版本就记一条 skip,连摘要都不去取。
func (s *Server) upgradeNodeXray(ctx context.Context, n model.Node, row model.UpdateNodeCore) error {
	target := xrayrelease.RecommendedVersion
	from := s.liveCoreVersion(ctx, n, row.Current, xrayVersion)
	if sameCoreVersion(from, target) {
		s.addNodeUpdateEvent(row, "skip", from, target, "已是目标版本")
		return nil
	}
	s.addNodeUpdateEvent(row, "start", from, target, "")

	assetURL, sha256, err := s.resolveXray(ctx, n, target)
	if err != nil {
		s.addNodeUpdateEvent(row, "failed", from, target, err.Error())
		return err
	}
	res, err := s.upgradeXray(ctx, n, target, assetURL, sha256)
	if res.From != "" {
		from = res.From
	}
	if err != nil {
		status := "failed"
		if res.RolledBack {
			status = "rolled_back"
		}
		s.addNodeUpdateEvent(row, status, from, target, err.Error())
		return err
	}
	s.addNodeUpdateEvent(row, "ok", from, target, "")
	s.refreshNodeAfterUpdate(ctx, n)
	return nil
}

// upgradeNodeSingBox 走控制面推送安装的路。
//
// sing-box 是从源码编出来的,节点上没法自己拉,只能用现成的 InstallNodeCore。
// 已经是钉死版本就 skip;先把这句话写回来,省掉一次编译与推送。
func (s *Server) upgradeNodeSingBox(ctx context.Context, n model.Node, row model.UpdateNodeCore) error {
	target := singboxbuild.DefaultVersion
	from := s.liveCoreVersion(ctx, n, row.Current, singBoxVersion)
	if sameCoreVersion(from, target) {
		s.addNodeUpdateEvent(row, "skip", from, target, "已是目标版本")
		return nil
	}
	s.addNodeUpdateEvent(row, "start", from, target, "")

	installer, ok := s.nodeOps.(CoreInstaller)
	if !ok {
		err := errors.New("控制面没有接上 sing-box 安装通道")
		s.addNodeUpdateEvent(row, "failed", from, target, err.Error())
		return err
	}
	if _, err := installer.InstallNodeCore(ctx, n, model.CoreSingBox, target); err != nil {
		s.addNodeUpdateEvent(row, "failed", from, target, err.Error())
		return err
	}
	s.addNodeUpdateEvent(row, "ok", from, target, "")
	s.refreshNodeAfterUpdate(ctx, n)
	return nil
}

// refreshNodeAfterUpdate 升级成功后顺手巡检一次这台节点,把观察到的版本落库。
//
// 失败只记日志:核心已经换上去了,一次巡检没成不改变升级的结果,也不该把整轮
// 标成失败。控制面没接节点通道时自然也没有可查的,直接跳过。
func (s *Server) refreshNodeAfterUpdate(ctx context.Context, n model.Node) {
	if s.nodeOps == nil {
		return
	}
	if _, err := s.freshNodeStatus(ctx, n, time.Now()); err != nil {
		s.logger.Warn("节点核心升级后巡检失败", "node", n.Name, "error", err)
	}
}

// defaultResolveXray 是 resolveXray 的默认实现:交给节点操作通道的 CoreUpgrader。
func (s *Server) defaultResolveXray(ctx context.Context, n model.Node, version string) (string, string, error) {
	up, ok := s.nodeOps.(CoreUpgrader)
	if !ok {
		return "", "", errNoNodeUpgrader
	}
	return up.ResolveXray(ctx, n, version)
}

// defaultUpgradeXray 是 upgradeXray 的默认实现:交给 CoreUpgrader。
func (s *Server) defaultUpgradeXray(ctx context.Context, n model.Node, version, assetURL, sha256 string) (node.UpgradeResult, error) {
	up, ok := s.nodeOps.(CoreUpgrader)
	if !ok {
		return node.UpgradeResult{}, errNoNodeUpgrader
	}
	return up.UpgradeXray(ctx, n, version, assetURL, sha256)
}

// liveCoreVersion 在升级前现场问一次节点,取这个核心此刻报的版本;问不到就退回
// 缓存里的 cached。
//
// 缓存是上一轮巡检的结论:节点在两次巡检之间被手工换过核心时,缓存会说"已是目标
// 版本",照着 skip 就把一台旧核心的节点永远留在旧版。所以跳不跳过要看现场。
// 查失败、或现场没报版本时仍用缓存 —— 不能因为一次 SSH 不通就把整轮判死,也不
// 能把"不知道"当成"已是目标版本"(空版本在 sameCoreVersion 里本来就不算相同)。
func (s *Server) liveCoreVersion(ctx context.Context, n model.Node, cached string, pick func(model.NodeStatus) string) string {
	if s.nodeOps == nil {
		return cached
	}
	st, err := s.freshNodeStatus(ctx, n, time.Now())
	if err != nil {
		return cached
	}
	if live := strings.TrimSpace(pick(st)); live != "" {
		return live
	}
	return cached
}

// sameCoreVersion 比较两个版本号是否相同,忽略打头的 v 与空白。
//
// 节点上报的是核心自己报的版本(26.3.27),目标常量带着 v(v26.3.27),直接比会
// 把同一版判成"要升级"。空版本一律不算相同:不知道现在是什么,就不能说不用动。
func sameCoreVersion(a, b string) bool {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	return a != "" && a == b
}

// truncateBytes 把字符串截到最多 limit 字节,且不切碎一个 UTF-8 字符。
func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
