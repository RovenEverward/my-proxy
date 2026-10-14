package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
)

// init 是「我有一台空 VPS,想立刻用上订阅」的那条路。
//
// 它只做编排:本机连上那台 VPS,把控制面装好,再让控制面自己把节点、入站、入口、
// 用户建出来。真正的建对象逻辑在远端的 `mpx bootstrap` 里 —— 那是控制面的内部
// 实现,不是给人单独敲的命令。
//
// 为什么不由本机直接调 /api:控制面的 token 在装好之前不存在,装好之后也只在
// VPS 的 /etc/mpx/env 里。把它捞回本机再发回去,等于让凭据多走一趟 SSH 命令行
// (会出现在两端的 ps 与 shell 历史里);让 VPS 上的进程自己读,它一步都不用挪。
//
// 这条命令天然是「控制面存在之前」运行的,所以它不能是 /api 的纯客户端 —— 它必须
// SSH。这是 CLI 边界上唯一一处刻意的例外,理由写在 docs/internal/decisions.md。
const (
	initRepo = "RovenEverward/my-proxy"
	initRef  = "main"

	// 上传到 VPS 的固定落点。用 /tmp 是因为安装脚本自己也是这么找二进制的
	// (参数 → 脚本旁边 → ./mpx → /tmp/mpx),两边的直觉一致。
	initRemoteScript = "/tmp/mpx-install.sh"
	initRemoteBinary = "/tmp/mpx"
)

// 每一步的预算。装控制面要下二进制、下 geodata,给得比一次普通 ssh 宽得多;
// 探路是几条 uname,几十秒足够。
const (
	initProbeTimeout     = 60 * time.Second
	initUploadTimeout    = 20 * time.Minute
	initInstallTimeout   = 25 * time.Minute
	initBootstrapTimeout = 25 * time.Minute
)

var initFlags struct {
	host          string
	sshUser       string
	sshPort       int
	sshPassword   bool
	identity      string
	binary        string
	installScript string
	addr          string
	apiPort       int
	name          string
	username      string
	sni           string
	realityDest   string
	remark        string
	subPrefix     string

	// password 不是 flag:它要么来自 --ssh-password 的交互提示,要么来自 stdin。
	// 刻意不提供「把密码写在命令行上」的选项 —— 那会进 shell 历史,也会出现在
	// 本机的 ps 里。
	password string
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "一条命令在一台新 VPS 上装好控制面、节点与订阅",
	Long: `在一台刚拿到的 VPS 上把整套东西建起来,跑完就有一份能用的订阅。

给地址和登录方式就够了:

  mpx init --host 203.0.113.10 --ssh-user root --ssh-password

它会依次做完这些事:装控制面(systemd 服务,监听 0.0.0.0:8080)、把控制面的公钥
装进这台机器自己的 sshd、注册节点、建一个 VLESS+TCP+REALITY 入站、建对外入口、
把订阅前缀指到本机、建一个用户,最后在节点上装 xray 核心并下发配置,打印订阅链接。

它假设控制面与节点在同一台机器上(单机全包)。要分开部署,按 mpx node add、
mpx inbound add、mpx host add 的 --help 一步步来。

重复跑不会炸:已有的节点、入站、入口、用户都会复用。但订阅前缀一旦设过就不再覆盖
(除非显式给 --sub-prefix),免得把手工配好的域名冲掉。`,
	Example: `  # 最省事的一路:提示输密码,装完把订阅链接打出来
  mpx init --host 203.0.113.10 --ssh-user root --ssh-password

  # 已经能用密钥登进去了(或者给 --identity 指一把私钥)
  mpx init --host 203.0.113.10

  # 自己编的二进制送上去装,不依赖 GitHub Releases
  mpx init --host 203.0.113.10 --binary ./mpx

  # 想直接给出订阅域名(前面挂了 TLS 反代)就用这个
  mpx init --host 203.0.113.10 --sub-prefix https://sub.example.com`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return runInit() },
}

