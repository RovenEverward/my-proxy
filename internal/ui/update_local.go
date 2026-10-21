package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/selfupdate"
	"github.com/RovenEverward/my-proxy/internal/updatecheck"
)

const (
	// updateBodyMax 是升级相关请求体的字节上限。
	updateBodyMax = 16 << 10
	// updateViewTimeout 是问一次控制面版本的整轮上限。控制面带 refresh=1 时可能
	// 会话上游,给宽一点,但也不能让页面一直挂着。
	updateViewTimeout = 30 * time.Second
	// updateInstallTimeout 是下载并替换本机二进制那一轮的上限。
	updateInstallTimeout = 5 * time.Minute
)

// updateLocalVersion 是本机 CLI 的版本,做成变量便于测试注入一个已知值。
var updateLocalVersion = buildinfo.Version

// updateDesktop 报告本进程是不是桌面构建:桌面构建的自更新取桌面安装包、页面
// 文案也叫「桌面程序」。做成变量便于测试注入。
var updateDesktop = buildinfo.Desktop

// updateSelfName 是页面上本机那一层的名字:桌面构建叫「桌面程序」,普通构建叫
// 「本机 CLI」。版本行、事件与提示都跟着它叫,页面再据 desktop 字段切换显示。
func updateSelfName() string {
	if updateDesktop() {
		return "桌面程序"
	}
	return "本机 CLI"
}

// updateRunningName 是「正在运行、升级完要重启的那个」:浏览器模式是 mpx ui,
// 桌面构建的窗口就是桌面程序。
func updateRunningName() string {
	if updateDesktop() {
		return "桌面程序"
	}
	return "mpx ui"
}

// updateSource 是取发布件的来源,默认是本项目在 GitHub 上的仓库。桌面构建取的是
// 桌面安装档(Source.Desktop),清单缺失或对不上都会被拒。
// 做成变量是为了让测试把地址指到假控制面,不真去连 GitHub。
var updateSource = func() selfupdate.Source {
	return selfupdate.Source{Repo: buildinfo.Repo(), Desktop: updateDesktop()}
}

// selfExecutable 返回本机 mpx 的绝对路径并解开符号链接 —— 与 cli 的
// selfExecutable 同语义;不导出 cli 的私有变量,免得引入环依赖(cli 已依赖 ui)。
//
// 注意:它只在升级开始时问一次,得到的是新二进制的目标路径;升级把旧二进制改名成
// <exe>.prev 之后,Linux 的 /proc/self/exe 会指向 .prev,重启时不能再问它。
var selfExecutable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("找不到本机可执行文件: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("解析本机可执行文件路径: %w", err)
	}
	return resolved, nil
}

// stripPrevSuffix 兜底修正升级路径:若拿到的已是改名后的 <exe>.prev(升级后再问
// selfExecutable 就会这样),去掉 .prev 后缀指回新二进制,别把新版本写进备份名,
// 也别在重启时又起旧版。
func stripPrevSuffix(exe string) string {
	return strings.TrimSuffix(exe, ".prev")
}

// updateViewBody 是 GET /local/update 的形状。
type updateViewBody struct {
	CLI     model.UpdatePart       `json:"cli"`
	Control model.UpdatePart       `json:"control"`
	Nodes   []model.UpdateNodeCore `json:"nodes"`
	Notes   string                 `json:"notes"`
	// Desktop 报告本进程是不是桌面构建:页面据此把本机那一层叫「桌面程序」还是
	// 「本机 CLI」。只加不改,旧页面忽略它照常工作。
	Desktop bool `json:"desktop"`
	// Prefetch 是桌面端后台预下载的状态;普通构建或不适用时为 nil(JSON 里不出现),
	// 旧页面忽略。
	Prefetch *prefetchStatus `json:"prefetch,omitempty"`
}

// serveUpdate 处理 /local/update*。调用前会话与来源已经校验过(与 setup 相同)。
//
// 这里只做三件事:报告版本、升级本机 CLI、把控制面升级请求转发出去;没有交互
// 确认,也没有后台任务。
func (s *Server) serveUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/local/update":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 GET")
			return
		}
		s.updateView(w, r)
	case "/local/update/cli":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 POST")
			return
		}
		s.updateCLI(w, r)
	case "/local/update/control":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 POST")
			return
		}
		s.updateControl(w, r)
	case "/local/update/prefetch":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 POST")
			return
		}
		s.updatePrefetch(w, r)
	case "/local/update/all":
		// POST 启动一键更新,GET 拉进度;同一个路径按方法分(见 update_all.go)。
		switch r.Method {
		case http.MethodPost:
			s.updateAllStart(w, r)
		case http.MethodGet:
			s.updateAllProgress(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 GET 或 POST")
		}
	default:
		writeError(w, http.StatusNotFound, "没有这个升级接口")
	}
}

