package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// UC-26:管理台域名 + ACME 自动证书的操作口。
//
// 四条命令对应四条接口:配置(PUT)、查看(GET status)、关闭(DELETE)、
// 签发(POST/GET issue)。签发是服务端后台任务 —— DNS-01 要等 CA 轮询,
// CLI 起完任务就按 seq 增量拉事件,拉到 done/failed 为止。
var acmeCmd = &cobra.Command{
	Use:   "acme",
	Short: "配置管理台域名与 ACME 自动证书",
	Long: `管理台域名与 ACME 自动证书(UC-26)。

管理台要对外开,浏览器要的是一张受信证书 —— 自签证书它只给满屏警告。mpx acme
用 Cloudflare 的 DNS-01 挑战向 Let's Encrypt 签证书:域名解析指到控制面、DNS
由 Cloudflare 托管、给一个能改 DNS 的 API token,签发与到期续签就都自动了。

配好后:浏览器开 https://<域名>/admin/;CLI 照常用 mpx connect 接入 ——
连域名时它会自动改走系统 CA 校验(不再要指纹),连 IP 仍是指纹钉自签证书。`,
}

var acmeSetFlags struct {
	domain    string
	cfToken   string
	cfTokenIn bool
	staging   bool
	noWait    bool
}

var acmeSetCmd = &cobra.Command{
	Use:   "set",
	Short: "设置管理台域名与 Cloudflare token,并触发首次签发",
	Long: `设置管理台域名与 Cloudflare API token,改完立刻发起一次签发(后台任务,
默认等它跑完并打出过程;--no-wait 只起任务不等)。

域名是管理台的访问地址(如 cp.example.net):它的 A/AAAA 记录要指到这台控制面,
DNS 由 Cloudflare 托管。token 在 CF 控制台建,权限给 Zone.DNS 编辑即可 ——
用 --cf-token-stdin 从管道喂比写在命令行里少留痕迹。

--staging 切到 Let's Encrypt 的 staging CA(证书不受信):联调与测速用,
测完去掉再跑一次就会换回正式 CA 重签。`,
	Example: `  mpx acme set --domain cp.example.net --cf-token-stdin < token.txt
  mpx acme set --domain cp.example.net --cf-token 'abc...' --staging`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var req model.ACMEConfigRequest
		req.Domain = acmeSetFlags.domain
		switch {
		case acmeSetFlags.cfToken != "":
			req.CFToken = acmeSetFlags.cfToken
		case acmeSetFlags.cfTokenIn:
			tok, err := readLine(os.Stdin)
			if err != nil {
				return err
			}
			req.CFToken = tok
		}
		if cmd.Flags().Changed("staging") {
			req.Staging = &acmeSetFlags.staging
		}
		if req.Domain == "" && req.CFToken == "" && req.Staging == nil {
			return errors.New("没给要改的东西:至少要 --domain 或 --cf-token/--cf-token-stdin 之一")
		}
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("PUT", "/api/acme/config", req)
		if err != nil {
			return err
		}
		cfg, err := decode[model.ACMEConfig](raw)
		if err != nil {
			return err
		}
		fmt.Printf("ACME 已配置:域名 %s,CA %s\n", dash(cfg.Domain), acmeCAName(cfg.Staging))
		if cfg.Domain == "" {
			return nil
		}
		return acmeIssue(c, acmeSetFlags.noWait)
	},
}

var acmeRenewFlags struct {
	noWait bool
}

var acmeRenewCmd = &cobra.Command{
	Use:   "renew",
	Short: "立刻发起一轮签发/续签(后台任务)",
	Long: `立刻发起一轮 ACME 签发。正常不需要手动跑:控制面到期前 30 天会自动续。
这条是给「刚改了配置想马上试」或「上次失败已修好、不想等退避」用的。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		return acmeIssue(c, acmeRenewFlags.noWait)
	},
}

var acmeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看 ACME 配置、证书与最近签发结果",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		raw, err := c.do("GET", "/api/acme/status", nil)
		if err != nil {
			return err
		}
		st, err := decode[model.ACMEStatus](raw)
		if err != nil {
			return err
		}
		if opts.output == outputJSON {
			fmt.Println(string(raw))
			return nil
		}
		pairs := [][2]string{
			{"管理台域名", dash(st.Domain)},
			{"Cloudflare token", yesNo(st.CFTokenSet)},
			{"CA", acmeCAName(st.Staging)},
			{"证书", acmeCertText(st)},
			{"最近尝试", timeOrDash(st.LastAttemptAt)},
			{"最近错误", dash(st.LastError)},
			{"下次重试", timeOrDash(st.NextRetryAt)},
			{"签发任务", st.IssueState},
		}
		return writeKV(pairs)
	},
}

var acmeUnsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "清掉域名与 token 配置,回到自签证书",
	Long: `清掉 ACME 配置(域名、CF token、staging 标志),TLS 监听回到只用自签证书。
已签下的证书留在库里不动 —— 哪天配回同一个域名它还能直接用。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		if _, err := c.do("DELETE", "/api/acme/config", nil); err != nil {
			return err
		}
		fmt.Println("ACME 已关闭:域名与 token 已清,TLS 回到自签证书(浏览器会显示不受信)。")
		return nil
	},
}