func init() {
	f := initCmd.Flags()
	f.StringVar(&initFlags.host, "host", "", "VPS 的地址或 IP,必填")
	f.StringVar(&initFlags.sshUser, "ssh-user", "root", "SSH 登录 VPS 用的用户(必须是 root)")
	f.IntVar(&initFlags.sshPort, "ssh-port", 22, "SSH 端口")
	f.BoolVar(&initFlags.sshPassword, "ssh-password", false, "提示输入 SSH 密码(装完公钥后就全程用密钥)")
	f.StringVar(&initFlags.identity, "identity", "", "用哪把私钥连 VPS,默认用 ssh 自己的默认身份")
	f.StringVar(&initFlags.binary, "binary", "", "本机编好的 linux mpx 二进制,送上去装(默认从 GitHub Releases 下)")
	f.StringVar(&initFlags.installScript, "install-script", "", "用哪份 install.sh,默认优先用仓库里那份")
	f.StringVar(&initFlags.addr, "addr", "0.0.0.0:8080", "控制面监听地址")
	f.IntVar(&initFlags.apiPort, "api-port", 0, "控制面端口,默认从 --addr 里取")
	f.StringVar(&initFlags.name, "name", "default", "节点名")
	f.StringVar(&initFlags.username, "username", "user1", "要建的用户名")
	f.StringVar(&initFlags.sni, "sni", bootstrapSNI, "REALITY 的 SNI")
	f.StringVar(&initFlags.realityDest, "reality-dest", bootstrapRealityDest, "REALITY 回落的目标")
	f.StringVar(&initFlags.remark, "remark", "", "客户端里看到的节点名,默认用节点名")
	f.StringVar(&initFlags.subPrefix, "sub-prefix", "", "订阅地址前缀,默认 http://<host>:<端口>")
}

