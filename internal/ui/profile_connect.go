// 正常模式下的「添加控制面」:复用接入已有控制面的那套手续(先取指纹、人核对、
// 设备密码换 token),只是把结果存成一个指名的新控制面再切过去。与 /local/setup/
// connect 的区别:那两个接口只在首次安装(setup 模式)可用,这里随时可用 —— 窗口
// 里加第二套控制面是正常操作,不该逼人回终端。
package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// profileNameMax 是新控制面名称的长度上限,与页面输入框的提示一致。
const profileNameMax = 32

// connectRunner 拿到接入用的 ConnectRunner;没给这个能力就 404(Profiles 那一份
// 已在 serveProfiles 入口查过)。会话与来源校验同 /local/profiles,在 ServeHTTP
// 里做过。
func (s *Server) connectRunner(w http.ResponseWriter) (ConnectRunner, bool) {
	if s.opts.Connect == nil {
		writeError(w, http.StatusNotFound, "这里不支持接入控制面")
		return nil, false
	}
	return s.opts.Connect, true
}

// profileConnectProbe:POST {addr} → {server, fingerprint}。与 setup 模式的
// /local/setup/connect/probe 是同一件事(整理地址 + 取指纹),指纹同样只取来交给
// 页面上的人核对(TOFU),这里不做任何判断。
func (s *Server) profileConnectProbe(w http.ResponseWriter, r *http.Request) {
	runner, ok := s.connectRunner(w)
	if !ok {
		return
	}
	var body struct {
		Addr string `json:"addr"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, profilesBodyMax)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return
	}
	if strings.TrimSpace(body.Addr) == "" {
		writeError(w, http.StatusBadRequest, "请填写控制面地址")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), connectProLimit)
	defer cancel()
	server, fp, err := runner.Probe(ctx, body.Addr)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"server": server, "fingerprint": fp})
}

// profileConnect:POST {server, fingerprint, password, name, profile}。
// 接入一个新控制面、存成名为 profile 的新控制面并设为当前,本进程随即切过去
// (setUpstream)。与 /local/profiles/use 不同,它会写配置文件 —— 要把换来的
// token 存下来。成功返回 {ok:true, profile};名字被占用 409,密码只在这一次
// 请求里用,不回显、不进任何日志。
func (s *Server) profileConnect(w http.ResponseWriter, r *http.Request) {
	runner, ok := s.connectRunner(w)
	if !ok {
		return
	}
	named, ok := runner.(ConnectNamedRunner)
	if !ok {
		writeError(w, http.StatusNotFound, "这里不支持按名字保存控制面")
		return
	}
	var body struct {
		Server      string `json:"server"`
		Fingerprint string `json:"fingerprint"`
		Password    string `json:"password"`
		Name        string `json:"name"`
		Profile     string `json:"profile"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, profilesBodyMax)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return
	}
	profile := strings.TrimSpace(body.Profile)
	switch {
	case strings.TrimSpace(body.Server) == "":
		writeError(w, http.StatusBadRequest, "缺少控制面地址,先取指纹")
		return
	case strings.TrimSpace(body.Fingerprint) == "":
		writeError(w, http.StatusBadRequest, "缺少证书指纹,先取指纹并核对")
		return
	case body.Password == "":
		writeError(w, http.StatusBadRequest, "请输入设备密码")
		return
	case profile == "":
		writeError(w, http.StatusBadRequest, "请给这个控制面起个名字")
		return
	case !validProfileName(profile):
		writeError(w, http.StatusBadRequest, "名称只能用字母、数字、- 和 _,1–32 个字符")
		return
	}
	// 名字先在这里问一次,给人立刻的 409;实现方(connectAndSaveProfile 一侧)
	// 落盘前还会再判一次,两次之间撞上的极端并发由那一层兜住。
	exists, err := s.profileExists(profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "控制面 "+profile+" 已存在,请换一个名字")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), connectLimit)
	defer cancel()
	res, err := named.ConnectProfile(ctx, body.Server, body.Fingerprint, body.Password, body.Name, profile)
	body.Password = ""
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if res == nil || res.Upstream == nil || res.Upstream.Host == "" {
		writeError(w, http.StatusBadGateway, "接入没有返回控制面地址")
		return
	}
	s.setUpstream(res.Upstream, res.Token, res.Transport)
	s.mu.Lock()
	s.opts.Profile = res.Profile
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "profile": res.Profile})
}

// profileExists 问 ProfileSource 要档表,看名字是不是已经被占用了。
func (s *Server) profileExists(name string) (bool, error) {
	list, err := s.opts.Profiles.List()
	if err != nil {
		return false, err
	}
	for _, p := range list {
		if p.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// validProfileName:字母数字 - _,1–32 个字符。与 CLI --profile 的习惯一致,排除
// 空格、点、斜杠这些进路径或命令行时有歧义的字符。
func validProfileName(name string) bool {
	if len(name) < 1 || len(name) > profileNameMax {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
