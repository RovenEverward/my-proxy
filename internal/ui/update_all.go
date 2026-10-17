package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/updatecheck"
)

// 一键更新:控制面 → 节点核心 → 本机 CLI → 重启 mpx ui,一次按钮把三层都升到
// 最新。它不长出新能力 —— 每一步都是已有命令(POST /api/update/control、
// POST /api/update/nodes、本机 updateCLI)的编排,任何一步失败就停下,后面的不做。

// 任务的几个状态与步骤名。state 还有一个 restarting:本机二进制换过了,进程即将
// 被新版本替换,进度接口在那之前仍然可问。
const (
	updateAllIdle       = "idle"
	updateAllRunning    = "running"
	updateAllDone       = "done"
	updateAllFailed     = "failed"
	updateAllRestarting = "restarting"

	updateStepControl = "control"
	updateStepNodes   = "nodes"
	updateStepCLI     = "cli"
	updateStepRestart = "restart"
)

// 进度响应的上限,与控制面 /api/update/nodes 同一套口径:每条消息截到 200 字节、
// 一次最多 20 条、整段响应按 4KB 的余量收口,客户端拿 Seq 当下一次 since 增量拉。
const (
	updateAllMaxEvents = 20
	updateAllMsgLimit  = 200
	updateAllBudget    = 3500
)

// 时间参数做成变量,测试里把它们调快,不用真等 2 秒一拍。
var (
	updatePollInterval = 2 * time.Second
	updateControlWait  = 180 * time.Second
	updateNodesWait    = 15 * time.Minute
	// updateRestartAck 是重启前等页面把 restarting 状态拉走的上限:没人轮询时也不
	// 能一直停在那,升级已经发生,重启要照常做。
	updateRestartAck   = 30 * time.Second
	updateRestartDelay = 1 * time.Second
)

// updateAllEvent 是一键更新任务里的一条事件。
type updateAllEvent struct {
	Seq    int64  `json:"seq"`
	Step   string `json:"step"`
	Status string `json:"status"`
	Msg    string `json:"msg"`
}

// updateAllSnapshot 是 GET /local/update/all 的形状。
type updateAllSnapshot struct {
	State  string           `json:"state"`
	Step   string           `json:"step"`
	Seq    int64            `json:"seq"`
	Events []updateAllEvent `json:"events"`
}

// updateAllState 是一次一键更新任务的内存状态,全部字段由 mu 保护 —— 任务
// goroutine 在写,进度接口在读。
type updateAllState struct {
	mu     sync.Mutex
	state  string
	step   string
	seq    int64
	events []updateAllEvent
	// 进入 restarting 时建好;进度接口把 restarting 状态送出去之后关掉(restartAck
	// 防重复关)。编排侧等这个信号,确认页面看见了再换进程。
	restartSent chan struct{}
	restartAck  bool
}

// updateAllStart:POST /local/update/all,启动一轮编排,任务在后台跑。
//
// 同一时刻只允许一轮:正在跑或正在重启时再点返回 409,而不是排队或合并。
func (s *Server) updateAllStart(w http.ResponseWriter, r *http.Request) {
	if !s.beginUpdateAll() {
		writeError(w, http.StatusConflict, "已有一键更新在跑")
		return
	}
	go s.runUpdateAll()
	writeJSONCode(w, http.StatusAccepted, map[string]any{"ok": true})
}

// updateAllProgress:GET /local/update/all?since=<seq>,拉任务进度。
//
// 只回 seq 比 since 新的事件;Seq 是事件总数,客户端拿它当下一轮的 since。游标
// 非法就 400,不默默当成 0 —— 那会让客户端误以为拿到了全量。
func (s *Server) updateAllProgress(w http.ResponseWriter, r *http.Request) {
	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "since 必须是非负整数")
			return
		}
		since = n
	}
	snap := s.allSnapshot(since)
	writeJSON(w, snap)
	if snap.State == updateAllRestarting {
		// 响应已经送出去了,告诉编排侧可以动手换进程。
		s.all.ackRestartSent()
	}
}