func runInit() error {
	host := strings.TrimSpace(initFlags.host)
	if host == "" {
		return errors.New("必须给 --host:那台 VPS 的地址或 IP")
	}
	apiPort, err := initAPIPort()
	if err != nil {
		return err
	}

	n := model.Node{
		Name:    "init",
		Address: host,
		SSHPort: initFlags.sshPort,
		SSHUser: initFlags.sshUser,
	}

	if initFlags.sshPassword {
		pw, err := promptPassword(fmt.Sprintf("请输入 %s@%s 的 SSH 密码", n.SSHUserOrDefault(), host))
		if err != nil {
			return err
		}
		initFlags.password = pw
	}

	auth, err := initAuth(n)
	if err != nil {
		return err
	}

	// ---- 看清对面是什么 -------------------------------------------------------

	step("看清这台机器")
	probeCtx, cancel := context.WithTimeout(context.Background(), initProbeTimeout)
	defer cancel()
	info, err := probeRemote(probeCtx, n, auth)
	if err != nil {
		return err
	}
	if info["os"] != "Linux" {
		return fmt.Errorf("控制面要在 Linux 上跑,这台机器报的是 %s", info["os"])
	}
	if info["uid"] != "0" {
		return fmt.Errorf("要用 root 登录(%s 现在的 uid 是 %s):装 systemd 服务、写 /etc/mpx 都要 root",
			n.SSHUserOrDefault(), info["uid"])
	}
	note("%s %s,root,fetch=%s", info["os"], info["arch"], firstNonEmpty(info["fetch"], "无"))

	// ---- 把安装脚本送上去 -----------------------------------------------------

	step("把安装脚本送上去")
	if err := uploadInstallScript(n, auth, info); err != nil {
		return err
	}

	// ---- 需要的话,把本地编好的二进制也送上去 ---------------------------------

	binArg := ""
	if p := strings.TrimSpace(initFlags.binary); p != "" {
		step("把本机的二进制送上去(可能几分钟,别掐)")

		content, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("读 %s: %w", p, err)
		}
		upCtx, cancel := context.WithTimeout(context.Background(), initUploadTimeout)
		defer cancel()
		if err := node.PutFileAs(upCtx, n, auth, initRemoteBinary, content); err != nil {
			return err
		}
		note("已送达 %s(%s)", initRemoteBinary, humanBytes(int64(len(content))))
		binArg = initRemoteBinary
	}

	// ---- 装控制面 -------------------------------------------------------------

	step("在 VPS 上装控制面(监听 %s)", initFlags.addr)

	install := fmt.Sprintf("bash %s --addr %s", node.ShellQuote(initRemoteScript), node.ShellQuote(initFlags.addr))
	if binArg != "" {
		install += " " + node.ShellQuote(binArg)
	}
	installCtx, cancel := context.WithTimeout(context.Background(), initInstallTimeout)
	defer cancel()
	if err := node.RunAsIO(installCtx, n, auth, install, os.Stdout, os.Stderr); err != nil {
		return err
	}

	// 装到的 mpx 认不认识 bootstrap 子命令 —— 不认的话后面必然失败,不如现在就说
	// 清楚。发布件比这个功能旧的时候就是这样,错误信息得能直接指出该做什么。
	checkCtx, cancel := context.WithTimeout(context.Background(), initProbeTimeout)
	defer cancel()
	out, err := node.RunAs(checkCtx, n, auth,
		"mpx bootstrap --help >/dev/null 2>&1 && echo MPX_BOOTSTRAP_OK || echo MPX_BOOTSTRAP_MISSING")
	if err != nil {
		return err
	}
	if !strings.Contains(out, "MPX_BOOTSTRAP_OK") {
		return errors.New("装上的 mpx 还没有 bootstrap 子命令(多半是发布件比这个功能旧)。" +
			"用 --binary 指一个本机编好的 linux mpx,或等新版发布后再试")
	}

	// ---- 让控制面自己把对象建出来 ---------------------------------------------

	step("建节点、入站、入口、订阅设置与用户")
	bootArgs := []string{
		"mpx", "bootstrap",
		"--address", host,
		"--ssh-user", n.SSHUserOrDefault(),
		"--ssh-port", strconv.Itoa(n.SSHPortOrDefault()),
		"--api-port", strconv.Itoa(apiPort),
		"--name", initFlags.name,
		"--username", initFlags.username,
		"--sni", initFlags.sni,
		"--reality-dest", initFlags.realityDest,
	}
	if initFlags.remark != "" {
		bootArgs = append(bootArgs, "--remark", initFlags.remark)
	}
	if initFlags.subPrefix != "" {
		bootArgs = append(bootArgs, "--sub-prefix", initFlags.subPrefix)
	}

	bootCtx, cancel := context.WithTimeout(context.Background(), initBootstrapTimeout)
	defer cancel()
	if err := node.RunAsIO(bootCtx, n, auth, shellJoin(bootArgs), os.Stdout, os.Stderr); err != nil {
		return err
	}

	// ---- 收尾:告诉本机怎么管它 ----------------------------------------------

	token := ""
	tokenCtx, cancel := context.WithTimeout(context.Background(), initProbeTimeout)
	defer cancel()
	if out, err := node.RunAs(tokenCtx, n, auth, "sed -n 's/^MPX_TOKEN=//p' /etc/mpx/env"); err == nil {
		token = strings.TrimSpace(out)
	}

	fmt.Println()
	fmt.Println("本机想管这台控制面就跑:")
	if token != "" {
		fmt.Printf("  mpx login --server http://%s:%d --token %s\n", host, apiPort, token)
	} else {
		fmt.Printf("  mpx login --server http://%s:%d --token <去 /etc/mpx/env 里拿>\n", host, apiPort)
	}
	fmt.Println()
	fmt.Println("注意:控制面监听着公网地址,走的是明文 HTTP,token 就是唯一的凭据。")
	fmt.Println("自己长期用的话,前面该放一个 TLS 反向代理,再把订阅前缀换成 https 的域名。")
	return nil
}

// initAuth 定下「怎么连这台机器」,必要时先用密码把操作者自己的公钥装上去。
//
// 密码那一路没法给远端喂 stdin(askpass 占着),而上传脚本和二进制都要走 stdin,
// 所以密码只在最开始用一次:装公钥、验一次密钥登录,之后就全是密钥。
func initAuth(n model.Node) (node.Auth, error) {
	if initFlags.password == "" {
		return node.Auth{Identity: initFlags.identity}, nil
	}

	keyPath := strings.TrimSpace(initFlags.identity)
	pubkey, err := node.EnsureSSHKey(keyPath)
	if err != nil {
		return node.Auth{}, err
	}

	step("用密码登一次,把本机公钥装到 VPS 上")
	ctx, cancel := context.WithTimeout(context.Background(), initProbeTimeout)
	defer cancel()
	if err := node.BootstrapNode(ctx, n, keyPath, pubkey, initFlags.password); err != nil {
		return node.Auth{}, err
	}
	note("之后全程用密钥")
	return node.Auth{Identity: keyPath}, nil
}

