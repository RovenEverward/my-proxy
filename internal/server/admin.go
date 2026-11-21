package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/store"
)

// UC-26:网页管理台的登录与账号接口。
//
// 四条路径,两种挂法:
//   - POST /api/admin/login:唯一不要凭据的新接口(它就是来领凭据的),挂在
//     requireAuth 之外,自带三道闸 —— 与 /api/connect 同款的 IP 限速 + 全局
//     限速 + 连续失败锁 15 分钟。锁是独立的一把:登录把闸锁上不该顺手把正在用
//     设备密码接设备的人也关在外面。
//   - POST /api/admin/logout:也不要求凭据(过期的会话也该能退),同样单挂。
//   - POST /api/admin/account、PUT /api/admin/password:在 api mux 里,走
//     requireAuth —— 建号与改密本身就是管理操作,rescue 路径(MPX_TOKEN)天然在内。
//
// 浏览器会话与 CLI token 是同一枚凭据的两种携带方式:login 同时回 {token}
// 并 Set-Cookie,requireAuth 里 cookie 与 Bearer 查的是同一张 admin_sessions
// 表。会话 30 天固定 TTL(滚动续期留给后续批)。

const (
	// adminCookieName 是浏览器会话的 cookie 名。Path=/ 是硬要求:SPA 发的是
	// 同源 /api/* 请求,path 定到 /admin 下会话根本带不上来。
	adminCookieName = "mpx_admin"
	// webSessionTTL 是 web 会话的固定有效期。
	webSessionTTL = 30 * 24 * time.Hour
	// sessionKindWeb 标记会话是浏览器登录领的;CLI 侧的 'cli' 留给后续批的
	// mpx connect 用,本批只发 web。kind 不参与鉴权分支,只是来源记录。
	sessionKindWeb = "web"
)

// adminLoginGuard 是登录限流,与 connectGuard 同构:每 IP 每桶、全局一桶、
// 连续失败 connectLockAfter 次就整体锁 connectLockFor。直接复用类型,不另写
// 一份只有常数不同的实现。
func newAdminLoginGuard() *connectGuard {
	return newConnectGuard()
}

// ---- Origin / Fetch-Site 校验 ----

// adminOriginOK 判同源。和 ui.go 的 originOK 同一思路,但 scheme 按请求本身
// 判定(ui 那份写死 http —— 它只听 127.0.0.1;管理台要接 HTTPS,不改 ui 包):
//
//   - Sec-Fetch-Site 给出时,只认 same-origin 与用户直接打开(none)。
//   - Origin 给出时必须与这次请求同源:scheme + host 都相等。
//   - 两个头都没有时放行 —— 那是 CLI / curl 一类的非浏览器客户端,不是 CSRF;
//     浏览器的同源或跨源写请求总会带至少一个。
func adminOriginOK(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil || u.Host != r.Host {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return u.Scheme == scheme
}

// writeAdminOriginRejected 统一回 403;不细说缺了什么头,省得帮人对协议。
func writeAdminOriginRejected(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "跨源请求被拒绝")
}

// readMethod 判这次请求是不是读操作;cookie 会话的同源校验只管写。
func readMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// ---- 会话凭据的提取 ----

// adminSessionToken 取出这次请求携带的会话凭据:先 Authorization Bearer,再
// mpx_admin cookie。CLI 不会带 cookie,浏览器不会带 Bearer,两种形态落在同一
// 张 admin_sessions 表上校验。
func adminSessionToken(r *http.Request) string {
	if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got != "" {
		return got
	}
	if c, err := r.Cookie(adminCookieName); err == nil {
		return c.Value
	}
	return ""
}

// setAdminCookie 在登录成功时种下会话 cookie。Secure/HttpOnly/SameSite=Lax 是
// 管理台 cookie 的默认形状;Max-Age 与库里的 expires_at 一致,让浏览器与服务端
// 同步到期,不留「cookie 还在、行没了」的窗口。
func setAdminCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
}

// clearAdminCookie 让浏览器忘掉 cookie。MaxAge=-1 是「立即过期」的标准写法。
func clearAdminCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ---- 登录 ----

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminLoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// adminResetHint 是密码不对/已锁定时给界面的指路:网页没有「已接入设备」这条
// 退路,救援只有一条 —— 去服务器上用环境变量 token 调 mpx admin。
const adminResetHint = "忘记密码：登录控制面所在的服务器，用 MPX_TOKEN 跑 mpx admin password 重设；" +
	"或先 mpx admin create 建号（仅在还没有账号时生效）。"

