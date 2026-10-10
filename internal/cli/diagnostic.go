package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

var diagnoseCmd = newLocalDiagnoseCmd()

func init() {
	rootCmd.AddCommand(diagnoseCmd)
	nodeCmd.AddCommand(newNodeDiagnoseCmd(), newNodeDiagnosticsCmd())
}

func diagnosticFlags(cmd *cobra.Command, local bool) {
	cmd.Flags().Bool("speed", !local, "启用有界上传和下载测速;节点完整体检默认开启")
	cmd.Flags().Int64("max-bytes", 0, "总 HTTP 流量上限(字节),默认 32 MiB")
	cmd.Flags().Int64("speed-bytes", 0, "每站每方向测速字节数,默认 1 MiB")
	cmd.Flags().Int("samples", 0, "延迟样本数,默认 3,最大 5")
	cmd.Flags().Duration("timeout", 0, "总执行时间,默认 120s,最大 170s")
	cmd.Flags().Duration("request-timeout", 0, "单次请求超时,默认 5s,最大 15s")
	cmd.Flags().StringArray("target", nil, "目标主机/IP,可重复;省略时使用测量位置默认目标")
	cmd.Flags().StringArray("target-http", nil, "目标 HTTP(S) URL,与 --target 按顺序对应;可单独指定")
	cmd.Flags().StringArray("target-name", nil, "目标名称,与目标按顺序对应")
	cmd.Flags().StringArray("target-region", nil, "目标已知地区,与目标按顺序对应;不会由 IP 推断")
	cmd.Flags().String("speed-site-name", "", "自定义测速站名称(需同时指定下载、上传、来源 URL)")
	cmd.Flags().String("speed-download", "", "自定义下载 URL,可包含 {bytes}")
	cmd.Flags().String("speed-upload", "", "自定义上传 URL")
	cmd.Flags().String("speed-source", "", "自定义测速站 API 文档 URL")
	cmd.Flags().Int64("speed-download-unit-bytes", 0, "下载 URL 使用 {chunks} 时每块字节数,实际请求向上取整")
	cmd.Flags().String("speed-upload-method", "", "自定义上传方法:POST(默认)或 PUT")
	cmd.Flags().String("ip-info-url", "", "GeoIP API URL")
	cmd.Flags().String("ipv4-url", "", "IPv4 出口 API URL")
	cmd.Flags().String("ipv6-url", "", "IPv6 出口 API URL")
	reputationEnv, qualityEnv := "", ""
	if local {
		reputationEnv, qualityEnv = "MPX_ABUSEIPDB_KEY", "MPX_IPAPI_IS_KEY"
	}
	cmd.Flags().String("ip-reputation-key-env", reputationEnv, "本地 AbuseIPDB key 所在环境变量名;不会上传密钥")
	cmd.Flags().String("ip-quality-key-env", qualityEnv, "本地 IPAPI.is key 所在环境变量名;不会上传密钥")
	if local {
		cmd.Flags().String("socks5", "", "经指定 socks5h://host:port 检测 HTTP,不支持 URL 凭证")
	} else {
		cmd.Flags().String("from", "node", "测量位置:node 或 control")
	}
}

