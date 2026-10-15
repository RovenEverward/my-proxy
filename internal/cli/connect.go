package cli

import (
	"bufio"
	"context"
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

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/pin"
)

// defaultControlPort 是地址里没写端口时用的端口:mpx init 装出来的控制面就在这。
const defaultControlPort = 8080

var connectFlags struct {
	name          string
	passwordStdin bool
	upgrade       bool
}

var connectCmd = &cobra.Command{
	Use:   "connect <地址>",
	Short: "用设备密码把这台设备接入控制面(每台设备一枚自己的 token)",
	Long: `把这台设备接入控制面:核对控制面的证书指纹,输入设备密码,换一枚这台设备
专属的 token,连同地址与指纹存进配置档(--profile,默认 default)。之后的命令
都走 HTTPS,并且只认这个指纹 —— 和 SSH 的 known_hosts 一个道理。

地址写 IP 或域名即可,端口默认 8080。--fp 给了就只认它(mpx init 打印的那一行
就带着);没给时会先把对方的指纹摆出来,要你输入 yes 确认。

设备密码是 mpx init 时设的;忘了的话在任何一台已接入的设备上 mpx device
password 重设。

--upgrade 用于老版本装的控制面:把当前配置档里的 http:// 地址换成 https:// 并
记下指纹,沿用原来的 token,不用密码。`,
	Example: `  mpx connect 203.0.113.10 --fp SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg
  mpx connect 203.0.113.10:8080 --name office-pc --profile work
  mpx connect --upgrade`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if connectFlags.upgrade {
			if len(args) > 0 {
				return errors.New("--upgrade 不带地址:它改的是当前配置档里那个控制面")
			}
			return runConnectUpgrade()
		}
		if len(args) != 1 {
			return errors.New("要给控制面地址,例如 mpx connect 203.0.113.10")
		}
		server, hostport, err := controlURL(args[0])
		if err != nil {
			return err
		}
		fp, err := confirmFingerprint(hostport)
		if err != nil {
			return err
		}
		var pw string
		if connectFlags.passwordStdin {
			pw, err = readLine(os.Stdin)
		} else {
			pw, err = promptPassword("设备密码: ")
		}
		if err != nil {
			return err
		}
		name := strings.TrimSpace(connectFlags.name)
		if name == "" {
			name = defaultDeviceName()
		}
		_, err = connectAndSave(server, fp, pw, name)
		return err
	},
}

// controlURL 把用户给的地址整理成 https://host:port。接受 "1.2.3.4"、
// "1.2.3.4:8080"、"[2001:db8::1]:8080"、"https://host:port";http:// 不接受 ——
// 设备密码不走明文。
func controlURL(arg string) (server, hostport string, err error) {
	s := strings.TrimSpace(arg)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", "", fmt.Errorf("地址 %q 看不懂", arg)
		}
		if u.Scheme != "https" {
			return "", "", errors.New("设备接入只走 HTTPS;直接写 IP 或域名即可,例如 mpx connect 203.0.113.10")
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

// connectAndSave 拿设备密码换 token,存进配置档并切过去。mpx init 收尾时也走它。
func connectAndSave(server, fp, password, name string) (*connectResult, error) {
	c := &Client{server: server, http: &http.Client{Transport: pin.Transport(fp)}}
	raw, err := c.do("POST", "/api/connect", map[string]string{"password": password, "device_name": name})
	if err != nil {
		return nil, err
	}
	res, err := decode[connectResult](raw)
	if err != nil {
		return nil, err
	}
	profile, err := saveProfile(Profile{Server: server, Token: res.Token, Fingerprint: fp})
	if err != nil {
		return nil, err
	}
	fmt.Printf("已接入 %s:本设备「%s」(设备 %d),存在配置档 %s。\n", server, res.Name, res.DeviceID, profile)
	return &res, nil
}

type connectResult struct {
	DeviceID int64  `json:"device_id"`
	Name     string `json:"name"`
	Token    string `json:"token"`
}

// saveProfile 把 p 写进 --profile 指的配置档(默认 default)并设为当前。
func saveProfile(p Profile) (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", err
	}
	name := firstNonEmpty(opts.profile, defaultProfile)
	if old, ok := cfg.Profiles[name]; ok && old.Server != "" && old.Server != p.Server {
		fmt.Printf("配置档 %s 的地址:%s → %s\n", name, old.Server, p.Server)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	cfg.Profiles[name] = p
	cfg.Current = name
	return name, saveConfig(cfg)
}

// runConnectUpgrade 把老配置档(http:// + 管理 token)换成 https:// + 指纹。
//
// 指纹是经旧的明文通道读回来的 —— 和此前每条命令走明文的风险相同,而且只发生
// 这一次;之后的每次连接都按指纹核对。
func runConnectUpgrade() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	name := firstNonEmpty(opts.profile, os.Getenv("MPX_PROFILE"), cfg.Current, defaultProfile)
	p, ok := cfg.Profiles[name]
	if !ok || p.Server == "" {
		return fmt.Errorf("配置档 %s 不存在或没有地址;新设备直接 mpx connect <地址>", name)
	}
	u, err := url.Parse(p.Server)
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		return fmt.Errorf("配置档 %s 已经是 https 地址,不用升级", name)
	}
	old := &Client{server: strings.TrimSuffix(p.Server, "/"), token: p.Token, http: &http.Client{}}
	raw, err := old.do("GET", "/api/status", nil)
	if err != nil {
		return err
	}
	st, err := decode[model.ControlStatus](raw)
	if err != nil {
		return err
	}
	if st.TLSFingerprint == "" {
		return errors.New("控制面没报证书指纹:它还是老版本,先升级控制面")
	}
	u.Scheme = "https"
	server := strings.TrimSuffix(u.String(), "/")
	// 换上之后马上按指纹走一次,确认真能连上再落盘。
	check := &Client{server: server, token: p.Token, http: &http.Client{Transport: pin.Transport(st.TLSFingerprint)}}
	if _, err := check.do("GET", "/api/status", nil); err != nil {
		return fmt.Errorf("换成 %s 后连不上: %w", server, err)
	}
	p.Server, p.Fingerprint = server, st.TLSFingerprint
	cfg.Profiles[name] = p
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("配置档 %s 已改走 %s,证书指纹 %s。\n", name, server, st.TLSFingerprint)
	return nil
}

