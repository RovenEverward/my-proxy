package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// handleSubscribe 是客户端真正访问的那个入口。
//
// 路径里既没有用户名也没有格式:用户只带一个不可猜的 token,格式由 User-Agent
// 规则决定。这样同一条订阅链接在 Clash 和 sing-box 里各自拿到对的配置。
func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	user, err := s.store.GetUserBySubToken(r.PathValue("token"))
	if err != nil {
		// 订阅路径猜错是常态(扫描器),不值得记日志。
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}

	format, ruleHit := settings.MatchFormat(r.UserAgent())
	pathFormat := r.PathValue("format")
	if pathFormat != "" {
		format = model.ClientFormat(pathFormat)
		if !format.Valid() {
			writeError(w, http.StatusBadRequest, "不支持的订阅格式 "+pathFormat)
			return
		}
	}
	if format == model.FormatBlock {
		writeError(w, http.StatusNotFound, "订阅已被拒绝")
		return
	}

	// 订阅页只在"这次本来要回没人认领的默认 links"、且对方报明是浏览器时才顶
	// 上来。路径点名了格式、或规则真的命中过(哪怕命中的就是 links),都说明
	// 对方要的不是页面(见 decisions 26)。
	if pathFormat == "" && !ruleHit && format == model.FormatLinks && wantsHTML(r) {
		s.serveSubPage(w, r, user, settings, "")
		return
	}

	body, err := s.renderSubscription(user, format)
	if err != nil {
		s.logger.Error("渲染订阅失败", "username", user.Username, "format", format, "error", err)
		writeError(w, http.StatusInternalServerError, "渲染订阅失败")
		return
	}

	s.setSubHeaders(w, user, settings, contentTypeOf(format))
	// 各家客户端会把这一条显示成"打开网页",指向的就是浏览器打开订阅地址看到
	// 的那一屏。
	w.Header().Set("Profile-Web-Page-Url", subBaseURL(settings, r)+"/sub/"+user.SubToken)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// subBaseURL 是拼订阅地址用的前缀:设置里存了就用它(去掉尾部斜杠),没存就用
