package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/pin"
	"github.com/RovenEverward/my-proxy/internal/probeagent"
)

// probeCmd 是境内常驻探测点的命令组(UC-12):
//
//	add     在控制面登记一台探测点,返回一次性接入命令
//	list    列出全部探测点与在线状态
//	rm      删除一台探测点(它的判定一并清)
//	rotate  吊销并换发凭据
//	run     在探测点主机上运行探测循环(接入 + 轮转探测)
var probeCmd = &cobra.Command{
	Use:   "probe",
	Short: "管理境内探测点(UC-12)",
	Long: `探测点是境内的一台常驻主机,它替控制面回答「这个节点/端点从大陆
能不能连上」。登记只发生在控制面上;探测端凭一次性接入命令换正式凭据,
之后按周期轮流探测每个对象,结果回传控制面,不通的对象可以从订阅里剔除。`,
}

var probeAddCmd = &cobra.Command{
	Use:   "add <名称>",
	Short: "登记一台探测点并打印接入命令",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", "/api/probes", map[string]string{"name": args[0]})
		if err != nil {
			return err
		}
		var res struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			JoinToken   string `json:"join_token"`
			JoinCommand string `json:"join_command"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		fmt.Printf("探测点「%s」已登记(#%d)。\n", res.Name, res.ID)
		fmt.Println("在境内主机上装好 mpx 后跑:")
		fmt.Printf("  %s --fp <指纹>\n", res.JoinCommand)
		fmt.Println("接入 token 是一次性的,这条命令只会在这里出现一次;丢了用 mpx probe rotate 重发。")
		fmt.Println("--fp 填控制面的证书指纹(mpx status 里能看到;http 地址不用)。")
		return nil
	},
}

var probeListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出全部探测点",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/probes", nil)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		var res struct {
			Probes []model.ProbePoint `json:"probes"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if len(res.Probes) == 0 {
			fmt.Println("还没有探测点:mpx probe add <名称> 登记一台。")
			return nil
		}
		rows := [][]string{}
		for _, p := range res.Probes {
			online, seen := "否", "—"
			if p.LastSeenAt != nil {
				seen = humanDuration(time.Since(*p.LastSeenAt)) + "前"
				if p.Online {
					online = "是"
				}
			}
			lastIP := p.LastIP
			if lastIP == "" {
				lastIP = "—"
			}
			rows = append(rows, []string{strconv.FormatInt(p.ID, 10), p.Name, online, seen, lastIP})
		}
		fmt.Print(tableText([]string{"ID", "名称", "在线", "最近上报", "最近来源"}, rows))
		return nil
	},
}

var probeRmCmd = &cobra.Command{
	Use:   "rm <ID>",
	Short: "删除一台探测点",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id <= 0 {
			return errors.New("探测点 ID 须为正整数")
		}
		if _, err := c.do("DELETE", "/api/probes/"+args[0], nil); err != nil {
			return err
		}
		fmt.Printf("探测点 #%d 已删除,它留下的判定也一并清了。\n", id)
		return nil
	},
}

var probeRotateCmd = &cobra.Command{
	Use:   "rotate <ID>",
	Short: "吊销探测点凭据并重发接入命令",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("POST", "/api/probes/"+args[0]+"/rotate", nil)
		if err != nil {
			return err
		}
		var res struct {
			JoinToken   string `json:"join_token"`
			JoinCommand string `json:"join_command"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if opts.output == outputJSON {
			return emitJSON(raw)
		}
		fmt.Println("凭据已换发。在探测点上重新跑:")
		fmt.Printf("  %s --fp <指纹>\n", res.JoinCommand)
		return nil
	},
}

// ---- probe run:探测端主循环 ----

var probeRunFlags struct {
	server string
	join   string
	fp     string
	once   bool
	tick   time.Duration
}

// probeConfig 是探测端记住的接入信息,与操作者 config.json 分文件:
// 探测点主机上跑的是常驻任务,不该碰(也不需要)操作者凭据。
type probeConfig struct {
	Server      string `json:"server"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func probeConfigFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("找不到用户配置目录: %w", err)
	}
	return filepath.Join(dir, "mpx", "probe.json"), nil
}

func loadProbeConfig() (*probeConfig, error) {
	p, err := probeConfigFile()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c probeConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("探测点配置不是合法 JSON: %w", err)
	}
	return &c, nil
}

func saveProbeConfig(c *probeConfig) error {
	p, err := probeConfigFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// probeHTTP 是探测端自己的客户端:只认 probe token,与管理 token 互不通用。
type probeHTTP struct {
	server string
	token  string
	http   *http.Client
}

func (c *probeHTTP) call(method, path string, body any) ([]byte, error) {
	var r *http.Request
	var err error
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r, err = http.NewRequest(method, c.server+path, strings.NewReader(string(raw)))
		if err == nil {
			r.Header.Set("Content-Type", "application/json")
		}
	} else {
		r, err = http.NewRequest(method, c.server+path, nil)
	}
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return nil, errors.New(e.Error)
		}
		return nil, fmt.Errorf("控制面返回 %d", resp.StatusCode)
	}
	return raw, nil
}