// beginUpdateAll 尝试开始一轮新任务。已有一轮在跑或正在重启时返回 false。
func (s *Server) beginUpdateAll() bool {
	st := &s.all
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.state == updateAllRunning || st.state == updateAllRestarting {
		return false
	}
	st.state = updateAllRunning
	st.step = ""
	st.seq = 0
	st.events = nil
	st.restartSent = nil
	st.restartAck = false
	return true
}

func (s *Server) setAllStep(step string) {
	st := &s.all
	st.mu.Lock()
	st.step = step
	st.mu.Unlock()
}

func (s *Server) setAllState(state string) {
	st := &s.all
	st.mu.Lock()
	st.state = state
	st.mu.Unlock()
}

// addAllEvent 追加一条事件。消息在这里就截断,不让一条超长的报错把进度响应撑爆。
func (s *Server) addAllEvent(step, status, msg string) {
	st := &s.all
	st.mu.Lock()
	defer st.mu.Unlock()
	st.seq++
	st.events = append(st.events, updateAllEvent{
		Seq:    st.seq,
		Step:   step,
		Status: status,
		Msg:    truncateUTF8(msg, updateAllMsgLimit),
	})
}

// ackRestartSent 在把 restarting 状态交给页面之后调用一次,之后的调用是空操作。
func (st *updateAllState) ackRestartSent() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.restartAck || st.restartSent == nil {
		return
	}
	st.restartAck = true
	close(st.restartSent)
}

// allSnapshot 组装一次进度响应。事件只带 since 之后最早的一批,条数与整段大小
// 两个上限都要有:每条约 200 字节的 20 条会超过 4KB。
func (s *Server) allSnapshot(since int64) updateAllSnapshot {
	st := &s.all
	st.mu.Lock()
	defer st.mu.Unlock()

	out := updateAllSnapshot{State: st.state, Step: st.step, Seq: st.seq, Events: []updateAllEvent{}}
	if out.State == "" {
		out.State = updateAllIdle
	}
	size := 0
	for _, ev := range st.events {
		if ev.Seq <= since {
			continue
		}
		if len(out.Events) >= updateAllMaxEvents {
			break
		}
		b, err := json.Marshal(ev)
		if err == nil && len(out.Events) > 0 && size+len(b) > updateAllBudget {
			break
		}
		out.Events = append(out.Events, ev)
		if err == nil {
			size += len(b)
		}
	}
	return out
}

// runUpdateAll 是任务主体:控制面 → 节点 → 本机 → (二进制换了才)重启。
// 任何一步失败记一条 failed 事件并停下,后面的不做。
func (s *Server) runUpdateAll() {
	ctx := context.Background()

	s.setAllStep(updateStepControl)
	if err := s.updateAllControl(ctx); err != nil {
		s.failUpdateAll(err)
		return
	}

	s.setAllStep(updateStepNodes)
	if err := s.updateAllNodes(ctx); err != nil {
		s.failUpdateAll(err)
		return
	}

	s.setAllStep(updateStepCLI)
	cliCtx, cancel := context.WithTimeout(ctx, updateInstallTimeout)
	changed, err := s.updateAllCLI(cliCtx)
	cancel()
	if err != nil {
		s.failUpdateAll(err)
		return
	}
	if !changed {
		s.setAllState(updateAllDone)
		return
	}

	s.setAllStep(updateStepRestart)
	if err := s.updateAllRestart(); err != nil {
		s.failUpdateAll(err)
	}
	// 重启成功不会返回:进程已被新二进制替换,状态停在 restarting。
}

// failUpdateAll 记下失败的那一步并把任务标成 failed。
func (s *Server) failUpdateAll(err error) {
	st := &s.all
	st.mu.Lock()
	step := st.step
	st.mu.Unlock()
	s.addAllEvent(step, "failed", err.Error())
	s.setAllState(updateAllFailed)
}