func diagnosticConfig(cmd *cobra.Command, local bool) (diagnostics.Config, error) {
	cfg := diagnostics.Config{Position: "local"}
	if local {
		cfg.SocksURL, _ = cmd.Flags().GetString("socks5")
	} else {
		cfg.Position, _ = cmd.Flags().GetString("from")
		if err := validateDiagnosticSource(cfg.Position); err != nil {
			return cfg, err
		}
	}
	cfg.Speed, _ = cmd.Flags().GetBool("speed")
	if !local && !cmd.Flags().Changed("speed") {
		networkOnly, _ := cmd.Flags().GetBool("network-only")
		cfg.Speed = !networkOnly
	}
	cfg.MaxBytes, _ = cmd.Flags().GetInt64("max-bytes")
	cfg.SpeedBytes, _ = cmd.Flags().GetInt64("speed-bytes")
	cfg.Samples, _ = cmd.Flags().GetInt("samples")
	for _, duration := range []struct {
		flag  string
		value *int
	}{{"timeout", &cfg.TotalTimeoutSeconds}, {"request-timeout", &cfg.RequestTimeoutSeconds}} {
		value, _ := cmd.Flags().GetDuration(duration.flag)
		if cmd.Flags().Changed(duration.flag) {
			if value <= 0 || value%time.Second != 0 || value > 3*time.Minute {
				return cfg, fmt.Errorf("--%s 必须是正的整秒时长,且不超过 3m", duration.flag)
			}
			*duration.value = int(value / time.Second)
		}
	}
	// Explicit zero must not silently become a default inside the engine.
	for _, flag := range []string{"max-bytes", "speed-bytes", "samples"} {
		if cmd.Flags().Changed(flag) && cmd.Flags().Lookup(flag).Value.String() == "0" {
			return cfg, fmt.Errorf("--%s 必须为正数", flag)
		}
	}
	cfg.IPInfoURL, _ = cmd.Flags().GetString("ip-info-url")
	cfg.IPv4URL, _ = cmd.Flags().GetString("ipv4-url")
	cfg.IPv6URL, _ = cmd.Flags().GetString("ipv6-url")
	cfg.AbuseIPDBKeyEnv, _ = cmd.Flags().GetString("ip-reputation-key-env")
	if !local && cmd.Flags().Changed("ip-reputation-key-env") {
		return cfg, fmt.Errorf("--ip-reputation-key-env 仅用于本地诊断;远程使用执行机器的 MPX_ABUSEIPDB_KEY")
	}
	cfg.IPAPIKeyEnv, _ = cmd.Flags().GetString("ip-quality-key-env")
	if !local && cmd.Flags().Changed("ip-quality-key-env") {
		return cfg, fmt.Errorf("--ip-quality-key-env 仅用于本地诊断;远程使用执行机器的 MPX_IPAPI_IS_KEY")
	}
	var err error
	cfg.Targets, err = diagnosticTargets(cmd)
	if err != nil {
		return cfg, err
	}
	site := diagnostics.SpeedSite{}
	site.Name, _ = cmd.Flags().GetString("speed-site-name")
	site.DownloadURL, _ = cmd.Flags().GetString("speed-download")
	site.UploadURL, _ = cmd.Flags().GetString("speed-upload")
	site.SourceURL, _ = cmd.Flags().GetString("speed-source")
	site.DownloadUnitBytes, _ = cmd.Flags().GetInt64("speed-download-unit-bytes")
	site.UploadMethod, _ = cmd.Flags().GetString("speed-upload-method")
	if cmd.Flags().Changed("speed-download-unit-bytes") && site.DownloadUnitBytes <= 0 {
		return cfg, fmt.Errorf("--speed-download-unit-bytes 必须为正数")
	}
	if site != (diagnostics.SpeedSite{}) || cmd.Flags().Changed("speed-upload-method") {
		if site.Name == "" || site.DownloadURL == "" || site.UploadURL == "" || site.SourceURL == "" {
			return cfg, fmt.Errorf("自定义测速站需同时指定 --speed-site-name/--speed-download/--speed-upload/--speed-source")
		}
		cfg.SpeedSites = []diagnostics.SpeedSite{site}
	}
	return cfg, cfg.Validate()
}

func diagnosticTargets(cmd *cobra.Command) ([]diagnostics.Target, error) {
	hosts, _ := cmd.Flags().GetStringArray("target")
	httpURLs, _ := cmd.Flags().GetStringArray("target-http")
	names, _ := cmd.Flags().GetStringArray("target-name")
	regions, _ := cmd.Flags().GetStringArray("target-region")
	count := len(hosts)
	if count == 0 {
		count = len(httpURLs)
	}
	if count == 0 && len(names) == 0 && len(regions) == 0 {
		return nil, nil
	}
	if count == 0 || (len(httpURLs) != 0 && len(httpURLs) != count) || (len(names) != 0 && len(names) != count) || (len(regions) != 0 && len(regions) != count) {
		return nil, fmt.Errorf("--target/--target-http/--target-name/--target-region 数量必须对应")
	}
	targets := make([]diagnostics.Target, count)
	for i := range targets {
		if len(hosts) != 0 {
			targets[i].Host = hosts[i]
		}
		if len(httpURLs) != 0 {
			targets[i].HTTPURL = httpURLs[i]
			if targets[i].Host == "" {
				u, err := url.Parse(httpURLs[i])
				if err != nil {
					return nil, fmt.Errorf("目标 HTTP URL 无效")
				}
				targets[i].Host = u.Hostname()
			}
		}
		targets[i].Name = targets[i].Host
		if len(names) != 0 {
			targets[i].Name = names[i]
		}
		if len(regions) != 0 {
			targets[i].Region = regions[i]
		}
	}
	return targets, nil
}

