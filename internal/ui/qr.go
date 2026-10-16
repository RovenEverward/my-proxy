package ui

import (
	"fmt"
	"net/http"
	"strings"

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

// serveQR:GET /local/qr.svg?d=<文本>。会话与来源校验在外层已经做过。
func (s *Server) serveQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "这个接口只接受 GET")
		return
	}
	d := r.URL.Query().Get("d")
	if d == "" || len(d) > 2048 {
		writeError(w, http.StatusBadRequest, "d 必须是 1–2048 字节的文本")
		return
	}
	svg, err := qrSVG(d)
	if err != nil {
		writeError(w, http.StatusBadRequest, "这段文本画不成二维码")
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(svg)
}
