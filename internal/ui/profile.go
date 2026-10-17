package ui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// profilesBodyMax 是切换配置档请求体的字节上限。
const profilesBodyMax = 4 << 10

// ProfileInfo 是页面上列出的一个配置档:名字、控制面地址、"是不是当前档",以及
// 这个档记下的证书指纹(给人核对用,http 控制面没有则为空)。token 绝不出现在
// 这里,页面拿到的永远是这一层信息。
type ProfileInfo struct {
	Name    string `json:"name"`
	Server  string `json:"server"`
	Current bool   `json:"current"`
	// Fingerprint 形如 SHA256:…。它本来就要摆到人眼前核对(接入时的 TOFU、
	// mpx status 里的「证书指纹」),不属于机密;token 才是。
	Fingerprint string `json:"fingerprint,omitempty"`
}

// ProfileSource 由 cli 实现:列出本机的配置档,并把某个档解析成转发要用的
// upstream/token/transport。ui 包不 import cli,靠这个接口解耦。
type ProfileSource interface {
	List() ([]ProfileInfo, error)
	Resolve(name string) (upstream *url.URL, token string, tr http.RoundTripper, err error)
}

// serveProfiles 处理 /local/profiles(列出)与 /local/profiles/use(切换),以及
// 正常模式下的 /local/profiles/connect(/probe)。接口都只接受 POST;会话与来源
// 校验已经在 ServeHTTP 里做过,与 /local/setup 同规矩。没有实现 ProfileSource 时
// 一律 404。
func (s *Server) serveProfiles(w http.ResponseWriter, r *http.Request) {
	if s.opts.Profiles == nil {
		writeError(w, http.StatusNotFound, "这里不支持切换配置档")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 POST")
		return
	}
	switch r.URL.Path {
	case "/local/profiles":
		s.profileList(w)
	case "/local/profiles/use":
		s.profileUse(w, r)
	case "/local/profiles/connect/probe":
		s.profileConnectProbe(w, r)
	case "/local/profiles/connect":
		s.profileConnect(w, r)
	default:
		writeError(w, http.StatusNotFound, "没有这个配置档接口")
	}
}

// profileList:POST /local/profiles,返回 [{name, server, current, fingerprint}]。
//
// current 以这个 ui 进程正在用的档为准(而不是配置文件里的 current):页面上的
// 勾要跟着实际转发目标走。
func (s *Server) profileList(w http.ResponseWriter) {
	list, err := s.opts.Profiles.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []ProfileInfo{}
	}
	s.mu.Lock()
	current := s.opts.Profile
	s.mu.Unlock()
	// 正在用的档以这个进程为准;首次安装模式下还没有转发目标,保留实现方给的
	// 结论(跟着配置文件里的 current 走)。
	if current != "" {
		for i := range list {
			list[i].Current = list[i].Name == current
		}
	}
	writeJSON(w, list)
}

// profileUse:POST /local/profiles/use,body {"name":"..."}。换掉本进程的转发目标,
// 不写配置文件:切换只影响这个 mpx ui 进程。
func (s *Server) profileUse(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, profilesBodyMax)
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "请给配置档名字")
		return
	}
	up, token, tr, err := s.opts.Profiles.Resolve(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if up == nil || up.Host == "" {
		writeError(w, http.StatusBadRequest, "配置档 "+name+" 没有控制面地址")
		return
	}
	s.setUpstream(up, token, tr)
	s.mu.Lock()
	s.opts.Profile = name
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "profile": name, "server": up.Redacted()})
}

// currentFingerprint 给 /ui/meta 找当前配置档记下的证书指纹:设置页要显示它,好
// 把窗口里连着的控制面和终端里 mpx status 报的那一行对上。档表每次向实现方现读
// (cli 读的是配置文件),切换或新接入配置档后自然跟着变,不用在这里另记状态。
func (s *Server) currentFingerprint() string {
	s.mu.Lock()
	name := s.opts.Profile
	src := s.opts.Profiles
	s.mu.Unlock()
	if src == nil || name == "" {
		return ""
	}
	list, err := src.List()
	if err != nil {
		return ""
	}
	for _, p := range list {
		if p.Name == name {
			return p.Fingerprint
		}
	}
	return ""
}