// acmeCAName 把 staging 标志翻成 CA 名字。
func acmeCAName(staging bool) string {
	if staging {
		return "Let's Encrypt staging(不受信)"
	}
	return "Let's Encrypt(正式)"
}

// acmeCertText 是 status 里「证书」那一格:没签、到期时间、是否属于当前域名。
func acmeCertText(st model.ACMEStatus) string {
	if !st.CertIssued {
		return "未签发"
	}
	notAfter := "未知"
	if st.CertNotAfter != nil {
		notAfter = st.CertNotAfter.Local().Format("2006-01-02 15:04") +
			fmt.Sprintf("(剩 %d 天)", int(time.Until(*st.CertNotAfter).Hours()/24))
	}
	return notAfter
}

// timeOrDash 把可空时间显示成 "YYYY-MM-DD HH:MM" 或 "-"。
func timeOrDash(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// acmeIssue 起一轮签发并按 seq 拉事件到终态;noWait 只起任务。
//
// 客户端侧等的上限比服务端的 issueBudget 多留一分钟:服务端是 5 分钟硬顶,
// CLI 等 6 分钟还没终态就放手(任务还在跑,status 可查)。
func acmeIssue(c *Client, noWait bool) error {
	raw, err := c.do("POST", "/api/acme/issue", nil)
	if err != nil {
		return err
	}
	res, err := decode[struct {
		Job int64 `json:"job"`
	}](raw)
	if err != nil {
		return err
	}
	if noWait {
		fmt.Printf("签发任务 #%d 已启动,进度看 mpx acme status。\n", res.Job)
		return nil
	}
	var since int64
	deadline := time.Now().Add(6 * time.Minute)
	for {
		raw, err := c.do("GET", fmt.Sprintf("/api/acme/issue?since=%d", since), nil)
		if err != nil {
			return err
		}
		p, err := decode[model.ACMEIssueProgress](raw)
		if err != nil {
			return err
		}
		for _, ev := range p.Events {
			fmt.Println("  " + ev.Msg)
		}
		since = p.Seq
		switch p.State {
		case model.ACMEIssueDone:
			fmt.Println("签发完成:管理台现在可以用 https://<域名>/admin/ 打开。")
			return nil
		case model.ACMEIssueFailed:
			return errors.New("签发失败(细节见上面的过程;配置可用 mpx acme status 核对)")
		}
		if time.Now().After(deadline) {
			return errors.New("等了 6 分钟签发还没结束;任务仍在跑,稍后 mpx acme status 看结果")
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

func init() {
	acmeSetCmd.Flags().StringVar(&acmeSetFlags.domain, "domain", "", "管理台域名,例如 cp.example.net")
	acmeSetCmd.Flags().StringVar(&acmeSetFlags.cfToken, "cf-token", "", "Cloudflare API token(Zone.DNS 编辑权限;脚本用)")
	acmeSetCmd.Flags().BoolVar(&acmeSetFlags.cfTokenIn, "cf-token-stdin", false, "从 stdin 读 Cloudflare API token")
	acmeSetCmd.Flags().BoolVar(&acmeSetFlags.staging, "staging", false, "用 Let's Encrypt staging CA(联调用,证书不受信)")
	acmeSetCmd.Flags().BoolVar(&acmeSetFlags.noWait, "no-wait", false, "只发起签发,不等结果")
	acmeRenewCmd.Flags().BoolVar(&acmeRenewFlags.noWait, "no-wait", false, "只发起签发,不等结果")
	acmeCmd.AddCommand(acmeSetCmd, acmeStatusCmd, acmeUnsetCmd, acmeRenewCmd)
	rootCmd.AddCommand(acmeCmd)
}
