package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// ---- 用户 ----

// 用户列表的默认与最大页大小。
//
// 默认不是"全部":列表每行都要渲染,一千个用户刷屏既看不见也没法用;要一次拿完
// 就自己按 offset 翻页(每页最多 1000)。
const (
	defaultUserPageSize = 50
	maxUserPageSize     = 1000
)

// userPage 是 GET /api/users 的响应:一页用户加上匹配总数。
//
// 总数必须有:客户端靠"offset+本页条数 < total"才能说"还有 N 个",只看本页满没满
// 在整除时会猜错。它是筛选之后的总数,不是本页条数。
//
// 每一条比单独读一个用户多一个 presence(见 presence.go);抬头也带一份整体情况。
// 算不出来时抬头缺席,每条的用户字段照旧 —— 读不到在线情况不该让整个列表失败。
type userPage struct {
	Users    []userListItem   `json:"users"`
	Total    int              `json:"total"`
	Limit    int              `json:"limit"`
	Offset   int              `json:"offset"`
	Presence *presenceSummary `json:"presence,omitempty"`
}

// handleListUsers 列出用户,支持筛选与分页。
//
// 筛选参数:status(按 model.User.Status 的判定)、search(用户名子串,不分大小写)、
// inbound_id(可用该入站的用户)。判定不在这里做,一律交给 store —— 它复用 model
// 里那份唯一的判定,这里再写一遍就会有两份。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := userPage{Limit: defaultUserPageSize}

	if v := q.Get("limit"); v != "" {
		n, ok := parseInt(w, v)
		if !ok {
			return
		}
		if n < 1 || n > maxUserPageSize {
			writeError(w, http.StatusBadRequest, "limit 要在 1 到 "+strconv.Itoa(maxUserPageSize)+" 之间")
			return
		}
		page.Limit = int(n)
	}
	if v := q.Get("offset"); v != "" {
		n, ok := parseInt(w, v)
		if !ok {
			return
		}
		if n < 0 {
			writeError(w, http.StatusBadRequest, "offset 不能是负数")
			return
		}
		page.Offset = int(n)
	}

	status := model.Status(q.Get("status"))
	if status != "" && !status.Valid() {
		writeError(w, http.StatusBadRequest, "status 只能是 "+statusChoices()+"(按判定优先级排列)")
		return
	}

	var inboundID int64
	if v := q.Get("inbound_id"); v != "" {
		id, ok := parseInt(w, v)
		if !ok {
			return
		}
		inboundID = id
	}

	// plan 筛选用 store 层:判定在那一处,这里只解析参数。
	var planID *int64
	if v := q.Get("plan"); v != "" {
		id, ok := parseInt(w, v)
		if !ok {
			return
		}
		if id <= 0 {
			writeError(w, http.StatusBadRequest, "plan 必须是正整数")
			return
		}
		planID = &id
	}

	users, total, err := s.store.ListUsersPage(store.UserQuery{
		Status:    status,
		Search:    q.Get("search"),
		InboundID: inboundID,
		PlanID:    planID,
		Limit:     page.Limit,
		Offset:    page.Offset,
	})
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	var summary *presenceSummary
	var presence map[string]*userPresence
	if sum, byUser, err := s.presence(time.Now()); err != nil {
		s.logger.Warn("读取用户在线情况失败,本次列表不带在线信息", "error", err)
	} else {
		summary = &sum
		presence = byUser
	}
	page.Users = userListItems(users, presence)
	page.Presence = summary
	page.Total = total
	writeJSON(w, http.StatusOK, page)
}

// userListItems 把一页用户拼成列表条目,给每条挂上它的在线情况。
//
// 没有活跃记录、也不在线的用户拿到零值,只是 online_nodes 是 [] 而不是 null:
// 前端会直接遍历它,null 在那边是一处要单独特判的坑。
func userListItems(users []model.User, presence map[string]*userPresence) []userListItem {
	out := make([]userListItem, 0, len(users))
	for _, u := range users {
		item := userListItem{userView: toUserView(u), Presence: userPresence{OnlineNodes: []int64{}}}
		if p := presence[u.Username]; p != nil {
			item.Presence = *p
		}
		out = append(out, item)
	}
	return out
}

// statusChoices 拼出状态取值的提示。取值列表来自 model,不在这里另抄一份 ——
// 新加一个状态时只需要改 model。
func statusChoices() string {
	parts := make([]string, 0, len(model.UserStatuses))
	for _, s := range model.UserStatuses {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, " / ")
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if !decodeBody(w, r, &req) {
		return
	}
	u := req.User
	// 请求体里没提 enabled,就是启用 —— 新建的用户默认能用。
	u.Enabled = req.takeEnabled(false, true)
	if msg := validateUser(&u); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// 带了 plan_id 就按指定的套餐;没带则交给 store 的默认归属规则(空入站归
	// 默认套餐、非空入站不挂套餐),这是旧客户端与脚本的兼容路径。
	planID, hasPlan, err := req.takePlanID()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if hasPlan {
		u.PlanID = planID
	}
	if _, err := s.store.GetUser(u.Username); err == nil {
		writeError(w, http.StatusConflict, "用户名已存在")
		return
	}
	if err := fillUserSecrets(&u); err != nil {
		writeError(w, http.StatusInternalServerError, "生成密钥失败")
		return
	}
	if err := s.store.CreateUser(&u); err != nil {
		if errors.Is(err, store.ErrPlanNotFound) {
			writeError(w, http.StatusBadRequest, "指定的套餐不存在")
			return
		}
		s.writeStoreError(w, err, "用户")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusCreated, toUserView(u))
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUser(r.PathValue("username"))
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	writeJSON(w, http.StatusOK, toUserView(*u))
}