// writeAdminLocked 是 writeConnectLocked 的登录版:只增字段的形状一样
// (error + ui_hint + locked_until),说法是网页管理台的,不提设备密码。
func writeAdminLocked(w http.ResponseWriter, code int, msg string, until time.Time, now time.Time) {
	left := until.Sub(now)
	if code == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(int(left.Seconds())+1))
	}
	writeJSON(w, code, map[string]string{
		"error":        msg,
		"ui_hint":      "用户名或密码连续输错太多次，登录已锁定，请 " + waitText(left) + "后再试。" + adminResetHint,
		"locked_until": until.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	// CSRF 与限速的次序:先判同源,再进桶 —— 跨源请求不算一次登录尝试。
	if !adminOriginOK(r) {
		writeAdminOriginRejected(w)
		return
	}
	now := time.Now()
	g := s.adminLogin
	if ok, retry := g.perIP.take(clientIP(r), now); !ok {
		tooMany(w, retry, "试得太频繁,请稍后再试",
			"试得太频繁：每台设备每分钟最多试 5 次，请 "+waitText(retry)+"后再试。")
		return
	}
	if ok, retry := g.global.take("*", now); !ok {
		tooMany(w, retry, "登录请求太多,请稍后再试",
			"这个控制面一分钟内收到的登录请求太多，请 "+waitText(retry)+"后再试。")
		return
	}
	if locked, left := g.locked(now); locked {
		writeAdminLocked(w, http.StatusTooManyRequests,
			"用户名或密码连续输错太多次,登录已锁定,"+left.Round(time.Minute).String()+"后再试", now.Add(left), now)
		return
	}
	if r.TLS == nil && !fromLoopback(r) {
		writeErrorHint(w, http.StatusForbidden, "账号密码只能经 HTTPS 登录",
			"账号密码只能经 HTTPS 登录，请用 https:// 的管理网址")
		return
	}

	var req adminLoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}

	username, hash, err := s.store.AdminAccount()
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErrorHint(w, http.StatusForbidden,
				"还没有管理员账号:在服务器上用 mpx admin create 建号(或由 mpx init 建)",
				"还没有管理员账号：请在控制面所在的服务器上建号后再登录")
			return
		}
		s.writeStoreError(w, err, "管理员账号")
		return
	}

	// 用户名不对也照跑一遍慢哈希:让「没有这个用户」与「密码不对」花一样的
	// 时间,不让响应时长变成账号探测的侧信道。限速兜着成本,一次 argon2 也就
	// 几十毫秒。
	phc := hash
	if subtleNameEqual(req.Username, username) != 1 {
		phc = dummyAdminPHC
	}
	ok, verr := verifyDevicePassword(req.Password, phc)
	if verr != nil {
		s.logger.Error("校验管理员口令失败", "err", verr)
		writeError(w, http.StatusInternalServerError, "校验口令失败")
		return
	}
	if !ok {
		s.logger.Warn("管理台登录失败", "remote", r.RemoteAddr, "user", req.Username)
		if g.record(false, now) {
			s.logger.Warn("登录连续失败,已锁定", "for", connectLockFor)
			writeAdminLocked(w, http.StatusUnauthorized, "用户名或密码不对", now.Add(connectLockFor), now)
			return
		}
		writeErrorHint(w, http.StatusUnauthorized, "用户名或密码不对",
			"用户名或密码不对。"+adminResetHint)
		return
	}
	g.record(true, now)

	tok, err := newDeviceToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成会话失败")
		return
	}
	expires := now.Add(webSessionTTL)
	if err := s.store.CreateAdminSession(hashToken(tok), sessionKindWeb, now, expires); err != nil {
		s.writeStoreError(w, err, "管理会话")
		return
	}
	s.logger.Info("管理台已登录", "remote", r.RemoteAddr)
	setAdminCookie(w, tok, expires)
	writeJSON(w, http.StatusOK, adminLoginResponse{Token: tok, ExpiresAt: expires})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if !adminOriginOK(r) {
		writeAdminOriginRejected(w)
		return
	}
	// 过期会话找不到行也照常回 204:登出的目的就是「这个 token 不再是凭据」,
	// 行在不在不影响结果;浏览器侧 cookie 一律清掉。
	if tok := adminSessionToken(r); tok != "" {
		if err := s.store.DeleteAdminSession(hashToken(tok)); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.writeStoreError(w, err, "管理会话")
			return
		}
	}
	clearAdminCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// ---- 账号 ----

type adminAccountRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminAccountResponse struct {
	Username string `json:"username"`
	Created  bool   `json:"created"`
}

// maxAdminNameLen 是用户名的长度上限;规则与设备名同级 —— 它出现在日志与登录
// 表单里,不是密钥。
const maxAdminNameLen = 64