// probeRemote 问清对面的系统、架构、身份和手边有什么下载工具。
//
// 一次 ssh 问完而不是问四次:每条 ssh 都要握手,而这里要的信息都很便宜。
func probeRemote(ctx context.Context, n model.Node, a node.Auth) (map[string]string, error) {
	script := `printf 'os=%s\n' "$(uname -s)"
printf 'arch=%s\n' "$(uname -m)"
printf 'uid=%s\n' "$(id -u)"
printf 'fetch=%s\n' "$(command -v curl || command -v wget || echo)"
printf 'mpx=%s\n' "$(command -v mpx || echo)"`

	out, err := node.RunAs(ctx, n, a, script)
	if err != nil {
		return nil, err
	}
	info := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			info[k] = v
		}
	}
	return info, nil
}

// uploadInstallScript 把 install.sh 放到 VPS 的固定落点上。
//
// 优先用本机那份:仓库就在手边时没必要让它去 GitHub 绕一圈,而且对连不上
// raw.githubusercontent.com 的机器(国内 VPS 很常见)这是唯一走得通的路。
// 手边没有才让 VPS 自己去取。
func uploadInstallScript(n model.Node, a node.Auth, info map[string]string) error {
	if path, ok := findInstallScript(); ok {
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读 %s: %w", path, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), initUploadTimeout)
		defer cancel()
		if err := node.PutFileAs(ctx, n, a, initRemoteScript, content); err != nil {
			return err
		}
		note("用的是本机的 %s", path)
		return nil
	}

	url := strings.TrimSuffix(firstNonEmpty(os.Getenv("MPX_RAW_BASE"),
		"https://raw.githubusercontent.com/"+initRepo+"/"+initRef), "/") + "/deploy/install.sh"

	fetch := info["fetch"]
	var cmd string
	switch {
	case strings.HasSuffix(fetch, "curl"):
		cmd = fmt.Sprintf("curl -fsSL %s -o %s", node.ShellQuote(url), node.ShellQuote(initRemoteScript))
	case strings.HasSuffix(fetch, "wget"):
		cmd = fmt.Sprintf("wget -qO %s %s", node.ShellQuote(initRemoteScript), node.ShellQuote(url))
	default:
		return errors.New("这台机器上既没有 curl 也没有 wget,没法取安装脚本。" +
			"在仓库目录里跑 mpx init(它会直接上传 deploy/install.sh),或用 --install-script 指一份")
	}

	ctx, cancel := context.WithTimeout(context.Background(), initUploadTimeout)
	defer cancel()
	if _, err := node.RunAs(ctx, n, a, cmd); err != nil {
		return err
	}
	note("从 %s 取的", url)
	return nil
}

// findInstallScript 在几个最可能的位置找仓库里的 deploy/install.sh。
//
// 找的是「二进制旁边」和「当前目录」:从仓库里直接 go run / go build 出来跑的时候,
// 这两种布局覆盖了绝大多数情况。
func findInstallScript() (string, bool) {
	if p := strings.TrimSpace(initFlags.installScript); p != "" {
		return p, true
	}

	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "deploy", "install.sh"),
			filepath.Join(dir, "..", "deploy", "install.sh"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "deploy", "install.sh"))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, true
		}
	}
	return "", false
}

// initAPIPort 定下控制面端口:显式给了就用,否则从 --addr 里读。
//
// 订阅前缀和登录提示都要用它,让两处各写一遍迟早会对不上。
func initAPIPort() (int, error) {
	if initFlags.apiPort > 0 {
		return initFlags.apiPort, nil
	}
	_, port, err := net.SplitHostPort(initFlags.addr)
	if err != nil {
		return 0, fmt.Errorf("--addr 要写成 地址:端口(比如 0.0.0.0:8080):%w", err)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("--addr 里的端口不合法: %q", port)
	}
	return p, nil
}

// shellJoin 把一串参数拼成一条远端命令,每个参数都单独加引号。
func shellJoin(parts []string) string {
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = node.ShellQuote(p)
	}
	return strings.Join(quoted, " ")
}
