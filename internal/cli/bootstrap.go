package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// bootstrap 是 `mpx init` 在远端调用的那一步:控制面已经装好了,它只负责通过
// 本机的 /api 把「一个立刻能用的节点」建出来 —— 节点、入站、对外入口、订阅前缀、
// 用户,最后在节点上装核心并把配置下发下去。
//
// 为什么是控制面本机的一个子命令,而不是笔记本上直接调 /api:token 只存在控制面
// 的 /etc/mpx/env 里,这里读它不需要把它送过 SSH,也就不会出现在任何一端的 ps
// 或 shell 历史里。它是隐藏命令,不进帮助列表 —— 它是 init 的内部实现,不是给人
// 单独敲的。
const (
	bootstrapEnvFile = "/etc/mpx/env"
	bootstrapKeyFile = "/var/lib/mpx/.ssh/id_ed25519.pub"

	// bootstrapLocalURL 是 install.sh 装出来的默认监听地址。用回环而不是公网
	// 地址:bootstrap 就在这台机器上,没必要为了自己绕一圈公网。
	bootstrapLocalURL = "http://127.0.0.1:8080"

	// REALITY 的 dest/SNI 默认用 cloudflare。
	//
	// 这里踩过一次:原先文档里写的是 www.microsoft.com,结果有节点连它自己都连不上,
	// REALITY 握手直接失败;换 cloudflare 立刻就好。dest 必须是「节点自己真能连上」
	// 的域名,cloudflare 是命中率最高的那个。
	bootstrapSNI         = "www.cloudflare.com"
	bootstrapRealityDest = "www.cloudflare.com:443"

	// bootstrapFlow 是 VLESS + TCP + REALITY 下唯一值得给的 flow。
	bootstrapFlow = "xtls-rprx-vision"
)

var bootstrapFlags struct {
	address     string
	name        string
	username    string
	sshUser     string
	sshPort     int
	port        int
	apiPort     int
	sni         string
	realityDest string
	subPrefix   string
	remark      string
	envFile     string
	keyFile     string
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "在控制面本机一键建好节点、入站、入口、订阅设置与用户",
	Long: `在控制面本机把一整套东西建出来,让订阅立刻可用。

它是 mpx init 在远端调用的内部实现,默认不出现在命令列表里。跑在控制面这台机器
上,所以 token 直接从 /etc/mpx/env 读,不经命令行。

做这些事:把控制面的公钥装进本机 sshd 的 authorized_keys(节点就是这台机器)、
注册节点、建一个 VLESS+TCP+REALITY 入站、建对外入口、把订阅前缀设成本机地址、
建一个用户,最后在节点上装 xray 核心并下发配置。

已经存在的对象会被复用,所以重复跑不会炸。`,
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   func(cmd *cobra.Command, args []string) error { return runBootstrap() },
}

func init() {
	f := bootstrapCmd.Flags()
	f.StringVar(&bootstrapFlags.address, "address", "", "这台机器对外可达的地址或 IP,必填(订阅里就写它)")
	f.StringVar(&bootstrapFlags.name, "name", "default", "节点名")
	f.StringVar(&bootstrapFlags.username, "username", "user1", "要建的用户名")
	f.StringVar(&bootstrapFlags.sshUser, "ssh-user", "root", "控制面用哪个用户 SSH 进这个节点")
	f.IntVar(&bootstrapFlags.sshPort, "ssh-port", 22, "控制面 SSH 进这个节点的端口")
	f.IntVar(&bootstrapFlags.port, "inbound-port", 443, "代理入站端口")
	f.IntVar(&bootstrapFlags.apiPort, "api-port", 8080, "控制面监听端口(算订阅前缀用)")
	f.StringVar(&bootstrapFlags.sni, "sni", bootstrapSNI, "REALITY 的 SNI")
	f.StringVar(&bootstrapFlags.realityDest, "reality-dest", bootstrapRealityDest, "REALITY 回落的目标")
	f.StringVar(&bootstrapFlags.subPrefix, "sub-prefix", "", "订阅地址前缀,默认 http://<address>:<api-port>")
	f.StringVar(&bootstrapFlags.remark, "remark", "", "客户端看到的节点名,默认用节点名")
	f.StringVar(&bootstrapFlags.envFile, "env-file", bootstrapEnvFile, "控制面的环境文件(里面有 MPX_TOKEN)")
	f.StringVar(&bootstrapFlags.keyFile, "key-file", bootstrapKeyFile, "控制面公钥的位置")
}