// updateView:GET /local/update,把本机 CLI 与控制面的版本拼成顶部 pill 要的形状。
//
// CLI 不自己连 GitHub:最新版本借控制面 /api/update?refresh=1 的结论,与
// mpx update 的口径一致。控制面问不到时,cli.latest 留空、available 为 false,
// 并把原因写进 cli.error。
func (s *Server) updateView(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), updateViewTimeout)
	defer cancel()

	view := updateViewBody{
		CLI:     model.UpdatePart{Current: updateLocalVersion()},
		Nodes:   []model.UpdateNodeCore{},
		Desktop: updateDesktop(),
	}

	raw, code, err := s.controlCall(ctx, http.MethodGet, "/api/update?refresh=1", nil)
	if err != nil {
		view.CLI.Error = "连不上控制面: " + err.Error()
		writeJSON(w, view)
		return
	}
	if code != http.StatusOK {
		view.CLI.Error = fmt.Sprintf("控制面 /api/update 返回 HTTP %d: %s", code, briefBody(raw))
		writeJSON(w, view)
		return
	}
	var snap model.UpdateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		view.CLI.Error = "解析控制面 /api/update 响应失败: " + err.Error()
		writeJSON(w, view)
		return
	}

	view.Control = snap.Control
	// 节点核心的版本行原样带给页面:一键更新的弹窗要列 xray / sing-box 从哪版到哪版。
	view.Nodes = snap.Nodes
	if view.Nodes == nil {
		view.Nodes = []model.UpdateNodeCore{}
	}
	view.CLI.Latest = snap.Control.Latest
	view.CLI.ReleaseURL = snap.Control.ReleaseURL
	// 落后几次发布对 CLI 与控制面是同一个数:用的是同一份最新版本。
	view.CLI.Behind = snap.Control.Behind
	view.CLI.Available = view.CLI.Current != "" && view.CLI.Latest != "" &&
		updatecheck.Ahead(view.CLI.Current, view.CLI.Latest)
	// 发现 CLI 有新版本就在后台预下载;弹窗据 prefetch 字段显示进度。只影响
	// 桌面构建,别的构建下 prefetchView 返回 nil。
	if view.CLI.Available {
		s.startPrefetch(view.CLI.Latest)
	}
	view.Prefetch = s.prefetchView()
	// 上游没问成时,控制面把原因放在 control.error;这也是 CLI 算不出最新的原因。
	if view.CLI.Latest == "" && snap.Control.Error != "" {
		view.CLI.Error = snap.Control.Error
	}

	// 发布正文只在 /api/status 的 update.notes 里,/api/update 不带。尽力而为:
	// 拿不到就空着,不让它影响已经拼好的版本结论。
	if nraw, ncode, nerr := s.controlCall(ctx, http.MethodGet, "/api/status", nil); nerr == nil && ncode == http.StatusOK {
		var st model.ControlStatus
		if json.Unmarshal(nraw, &st) == nil {
			view.Notes = st.Update.Notes
		}
	}

	writeJSON(w, view)
}

// updateCLI:POST /local/update/cli,等价于 mpx update apply cli -y。
//
// Body 可选 {"version":"vX.Y.Z"};留空表示最新。这里不做交互确认,但因为按钮
// 背后就是一次显式的点击。
func (s *Server) updateCLI(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, updateBodyMax)
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), updateInstallTimeout)
	defer cancel()

	from, to, changed, _, code, err := s.applyCLIUpdate(ctx, body.Version)
	if err != nil {
		writeError(w, code, err.Error())
		return
	}
	if !changed {
		writeJSON(w, map[string]any{"ok": true, "from": from, "to": to, "message": updateSelfName() + " 已是最新"})
		return
	}
	// 不 os.Exit:页面自己会提示重启(桌面程序或 mpx ui)后生效。
	writeJSON(w, map[string]any{"ok": true, "from": from, "to": to,
		"message": fmt.Sprintf("已升级到 %s；正在运行的 %s 需重启后生效", to, updateRunningName())})
}

