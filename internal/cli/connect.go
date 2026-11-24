package cli

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
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

// defaultControlPort 是地址里没写端口时用的默认端口。
const defaultControlPort = 8080

var connectFlags struct {
	tokenStdin bool
	sshTarget  string
	sshCmd     string
}

// sshTargetAuto 是 --ssh 不带值时的占位:目标取 connect 地址的主机名。
const sshTargetAuto = "auto"

var connectCmd = &cobra.Command{
	Use:   "connect <地址> [ssh目标]",
	Short: "接入控制面（推荐 --ssh 通道，或 --token 直连）",
	Long: `接入控制面并保存为本机 profile（--profile 指定名称，默认 default）。

两种方式:
  1. --ssh <ssh目标>（推荐）：每条 API 请求经 ssh 隧道发到控制面本机执行，
     SSH 本身就是认证，本机不存 token、不问指纹。
  2. --token <MPX_TOKEN>（或 --token-stdin）：直连控制面 HTTPS，校验系统 CA
     或自签证书指纹（--fp），验证通过后保存凭据。`,
	Example: `  mpx connect mpx.example.net --ssh la-1
  mpx connect mpx.example.net --ssh root@203.0.113.10
  mpx connect 203.0.113.10 --fp SHA256:... --token <MPX_TOKEN>
  echo "$MPX_TOKEN" | mpx connect 203.0.113.10 --fp SHA256:... --token-stdin`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		server, hostport, err := controlURL(args[0])
		if err != nil {
			return err
		}
		sshGiven := cmd.Flags().Changed("ssh") || len(args) == 2
		target := connectFlags.sshTarget
		if len(args) == 2 {
			if cmd.Flags().Changed("ssh") && target != "" && target != sshTargetAuto {
				return errors.New("SSH 目标给了两次:位置参数与 --ssh=<值> 留一个")
			}
			target = args[1]
		}
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
		tok := strings.TrimSpace(firstNonEmpty(opts.token, os.Getenv("MPX_TOKEN")))
		if connectFlags.tokenStdin {
			if tok, err = readLine(os.Stdin); err != nil {
				return err
			}
		} else if tok == "" {
			if tok, err = promptPassword("控制面 MPX_TOKEN: "); err != nil {
				return err
			}
		}
		return connectWithToken(server, trust, tok)
	},
}

// controlURL 把用户给的地址整理成 https://host:port。
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

// trustDecision 是接入时认定的证书校验方式:指纹钉或公共 CA。
type trustDecision struct {
	fp string
	ca bool
}

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

func probeCA(hostport string) (bool, error) {
	return probeCAWithRoots(hostport, nil)
}

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
		return false, fmt.Errorf("连不上控制面 %s: %w", hostport, err)
	}
	conn.Close()
	return true, nil
}

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

func trustTransport(trust trustDecision) *http.Transport {
	if trust.ca {
		return pin.Transport("")
	}
	return pin.Transport(trust.fp)
}

// connectViaSSH 是 SSH 通道接入:经通道发一次 /api/status 验证,不存 token、不问指纹。
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

// connectWithToken 验证 Bearer token 可达控制面 /api/status 后落盘保存。
func connectWithToken(server string, trust trustDecision, token string) error {
	c := &Client{server: server, token: token, http: &http.Client{Transport: trustTransport(trust)}}
	if _, err := c.do(http.MethodGet, "/api/status", nil); err != nil {
		return err
	}
	profile, err := saveProfile(Profile{Server: server, Token: token, Fingerprint: trust.fp, CA: trust.ca})
	if err != nil {
		return err
	}
	fmt.Printf("已接入 %s,保存为控制面「%s」。\n", server, profile)
	return nil
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

func readLine(f *os.File) (string, error) {
	s, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && s == "" {
		return "", fmt.Errorf("从 stdin 读: %w", err)
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func init() {
	connectCmd.Flags().BoolVar(&connectFlags.tokenStdin, "token-stdin", false, "从 stdin 读管理 token(脚本用)")
	connectCmd.Flags().StringVar(&connectFlags.sshTarget, "ssh", "", "用 SSH 通道接入:目标串(user@host 或 ~/.ssh/config 别名)。不带值时取连接地址主机名")
	connectCmd.Flags().Lookup("ssh").NoOptDefVal = sshTargetAuto
	connectCmd.Flags().StringVar(&connectFlags.sshCmd, "ssh-cmd", "", "覆盖默认的远端通道命令 api-relay")
	rootCmd.AddCommand(connectCmd)
}
