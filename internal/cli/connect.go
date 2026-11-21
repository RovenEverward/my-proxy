package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

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
--help 的救援说明)。`,
	Example: `  mpx connect 203.0.113.10 --fp SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg
  mpx connect 203.0.113.10:8080 --username ops --profile work
  echo 'correct horse battery' | mpx connect 203.0.113.10 --fp SHA256:... --password-stdin`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		server, hostport, err := controlURL(args[0])
		if err != nil {
			return err
		}
		fp, err := confirmFingerprint(hostport)
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
		_, err = connectAndSave(server, fp, user, pw)
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

// connectAndSave 用账号密码换 token,存进控制面并切过去。mpx init 收尾时也走它。
func connectAndSave(server, fp, username, password string) (*loginResult, error) {
	res, profile, err := connectAndSaveQuiet(server, fp, username, password)
	if err != nil {
		return nil, err
	}
	fmt.Printf("已接入 %s(账号 %s,会话 %s 到期),保存为控制面「%s」。\n",
		server, username, res.ExpiresAt.Local().Format("2006-01-02 15:04"), profile)
	return &res, nil
}

// connectAndSaveQuiet 是 connectAndSave 的安静版:同一件事,但不打印 —— mpx ui
// 的「接入已有控制面」也走它,提示由页面自己给。
func connectAndSaveQuiet(server, fp, username, password string) (loginResult, string, error) {
	c := &Client{server: server, http: &http.Client{Transport: pin.Transport(fp)}}
	raw, err := postAdminLogin(c, username, password)
	if err != nil {
		return loginResult{}, "", err
	}
	res, err := decode[loginResult](raw)
	if err != nil {
		return loginResult{}, "", err
	}
	profile, err := saveProfile(Profile{Server: server, Token: res.Token, Fingerprint: fp})
	if err != nil {
		return loginResult{}, "", err
	}
	return res, profile, nil
}

// connectAndSaveProfile 是 connectAndSaveQuiet 的带档名版:存进名为 profile 的
// **新**控制面并设为当前。档已存在时报错、不落盘 —— mpx ui 的「添加控制面」用
// 它,不能把 default 或别的档里已有的 token 覆盖掉。不经过 saveProfile:那里写死
// 了 --profile/default,而且会覆盖同名档。
func connectAndSaveProfile(server, fp, username, password, profile string) (loginResult, error) {
	c := &Client{server: server, http: &http.Client{Transport: pin.Transport(fp)}}
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
	cfg.Profiles[profile] = Profile{Server: server, Token: res.Token, Fingerprint: fp}
	cfg.Current = profile
	if err := saveConfig(cfg); err != nil {
		return loginResult{}, err
	}
	return res, nil
}

// saveProfile 把 p 写进 --profile 指的控制面(默认 default)并设为当前。
func saveProfile(p Profile) (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", err
	}
	name := firstNonEmpty(opts.profile, defaultProfile)
	if old, ok := cfg.Profiles[name]; ok && old.Server != "" && old.Server != p.Server {
		fmt.Printf("控制面 %s 的地址:%s → %s\n", name, old.Server, p.Server)
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