func validateDiagnosticSource(source string) error {
	if source != "node" && source != "control" {
		return fmt.Errorf("--from 只能是 node 或 control")
	}
	return nil
}

type diagnosticPayload struct {
	diagnostics.Config
	NetworkOnly bool  `json:"network_only,omitempty"`
	Speed       *bool `json:"speed,omitempty"`
}

type diagnosticAllResult struct {
	NodeID int64                   `json:"node_id"`
	Name   string                  `json:"name"`
	Record *model.DiagnosticRecord `json:"record,omitempty"`
	Error  string                  `json:"error,omitempty"`
}

func diagnosticExecutionOutput(cmd *cobra.Command, raw []byte, requestErr error) error {
	if requestErr == nil {
		return diagnosticRemoteOutput(cmd, raw)
	}
	var partial struct {
		Record *model.DiagnosticRecord `json:"record"`
	}
	if json.Unmarshal(raw, &partial) == nil && partial.Record != nil && json.Valid(partial.Record.Report) {
		if opts.output == outputJSON {
			if err := capabilityJSON(cmd, raw); err != nil {
				return err
			}
		} else {
			recordRaw, err := json.Marshal(partial.Record)
			if err != nil {
				return err
			}
			if err := diagnosticRemoteOutput(cmd, recordRaw); err != nil {
				return err
			}
		}
	}
	return requestErr
}

func newNodeDiagnoseCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "diagnose [id]", Short: "一键执行节点健康、入口能力和网络完整体检", Args: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		if (all && len(args) != 0) || (!all && len(args) != 1) {
			return fmt.Errorf("请指定一个节点 id 或 --all,两者互斥")
		}
		return nil
	}, Example: "  mpx node diagnose 1\n  mpx node diagnose --all\n  mpx node diagnose 1 --network-only --from control",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := diagnosticConfig(cmd, false)
			if err != nil {
				return err
			}
			networkOnly, _ := cmd.Flags().GetBool("network-only")
			all, _ := cmd.Flags().GetBool("all")
			var id int64
			if !all {
				id, err = idArg(args[0])
				if err != nil {
					return err
				}
				if id <= 0 {
					return fmt.Errorf("节点 id 必须为正整数")
				}
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			if all {
				return diagnoseAllNodes(cmd, c, cfg, networkOnly)
			}
			cfg.NodeID = strconv.FormatInt(id, 10)
			raw, err := diagnosticRequest(cmd, c, nodePath(id)+"/diagnostics", diagnosticPayload{Config: cfg, NetworkOnly: networkOnly, Speed: &cfg.Speed})
			return diagnosticExecutionOutput(cmd, raw, err)
		}}
	diagnosticFlags(cmd, false)
	cmd.Flags().Bool("all", false, "逐个完整检查所有节点;单个失败继续其他节点")
	cmd.Flags().Bool("network-only", false, "高级选项:仅执行网络诊断")
	return cmd
}