// handleUpdateUser 是整体替换:调用方先读、改、再写回。
//
// 留空的 Key / SubToken 会沿用原值,这样 CLI 不必把它们回传。
//
// 套餐归属单独处理:请求体里没带 plan_id 就保持原值(老客户端整体 PUT 不会
// 丢套餐);带了 null 拒绝(不能把用户移出套餐);带了 id 改挂。改挂只在确实发生变化时才调用
// SetUserPlan,这样 all_nodes 套餐被写入非空 inbound_ids 时 store 的摘除规则
// 仍然生效 —— 客户端原样回传同一个套餐时不会把它又挂回去。
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("username")
	old, err := s.store.GetUser(name)
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	var req userRequest
	if !decodeBody(w, r, &req) {
		return
	}
	u := req.User
	// 改用户是整体替换,但没提 enabled 时按保持原样 —— 不然只想改流量上限
	// 的一次 PUT 会把停用的用户悄悄启用。
	u.Enabled = req.takeEnabled(old.Enabled, false)
	u.ID = old.ID
	u.Username = name
	if msg := validateUser(&u); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	planID, hasPlan, err := req.takePlanID()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 在动库之前先确认套餐存在,免得改完字段才发现套餐是坏的。
	if hasPlan && planID != nil {
		if _, err := s.store.GetPlan(*planID); err != nil {
			if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrPlanNotFound) {
				writeError(w, http.StatusBadRequest, "指定的套餐不存在")
				return
			}
			s.writeStoreError(w, err, "套餐")
			return
		}
	}
	if u.Key == "" {
		u.Key = old.Key
	}
	if u.SubToken == "" {
		u.SubToken = old.SubToken
	}
	u.CreatedAt = old.CreatedAt
	if err := s.store.UpdateUser(&u); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	if hasPlan && !samePlanID(old.PlanID, planID) {
		if _, err := s.store.SetUserPlan(name, planID); err != nil {
			if errors.Is(err, store.ErrPlanNotFound) {
				writeError(w, http.StatusBadRequest, "指定的套餐不存在")
				return
			}
			s.writeStoreError(w, err, "用户套餐")
			return
		}
	}
	s.invalidateSubCache()
	// 重新读一次以反映被摘掉/改挂之后的套餐归属。
	if updated, err := s.store.GetUser(name); err == nil {
		writeJSON(w, http.StatusOK, toUserView(*updated))
		return
	}
	writeJSON(w, http.StatusOK, toUserView(u))
}

// samePlanID 比较两个套餐归属是否相同(nil 表示没挂套餐)。
func samePlanID(a, b *int64) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteUser(r.PathValue("username")); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// handleResetUserTraffic 手动把用户的周期用量归零。
//
// 起算点一并推到 now,于是手动重置同时也把周期窗口重新开始 —— 用户要求"重新
// 开始算"时想的就是这个意思,只清数字却让周期照旧走到头会显得莫名其妙。
func (s *Server) handleResetUserTraffic(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ResetUserTraffic(r.PathValue("username"), time.Now()); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

// handleRevokeSub 换掉订阅 token,不动用户的协议凭证。
func (s *Server) handleRevokeSub(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUser(r.PathValue("username"))
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	token, err := model.NewSubscriptionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成订阅 token 失败")
		return
	}
	u.SubToken = token
	if err := s.store.UpdateUser(u); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	// 换了 token 就是一条新链接:拉取记录清掉,让它重新等第一次拉取。
	if err := s.store.ClearSubPull(u.ID); err != nil {
		s.logger.Warn("清除订阅拉取记录失败", "username", u.Username, "error", err)
	}
	s.forgetSubPull(u.ID)
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, toUserView(*u))
}

// handleListSubPulls 返回全部用户的订阅拉取记录,没有记录时回 []。
// 权限与 GET /api/users 相同:都挂在 /api 的管理 token 之下。
func (s *Server) handleListSubPulls(w http.ResponseWriter, r *http.Request) {
	pulls, err := s.store.ListSubPulls()
	if err != nil {
		s.writeStoreError(w, err, "订阅拉取记录")
		return
	}
	writeJSON(w, http.StatusOK, pulls)
}

// handleUserLinks 给出该用户在各格式下的订阅地址。
func (s *Server) handleUserLinks(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUser(r.PathValue("username"))
	if err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}

	base := subBaseURL(settings, r)
	sub := base + "/sub/" + u.SubToken

	out := map[string]string{"auto": sub}
	// 只加的字段:链接开头没填时 auto 按这次请求的 Host 拼(本机开发、经 mpx ui 转发都会变),
	// 页面拿它判断「链接开头就是这台控制面」。旧读者只认 auto 及其余格式,不受影响。
	if settings.SubURLPrefix == "" {
		out["base"] = base
	}
	for _, f := range []model.ClientFormat{
		model.FormatLinks, model.FormatBase64, model.FormatClash, model.FormatClashMeta, model.FormatSingBox, model.FormatXray,
	} {
		out[string(f)] = sub + "/" + string(f)
	}
	writeJSON(w, http.StatusOK, out)
}