var probeRunCmd = &cobra.Command{
	Use:   "run",
	Short: "在境内主机上运行探测循环",
	Long: `探测端主循环:先用一次性 join token 换正式凭据(只跑一次,之后记住),
然后按控制面给的周期轮流探测每个对象 —— 一次一个或一小批,不是全量扫。
境内侧探测本机跑;控制面侧探测由本机请求控制面代探。

凭据存在 ~/.config/mpx/probe.json,与操作者配置互不影响。`,
	Example: `  mpx probe run --server https://cp.example.com --join <一次性token> --fp <指纹>
  mpx probe run            # 已接入过,直接用记住的凭据
  mpx probe run --once     # 只探一轮到期对象就退出(调试/手动触发用)`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := ensureProbeJoined(probeRunFlags.server, probeRunFlags.join, probeRunFlags.fp)
		if err != nil {
			return err
		}
		tr := pin.Transport(cfg.Fingerprint)
		c := &probeHTTP{server: strings.TrimSuffix(cfg.Server, "/"), token: cfg.Token, http: &http.Client{Transport: tr}}
		call := func(method, path string, body any) ([]byte, error) { return c.call(method, path, body) }
		if probeRunFlags.once {
			return probeagent.Tick(cmd.Context(), call)
		}
		fmt.Printf("探测点已就绪,连的是 %s。按控制面周期轮转探测,Ctrl-C 结束。\n", cfg.Server)
		probeagent.Loop(cmd.Context(), call, func(err error) {
			fmt.Fprintf(os.Stderr, "这一轮探测失败: %v\n", err)
		})
		return nil
	},
}

// ensureProbeJoined 确保探测端有正式凭据:给 --join 先换发,没给就读已保存的。
func ensureProbeJoined(server, join, fp string) (*probeConfig, error) {
	if join == "" {
		cfg, err := loadProbeConfig()
		if err != nil {
			return nil, fmt.Errorf("还没接入过探测点:用 mpx probe add 打印的接入命令跑一次(%w)", err)
		}
		if cfg.Token == "" || cfg.Server == "" {
			return nil, errors.New("探测点配置里没有凭据:重新用接入命令接入")
		}
		return cfg, nil
	}
	if server == "" {
		return nil, errors.New("接入需要 --server 指到控制面地址")
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("控制面地址不合法: %s", server)
	}
	isHTTPS := strings.EqualFold(u.Scheme, "https")
	if isHTTPS && fp == "" {
		// 与 mpx connect 同一个 TOFU 口径:先取指纹给人确认,不直接信。
		hostport := u.Host
		if !strings.Contains(hostport, ":") {
			hostport = net.JoinHostPort(hostport, "443")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		got, err := pin.Probe(ctx, hostport)
		if err != nil {
			return nil, fmt.Errorf("连不上控制面 %s: %w", hostport, err)
		}
		return nil, fmt.Errorf("控制面 %s 的证书指纹是 %s;接入命令请带 --fp 明确给出这个指纹", hostport, got)
	}
	if isHTTPS {
		fp, err = pin.Normalize(fp)
		if err != nil {
			return nil, err
		}
	} else {
		fp = ""
	}
	c := &probeHTTP{server: strings.TrimSuffix(server, "/"), http: &http.Client{Transport: pin.Transport(fp)}}
	raw, err := c.call("POST", "/api/probes/hello", map[string]string{"join_token": join})
	if err != nil {
		return nil, err
	}
	var res struct {
		ProbeID int64  `json:"probe_id"`
		Name    string `json:"name"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	cfg := &probeConfig{Server: server, Token: res.Token, Fingerprint: fp}
	if err := saveProbeConfig(cfg); err != nil {
		return nil, fmt.Errorf("凭据到手但存不下来: %w", err)
	}
	fmt.Printf("已接入控制面 %s,成为探测点「%s」(#%d)。\n", server, res.Name, res.ProbeID)
	return cfg, nil
}

func init() {
	probeRunCmd.Flags().StringVar(&probeRunFlags.server, "server", "", "控制面地址(首次接入必填)")
	probeRunCmd.Flags().StringVar(&probeRunFlags.join, "join", "", "一次性接入 token(mpx probe add 给的)")
	probeRunCmd.Flags().StringVar(&probeRunFlags.fp, "fp", "", "控制面证书指纹(https 必填)")
	probeRunCmd.Flags().BoolVar(&probeRunFlags.once, "once", false, "只探一轮到期对象就退出")
	probeRunCmd.Flags().DurationVar(&probeRunFlags.tick, "tick", time.Minute, "调度间隔(默认 1 分钟)")
	probeCmd.AddCommand(probeAddCmd, probeListCmd, probeRmCmd, probeRotateCmd, probeRunCmd)
}
