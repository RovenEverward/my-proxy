package server

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FingerprintPath 是证书指纹纯文本文件的位置:库旁边的 tls-fingerprint。
func FingerprintPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "tls-fingerprint")
}

func writeFingerprintFile(path, fp string) error {
	return os.WriteFile(path, []byte(fp+"\n"), 0o644)
}

// plaintextWarningHeader 是明文管理请求收到的弃用提示。CLI 看到它就在屏幕上
// 提醒一次,让旧安装在关掉明文管理之前有一个版本的时间切到 mpx connect。
const plaintextWarningHeader = "X-Mpx-Warning"

const plaintextWarning = "管理接口正在走明文 HTTP,下一个版本将只接受 HTTPS;请用 mpx connect 重新接入"

// plaintextAPIWarning 给"从别的机器、经明文 HTTP 打到 /api"的请求挂上弃用提示,
// 并在日志里记一笔(每小时至多一次,免得刷屏)。
//
// 这一版只提示、不拒绝:旧安装的 CLI 配置里还是 http:// 地址,直接拒绝会让
// 升级完控制面的人当场失联。回环来的明文请求不算 —— 那是本机或 SSH 隧道,
// 链路本身不出这台机器。
func (s *Server) plaintextAPIWarning(next http.Handler) http.Handler {
	var mu sync.Mutex
	var last time.Time
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil && strings.HasPrefix(r.URL.Path, "/api/") && !fromLoopback(r) {
			w.Header().Set(plaintextWarningHeader, plaintextWarning)
			mu.Lock()
			if time.Since(last) > time.Hour {
				last = time.Now()
				s.logger.Warn("有管理请求走了明文 HTTP", "remote", r.RemoteAddr, "path", r.URL.Path)
			}
			mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