// 这次请求的协议与主机。管理接口的 user links 与订阅页共用这一段,免得两处各
// 拼一份、早晚不一致。
func subBaseURL(settings *model.Settings, r *http.Request) string {
	if base := strings.TrimSuffix(settings.SubURLPrefix, "/"); base != "" {
		return base
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// renderSubscription 产出订阅正文。不可用的用户拿到的是一个占位节点而不是真
// 配置:他们的凭证在节点上已经被摘掉了,给真配置只会让客户端连不上还不报错。
func (s *Server) renderSubscription(user *model.User, format model.ClientFormat) ([]byte, error) {
	eps, err := s.endpointsFor(user)
	if err != nil {
		return nil, err
	}

	settings, err := s.store.GetSettings()
	if err != nil {
		return nil, err
	}

	switch format {
	case model.FormatLinks:
		return []byte(strings.Join(render.Links(eps), "\n")), nil
	case model.FormatBase64:
		return []byte(render.LinksBase64(eps)), nil
	case model.FormatClash, model.FormatClashMeta:
		return render.Clash(eps, format == model.FormatClashMeta, []byte(settings.TemplateFor(format)))
	case model.FormatSingBox:
		return render.SingBox(eps, []byte(settings.TemplateFor(model.FormatSingBox)))
	case model.FormatXray:
		return render.Xray(eps, []byte(settings.TemplateFor(model.FormatXray)))
	default:
		return nil, fmt.Errorf("不支持的订阅格式 %q", format)
	}
}

// endpointsFor 收集该用户在全部节点上的可用入口。
//
// 订阅是「所有节点合成一份」,所以这里把全部节点都过一遍;入站授权和状态由
// render.Resolve 与 placeholder 逻辑负责。
func (s *Server) endpointsFor(user *model.User) ([]render.Endpoint, error) {
	if status := user.Status(time.Now()); status != model.StatusActive {
		return []render.Endpoint{placeholderEndpoint(user, status)}, nil
	}

	nodes, err := s.store.ListNodes()
	if err != nil {
		return nil, err
	}
	inbounds, err := s.store.ListInbounds(0)
	if err != nil {
		return nil, err
	}
	hosts, err := s.store.ListHosts(0)
	if err != nil {
		return nil, err
	}

	byNode := make(map[int64][]model.Inbound, len(nodes))
	for _, in := range inbounds {
		byNode[in.NodeID] = append(byNode[in.NodeID], in)
	}
	byInbound := make(map[int64][]model.Host, len(hosts))
	for _, h := range hosts {
		byInbound[h.InboundID] = append(byInbound[h.InboundID], h)
	}

	var eps []render.Endpoint
	for _, n := range nodes {
		eps = append(eps, render.Resolve(n, byNode[n.ID], byInbound, user)...)
	}
	// 节点名唯一的约束落在整份订阅这一层:同一个入站的不同入口、以及不同节点上
	// 恰巧取了一样的名字,都只有把所有人的入口放在一起看才知道撞没撞。
	return render.DedupeRemarks(eps), nil
}

// placeholderEndpoint 是一个连不通、但各家客户端都肯接受的节点。
func placeholderEndpoint(user *model.User, status model.Status) render.Endpoint {
	return render.Endpoint{
		Remark:   statusRemark(status),
		Address:  "0.0.0.0",
		Port:     1,
		Protocol: model.ProtocolVLESS,
		UUID:     model.DeriveUUID(user.Key),
		Network:  model.NetworkTCP,
		Security: model.SecurityNone,
	}
}

// statusRemark 是占位节点的名字,也是客户端里唯一能看到的一句话。
// 三种不可用状态用户要做的事不一样 —— 停用要申诉、过期要续费、超限要加流量,
// 所以名字得区分开,不能都写成「已停用」。
func statusRemark(status model.Status) string {
	switch status {
	case model.StatusDisabled:
		return "订阅已停用"
	case model.StatusExpired:
		return "订阅已过期"
	case model.StatusLimited:
		return "订阅已超限"
	default:
		return "订阅不可用"
	}
}

// setSubHeaders 写各家客户端认的那几个响应头。
func (s *Server) setSubHeaders(w http.ResponseWriter, user *model.User, settings *model.Settings, contentType string) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", "attachment; filename="+user.Username)
	h.Set("Profile-Title", encodeTitle(expandUser(settings.ProfileTitle, user)))
	h.Set("Subscription-Userinfo", userInfo(user))
	if settings.SupportLink != "" {
		h.Set("Support-Url", settings.SupportLink)
	}
	if settings.UpdateInterval > 0 {
		h.Set("Profile-Update-Interval", strconv.Itoa(settings.UpdateInterval))
	}
}

func contentTypeOf(format model.ClientFormat) string {
	switch format {
	case model.FormatClash, model.FormatClashMeta:
		return "text/yaml; charset=utf-8"
	case model.FormatSingBox, model.FormatXray:
		return "application/json; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

// userInfo 是订阅页和客户端用来显示用量的那一段。
// 本工具只统计总量,不分上行下行,因此 upload 固定为 0。
func userInfo(u *model.User) string {
	expire := 0
	if u.ExpireAt != nil {
		expire = int(u.ExpireAt.Unix())
	}
	return fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d", u.UsedTraffic, u.DataLimit, expire)
}

func expandUser(s string, u *model.User) string {
	return strings.ReplaceAll(s, "{USERNAME}", u.Username)
}

// encodeTitle 按客户端约定编码订阅名:含非 ASCII 字符时整段 base64 并加前缀。
// 中文名不这样处理会在部分客户端里显示成乱码。
func encodeTitle(title string) string {
	for _, r := range title {
		if r > 127 {
			return "base64:" + base64.StdEncoding.EncodeToString([]byte(title))
		}
	}
	return title
}
