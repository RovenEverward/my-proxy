package cli

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/RovenEverward/my-proxy/internal/pin"
)

// defaultControlPort 是地址里没写端口时用的端口:mpx init 装出来的控制面就在这。
const defaultControlPort = 8080

// defaultAdminUser 是 mpx init 与窗口向导建管理员账号时用的默认用户名。
// mpx connect 的 --username 默认值与它一致:单机只有一个账号,不让每台设备
// 记一个可能拼错的用户名。
const defaultAdminUser = "admin"

var connectFlags struct {
	username      string
	password      string
	passwordStdin bool
	sshTarget     string
	sshCmd        string
}

// sshTargetAuto 是 --ssh 不带值时的占位:目标取 connect 地址的主机名。
const sshTargetAuto = "auto"

var connectCmd = &cobra.Command{
	Use:   "connect <地址>",
	Short: "用管理员账号登录控制面,把这枚会话 token 存进本机",
	Long: `登录控制面:核对控制面的证书指纹,输入管理员用户名与密码,换一枚 CLI 会话
token,连同地址与指纹保存在本机(--profile 指定名称,默认 default)。之后的命令
都走 HTTPS,并且只认这个指纹 —— 和 SSH 的 known_hosts 一个道理。

地址写 IP 或域名即可,端口默认 8080。--fp 给了就只认它(mpx init 打印的那一行
就带着);没给时会先把对方的指纹摆出来,要你输入 yes 确认。

账号是 mpx init 时建的管理员账号(默认用户名 admin);忘了密码的话,登录控制面
所在的服务器用 MPX_TOKEN 跑 mpx admin password 重设(见 mpx admin password
--help 的救援说明)。

不要密码也不要凭据的走 --ssh:目标是 ssh 的目标串(~/.ssh/config 的别名或
user@host,可写成第二个位置参数或 --ssh=值)。之后的每条 API 请求都装进 ssh
隧道发到控制面本机执行 —— SSH 本身就是认证,本机不存 token,不会过期,
指纹和密码也都不问。`,
	Example: `  mpx connect 203.0.113.10 --fp SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg
  mpx connect 203.0.113.10:8080 --username ops --profile work
  echo 'correct horse battery' | mpx connect 203.0.113.10 --fp SHA256:... --password-stdin
  mpx connect mpx.example.net --ssh la-1          # 别名,~/.ssh/config 里那条
  mpx connect mpx.example.net --ssh root@203.0.113.10
  mpx connect mpx.example.net --ssh               # 目标取连接地址的主机名`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		server, hostport, err := controlURL(args[0])
		if err != nil {
			return err
		}
		// 第二个位置参数等价 --ssh=<目标>:写起来更像 ssh 本身的用法。
		// `--ssh la-1` 这种写法 cobra 会把 la-1 留在 args 里(flag 是 auto 占位),
		// 一样按目标接 —— 只有 --ssh=显式值 与位置参数同时出现时才算给了两次。
		sshGiven := cmd.Flags().Changed("ssh") || len(args) == 2
		target := connectFlags.sshTarget
		if len(args) == 2 {
			if cmd.Flags().Changed("ssh") && target != "" && target != sshTargetAuto {
				return errors.New("SSH 目标给了两次:位置参数与 --ssh=<值> 留一个")
			}
			target = args[1]
		}
		// UC-29:SSH 通道模式下信任由 ssh 自身承担(host key + 密钥),TLS
		// 指纹与密码都不参与 —— 不询问、不探测,直连验证通道。
		if sshGiven {
			if target == "" || target == sshTargetAuto {
				target = sshTargetFromServer(server)
			}
			return connectViaSSH(cmd.Context(), server, target, connectFlags.sshCmd)
		}
		trust, err := decideTrust(hostport)
		if err != nil {
			return err
		}
		user := strings.TrimSpace(connectFlags.username)
		if user == "" {
			user = defaultAdminUser
		}
		var pw string
		switch {
		case connectFlags.password != "":
			pw = connectFlags.password
		case connectFlags.passwordStdin:
			if pw, err = readLine(os.Stdin); err != nil {
				return err
			}
		default:
			if pw, err = promptPassword(fmt.Sprintf("%s 的管理员密码: ", user)); err != nil {
				return err
			}
		}
		_, err = connectAndSave(server, trust, user, pw)
		return err
	},
}