func runBootstrap() error {
	address := strings.TrimSpace(bootstrapFlags.address)
	if address == "" {
		return errors.New("必须给 --address:这台机器对外可达的地址(客户端订阅里写的就是它)")
	}
	token, err := controlPlaneToken()
	if err != nil {
		return err
	}
	c := &Client{
		server: strings.TrimSuffix(firstNonEmpty(opts.server, bootstrapLocalURL), "/"),
		token:  token,
		http:   &http.Client{},
	}

	step("把控制面的公钥装进本机 sshd")
	if err := trustControlPlaneKey(); err != nil {
		return err
	}

	step("注册节点")
	nodeID, nodeName, err := ensureNode(c, address)
	if err != nil {
		return err
	}

	step("建入站")
	inboundID, err := ensureInbound(c, nodeID)
	if err != nil {
		return err
	}

	step("建对外入口")
	if err := ensureHost(c, inboundID, nodeName); err != nil {
		return err
	}

	step("设置订阅前缀")
	if err := ensureSubPrefix(c, address); err != nil {
		return err
	}

	step("建用户")
	if err := ensureUser(c); err != nil {
		return err
	}

	// 装在最后:InstallNode 装完会顺手下发一次配置,那时入站和用户都已经在库里,
	// 新机器跑完这一条就直接有得用,不用再补一次 node sync。
	step("在节点上装 xray 核心并下发配置(要下载几十兆,慢的话几分钟)")
	installPath := "/api/nodes/" + strconv.FormatInt(nodeID, 10) + "/install"
	if _, err := c.doWithin(installTimeout, "POST", installPath, nil); err != nil {
		return err
	}

	links, err := userLinks(c, bootstrapFlags.username)
	if err != nil {
		return err
	}
	return printBootstrapLinks(links)
}

// step 把进度写到 stderr:最后 stdout 上留给订阅链接,好让调用方(或管道)直接
// 拿到那份表格,不被进度行搅在一起。
func step(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "==> "+format+"\n", a...)
}