// applyCLIUpdate 是 updateCLI 与一键更新(update_all.go)共用的本机升级实现:把
// 本机二进制升到 version(空表示最新)。
//
// changed 报告二进制是否真的被换掉 —— 一键更新只在这个值为真时重启 mpx ui。exe 是
// 这次实际写入的新二进制路径(升级前问到的目标路径,已去掉可能的 .prev 后缀),一键
// 更新拿它去重启,免得重启时 os.Executable() 指到刚改名的 <exe>.prev。出错时 code
// 是该回给页面的 HTTP 状态码,与 updateCLI 原来的口径一一对应。
//
// 真正动手(开始下载)之后的成功与失败都会通过 Options.CLIUpdated 记一笔,让
// mpx update show 能看到页面发起的这次升级;还没开始下载就失败的(版本号不合法、
// 问不到最新版)不算一次升级尝试,不记。
func (s *Server) applyCLIUpdate(ctx context.Context, version string) (from, to string, changed bool, exe string, code int, err error) {
	current := updateLocalVersion()
	if err := selfupdate.CheckLocalUpdate(current); err != nil {
		return current, "", false, "", http.StatusConflict, err
	}
	target := strings.TrimSpace(version)
	explicit := target != ""
	src := updateSource()

	if target == "" {
		latest, lerr := src.Latest(ctx)
		if lerr != nil {
			return current, "", false, "", http.StatusBadGateway, lerr
		}
		target = latest
	}

	if derr := selfupdate.Decide(current, target, explicit, false); derr != nil {
		if errors.Is(derr, selfupdate.ErrUpToDate) {
			return current, current, false, "", 0, nil
		}
		return current, target, false, "", http.StatusBadRequest, derr
	}

	path, eerr := selfExecutable()
	if eerr != nil {
		return current, target, false, "", http.StatusInternalServerError, eerr
	}
	// 升级前问到的就是新二进制的目标路径;万一已经是改名后的 .prev(重复升级或
	// 别的调用时机),兜底去掉后缀,保证写入与重启都落在真正的新二进制上。
	path = stripPrevSuffix(path)
	started := time.Now()
	bin, ok := s.stagedFor(target)
	if !ok {
		// 没有合格暂存:保持原来的现下现装。
		var ferr error
		bin, _, ferr = src.Fetch(ctx, target, runtime.GOOS, runtime.GOARCH)
		if ferr != nil {
			s.noteCLIUpdate(current, target, started, ferr)
			return current, target, false, path, http.StatusBadGateway, ferr
		}
	}
	if ierr := selfupdate.Install(path, bin, target); ierr != nil {
		s.noteCLIUpdate(current, target, started, ierr)
		ic := http.StatusInternalServerError
		if errors.Is(ierr, selfupdate.ErrNeedSudo) || errors.Is(ierr, fs.ErrPermission) {
			ic = http.StatusForbidden
			if updateDesktop() {
				// 桌面程序没有「sudo 重跑 mpx」这条路:安装目录动不了就指引用
				// 安装包,别把 CLI 的 sudo 提示原样端给窗口里的用户。
				ierr = fmt.Errorf("桌面程序装在 %s,当前用户没有写权限;请下载新版安装包安装", filepath.Dir(path))
			}
		}
		return current, target, false, path, ic, ierr
	}
	s.noteCLIUpdate(current, target, started, nil)
	return current, target, true, path, 0, nil
}

// noteCLIUpdate 把一次本机 CLI 升级的结果交给 cli 侧的回调去落 update.json。
// 回调为空时什么也不做 —— 这是 ui 包不认识 cli 包的地方。
func (s *Server) noteCLIUpdate(from, to string, started time.Time, err error) {
	if s.opts.CLIUpdated == nil {
		return
	}
	s.opts.CLIUpdated(from, to, started, err)
}

// updatePrefetch:POST /local/update/prefetch,让后台立刻重试当前目标的下载,
// 不等退避。没有可重试的目标时是 200 的空操作,不制造假错误。
func (s *Server) updatePrefetch(w http.ResponseWriter, r *http.Request) {
	pf := s.prefetchView()
	if pf == nil || pf.State == prefetchDisabled {
		writeError(w, http.StatusConflict, "这台机器没有启用后台预下载")
		return
	}
	p := s.getPrefetcher()
	p.retry()
	writeJSON(w, map[string]any{"ok": true, "prefetch": p.snapshot()})
}

// updateControl:POST /local/update/control,把请求原样转给控制面。
//
// 页面能做的事以 uiAllow 为界;这条对应 mpx update apply control。转发层不解析
// 正文,状态码与 JSON 原样透传,错误由控制面自己说清楚。
func (s *Server) updateControl(w http.ResponseWriter, r *http.Request) {
	if up, _, _ := s.controlTarget(); up == nil {
		writeError(w, http.StatusServiceUnavailable, "还没有控制面:先完成首次安装")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, updateBodyMax)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
		return
	}

	raw, code, err := s.controlCall(r.Context(), http.MethodPost, "/api/update/control", body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(raw)
}

// controlTarget 取当前控制面地址、token 与传输。
func (s *Server) controlTarget() (*url.URL, string, http.RoundTripper) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts.Upstream, s.opts.Token, s.opts.Transport
}

// controlCall 以本机 UI 的身份问一次控制面,带上管理 token;没有控制面时报错。
//
// 不复用转发代理:那些请求要过 uiAllow,而本文件里的路径是 UI 自己发起的、
// 有明确目的地的内部调用。body 为 nil 表示 GET,无正文。
func (s *Server) controlCall(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	up, token, tr := s.controlTarget()
	if up == nil {
		return nil, 0, errors.New("没有控制面地址")
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(up.String(), "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Transport: tr, Timeout: updateViewTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("请求控制面 %s 失败: %w", up.Redacted(), err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取控制面响应: %w", err)
	}
	return data, resp.StatusCode, nil
}

// briefBody 把一段响应正文压成一句话,给错误信息用。
func briefBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "空响应"
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
