package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// 这一层走 xray 自带的 `xray api` 命令,而不是在 Go 里实现一个 gRPC 客户端。
//
// 理由:gRPC 客户端要复刻 xray 的 proto 定义和 TypedMessage 那层嵌套编码(操作类型
// 名、账号类型名、逐层序列化),而 xray 二进制里已经有一份不会漂移的实现。走命令行
// 还有个额外好处:出错时用户能照着同一条命令在节点上自己跑一遍,看到同样的输出。
// 代价是要解析它的 JSON 输出 —— 比复刻 proto 便宜得多。
//
// 注意:这几个函数都还没有在真实节点上验证过。

// apiServer 拼出指向节点本机 API 入站的参数。
//
// API 入站只监听回环(见 render/serverconfig.go),所以这里固定是 127.0.0.1。
func apiServer(n model.Node) string {
	return "--server=127.0.0.1:" + strconv.Itoa(n.APIPortOrDefault())
}

// apiRun 执行一条 `xray api` 子命令。args 需要是已经 quote 好的片段。
func apiRun(ctx context.Context, r Runner, n model.Node, sub string, stdin []byte, args ...string) (string, error) {
	parts := append([]string{xrayBin + `"$XRAY_BIN" api ` + sub, apiServer(n)}, args...)
	return r.RunWithInput(ctx, n, strings.Join(parts, " "), stdin)
}

// AddUser 在节点的某个入站上增量添加一个用户,不重启 xray。
//
// 不重启是重点:配额恢复、到期续费都会触发加用户,若每次都重启,节点上所有人的
// 连接都会跟着断一次。
func AddUser(ctx context.Context, r Runner, n model.Node, in model.Inbound, u model.User, now time.Time) error {
	frag, err := render.InboundAddUserConfig(in, u, now)
	if err != nil {
		return err
	}
	out, err := apiRun(ctx, r, n, "adu", frag, "stdin:")
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Added 1 user(s)") {
		return fmt.Errorf("节点 %s: 预期添加 1 个用户,实际输出:%s", n.Name, strings.TrimSpace(out))
	}
	return nil
}

// RemoveUser 从节点的某个入站上摘掉一个用户,不重启 xray。
//
// 入站的 tag 与用户的 email 都会进命令行,所以都要 quote。
func RemoveUser(ctx context.Context, r Runner, n model.Node, tag, email string) error {
	out, err := apiRun(ctx, r, n, "rmu", nil, "-tag="+shellQuote(tag), shellQuote(email))
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Removed 1 user(s)") {
		return fmt.Errorf("节点 %s: 预期移除 1 个用户,实际输出:%s", n.Name, strings.TrimSpace(out))
	}
	return nil
}

// InboundUsers 读出一个入站上此刻实际存在的用户名。
//
// 这是「节点现状」的那一半:控制面拿它和数据库里的期望名单比对,把差出来的补上
// 或摘掉。节点被手工改过、或某次下发中途失败,下一轮都能自己收敛回来。
func InboundUsers(ctx context.Context, r Runner, n model.Node, tag string) ([]string, error) {
	out, err := apiRun(ctx, r, n, "inbounduser", nil, "-tag="+shellQuote(tag))
	if err != nil {
		return nil, err
	}
	return parseInboundUsers(out)
}

// inboundUserEntry 只取 email:其余字段(level、account)在这里没有用。
type inboundUserEntry struct {
	Email string `json:"email"`
}

type inboundUsersResponse struct {
	Users []inboundUserEntry `json:"users"`
}

func parseInboundUsers(out string) ([]string, error) {
	out = strings.TrimSpace(out)
	start := strings.Index(out, "{")
	if start < 0 {
		return nil, fmt.Errorf("读不到 inbounduser 响应:%s", out)
	}
	var resp inboundUsersResponse
	if err := json.Unmarshal([]byte(out[start:]), &resp); err != nil {
		return nil, fmt.Errorf("解析 inbounduser 响应失败: %w", err)
	}
	names := make([]string, 0, len(resp.Users))
	for _, u := range resp.Users {
		if u.Email != "" {
			names = append(names, u.Email)
		}
	}
	return names, nil
}

// UserTraffic 是一次采样里某个用户的上下行字节数。
type UserTraffic struct {
	Username string
	Uplink   int64
	Downlink int64
}

// QueryUserTraffic 读出节点上所有用户的流量计数,并顺手把它们清零。
//
// 读走即清零是有意的:控制面按周期取增量累加进数据库。若不清零,就得自己保存
// 上一次的读数,而那份读数会在控制面重启或漏掉一次采样时悄悄失真。
func QueryUserTraffic(ctx context.Context, r Runner, n model.Node) (map[string]UserTraffic, error) {
	out, err := apiRun(ctx, r, n, "statsquery", nil, "-pattern="+shellQuote("user>>>"), "-reset")
	if err != nil {
		return nil, err
	}
	return parseUserTraffic(out)
}

// statEntry 对应 xray stats 里的一个计数器。
//
// value 用 RawMessage 是因为 protojson 把 int64 输出成字符串("1234"),但别的
// 版本或别的打印方式可能直接给数字,两种都得收。
type statEntry struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

type statsResponse struct {
	Stat []statEntry `json:"stat"`
}

// parseUserTraffic 把 statsquery 的 JSON 输出整理成「用户名 → 上下行」。
//
// 计数器名字形如 user>>>alice>>>traffic>>>uplink。不认识的计数器直接跳过:
// xray 还会输出入站维度的计数,那些不属于任何用户。
func parseUserTraffic(out string) (map[string]UserTraffic, error) {
	out = strings.TrimSpace(out)
	start := strings.Index(out, "{")
	if start < 0 {
		return nil, fmt.Errorf("读不到 stats 响应:%s", out)
	}
	var resp statsResponse
	if err := json.Unmarshal([]byte(out[start:]), &resp); err != nil {
		return nil, fmt.Errorf("解析 stats 响应失败: %w", err)
	}

	result := make(map[string]UserTraffic)
	for _, e := range resp.Stat {
		parts := strings.Split(e.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		v, err := statValue(e.Value)
		if err != nil {
			return nil, fmt.Errorf("计数器 %s 的值无法解析: %w", e.Name, err)
		}
		t := result[parts[1]]
		t.Username = parts[1]
		switch parts[3] {
		case "uplink":
			t.Uplink += v
		case "downlink":
			t.Downlink += v
		default:
			continue
		}
		result[parts[1]] = t
	}
	return result, nil
}

func statValue(raw json.RawMessage) (int64, error) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}