// controlURL 把用户给的地址整理成 https://host:port。接受 "1.2.3.4"、
// "1.2.3.4:8080"、"[2001:db8::1]:8080"、"https://host:port";http:// 不接受 ——
// 账号密码不走明文。
func controlURL(arg string) (server, hostport string, err error) {
	s := strings.TrimSpace(arg)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", "", fmt.Errorf("地址 %q 看不懂", arg)
		}
		if u.Scheme != "https" {
			return "", "", errors.New("接入只走 HTTPS;直接写 IP 或域名即可,例如 mpx connect 203.0.113.10")
		}
		s = u.Host
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = strings.Trim(s, "[]"), strconv.Itoa(defaultControlPort)
	}
	if host == "" {
		return "", "", fmt.Errorf("地址 %q 里没有主机", arg)
	}
	hostport = net.JoinHostPort(host, port)
	return "https://" + hostport, hostport, nil
}

// trustDecision 是接入时认定的证书校验方式:指纹钉(UC-24 的自签身份)
// 或公共 CA(mpx acme 签下的受信证书)。两者互斥:ca 为 true 时 fp 必为空。
type trustDecision struct {
	fp string // 指纹钉;空串表示不钉
	ca bool   // 用系统 CA 链校验(带 SNI,服务端会出 ACME 证书)
}

// decideTrust 定下这次接入怎么认证书。
//
// 顺序:--fp 给了就只认它(连域名时也一样 —— 钉的是自签证书,SNI 无指纹,
// 服务端回自签那张,校验照常过);没给且主机是域名时,先按公共 CA 试一次:
// 受信就走 CA 模式(之后命令都发 SNI),不受信(还是自签)才落回指纹流程。
// 主机是 IP 字面量时跳过 CA 探测 —— 公网 CA 不给裸 IP 发证书,试了也是白试。
func decideTrust(hostport string) (trustDecision, error) {
	if opts.fingerprint != "" {
		fp, err := pin.Normalize(opts.fingerprint)
		return trustDecision{fp: fp}, err
	}
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if net.ParseIP(host) == nil {
		ca, err := probeCA(hostport)
		if err != nil {
			return trustDecision{}, err
		}
		if ca {
			return trustDecision{ca: true}, nil
		}
	}
	fp, err := confirmFingerprint(hostport)
	return trustDecision{fp: fp}, err
}

// probeCA 报告这台控制面对 host 这个名字出的证书是否被系统信任。
// 它带着 SNI 拨一次 TLS:受信(ACME 已配好)→ true;证书不被信任
// (还是自签、或签给了别的名字)→ false,让调用方落回指纹流程;
// 拨不通/超时 → error,接入失败(这和指纹流程拨不通一个待遇)。
func probeCA(hostport string) (bool, error) {
	return probeCAWithRoots(hostport, nil)
}

// probeCAWithRoots 是 probeCA 的可注入版:roots 为 nil 用系统根池(测试
// 传入自建的池,验证「测试 CA 签的证书 → CA 模式」这条路)。
func probeCAWithRoots(hostport string, roots *x509.CertPool) (bool, error) {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	d := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", hostport, &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	})
	if err != nil {
		var ue x509.UnknownAuthorityError
		var he x509.HostnameError
		var ce x509.CertificateInvalidError
		if errors.As(err, &ue) || errors.As(err, &he) || errors.As(err, &ce) {
			return false, nil
		}
		// 不是校验问题的错(拨不通、超时、TLS 握手被断):当连接失败处理。
		return false, fmt.Errorf("连不上控制面 %s: %w", hostport, err)
	}
	conn.Close()
	return true, nil
}

