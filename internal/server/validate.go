package server

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// 校验集中在入口一处:存储层和渲染层都假定拿到的对象已经是合法的。

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// userView 是用户对外(CLI / API)的样子。
//
// 与 model.User 的差别只有两处:带上算出来的状态,以及不暴露 Key —— 有了 Key
// 就能推出该用户在所有协议上的凭证,没有理由把它发出去。
type userView struct {
	ID            int64               `json:"id"`
	Username      string              `json:"username"`
	SubToken      string              `json:"sub_token"`
	Enabled       bool                `json:"enabled"`
	Status        model.Status        `json:"status"`
	DataLimit     int64               `json:"data_limit"`
	UsedTraffic   int64               `json:"used_traffic"`
	LifetimeUsed  int64               `json:"lifetime_used_traffic"`
	ExpireAt      *time.Time          `json:"expire_at,omitempty"`
	ResetStrategy model.ResetStrategy `json:"reset_strategy"`
	ResetAt       *time.Time          `json:"traffic_reset_at,omitempty"`
	InboundIDs    []int64             `json:"inbound_ids,omitempty"`
	Note          string              `json:"note,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
}

func toUserView(u model.User) userView {
	return userView{
		ID:            u.ID,
		Username:      u.Username,
		SubToken:      u.SubToken,
		Enabled:       u.Enabled,
		Status:        u.Status(time.Now()),
		DataLimit:     u.DataLimit,
		UsedTraffic:   u.UsedTraffic,
		LifetimeUsed:  u.LifetimeUsedTraffic,
		ExpireAt:      u.ExpireAt,
		ResetStrategy: u.ResetStrategy,
		ResetAt:       u.TrafficResetAt,
		InboundIDs:    u.InboundIDs,
		Note:          u.Note,
		CreatedAt:     u.CreatedAt,
	}
}

func userViews(users []model.User) []userView {
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, toUserView(u))
	}
	return out
}

// userRequest 是建用户与改用户的请求体。
//
// 比 model.User 只多一件事:Enabled 是 *bool,为了分得清「没说」和「说了
// false」。model.User 里它是裸 bool,零值就是 false —— 手工打 API 时
// `curl -d '{"username":"dave"}'` 会悄没声地建出一个连不上的用户,而 CLI
// 不写 --disabled 就是启用,同一个字段两种结果。
//
// 嵌入的 model.User 里那个同名字段会被外层这个遮住(JSON 里同名的取浅的那个),
// 其余字段照常解析。
type userRequest struct {
	model.User
	Enabled *bool `json:"enabled"`
}

// takeEnabled 把三态收成一个 bool:没说时,新建按启用、改动按保持原样。
func (req *userRequest) takeEnabled(old bool, creating bool) bool {
	switch {
	case req.Enabled != nil:
		return *req.Enabled
	case creating:
		return true
	default:
		return old
	}
}

// fillUserSecrets 给新建用户生成密钥与订阅 token。
//
// 无条件覆盖:请求体里带什么都不作数。用户凭证是控制面发出去的,让调用方挑
// 只会多出一条能挑重复、挑弱值的路,而 CLI 还得把同一套生成逻辑再写一遍。
func fillUserSecrets(u *model.User) error {
	key, err := model.NewKey()
	if err != nil {
		return err
	}
	u.Key = key

	token, err := model.NewSubscriptionToken()
	if err != nil {
		return err
	}
	u.SubToken = token

	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	return nil
}

// ensureReality 给 REALITY 入站备齐密钥对与 shortId。
//
// 这两样都由控制面生成、都不接受调用方指定:私钥不下发(见 model.Inbound 的
// json:"-"),而要下发给客户端的是从私钥算出来的公钥,调用方单独给一个公钥只会
// 给出一对配不上的密钥。
//
// shortId 也必须有一个:服务端列表为空时,客户端只要带了任意 non-empty 的 sid
// 就会握手失败,而不少客户端默认会自己生成一个。留空等于把这些客户端挡在门外。
//
// old 非空表示这是改一条已有入站:已经发出去的密钥对与 shortId 必须原样留住,
// 换掉等于让所有客户端的配置当场失效。
func ensureReality(in *model.Inbound, old *model.Inbound) error {
	if in.Security != model.SecurityReality {
		return nil
	}
	keepOld := old != nil && old.Security == model.SecurityReality

	if keepOld && old.RealityPriv != "" {
		in.RealityPriv, in.RealityPub = old.RealityPriv, old.RealityPub
	} else {
		priv, pub, err := model.GenerateRealityKeypair()
		if err != nil {
			return err
		}
		in.RealityPriv, in.RealityPub = priv, pub
	}

	if len(in.ShortIDs) > 0 {
		return nil
	}
	if keepOld && len(old.ShortIDs) > 0 {
		in.ShortIDs = old.ShortIDs
		return nil
	}
	sid, err := model.NewShortID()
	if err != nil {
		return err
	}
	in.ShortIDs = []string{sid}
	return nil
}

func validateNode(n *model.Node) string {
	if n.Name == "" {
		return "节点名不能为空"
	}
	if n.Address == "" {
		return "节点地址不能为空"
	}
	// 这两个值会进 systemd 单元文件和 systemctl 的命令行:空白会把单元文件切错,
	// 斜杠会让单元文件落到别的目录去。在这里挡下来,比到了节点上报一句
	// "Unit mpx xray.service not found" 好认得多。
	if strings.ContainsAny(n.ServiceName, " \t\n/") {
		return "服务名里不能有空白或斜杠"
	}
	if n.ConfigPath != "" && (!strings.HasPrefix(n.ConfigPath, "/") || strings.ContainsAny(n.ConfigPath, " \t\n\"")) {
		return "配置路径要写成绝对路径,且不带空白和引号"
	}
	return ""
}

func validateInbound(i *model.Inbound) string {
	if i.NodeID == 0 {
		return "必须指定所属节点"
	}
	if !i.Protocol.Valid() {
		return "不支持的协议: " + string(i.Protocol)
	}
	if !i.Network.Valid() {
		return "不支持的传输方式: " + string(i.Network)
	}
	if !i.Security.Valid() {
		return "不支持的安全层: " + string(i.Security)
	}
	if i.Port < 1 || i.Port > 65535 {
		return "端口必须在 1-65535 之间"
	}
	if i.Network == model.NetworkGRPC && i.GRPCService == "" {
		return "gRPC 传输必须填 serviceName"
	}
	if i.Security == model.SecurityTLS && (i.CertFile == "" || i.KeyFile == "") {
		return "TLS 必须填证书与私钥在节点上的路径"
	}
	if i.Security == model.SecurityReality {
		if i.RealityDest == "" || i.SNI == "" {
			return "REALITY 必须填 dest 与 SNI"
		}
		// REALITY 靠借用目标站点的 TLS 握手,承载不了 ws 的 HTTP Upgrade。
		if i.Network != model.NetworkTCP {
			return "REALITY 只支持 tcp 传输"
		}
	}
	if i.Flow != "" && i.Protocol != model.ProtocolVLESS {
		return "flow 只有 VLESS 支持"
	}
	return ""
}

func validateHost(h *model.Host) string {
	if h.InboundID == 0 {
		return "必须指定所属入站"
	}
	return ""
}

func validateUser(u *model.User) string {
	if !usernamePattern.MatchString(u.Username) {
		return "用户名只能用字母、数字、下划线、点和减号,长度 1-64"
	}
	if u.DataLimit < 0 {
		return "流量上限不能是负数"
	}
	// 留空按 none 处理。认不出的值必须挡在这里:模型层虽然已经不会再被它卡死,
	// 但一个语义不明的策略存进库里,使用者看到的和实际发生的会不一致。
	if u.ResetStrategy == "" {
		u.ResetStrategy = model.ResetNone
	}
	if !u.ResetStrategy.Valid() {
		return "流量重置策略只能是 none / daily / weekly / monthly"
	}
	return ""
}

func compilePattern(p string) (*regexp.Regexp, error) {
	if p == "" {
		return nil, errors.New("正则不能为空")
	}
	return regexp.Compile(p)
}

func parseInt(w http.ResponseWriter, v string) (int64, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "参数不是整数: "+v)
		return 0, false
	}
	return n, true
}

// 让编译器盯着:store.ErrNotFound 的语义必须由存储层保持。
var _ = store.ErrNotFound
