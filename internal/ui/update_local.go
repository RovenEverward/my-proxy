package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// updateSource 是取发布件的来源,默认是本项目在 GitHub 上的仓库。
// 做成变量是为了让测试把地址指到假控制面,不真去连 GitHub。
var updateSource = func() selfupdate.Source {
	return selfupdate.Source{Repo: buildinfo.Repo()}
}

// selfExecutable 返回本机 mpx 的绝对路径并解开符号链接 —— 与 cli 的
// selfExecutable 同语义;不导出 cli 的私有变量,免得引入环依赖(cli 已依赖 ui)。
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

// updateViewBody 是 GET /local/update 的形状。
type updateViewBody struct {
	CLI     model.UpdatePart `json:"cli"`
	Control model.UpdatePart `json:"control"`
	Notes   string           `json:"notes"`
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

	view := updateViewBody{CLI: model.UpdatePart{Current: updateLocalVersion()}}

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
	view.CLI.Latest = snap.Control.Latest
	view.CLI.ReleaseURL = snap.Control.ReleaseURL
	// 落后几次发布对 CLI 与控制面是同一个数:用的是同一份最新版本。
	view.CLI.Behind = snap.Control.Behind
	view.CLI.Available = view.CLI.Current != "" && view.CLI.Latest != "" &&
		updatecheck.Ahead(view.CLI.Current, view.CLI.Latest)
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
	target := strings.TrimSpace(body.Version)
	explicit := target != ""
	current := updateLocalVersion()
	src := updateSource()

	ctx, cancel := context.WithTimeout(r.Context(), updateInstallTimeout)
	defer cancel()

	if target == "" {
		latest, err := src.Latest(ctx)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		target = latest
	}

	if err := selfupdate.Decide(current, target, explicit, false); err != nil {
		if errors.Is(err, selfupdate.ErrUpToDate) {
			writeJSON(w, map[string]any{"ok": true, "from": current, "to": current, "message": "本机 CLI 已是最新"})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	exe, err := selfExecutable()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bin, _, err := src.Fetch(ctx, target, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := selfupdate.Install(exe, bin, target); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, selfupdate.ErrNeedSudo) {
			code = http.StatusForbidden
		}
		writeError(w, code, err.Error())
		return
	}

	// 不 os.Exit:页面自己会提示重启 mpx ui 后生效。
	writeJSON(w, map[string]any{"ok": true, "from": current, "to": target,
		"message": fmt.Sprintf("已升级到 %s；正在运行的 mpx ui 需重启后生效", target)})
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
