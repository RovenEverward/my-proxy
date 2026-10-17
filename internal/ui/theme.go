package ui

import (
	"encoding/json"
	"net/http"
)

// themeBodyMax 是主题请求体的字节上限:只有 {"theme":"..."} 这么点内容。
const themeBodyMax = 1 << 10

// serveTheme 处理 POST /local/app/theme:页面把当前主题告诉桌面窗口,让
// mygo 的原生外观(标题栏、滚动条、窗口底色)一起换。会话与来源校验已经在
// ServeHTTP 里做过,与 /local/profiles 同规矩。浏览器模式没有 SetTheme,404。
func (s *Server) serveTheme(w http.ResponseWriter, r *http.Request) {
	if s.opts.SetTheme == nil {
		writeError(w, http.StatusNotFound, "这里没有桌面窗口可以跟随主题")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, themeBodyMax)
	var body struct {
		Theme string `json:"theme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return
	}
	switch body.Theme {
	case "light", "dark", "system":
	default:
		writeError(w, http.StatusBadRequest, "主题只能是 light、dark 或 system")
		return
	}
	if err := s.opts.SetTheme(body.Theme); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "theme": body.Theme})
}