func defaultDeviceName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "device"
	}
	return h
}

// readLine 读一行并去掉行尾换行。
func readLine(f *os.File) (string, error) {
	s, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && s == "" {
		return "", fmt.Errorf("从 stdin 读: %w", err)
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// ---- mpx device password ---------------------------------------------------

var devicePasswordFlags struct {
	revokeAll     bool
	disable       bool
	passwordStdin bool
}

var devicePasswordCmd = &cobra.Command{
	Use:   "password",
	Short: "设置或修改设备密码(新设备 mpx connect 时要输入它)",
	Long: `设置或修改设备密码。新设备用 mpx connect 接入时输入它,换一枚自己的 token。

改密码不影响已经接入的设备;要把它们一并踢掉加 --revoke-all。--disable 关闭
设备密码接入(已接入的设备照常)。密码至少 12 个字符。`,
	Example: `  mpx device password
  mpx device password --revoke-all
  mpx device password --disable`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		if devicePasswordFlags.disable {
			if _, err := c.do("DELETE", "/api/device-password", nil); err != nil {
				return err
			}
			fmt.Println("已关闭设备密码接入;已接入的设备不受影响。")
			return nil
		}
		pw, err := readNewDevicePassword(devicePasswordFlags.passwordStdin)
		if err != nil {
			return err
		}
		raw, err := c.do("PUT", "/api/device-password", map[string]any{"password": pw, "revoke_all": devicePasswordFlags.revokeAll})
		if err != nil {
			return err
		}
		res, err := decode[struct {
			Revoked int64 `json:"revoked"`
		}](raw)
		if err != nil {
			return err
		}
		if devicePasswordFlags.revokeAll {
			fmt.Printf("设备密码已更新,吊销了 %d 台设备(包括这台的话,要重新 mpx connect)。\n", res.Revoked)
			return nil
		}
		fmt.Println("设备密码已更新;已接入的设备不受影响。")
		return nil
	},
}

// readNewDevicePassword 读新设备密码:交互时输两次核对,stdin 时读一行。
func readNewDevicePassword(fromStdin bool) (string, error) {
	var pw string
	var err error
	if fromStdin {
		pw, err = readLine(os.Stdin)
	} else {
		if pw, err = promptPassword("新的设备密码(至少 12 个字符): "); err != nil {
			return "", err
		}
		again, err := promptPassword("再输一次: ")
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", errors.New("两次输入不一样")
		}
	}
	if err != nil {
		return "", err
	}
	if n := len([]rune(pw)); n < 12 {
		return "", fmt.Errorf("设备密码至少 12 个字符,这个只有 %d 个", n)
	}
	return pw, nil
}

func init() {
	connectCmd.Flags().StringVar(&connectFlags.name, "name", "", "这台设备叫什么,默认用主机名")
	connectCmd.Flags().BoolVar(&connectFlags.passwordStdin, "password-stdin", false, "从 stdin 读设备密码(脚本用)")
	connectCmd.Flags().BoolVar(&connectFlags.upgrade, "upgrade", false, "把当前配置档的 http:// 地址换成 https:// 并记下指纹(老安装用)")
	devicePasswordCmd.Flags().BoolVar(&devicePasswordFlags.revokeAll, "revoke-all", false, "同时吊销所有已接入的设备")
	devicePasswordCmd.Flags().BoolVar(&devicePasswordFlags.disable, "disable", false, "关闭设备密码接入")
	devicePasswordCmd.Flags().BoolVar(&devicePasswordFlags.passwordStdin, "password-stdin", false, "从 stdin 读新密码(脚本用)")
	deviceCmd.AddCommand(devicePasswordCmd)
	rootCmd.AddCommand(connectCmd)
}