func diagnoseAllNodes(cmd *cobra.Command, c *Client, cfg diagnostics.Config, networkOnly bool) error {
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	// The node-list uses its existing array contract and the caller's context.
	listCtx, listCancel := context.WithTimeout(cmd.Context(), defaultTimeout)
	rawNodes, _, err := c.doOnce(listCtx, http.MethodGet, "/api/nodes", nil, false)
	listCancel()
	if err != nil {
		return err
	}
	nodes, err := decode[[]model.NodeView](rawNodes)
	if err != nil {
		return err
	}
	results := make([]diagnosticAllResult, 0, len(nodes))
	failed := 0
	var stopped error
	for _, node := range nodes {
		if err := cmd.Context().Err(); err != nil {
			stopped = err
			break
		}
		cfg.NodeID = strconv.FormatInt(node.ID, 10)
		raw, requestErr := diagnosticRequest(cmd, c, nodePath(node.ID)+"/diagnostics", diagnosticPayload{Config: cfg, NetworkOnly: networkOnly, Speed: &cfg.Speed})
		item := diagnosticAllResult{NodeID: node.ID, Name: node.Name}
		if requestErr == nil {
			var record model.DiagnosticRecord
			if err := json.Unmarshal(raw, &record); err != nil || !json.Valid(record.Report) {
				requestErr = fmt.Errorf("体检返回无效报告")
			} else {
				item.Record = &record
			}
		} else {
			var partial struct {
				Record *model.DiagnosticRecord `json:"record"`
			}
			if json.Unmarshal(raw, &partial) == nil && partial.Record != nil && json.Valid(partial.Record.Report) {
				item.Record = partial.Record
			}
		}
		if requestErr != nil {
			item.Error = requestErr.Error()
			failed++
		}
		results = append(results, item)
		if opts.output != outputJSON {
			cmd.Printf("节点: %s (ID %d)\n", node.Name, node.ID)
			if item.Record != nil {
				recordRaw, _ := json.Marshal(item.Record)
				if err := diagnosticRemoteOutput(cmd, recordRaw); err != nil {
					item.Error = err.Error()
					if requestErr == nil {
						failed++
					}
				}
			}
			if item.Error != "" {
				cmd.Printf("体检失败: %s\n", item.Error)
			}
			cmd.Println()
		}
		if err := cmd.Context().Err(); err != nil {
			stopped = err
			break
		}
	}
	if opts.output == outputJSON {
		raw, err := json.Marshal(results)
		if err != nil {
			return err
		}
		if err := capabilityJSON(cmd, raw); err != nil {
			return err
		}
	}
	if stopped != nil {
		return stopped
	}
	if failed > 0 {
		return fmt.Errorf("%d/%d 个节点体检失败", failed, len(results))
	}
	return nil
}

func newNodeDiagnosticsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "diagnostics <id>", Short: "读取节点在指定位置的最新体检报告", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := idArg(args[0])
			if err != nil {
				return err
			}
			if id <= 0 {
				return fmt.Errorf("节点 id 必须为正整数")
			}
			source, _ := cmd.Flags().GetString("from")
			if err := validateDiagnosticSource(source); err != nil {
				return err
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			raw, err := c.do("GET", nodePath(id)+"/diagnostics?from="+source, nil)
			if err != nil {
				return err
			}
			return diagnosticRemoteOutput(cmd, raw)
		}}
	cmd.Flags().String("from", "node", "报告来源:node 或 control")
	return cmd
}

func newLocalDiagnoseCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "diagnose", Short: "在本机直连或经 SOCKS 代理执行综合体检,无需登录控制面", Args: cobra.NoArgs,
		Example: "  mpx diagnose\n  mpx diagnose --socks5 socks5h://127.0.0.1:1080 --output json",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := diagnosticConfig(cmd, true)
			if err != nil {
				return err
			}
			report, err := diagnostics.Run(cmd.Context(), cfg, diagnostics.LocalExecutor{})
			if outputErr := diagnosticLocalOutput(cmd, report); outputErr != nil {
				return outputErr
			}
			return err
		}}
	diagnosticFlags(cmd, true)
	return cmd
}