// handleAdminAccount 是「建号或改名」共用的一个口(POST /api/admin/account):
//
//   - 没账号:必须带 username + password,建号。这是 mpx init 与服务器救援的
//     入口 —— 网页管理台自己进不了门,第一个账号只能由已认证请求种下。
//   - 已有账号:只能改 username(返回 created=false);要换密码走 PUT
//     /api/admin/password —— 那里强制验证旧密码,这里不偷偷放行。
func (s *Server) handleAdminAccount(w http.ResponseWriter, r *http.Request) {
	if !adminOriginOK(r) {
		writeAdminOriginRejected(w)
		return
	}
	var req adminAccountRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || len([]rune(username)) > maxAdminNameLen {
		writeErrorField(w, http.StatusBadRequest, "用户名不能为空,也不能超过 64 个字符", "username")
		return
	}

	cur, _, err := s.store.AdminAccount()
	switch {
	case errors.Is(err, store.ErrNotFound):
		// 建号:密码必填,长度口径与设备密码一致(它是同一套 argon2id 校验)。
		if err := checkDevicePasswordStrength(req.Password); err != nil {
			writeErrorField(w, http.StatusUnprocessableEntity, err.Error(), "password")
			return
		}
		hash, err := hashDevicePassword(req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "哈希口令失败")
			return
		}
		if err := s.store.CreateAdminAccount(username, hash, time.Now()); err != nil {
			s.writeStoreError(w, err, "管理员账号")
			return
		}
		s.logger.Info("管理员账号已建立", "user", username)
		writeJSON(w, http.StatusCreated, adminAccountResponse{Username: username, Created: true})
	case err != nil:
		s.writeStoreError(w, err, "管理员账号")
	default:
		// 改名:同名是空操作,直接回当前值。
		if username == cur {
			writeJSON(w, http.StatusOK, adminAccountResponse{Username: cur})
			return
		}
		if err := s.store.SetAdminUsername(username); err != nil {
			s.writeStoreError(w, err, "管理员账号")
			return
		}
		s.logger.Info("管理员用户名已改", "from", cur, "to", username)
		writeJSON(w, http.StatusOK, adminAccountResponse{Username: username})
	}
}

type adminPasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// handleAdminPassword 改口令(PUT /api/admin/password):改成功吊销全部会话
// —— 包括发起者自己:单账号模型下「全部重来」是最不容易出错的语义,发起者
// 拿新密码重新登录一次即可。
//
// 旧密码只在「凭会话/设备」改时验:凭 env 主钥匙(MPX_TOKEN)来的请求免验,
// 因为忘记密码的救援就是拿它改的 —— 旧密码已经丢了,验它没有意义。接口形状
// 不变,old_password 照传,master 侧忽略其值。
func (s *Server) handleAdminPassword(w http.ResponseWriter, r *http.Request) {
	if !adminOriginOK(r) {
		writeAdminOriginRejected(w)
		return
	}
	var req adminPasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	_, hash, err := s.store.AdminAccount()
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "还没有管理员账号:先 POST /api/admin/account 建号")
			return
		}
		s.writeStoreError(w, err, "管理员账号")
		return
	}
	if master, _ := r.Context().Value(authMasterKey).(bool); !master {
		ok, err := verifyDevicePassword(req.OldPassword, hash)
		if err != nil {
			s.logger.Error("校验旧口令失败", "err", err)
			writeError(w, http.StatusInternalServerError, "校验口令失败")
			return
		}
		if !ok {
			writeErrorField(w, http.StatusForbidden, "旧密码不对", "old_password")
			return
		}
	}
	if err := checkDevicePasswordStrength(req.NewPassword); err != nil {
		writeErrorField(w, http.StatusUnprocessableEntity, err.Error(), "new_password")
		return
	}
	newHash, err := hashDevicePassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "哈希口令失败")
		return
	}
	if err := s.store.SetAdminPasswordHash(newHash); err != nil {
		s.writeStoreError(w, err, "管理员口令")
		return
	}
	revoked, err := s.store.DeleteAllAdminSessions()
	if err != nil {
		s.writeStoreError(w, err, "管理会话")
		return
	}
	s.logger.Info("管理员口令已改,会话全部吊销", "revoked", revoked)
	clearAdminCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"set": true, "revoked": revoked})
}

// subtleNameEqual 与 tokenEqual 一个道理:先哈希再定长比较,免得长度不同
// 提前返回泄露用户名长度。
func subtleNameEqual(a, b string) int {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:])
}

// dummyAdminPHC 是用户名不匹配时拿去做空转的 PHC。常量固定一个合法形状 —
// 参数与现行哈希一致,verifyDevicePassword 不会解析失败,也永远验不过。
var dummyAdminPHC = func() string {
	h, err := hashDevicePassword("dummy-password-for-timing")
	if err != nil {
		panic(err)
	}
	return h
}()
