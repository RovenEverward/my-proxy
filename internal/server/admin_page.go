package server

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
	"github.com/RovenEverward/my-proxy/internal/store"
	"github.com/RovenEverward/my-proxy/internal/ui"
)

// UC-26 批 2:控制面伺服网页管理台。
//
//   - /admin/           管理台 SPA 的静态树(嵌入的 internal/ui/assets;hash
//     路由,入口就是 index.html,不需要 fallback)。
//   - /flags、/fonts、/styles  前端产物里以绝对路径引用的共享资源:国旗
//     (assets/flags.zip)、字体、样式表。挂在根上是因为 CSS 的 url()
//     没法相对到 /admin。
//   - GET /api/admin/meta  页首元信息,在 requireAuth 内:登录后会话 cookie
//     与 Bearer 都能取;未登录 401,前端据此跳 #login。
//
// /admin、/flags、/fonts、/styles 都不查身份:登录页本身也要从这里加载,
// 凭据管的是 /api。/admin 只受理 TLS 或回环请求(与登录接口同一道门),
// 明文公网回提示不伺服页面。

// adminCSP 是管理台与其共享资源的唯一 CSP;internal/ui 的本机服务已退役,
// 这是剩下的唯一一份,改动直接改这里。
const adminCSP = "default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// adminSecurityHeaders 给管理台与其共享资源补一致的安全头:同源才准加载脚本
// 与样式,禁嗅探、禁嵌入、禁跨源读。
func adminSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", adminCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// adminTLSOnly 用与登录接口相同的「TLS 或回环」判据:明文公网问 /admin 不
// 伺服页面,回同口径提示。
func adminTLSOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil && !fromLoopback(r) {
			writeError(w, http.StatusForbidden, "管理台只受理 https 请求(本机回环除外)")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// adminStatic 把嵌入的 assets 整树挂在 /admin/ 下;按路径补缓存头:
// build/assets 下是 vite 的内容哈希文件名,可以放心 immutable;入口
// html、boot.js 与其余文件每次问一句(no-cache,不是不许缓存)。
func (s *Server) adminStatic() http.Handler {
	fsHandler := http.StripPrefix("/admin/", http.FileServerFS(ui.Admin()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/admin/")
		if strings.HasPrefix(rel, "build/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fsHandler.ServeHTTP(w, r)
	})
}

// subStatic 把嵌入 assets 的一个子目录挂到 prefix 下;子目录不存在时给个
// 全 404 的空树(编译期 embed 必有,这只是防御写法)。
func subStatic(prefix, dir string) http.Handler {
	sub, err := fs.Sub(ui.Admin(), dir)
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.StripPrefix(prefix, http.FileServerFS(sub))
}

// handleFlag 伺服 /flags/<code>.svg:未登录也要能画出登录页与列表里的
// 旗。内容不变,长期缓存。
func (s *Server) handleFlag(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !strings.HasSuffix(code, ".svg") {
		http.NotFound(w, r)
		return
	}
	rc, ok := ui.Flag(strings.TrimSuffix(code, ".svg"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// adminMeta 是 /api/admin/meta 的响应形状;字段与前端 runtime/types.ts 的
// Meta 一一对应。加字段是兼容演进,删字段先看前端。
type adminMeta struct {
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint,omitempty"`
	AdminExists bool   `json:"admin_exists"`
	Username    string `json:"username,omitempty"`
}

// handleAdminMeta 是页首元信息(取代已退役的 /ui/meta),只放登录后界面
// 起步需要的东西:版本、自签证书指纹(页面展示用;核对仍走 CLI)、管理员
// 账号名(账户菜单显示)。它在 requireAuth 内,未登录拿到统一 401,不
// 额外泄露「有没有账号」。
func (s *Server) handleAdminMeta(w http.ResponseWriter, r *http.Request) {
	meta := adminMeta{Version: buildinfo.Version(), Fingerprint: s.tlsFingerprint}
	if username, _, err := s.store.AdminAccount(); err == nil {
		meta.AdminExists = true
		meta.Username = username
	} else if !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, err, "管理员账号")
		return
	}
	writeJSON(w, http.StatusOK, meta)
}
