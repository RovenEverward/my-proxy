package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rsc.io/qr"
)

// qrSVG 把一段文本画成二维码 SVG(每个模块一格,四周留 4 格静区,黑块白底)。
// 和 internal/server/page.go 里的同名函数一致,只是返回 []byte 便于直接写响应。
func qrSVG(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	n := code.Size
	const quiet = 4
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="二维码"><rect width="100%%" height="100%%" fill="#fff"/><path fill="#0a0a0a" d="`, n+2*quiet, n+2*quiet)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return []byte(b.String()), nil
}

// qrFetchTimeout 是 qr.svg?u= 问一次控制面上限。它只是一次小查询,不该让页面
// 挂太久;真正的传输复用控制面已经配好的 token / 证书指纹。
const qrFetchTimeout = 10 * time.Second

// serveQR:GET /local/qr.svg?d=<文本> 或 ?u=<用户名>。会话与来源校验在外层已经
// 做过。
//
// d 是页面手里的现成文本,原样画码;u 是本机进程按用户名去控制面取 auto 那条
// 订阅地址再画 —— 页面不必知道控制面地址与 token。
func (s *Server) serveQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 GET")
		return
	}
	d := r.URL.Query().Get("d")
	u := r.URL.Query().Get("u")
	if d == "" && u != "" {
		link, code, err := s.autoSubLink(r.Context(), u)
		if err != nil {
			writeError(w, code, err.Error())
			return
		}
		s.writeQRSVG(w, link)
		return
	}
	if d == "" || len(d) > 2048 {
		writeError(w, http.StatusBadRequest, "d 必须是 1–2048 字节的文本")
		return
	}
	s.writeQRSVG(w, d)
}

// autoSubLink 用本机进程已有的上游客户端取某个用户 auto 格式的订阅地址。
// 用户不存在回 404,其余上游问题回 502。
func (s *Server) autoSubLink(ctx context.Context, username string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, qrFetchTimeout)
	defer cancel()

	path := "/api/users/" + url.PathEscape(username) + "/links"
	data, code, err := s.controlCall(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", http.StatusBadGateway, fmt.Errorf("读取用户订阅地址失败: %w", err)
	}
	switch code {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", http.StatusNotFound, fmt.Errorf("用户不存在")
	default:
		return "", http.StatusBadGateway, fmt.Errorf("控制面返回 %d: %s", code, briefBody(data))
	}

	var links map[string]string
	if err := json.Unmarshal(data, &links); err != nil {
		return "", http.StatusBadGateway, fmt.Errorf("控制面返回的订阅地址不是合法 JSON")
	}
	link := links["auto"]
	if link == "" {
		return "", http.StatusBadGateway, fmt.Errorf("控制面没有返回 auto 订阅地址")
	}
	return link, http.StatusOK, nil
}

func (s *Server) writeQRSVG(w http.ResponseWriter, text string) {
	svg, err := qrSVG(text)
	if err != nil {
		writeError(w, http.StatusBadRequest, "这段文本画不成二维码")
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(svg)
}