// updateAllControl 升级控制面:已是最新记 skip;否则把升级请求转给控制面
// (等价 mpx update apply control),然后等它换完二进制、API 重新可用。
func (s *Server) updateAllControl(ctx context.Context) error {
	snap, err := s.fetchControlSnapshot(ctx)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(snap.Control.Latest)
	if target == "" {
		if msg := strings.TrimSpace(snap.Control.Error); msg != "" {
			return fmt.Errorf("控制面没查到最新版本: %s", msg)
		}
		return errors.New("控制面没查到最新版本")
	}
	switch {
	case sameVersion(snap.Control.Current, target):
		s.addAllEvent(updateStepControl, "skip", "控制面已是 "+snap.Control.Current)
		return nil
	case updatecheck.Ahead(target, snap.Control.Current):
		// 比最新发布还新(源码直装的开发版):没得升,也别去动它,否则会在
		// "等到 current 等于 target"上白等满超时。
		s.addAllEvent(updateStepControl, "skip", "控制面 "+snap.Control.Current+" 不低于最新发布 "+target)
		return nil
	}
	from := snap.Control.Current

	raw, code, err := s.controlCall(ctx, http.MethodPost, "/api/update/control", []byte("{}"))
	if err != nil {
		return fmt.Errorf("发出控制面升级请求失败: %w", err)
	}
	// 202:已接单;409:上一轮还在收尾,同样等它到位;其余都算被拒。
	if !(code >= 200 && code < 300) && code != http.StatusConflict {
		return fmt.Errorf("控制面拒绝升级: HTTP %d: %s", code, briefBody(raw))
	}

	deadline := time.Now().Add(updateControlWait)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(updatePollInterval):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等控制面升到 %s 超时(%s)", target, updateControlWait)
		}
		snap, err := s.fetchControlSnapshot(ctx)
		if err != nil {
			continue // 控制面正在重启,请求失败是预期内的,忍到截止时间
		}
		if !sameVersion(snap.Control.Current, target) {
			continue
		}
		if _, scode, serr := s.controlCall(ctx, http.MethodGet, "/api/status", nil); serr != nil || scode != http.StatusOK {
			continue
		}
		s.addAllEvent(updateStepControl, "ok", "控制面 "+versionOrUnknown(from)+" → "+target)
		return nil
	}
}

// fetchControlSnapshot 问一次控制面的版本结论(/api/update?refresh=1)。
func (s *Server) fetchControlSnapshot(ctx context.Context) (model.UpdateSnapshot, error) {
	raw, code, err := s.controlCall(ctx, http.MethodGet, "/api/update?refresh=1", nil)
	if err != nil {
		return model.UpdateSnapshot{}, fmt.Errorf("问控制面版本失败: %w", err)
	}
	if code != http.StatusOK {
		return model.UpdateSnapshot{}, fmt.Errorf("控制面 /api/update 返回 HTTP %d: %s", code, briefBody(raw))
	}
	var snap model.UpdateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return model.UpdateSnapshot{}, fmt.Errorf("解析控制面 /api/update 响应失败: %w", err)
	}
	return snap, nil
}