// confirmFingerprint 定下要认的指纹:给了 --fp 就用它(连接时校验);没给就先
// 取回对方的指纹摆给人看,输入 yes 才继续。
func confirmFingerprint(hostport string) (string, error) {
	if opts.fingerprint != "" {
		return pin.Normalize(opts.fingerprint)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fp, err := pin.Probe(ctx, hostport)
	if err != nil {
		return "", fmt.Errorf("连不上控制面 %s: %w", hostport, err)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("控制面 %s 的证书指纹是 %s;非交互时要用 --fp 明确给出指纹", hostport, fp)
	}
	fmt.Fprintf(os.Stderr, "控制面 %s 的证书指纹:\n  %s\n", hostport, fp)
	fmt.Fprintln(os.Stderr, "跟 mpx init 打印的那一行(或已接入设备上 mpx status 里的「证书指纹」)对一下。")
	fmt.Fprint(os.Stderr, "确认是你的控制面就输入 yes:")
	ans, err := readLine(os.Stdin)
	if err != nil {
		return "", err
	}
	if strings.ToLower(strings.TrimSpace(ans)) != "yes" {
		return "", errors.New("没有确认指纹,没接入")
	}
	return fp, nil
}

// loginResult 是 POST /api/admin/login 的应答:一枚 CLI 会话 token 与它的
// 到期时刻(会话 30 天固定 TTL,见控制面 admin.go)。
type loginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// trustTransport 按接入时的认定造 HTTP 传输:CA 模式发 SNI、走系统校验;
// 指纹模式照旧 pin.Transport。
func trustTransport(trust trustDecision) *http.Transport {
	if trust.ca {
		return pin.Transport("")
	}
	return pin.Transport(trust.fp)
}

// connectAndSave 用账号密码换 token,存进控制面并切过去。mpx init 收尾时也走它。
// connectViaSSH 是 UC-29 的 SSH 通道接入:经通道发一次 /api/status 验证
// 「目标对 + 控制面对 + 版本对」,然后把 (server, ssh目标, 远端命令) 存进
// profile。不铸会话、不存 token、不问指纹 —— 之后每条 API 请求都走这条通道。
func connectViaSSH(ctx context.Context, server, target, remoteCmd string) error {
	if remoteCmd == "" {
		remoteCmd = sshRelayCmd(apiPortFromServer(server))
	}
	req, err := newRelayRequest(ctx, http.MethodGet, "/api/status", nil)
	if err != nil {
		return err
	}
	resp, err := sshRequest(ctx, target, remoteCmd, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("通道通了,但控制面回了 %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	profile, err := saveProfile(Profile{Server: server, SSH: target, SSHCmd: remoteCmd})
	if err != nil {
		return err
	}
	fmt.Printf("已接入 %s(经 ssh %s,通道认证,不存凭据),保存为控制面「%s」。\n", server, target, profile)
	return nil
}

func connectAndSave(server string, trust trustDecision, username, password string) (*loginResult, error) {
	res, profile, err := connectAndSaveQuiet(server, trust, username, password)
	if err != nil {
		return nil, err
	}
	fmt.Printf("已接入 %s(账号 %s,会话 %s 到期),保存为控制面「%s」。\n",
		server, username, res.ExpiresAt.Local().Format("2006-01-02 15:04"), profile)
	return &res, nil
}

// connectAndSaveQuiet 是 connectAndSave 的安静版:同一件事,但不打印 ——
// 调用方要把结果包装成自己的输出(向导、脚本)时走它。
func connectAndSaveQuiet(server string, trust trustDecision, username, password string) (loginResult, string, error) {
	c := &Client{server: server, http: &http.Client{Transport: trustTransport(trust)}}
	raw, err := postAdminLogin(c, username, password)
	if err != nil {
		return loginResult{}, "", err
	}
	res, err := decode[loginResult](raw)
	if err != nil {
		return loginResult{}, "", err
	}
	profile, err := saveProfile(Profile{Server: server, Token: res.Token, Fingerprint: trust.fp, CA: trust.ca})
	if err != nil {
		return loginResult{}, "", err
	}
	return res, profile, nil
}

// connectAndSaveProfile 是 connectAndSaveQuiet 的带档名版:存进名为 profile 的
// **新**控制面并设为当前。档已存在时报错、不落盘 —— 不能把 default 或别的档里
// 已有的 token 覆盖掉。不经过 saveProfile:那里写死 --profile/default,而且会覆
// 盖同名档。
func connectAndSaveProfile(server string, trust trustDecision, username, password, profile string) (loginResult, error) {
	c := &Client{server: server, http: &http.Client{Transport: trustTransport(trust)}}
	raw, err := postAdminLogin(c, username, password)
	if err != nil {
		return loginResult{}, err
	}
	res, err := decode[loginResult](raw)
	if err != nil {
		return loginResult{}, err
	}
	cfg, err := loadConfig()
	if err != nil {
		return loginResult{}, err
	}
	if _, ok := cfg.Profiles[profile]; ok {
		return loginResult{}, fmt.Errorf("控制面 %s 已存在,请换一个名字", profile)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	cfg.Profiles[profile] = Profile{Server: server, Token: res.Token, Fingerprint: trust.fp, CA: trust.ca}
	cfg.Current = profile
	if err := saveConfig(cfg); err != nil {
		return loginResult{}, err
	}
	return res, nil
}

// saveProfile 把 p 写进 --profile 指的控制面(默认 default)并设为当前。
//
// SSH 通道信息按「同地址保留、换地址丢弃」处理:profile 已记了 SSH 目标而新
// 写入没带,地址不变就保住通道,地址变了旧目标八成也指错机器,不带更干净。
// 反过来:新写入带了 SSH 目标时清掉 token/指纹字段 —— 通道模式不存凭据,
// 留着旧 token 只会让人误以为它还在用。
func saveProfile(p Profile) (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", err
	}
	name := firstNonEmpty(opts.profile, defaultProfile)
	if old, ok := cfg.Profiles[name]; ok && old.Server != "" && old.Server != p.Server {
		fmt.Printf("控制面 %s 的地址:%s → %s\n", name, old.Server, p.Server)
	}
	if old, ok := cfg.Profiles[name]; ok && p.SSH == "" && old.SSH != "" &&
		strings.TrimSuffix(old.Server, "/") == strings.TrimSuffix(p.Server, "/") {
		p.SSH, p.SSHCmd = old.SSH, old.SSHCmd
	}
	if p.SSH != "" {
		p.Token, p.Fingerprint, p.CA = "", "", false
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	cfg.Profiles[name] = p
	cfg.Current = name
	return name, saveConfig(cfg)
}

// readLine 读一行并去掉行尾换行。
func readLine(f *os.File) (string, error) {
	s, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && s == "" {
		return "", fmt.Errorf("从 stdin 读: %w", err)
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func init() {
	connectCmd.Flags().StringVar(&connectFlags.username, "username", "", "管理员用户名,默认 admin(mpx init 建的那个账号)")
	connectCmd.Flags().StringVar(&connectFlags.password, "password", "", "管理员密码(脚本用;不给则交互输入)")
	connectCmd.Flags().BoolVar(&connectFlags.passwordStdin, "password-stdin", false, "从 stdin 读管理员密码(脚本用)")
	connectCmd.Flags().StringVar(&connectFlags.sshTarget, "ssh", "", "用 SSH 通道接入:目标是 ssh 的目标串(user@host、~/.ssh/config 别名;可带 -p 等参数)。不带值时取连接地址的主机名。每条 API 请求走 ssh 隧道,不存凭据不问密码")
	connectCmd.Flags().Lookup("ssh").NoOptDefVal = sshTargetAuto
	connectCmd.Flags().StringVar(&connectFlags.sshCmd, "ssh-cmd", "", "覆盖默认的远端通道命令 api-relay(非默认安装路径/端口时用)")
	rootCmd.AddCommand(connectCmd)
}

// postAdminLogin 拿账号密码换会话 token(POST /api/admin/login,kind=cli)。
// 只发一次:撞上限流或锁定(429)时等下去没有意义 —— 锁定要等 15 分钟,人在
// 终端前干等 30 秒只会以为卡住了;直接把原因和该等多久说出来。口令不对与锁定
// 的说法在 loginError 里补全。
func postAdminLogin(c *Client, username, password string) ([]byte, error) {
	payload, err := json.Marshal(map[string]string{
		"username": username, "password": password, "kind": "cli",
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	raw, _, err := c.doOnce(ctx, http.MethodPost, "/api/admin/login", payload, true)
	if err != nil {
		return nil, loginError(err, time.Now())
	}
	return raw, nil
}

// loginError 把登录失败翻成说得清楚的话:
//   - 用户名或密码不对(401):告诉人忘了密码怎么救援;
//   - 已锁定(带 locked_until):锁到本机几点、还要等多久,已有会话不受影响;
//   - 还没账号(403):指路去建号。
//
// Message 是命令行的说法,UIHint 是窗口的说法(不提命令)。锁定时间按本机时区
// 换算 —— 控制面不知道使用者在哪个时区,所以由客户端来算。
func loginError(err error, now time.Time) error {
	var ae *APIError
	if !errors.As(err, &ae) {
		return err
	}
	var body struct {
		LockedUntil string `json:"locked_until"`
	}
	_ = json.Unmarshal(ae.Body, &body)
	if until, perr := time.Parse(time.RFC3339, body.LockedUntil); perr == nil && until.After(now) {
		when := until.Local().Format("15:04")
		wait := loginWaitText(until.Sub(now))
		head := "用户名或密码连续输错太多次,登录已锁定,到 " + when + "(本机时间)解锁,还要等约 " + wait + "。已登录的会话不受影响。"
		uiHead := "用户名或密码连续输错太多次，登录已锁定，到 " + when + "（本机时间）解锁，还要等约 " + wait + "。已登录的会话不受影响。"
		return &APIError{StatusCode: ae.StatusCode, Body: ae.Body,
			Message: head + "忘了密码:在控制面所在的服务器上用 MPX_TOKEN 跑 mpx admin password 重设。",
			UIHint:  uiHead + "忘了密码：需要到控制面所在的服务器上重设管理员密码后再登录。"}
	}
	if ae.StatusCode == http.StatusUnauthorized {
		out := *ae
		out.Message = "用户名或密码不对。忘了密码:登录控制面所在的服务器,用 MPX_TOKEN 跑 mpx admin password 重设(见 mpx admin password --help)。"
		if out.UIHint == "" {
			out.UIHint = "用户名或密码不对。忘了密码：需要到控制面所在的服务器上重设管理员密码后再登录。"
		}
		return &out
	}
	if ae.StatusCode == http.StatusForbidden {
		out := *ae
		if out.UIHint == "" {
			out.UIHint = out.Message
		}
		return &out
	}
	return err
}

// loginWaitText 与控制面同一口径:不到一分钟按秒,其余按分钟向上取整。
func loginWaitText(d time.Duration) string {
	if d < time.Minute {
		sec := int((d + time.Second - 1) / time.Second)
		if sec < 1 {
			sec = 1
		}
		return strconv.Itoa(sec) + " 秒"
	}
	return strconv.Itoa(int((d+time.Minute-1)/time.Minute)) + " 分钟"
}