// Execution is manual and cancellation follows Cobra's context. Keep the error
// response so operators can see partial evidence without starting another run.
func diagnosticRequest(cmd *cobra.Command, c *Client, path string, cfg diagnosticPayload) ([]byte, error) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	budget := 6*time.Minute + 45*time.Second
	if cfg.NetworkOnly {
		budget = 3*time.Minute + 15*time.Second
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), budget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("执行诊断请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (256<<10)+4097))
	if err != nil {
		return nil, err
	}
	if len(raw) > (256<<10)+4096 {
		return nil, fmt.Errorf("诊断响应超过 260 KiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, &APIError{StatusCode: resp.StatusCode, Message: errorMessage(resp.StatusCode, raw)}
	}
	return raw, nil
}

func diagnosticLocalOutput(cmd *cobra.Command, report diagnostics.Report) error {
	if opts.output == outputJSON {
		raw, err := json.Marshal(report)
		if err != nil {
			return err
		}
		return capabilityJSON(cmd, raw)
	}
	printDiagnosticReport(cmd, report, report.Position, report.FinishedAt)
	return nil
}

func diagnosticRemoteOutput(cmd *cobra.Command, raw []byte) error {
	if opts.output == outputJSON {
		return capabilityJSON(cmd, raw)
	}
	record, err := decode[model.DiagnosticRecord](raw)
	if err != nil {
		return err
	}
	cmd.Printf("测量时节点地址: %s\n", dash(record.MeasuredAddress))
	var envelope struct {
		Network json.RawMessage `json:"network"`
	}
	if err := json.Unmarshal(record.Report, &envelope); err != nil {
		return err
	}
	network := record.Report
	if envelope.Network != nil {
		checkup, err := decode[model.NodeCheckupReport](record.Report)
		if err != nil {
			return err
		}
		for _, summary := range checkup.Summary {
			cmd.Printf("摘要: %s\n", summary)
		}
		health := checkup.Health
		cmd.Printf("健康: 可达 %s  服务运行 %s  配置漂移 %s  版本 %s  检查时间 %s\n", yesNo(health.Reachable), yesNo(health.Active), dash(checkup.Drift), dash(health.Version), capabilityTime(health.CheckedAt))
		if health.Error != "" {
			cmd.Printf("健康检查原因: %s\n", health.Error)
		}
		if health.ObservedHash != "" {
			cmd.Printf("观察配置哈希: %s\n", health.ObservedHash)
		}
		for _, cert := range health.Certificates {
			cmd.Printf("证书: %s  剩余 %d 天  原因 %s\n", cert.InboundName, cert.DaysLeft, dash(cert.Error))
		}
		if len(checkup.Entrances) == 0 {
			cmd.Println("入口能力: 无入口测量结果")
		}
		for _, entrance := range checkup.Entrances {
			cmd.Printf("入口: %s  入站 ID %d  主机 ID %d\n", entrance.Name, entrance.InboundID, entrance.HostID)
			if entrance.Error != "" {
				cmd.Printf("入口检查原因: %s\n", entrance.Error)
			}
			rows := [][]string{}
			for _, result := range entrance.Results {
				rows = append(rows, []string{result.Target, string(result.ObservedStatus), string(result.Status), dash(result.Region), dash(result.ExitIP), dash(result.Reason)})
			}
			if len(rows) > 0 {
				cmd.Print(tableText([]string{"目标", "本次观察", "稳定状态", "地区", "出口 IP", "原因"}, rows))
			}
		}
		network = checkup.Network
		if len(network) == 0 || string(network) == "null" {
			cmd.Println("网络: 未返回测量结果")
			return nil
		}
	}
	report, err := decode[diagnostics.Report](network)
	if err != nil {
		return err
	}
	printDiagnosticReport(cmd, report, record.Source, record.CheckedAt)
	return nil
}

func printDiagnosticReport(cmd *cobra.Command, report diagnostics.Report, source string, checkedAt time.Time) {
	age := "未知"
	if !checkedAt.IsZero() {
		age = time.Since(checkedAt).Round(time.Second).String()
	}
	cmd.Printf("测量位置: %s  SOCKS: %s  检查时间: %s  距今: %s\n", source, yesNo(report.ThroughSOCKS), capabilityTime(checkedAt), age)
	cmd.Printf("耗时: %.0f ms  实测流量: %d 字节  预留流量: %d/%d 字节  时间上限: %ds\n", report.DurationMS, report.Budget.TransferredBytes, report.Budget.ReservedBytes, report.Budget.MaxBytes, report.Budget.TotalTimeoutSeconds)
	for _, tool := range report.Tools {
		if !tool.Available {
			cmd.Printf("工具 %s: 不可用 %s\n", tool.Name, tool.Detail)
		}
	}
	for _, target := range report.Targets {
		cmd.Printf("目标: %s (%s)  地区: %s  HTTP: %s\n", target.Target.Name, target.Target.Host, dash(target.Target.Region), dash(target.Target.HTTPURL))
		cmd.Printf("DNS: %s %s\n", target.DNS.Status, target.DNS.Reason)
		rows := [][]string{}
		for _, latency := range []diagnostics.Latency{target.ICMP, target.HTTP} {
			mean, jitter := "-", "-"
			if latency.ValidSamples > 0 {
				mean = fmt.Sprintf("%.2f", latency.MeanMS)
			}
			if latency.JitterSamples > 0 {
				jitter = fmt.Sprintf("%.2f", latency.JitterMS)
			}
			rows = append(rows, []string{latency.Protocol, latency.Timing, latency.Status.Status, fmt.Sprintf("%d/%d", latency.ValidSamples, latency.Attempted), mean, jitter, diagnosticPercent(latency.LossPercent), diagnosticPercent(latency.HTTPFailureRatePercent), dash(latency.Reason)})
		}
		cmd.Print(tableText([]string{"协议", "计时含义", "状态", "有效/尝试", "均值 ms", "抖动 ms", "ICMP 丢包", "HTTP 失败率", "原因"}, rows))
		cmd.Printf("线路: %s  工具: %s  方向: %s  原因: %s\n", target.Route.Status.Status, dash(target.Route.Tool), target.Route.Direction, dash(target.Route.Reason))
		rows = nil
		for _, hop := range target.Route.Hops {
			rtt := "-"
			if hop.RTTMS != nil {
				rtt = fmt.Sprintf("%.2f", *hop.RTTMS)
			}
			rows = append(rows, []string{strconv.Itoa(hop.TTL), dash(hop.Address), dash(hop.ASN), rtt, diagnosticPercent(hop.LossPercent), yesNo(hop.Unanswered)})
		}
		if len(rows) > 0 {
			cmd.Print(tableText([]string{"跳", "IP", "ASN", "RTT ms", "丢包", "无应答"}, rows))
		}
		if target.Route.Evidence != "" {
			cmd.Printf("线路证据: %s\n", target.Route.Evidence)
		}
	}
	rows := [][]string{}
	for _, speed := range report.Speed {
		rows = append(rows, []string{speed.Site, speed.Direction, speed.Status.Status, fmt.Sprintf("%.2f", speed.Mbps), strconv.FormatInt(speed.Bytes, 10), fmt.Sprintf("%.0f", speed.DurationMS), dash(speed.SourceURL), dash(speed.Reason)})
	}
	if len(rows) > 0 {
		cmd.Print(tableText([]string{"测速站", "方向", "状态", "Mbps", "实测字节", "耗时 ms", "来源", "原因"}, rows))
	}
	for _, ip := range report.IP {
		flags := []string{}
		for key, value := range ip.ProviderFlags {
			flags = append(flags, fmt.Sprintf("%s=%t", key, value))
		}
		sort.Strings(flags)
		cmd.Printf("IP %s: %s  地址: %s  ASN: %s  组织: %s  地区: %s/%s/%s\n", ip.Family, ip.Status.Status, dash(ip.Address), dash(ip.ASN), dash(ip.Organization), dash(ip.Country), dash(ip.Region), dash(ip.City))
		cmd.Printf("来源: %s  时间: %s  提供商标记: %s  原因: %s\n", ip.Source, capabilityTime(ip.CheckedAt), dash(strings.Join(flags, ", ")), dash(ip.Reason))
		if ip.AbuseConfidenceScore != nil {
			cmd.Printf("AbuseIPDB 报告置信度: %d\n", *ip.AbuseConfidenceScore)
		}
		if ip.TotalReports != nil {
			cmd.Printf("AbuseIPDB 举报数: %d\n", *ip.TotalReports)
		}
	}
	for _, warning := range report.Warnings {
		cmd.Printf("提示: %s\n", warning)
	}
}

func diagnosticPercent(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", *value)
}