// updateAllNodes 升级各节点的核心:让控制面启动任务(等价 mpx update apply
// nodes),把它的每条节点事件转成本地事件,直到任务结束。
func (s *Server) updateAllNodes(ctx context.Context) error {
	raw, code, err := s.controlCall(ctx, http.MethodPost, "/api/update/nodes", []byte("{}"))
	if err != nil {
		return fmt.Errorf("启动节点核心升级失败: %w", err)
	}
	switch code {
	case http.StatusNotFound:
		return errors.New("控制面不支持节点升级")
	case http.StatusAccepted, http.StatusConflict:
		// 202:已启动;409:已有任务在跑 —— 两种都接着轮询进度。
	default:
		return fmt.Errorf("控制面拒绝节点核心升级: HTTP %d: %s", code, briefBody(raw))
	}

	var since int64
	misses := 0
	deadline := time.Now().Add(updateNodesWait)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(updatePollInterval):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("节点核心升级超时(%s 没有结果)", updateNodesWait)
		}
		raw, code, err := s.controlCall(ctx, http.MethodGet,
			fmt.Sprintf("/api/update/nodes?since=%d", since), nil)
		if err != nil || code != http.StatusOK {
			// 控制面这会儿不该重启,偶发失败容忍几拍,连着失败才报。
			misses++
			if misses >= 3 {
				if err != nil {
					return fmt.Errorf("读取节点升级进度失败: %w", err)
				}
				return fmt.Errorf("读取节点升级进度: HTTP %d: %s", code, briefBody(raw))
			}
			continue
		}
		misses = 0
		var p model.UpdateNodesProgress
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("解析节点升级进度失败: %w", err)
		}
		for _, ev := range p.Events {
			s.addAllEvent(updateStepNodes, string(ev.Status), nodeEventMsg(ev))
		}
		since = p.Seq
		switch p.State {
		case model.UpdateNodesDone:
			return nil
		case model.UpdateNodesFailed:
			return errors.New("节点核心升级失败")
		}
	}
}

// nodeEventMsg 把一条节点事件压成一句话,页面按时间顺序列出来。
func nodeEventMsg(ev model.UpdateNodeEvent) string {
	return fmt.Sprintf("%s %s %s → %s %s", ev.Node, ev.Core, ev.From, ev.To, ev.Status)
}

// updateAllCLI 升级本机 CLI(复用 updateCLI 的实现),返回二进制是否真的被换掉。
func (s *Server) updateAllCLI(ctx context.Context) (bool, error) {
	from, to, changed, _, err := applyCLIUpdate(ctx, "")
	if err != nil {
		return false, err
	}
	if !changed {
		s.addAllEvent(updateStepCLI, "skip", "本机 CLI 已是最新")
		return false, nil
	}
	s.addAllEvent(updateStepCLI, "ok", "本机 CLI "+from+" → "+to)
	return true, nil
}

// updateAllRestart 用新二进制原地重启 mpx ui。交接文件带上监听地址与会话,新进程
// 读完后监听同一个地址、认同一个会话,浏览器的 cookie 继续有效。
func (s *Server) updateAllRestart() error {
	path, err := writeRestartState(s.listenAddr(), s.sessionValue())
	if err != nil {
		return err
	}
	s.addAllEvent(updateStepRestart, "start", "重启 mpx ui 启用新版本")

	st := &s.all
	st.mu.Lock()
	st.restartSent = make(chan struct{})
	st.state = updateAllRestarting
	sent := st.restartSent
	st.mu.Unlock()

	// 先让页面看到 restarting(没人轮询时按上限兜底),把响应送出去之后再稳一拍,
	// 才动手换进程 —— 立刻 exec 会连"正在重启"这一次响应都送不出去。
	select {
	case <-sent:
	case <-time.After(updateRestartAck):
	}
	time.Sleep(updateRestartDelay)
	if err := restartSelf(path); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("重启 mpx ui 失败: %w", err)
	}
	return nil
}

// listenAddr 返回本服务绑定的地址,重启交接用。
func (s *Server) listenAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// sessionValue 返回当前会话值,重启交接用。
func (s *Server) sessionValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

// sameVersion 比较两个版本号是不是同一个,忽略打头的 v 与空白 —— 目标常带 v,
// 控制面报的 current 不一定带,直接字符串比会把同一版判成两版。
func sameVersion(a, b string) bool {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	return a != "" && a == b
}

// versionOrUnknown 把空版本号换成"未知",免得事件消息里出现空荡荡的箭头。
func versionOrUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "未知"
	}
	return v
}

// truncateUTF8 把字符串截到最多 limit 字节,且不切碎一个 UTF-8 字符。
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