// controlPlaneToken 按 --token > MPX_TOKEN > 环境文件 的顺序找出管理 token。
//
// 环境文件排在最后是它的常态:install.sh 把 token 写在 /etc/mpx/env(0600),
// bootstrap 以 root 跑,读得到,而它从头到尾没进过命令行。
func controlPlaneToken() (string, error) {
	if opts.token != "" {
		return opts.token, nil
	}
	if t := os.Getenv("MPX_TOKEN"); t != "" {
		return t, nil
	}
	data, err := os.ReadFile(bootstrapFlags.envFile)
	if err != nil {
		return "", fmt.Errorf("读 %s 失败(控制面装好了吗?): %w", bootstrapFlags.envFile, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "MPX_TOKEN="); ok {
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if v != "" {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("%s 里没有 MPX_TOKEN", bootstrapFlags.envFile)
}

// trustControlPlaneKey 把控制面那把公钥追加进本机 sshd 的 authorized_keys。
//
// 单机全包时「节点」就是这台机器,所以控制面要 SSH 的对象是自己。install.sh 已经
// 生成好了那对密钥(在数据目录里,和 systemd 的 StateDirectory 对齐),差的只是把
// 公钥摆进 sshd 认的位置 —— 这一步本来是安装提示里让人手抄一行的活,一键路径里
// 不该留手工步骤。
//
// 公钥按整行去重再追加:重复跑不会让文件长出一串一样的行。
func trustControlPlaneKey() error {
	raw, err := os.ReadFile(bootstrapFlags.keyFile)
	if err != nil {
		return fmt.Errorf("读控制面公钥 %s 失败(先用 deploy/install.sh 装控制面): %w", bootstrapFlags.keyFile, err)
	}
	pk := strings.TrimSpace(string(raw))
	if pk == "" {
		return fmt.Errorf("控制面公钥 %s 是空的", bootstrapFlags.keyFile)
	}

	u, err := user.Lookup(bootstrapFlags.sshUser)
	if err != nil {
		return fmt.Errorf("找不到用户 %q: %w", bootstrapFlags.sshUser, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("用户 %q 的 uid 不是数字: %w", bootstrapFlags.sshUser, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("用户 %q 的 gid 不是数字: %w", bootstrapFlags.sshUser, err)
	}

	dir := filepath.Join(u.HomeDir, ".ssh")
	// sshd 对权限很挑:.ssh 要 0700、authorized_keys 要 0600,差一点就静默不收
	// 这把钥匙(认证失败,但日志里只说「没有可用密钥」)。
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("建 %s: %w", dir, err)
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		return fmt.Errorf("改 %s 属主: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("改 %s 权限: %w", dir, err)
	}

	ak := filepath.Join(dir, "authorized_keys")
	existing, err := os.ReadFile(ak)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读 %s: %w", ak, err)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pk {
			return nil // 已经在里面了
		}
	}
	f, err := os.OpenFile(ak, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", ak, err)
	}
	defer f.Close()
	if _, err := f.WriteString(pk + "\n"); err != nil {
		return fmt.Errorf("写 %s: %w", ak, err)
	}
	// O_CREATE 建出来的文件属主是当前进程(root),但 sshd 用的是目标用户自己的
	// 家目录,文件属主不对一样会被拒。权限同理:umask 可能是 022。
	if err := f.Chown(uid, gid); err != nil {
		return fmt.Errorf("改 %s 属主: %w", ak, err)
	}
	if err := f.Chmod(0o600); err != nil {
		return fmt.Errorf("改 %s 权限: %w", ak, err)
	}
	return nil
}

// ensureNode 按名字找节点,没有就建一个。
//
// 不带 ssh_password:公钥上一步已经装好了,而且 bootstrap 跑在控制面本机,
// 没必要再走一次「用密码登进去装公钥」—— 那一步是给远程节点准备的。
func ensureNode(c *Client, address string) (int64, string, error) {
	name := bootstrapFlags.name
	raw, err := c.do("GET", "/api/nodes", nil)
	if err != nil {
		return 0, "", err
	}
	nodes, err := decode[[]model.Node](raw)
	if err != nil {
		return 0, "", err
	}
	for _, n := range nodes {
		if n.Name == name {
			note("节点 %s 已经在了(id=%d),复用它", name, n.ID)
			return n.ID, n.Name, nil
		}
	}

	n := model.Node{
		Name:    name,
		Address: address,
		SSHPort: bootstrapFlags.sshPort,
		SSHUser: bootstrapFlags.sshUser,
	}
	body := struct {
		model.Node
		SSHPassword string `json:"ssh_password,omitempty"`
	}{Node: n}
	raw, err = c.do("POST", "/api/nodes", body)
	if err != nil {
		return 0, "", err
	}
	created, err := decode[createdNode](raw)
	if err != nil {
		return 0, "", err
	}
	note("节点 %s 建好了(id=%d)", created.Name, created.ID)
	if created.Display != "" {
		note("显示名:%s(按 IP 自动识别)", created.Display)
	}
	if created.LocateNote != "" {
		note("%s", created.LocateNote)
	}
	return created.ID, created.Name, nil
}

// ensureInbound 找一个同协议同端口的入站,没有就建一个。
//
// 入站的密钥对由服务端生成(见 ensureReality),请求体里不用给 —— 这也是为什么
// 这里只填「描述这个入口长什么样」的那几个字段。
func ensureInbound(c *Client, nodeID int64) (int64, error) {
	port := bootstrapFlags.port
	raw, err := c.do("GET", "/api/inbounds?node_id="+strconv.FormatInt(nodeID, 10), nil)
	if err != nil {
		return 0, err
	}
	inbounds, err := decode[[]model.Inbound](raw)
	if err != nil {
		return 0, err
	}
	for _, in := range inbounds {
		if in.Protocol == model.ProtocolVLESS && in.Security == model.SecurityReality && in.Port == port {
			note("入站 %s 已经在了(id=%d),复用它", in.Tag, in.ID)
			return in.ID, nil
		}
	}

	in := model.Inbound{
		NodeID:      nodeID,
		Protocol:    model.ProtocolVLESS,
		Port:        port,
		Network:     model.NetworkTCP,
		Security:    model.SecurityReality,
		SNI:         bootstrapFlags.sni,
		RealityDest: bootstrapFlags.realityDest,
		Flow:        bootstrapFlow,
	}
	raw, err = c.do("POST", "/api/inbounds", in)
	if err != nil {
		return 0, err
	}
	created, err := decode[model.Inbound](raw)
	if err != nil {
		return 0, err
	}
	note("入站 %s 建好了(id=%d)", created.Tag, created.ID)
	return created.ID, nil
}

// ensureHost 给入站建一个对外入口(客户端看到的节点名就是它)。
//
// 一个入站可以挂多个入口(同一台机器走不同域名/端口),一键路径只需要一个。
// 已经有入口就不再加 —— 再加一个会让订阅里出现两个一模一样的节点。
func ensureHost(c *Client, inboundID int64, nodeName string) error {
	path := "/api/inbounds/" + strconv.FormatInt(inboundID, 10) + "/hosts"
	raw, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	// 列表接口给的是 HostView(多带一份算好的生效值),解成 Host 也能对上 ——
	// 这里只关心「有没有入口」。
	hosts, err := decode[[]model.HostView](raw)
	if err != nil {
		return err
	}
	if len(hosts) > 0 {
		note("入站 %d 已经有 %d 个入口了,不再加", inboundID, len(hosts))
		return nil
	}
	// 入口上留空 = 回落到节点地址与入站端口,正是单机全包想要的。
	h := model.Host{Remark: firstNonEmpty(bootstrapFlags.remark, nodeName)}
	raw, err = c.do("POST", path, h)
	if err != nil {
		return err
	}
	created, err := decode[model.Host](raw)
	if err != nil {
		return err
	}
	note("入口 %q 建好了(id=%d)", created.Remark, created.ID)
	return nil
}

// ensureSubPrefix 把订阅前缀设成这台机器的公网地址。
//
// 不设的话订阅链接会按请求的 Host 现算 —— 只有在浏览器里直接打开控制面时才凑巧
// 对,把链接复制到客户端里就指向了错误的地方。设过别的值就尊重它,不覆盖。
func ensureSubPrefix(c *Client, address string) error {
	raw, err := c.do("GET", "/api/settings", nil)
	if err != nil {
		return err
	}
	s, err := decode[model.Settings](raw)
	if err != nil {
		return err
	}
	if s.SubURLPrefix != "" && bootstrapFlags.subPrefix == "" {
		note("订阅前缀已经是 %s,不动它", s.SubURLPrefix)
		return nil
	}
	prefix := firstNonEmpty(bootstrapFlags.subPrefix, fmt.Sprintf("http://%s:%d", address, bootstrapFlags.apiPort))
	if s.SubURLPrefix == prefix {
		return nil
	}
	s.SubURLPrefix = prefix
	// PUT 是整体替换,所以必须把 GET 回来的整份原样送回去,否则规则和自定义
	// 模板会被这一下清空。
	if _, err := c.do("PUT", "/api/settings", s); err != nil {
		return err
	}
	note("订阅前缀设成 %s", prefix)
	return nil
}

// createUserBody 是建用户的请求体。
//
// 不能直接发 model.User:它的 Enabled 是裸 bool,序列化时总会带上 "enabled",
// 而服务端把「显式说了 false」和「没说」分得很清楚(见 server.userRequest)。
// 发一个零值的 model.User 会被理解成「建一个停用的用户」,而它看起来一切正常
// —— 订阅链接能拿到,打开却写着「订阅已停用」。这里把 Enabled 显式说成 true。
type createUserBody struct {
	model.User
	Enabled bool `json:"enabled"`
}

// ensureUser 建用户,已经有了就跳过。
//
// 「已经有了」靠 404 判断,不靠先 GET 列表再找:列表接口分页,靠它判断存在性
// 迟早会在用户多起来之后悄悄失灵。
func ensureUser(c *Client) error {
	username := bootstrapFlags.username
	_, err := c.do("GET", userPath(username), nil)
	if err == nil {
		note("用户 %s 已经在了,复用它", username)
		return nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		return err
	}
	body := createUserBody{User: model.User{Username: username}, Enabled: true}
	raw, err := c.do("POST", "/api/users", body)
	if err != nil {
		return err
	}
	created, err := decode[userRow](raw)
	if err != nil {
		return err
	}
	if !created.Enabled {
		return fmt.Errorf("用户 %s 建出来是停用的,这不正常(检查服务端的 enabled 处理)", username)
	}
	note("用户 %s 建好了(id=%d)", created.Username, created.ID)
	return nil
}

// userLinks 取某个用户的全部订阅地址。
func userLinks(c *Client, username string) (map[string]string, error) {
	raw, err := c.do("GET", userPath(username)+"/links", nil)
	if err != nil {
		return nil, err
	}
	return decode[map[string]string](raw)
}

// printBootstrapLinks 把订阅地址摆成人能读的样子。
//
// 用 auto 那一行当主推:客户端里只该填这一个地址,剩下的格式由控制面按
// User-Agent 自己选。
func printBootstrapLinks(links map[string]string) error {
	if opts.output == outputJSON {
		return emitJSONValue(links)
	}
	rows := make([][]string, 0, len(links))
	for _, k := range linkOrder {
		if v, ok := links[k]; ok {
			rows = append(rows, []string{k, v, linkNote(k)})
		}
	}
	fmt.Println()
	fmt.Println("搞定。把下面这条填进客户端(clash / sing-box / v2rayN 都认它):")
	fmt.Println()
	fmt.Println("  " + links["auto"])
	fmt.Println()
	if err := writeTable([]string{"格式", "订阅地址", "说明"}, rows); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("要再拿一次就跑:mpx user links " + bootstrapFlags.username)
	return nil
}

// linkNote 给每种订阅格式补一句「它是给谁用的」。
func linkNote(format string) string {
	switch format {
	case "auto":
		return "按 User-Agent 自动选,推荐填这个"
	case "links":
		return "分享链接(vless:// 等),手动导入用"
	case "base64-links":
		return "base64 过的分享链接"
	case "clash", "clash-meta":
		return "Clash / mihomo"
	case "sing-box":
		return "sing-box"
	case "xray":
		return "xray 客户端配置"
	}
	return ""
}

// note 打一行缩进的补充说明,跟在 step 后面。
func note(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "    "+format+"\n", a...)
}
