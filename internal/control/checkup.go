package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/fusion"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/store"
)

const (
	checkupBudget         = 6 * time.Minute
	checkupEntranceBudget = 65 * time.Second
	checkupReportLimit    = 256 << 10
)

// CheckupNode owns one slot and one latest report for the entire manual checkup.
// Capability probes keep their normal authorization and scheduler concurrency rules.
func (c *Control) CheckupNode(ctx context.Context, n model.Node, cfg diagnostics.Config, now time.Time) (model.DiagnosticRecord, error) {
	if cfg.Position == "" {
		cfg.Position = "node"
	}
	record := model.DiagnosticRecord{NodeID: n.ID, Source: cfg.Position, CheckedAt: now}
	if cfg.Position != "node" && cfg.Position != "control" {
		return record, fmt.Errorf("服务端测量位置只能是 node 或 control")
	}
	if cfg.SocksURL != "" || cfg.AbuseIPDBKey != "" || cfg.AbuseIPDBKeyEnv != "" || cfg.IPAPIKey != "" || cfg.IPAPIKeyEnv != "" {
		return record, fmt.Errorf("服务端检测不接受代理凭证或自选 API 密钥")
	}
	if cfg.Engine == "" {
		cfg.Engine = "fusion"
		if cfg.Position == "control" {
			cfg.Engine = "light"
		}
	}
	// Validate without applying defaults: runDiagnostic supplies the control-side
	// node target and server-owned key environment names.
	if err := cfg.Validate(); err != nil {
		return record, err
	}
	if err := ctx.Err(); err != nil {
		return record, err
	}
	select {
	case c.diagnosticSlot <- struct{}{}:
		defer func() { <-c.diagnosticSlot }()
	default:
		return record, ErrDiagnosticBusy
	}
	current, err := c.store.GetNode(n.ID)
	if err != nil {
		return record, err
	}
	n = *current
	record.MeasuredAddress = n.Address
	previous, err := c.store.GetDiagnosticReport(n.ID, cfg.Position)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return record, err
	}
	if previous != nil && now.Before(previous.CheckedAt.Add(time.Minute)) {
		return record, ErrDiagnosticRateLimit
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.CheckupTimeout())
	defer cancel()
	report := model.NodeCheckupReport{Summary: []string{}, Drift: "unknown", Entrances: []model.EntranceDiagnostic{}}
	inbounds, baselineErr := c.store.ListInbounds(n.ID)
	deployment, deploymentErr := c.store.GetCoreDeployment(n.ID, model.CoreSingBox)
	report.Health, err = c.CheckNode(ctx, n, now)
	if err != nil {
		report.Health = model.NodeStatus{NodeID: n.ID, CheckedAt: now, Error: err.Error()}
	}
	if baselineErr == nil && deploymentErr == nil {
		report.Drift = checkupCoreDrift(n, *deployment, inbounds, report.Health, &report.Summary)
	} else {
		report.Summary = append(report.Summary, "读取配置漂移基准失败: "+errors.Join(baselineErr, deploymentErr).Error())
	}
	report.Summary = append(report.Summary, checkupHealthSummary(report.Health, report.Drift)...)
	runErr := errors.Join(err, baselineErr, deploymentErr)
	if ctx.Err() == nil && cfg.Engine == "fusion" {
		result, fusionErr := node.RunFusion(ctx, c.runner, n, fusion.Config{Speed: cfg.Speed, Hardware: cfg.Hardware, TimeoutSeconds: cfg.FusionTimeoutSeconds}, c.cacheDir)
		report.Fusion, err = json.Marshal(result)
		runErr = errors.Join(runErr, fusionErr, err)
		report.Summary = append(report.Summary, "融合怪 "+result.Version+": "+result.Status)
		if fusionErr != nil {
			report.Summary = append(report.Summary, "融合怪测评未完成: "+fusionErr.Error())
		}
		if cfg.Speed && ctx.Err() == nil {
			runErr = errors.Join(runErr, c.checkupFusionSpeedFallback(ctx, n, result, &report))
		}
	} else if ctx.Err() == nil {
		network, networkErr := c.runDiagnostic(ctx, n, cfg, now)
		report.Network = network.Report
		if network.MeasuredAddress != "" {
			if network.MeasuredAddress != n.Address {
				report.Summary = append(report.Summary, "节点地址在体检期间变化: 健康检查地址 "+n.Address+"，网络检测地址 "+network.MeasuredAddress)
			}
			record.MeasuredAddress = network.MeasuredAddress
		}
		runErr = errors.Join(runErr, networkErr)
		if networkErr != nil {
			report.Summary = append(report.Summary, "网络检测未完成: "+networkErr.Error())
		}
		checkupNetworkSummary(&report)
	} else {
		report.Summary = append(report.Summary, "测评未运行: "+ctx.Err().Error())
	}
	entranceErr := c.checkupEntrances(ctx, n, now, &report)
	runErr = errors.Join(runErr, entranceErr, ctx.Err())
	if ctx.Err() != nil {
		report.Summary = append(report.Summary, "体检中断，已保存完成部分: "+ctx.Err().Error())
	}
	record.Report, err = marshalCheckupReport(report)
	if err != nil {
		return record, errors.Join(runErr, err)
	}
	return record, errors.Join(runErr, c.store.SaveDiagnosticReport(record))
}

// checkupFusionSpeedFallback keeps independent measurements out of the official
// Fusion report and uses the node identity already captured for this checkup.
func (c *Control) checkupFusionSpeedFallback(ctx context.Context, n model.Node, result fusion.Report, report *model.NodeCheckupReport) error {
	parsed, measured := checkupFusionSpeedMeasured(result.Data)
	if !parsed || measured || ctx.Err() != nil {
		return nil
	}
	report.Summary = append(report.Summary, "融合怪缺少可用测速结果；独立速度补测与融合怪报告分别保存")
	seconds := result.TimeoutSeconds
	if seconds == 0 {
		seconds = fusion.DefaultTimeoutSeconds
	}
	deadline := result.StartedAt.Add(time.Duration(seconds) * time.Second)
	if cap := time.Now().Add(50 * time.Second); cap.Before(deadline) {
		deadline = cap
	}
	if outer, ok := ctx.Deadline(); ok && outer.Before(deadline) {
		deadline = outer
	}
	if !deadline.After(time.Now()) {
		report.Summary = append(report.Summary, "独立速度补测未运行: 融合怪原有时间预算已耗尽")
		return nil
	}
	child, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	network, runErr := diagnostics.RunSpeedFallback(child, strconv.FormatInt(n.ID, 10), diagnosticSSHExecutor{runner: c.runner, node: n})
	raw, marshalErr := json.Marshal(network)
	if marshalErr == nil {
		report.Network = raw
	}
	if runErr != nil {
		report.Summary = append(report.Summary, "独立速度补测未完成: "+runErr.Error())
	} else {
		report.Summary = append(report.Summary, "独立速度补测已完成；融合怪原始状态与错误保持不变")
	}
	checkupNetworkSummary(report)
	return errors.Join(runErr, marshalErr)
}

// Only throughput in the pinned speed schema counts. Candidate nodes, latency,
// durations and errors do not prove a transfer occurred.
func checkupFusionSpeedMeasured(data json.RawMessage) (parsed, measured bool) {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil {
		return false, false
	}
	var schema string
	if json.Unmarshal(root["schema_version"], &schema) != nil || schema != "goecs.report/v1" {
		return false, false
	}
	var components []json.RawMessage
	_ = json.Unmarshal(root["components"], &components)
	for _, raw := range components {
		var component struct {
			Name    string          `json:"name"`
			Schema  string          `json:"schema_version"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(raw, &component) != nil || component.Name != "speed.registry" || component.Schema != "goecs.speed/v1" {
			continue
		}
		var payload struct {
			Schema            string            `json:"schema_version"`
			Benchmarks        []json.RawMessage `json:"benchmarks"`
			PrivateBenchmarks []json.RawMessage `json:"private_benchmarks"`
		}
		if json.Unmarshal(component.Payload, &payload) != nil || payload.Schema != "goecs.speed/v1" {
			continue
		}
		for _, rows := range [][]json.RawMessage{payload.Benchmarks, payload.PrivateBenchmarks} {
			for _, raw := range rows {
				var row struct {
					Status   string   `json:"status"`
					Download *float64 `json:"download_mbps"`
					Upload   *float64 `json:"upload_mbps"`
				}
				if json.Unmarshal(raw, &row) != nil || (row.Status != "available" && row.Status != "ok") {
					continue
				}
				if (row.Download != nil && *row.Download > 0) || (row.Upload != nil && *row.Upload > 0) {
					return true, true
				}
			}
		}
	}
	return true, false
}

func checkupDrift(deployed string, health model.NodeStatus) string {
	if !health.Reachable || health.ObservedHash == "" {
		return "unknown"
	}
	if deployed == "" {
		return "missing-baseline"
	}
	if deployed != health.ObservedHash {
		return "changed"
	}
	return "match"
}

func checkupCoreDrift(n model.Node, deployment model.CoreDeployment, inbounds []model.Inbound, health model.NodeStatus, summary *[]string) string {
	if !health.Reachable {
		return "unknown"
	}
	states := []string{}
	if n.DeployedHash != "" || len(model.InboundsForCore(inbounds, model.CoreXray)) > 0 || len(health.Cores) == 0 {
		states = append(states, checkupDrift(n.DeployedHash, health))
	}
	for _, core := range health.Cores {
		if core.Core != model.CoreSingBox {
			continue
		}
		state := checkupDrift(deployment.DeployedHash, model.NodeStatus{Reachable: true, ObservedHash: core.ObservedHash})
		states = append(states, state)
		if state != "match" {
			*summary = append(*summary, "sing-box 配置漂移检查: "+state)
		}
	}
	if len(states) == 0 {
		return "unknown"
	}
	// Any observed drift is actionable even if another core cannot be checked.
	for _, priority := range []string{"changed", "unknown", "missing-baseline"} {
		for _, state := range states {
			if state == priority {
				return state
			}
		}
	}
	return "match"
}

func checkupHealthSummary(health model.NodeStatus, drift string) []string {
	var summary []string
	if !health.Reachable {
		return []string{"节点健康检查失败: " + health.Error}
	}
	if !health.Active {
		summary = append(summary, "节点代理服务未运行")
	}
	switch drift {
	case "changed":
		summary = append(summary, "节点实测配置哈希与最后下发的配置不一致")
	case "missing-baseline":
		summary = append(summary, "节点没有已下发配置哈希，无法判断配置漂移")
	case "unknown":
		summary = append(summary, "节点配置哈希未读到，无法判断配置漂移")
	}
	for _, core := range health.Cores {
		if !core.Active {
			summary = append(summary, string(core.Core)+" 服务未运行")
		}
		if core.Error != "" {
			summary = append(summary, string(core.Core)+" 检查失败: "+core.Error)
		}
	}
	for _, cert := range health.Certificates {
		if cert.Error != "" {
			summary = append(summary, fmt.Sprintf("入站 %d 证书检查失败: %s", cert.InboundID, cert.Error))
		} else if cert.NotAfter != nil && cert.DaysLeft <= model.CertWarnDays {
			summary = append(summary, fmt.Sprintf("入站 %d 证书剩余 %d 天", cert.InboundID, cert.DaysLeft))
		}
	}
	return summary
}

func checkupNetworkSummary(report *model.NodeCheckupReport) {
	var network diagnostics.Report
	if json.Unmarshal(report.Network, &network) != nil {
		return
	}
	for _, tool := range network.Tools {
		if !tool.Available {
			report.Summary = append(report.Summary, "网络检测工具不可用: "+tool.Name)
		}
	}
	for _, target := range network.Targets {
		for _, item := range []struct {
			name   string
			status diagnostics.Status
		}{
			{"DNS", target.DNS}, {"ICMP", target.ICMP.Status}, {"HTTP", target.HTTP.Status}, {"路由", target.Route.Status},
		} {
			if item.status.Status != "ok" {
				report.Summary = append(report.Summary, target.Target.Name+" "+item.name+" "+item.status.Status+": "+item.status.Reason)
			}
		}
	}
	for _, speed := range network.Speed {
		if speed.Status.Status != "ok" {
			report.Summary = append(report.Summary, speed.Site+" "+speed.Direction+" 测速 "+speed.Status.Status+": "+speed.Reason)
		}
	}
	for _, ip := range network.IP {
		if ip.Status.Status != "ok" {
			report.Summary = append(report.Summary, ip.Family+" IP 检测 "+ip.Status.Status+": "+ip.Reason)
		}
	}
}

func (c *Control) checkupEntrances(ctx context.Context, n model.Node, now time.Time, report *model.NodeCheckupReport) error {
	inbounds, err := c.store.ListInbounds(n.ID)
	if err != nil {
		report.Summary = append(report.Summary, "读取入站失败: "+err.Error())
		return err
	}
	for _, in := range inbounds {
		hosts, err := c.store.ListHosts(in.ID)
		if err != nil {
			entry := model.EntranceDiagnostic{InboundID: in.ID, Name: in.TagOrDefault(), Error: "读取入口失败: " + err.Error()}
			appendCheckupEntrance(report, entry)
			continue
		}
		if len(hosts) == 0 {
			hosts = []model.Host{{}}
		}
		for _, host := range hosts {
			name := in.TagOrDefault()
			if host.Remark != "" {
				name = host.Remark
			}
			entry := model.EntranceDiagnostic{InboundID: in.ID, HostID: host.ID, Name: name, Results: []model.CapabilityResult{}}
			if err := ctx.Err(); err != nil {
				entry.Error = "未检测: " + err.Error()
			} else if !report.Health.Reachable {
				entry.Error = "节点健康检查无法连接，未进行入口探测"
			} else {
				child, cancel := context.WithTimeout(ctx, checkupEntranceBudget)
				// ProbeCapabilities re-reads the inbound, host, generation and users.
				current, probeErr := c.store.GetNode(n.ID)
				if probeErr == nil {
					entry.Results, probeErr = c.ProbeCapabilities(child, *current, model.CapabilityConfig{InboundID: in.ID, HostID: host.ID, Targets: append([]string(nil), model.CapabilityTargets...)}, time.Now().UTC())
				}
				probeErr = errors.Join(probeErr, child.Err())
				cancel()
				if probeErr != nil {
					entry.Error = probeErr.Error()
				}
			}
			appendCheckupEntrance(report, entry)
		}
	}
	if len(inbounds) == 0 {
		report.Summary = append(report.Summary, "节点没有配置入站，未进行入口探测")
	}
	return nil
}

func appendCheckupEntrance(report *model.NodeCheckupReport, entry model.EntranceDiagnostic) {
	report.Entrances = append(report.Entrances, entry)
	label := fmt.Sprintf("入站 %d/入口 %d", entry.InboundID, entry.HostID)
	if entry.Error != "" {
		report.Summary = append(report.Summary, label+" 检测失败: "+entry.Error)
	}
	for _, result := range entry.Results {
		status := result.ObservedStatus
		if status == "" {
			status = model.CapabilityUnknown
		}
		if status != model.CapabilityAvailable {
			report.Summary = append(report.Summary, label+" "+result.Target+" 本次实测 "+string(status)+": "+result.Reason)
		}
	}
}

// Keep a valid bounded partial report even when names, health errors or the
// number of entrances exceed the storage contract. Omission is always explicit.
func marshalCheckupReport(report model.NodeCheckupReport) ([]byte, error) {
	raw, err := json.Marshal(report)
	if err != nil || len(raw) <= checkupReportLimit {
		return raw, err
	}
	// Bound escaped Fusion evidence before trimming project health and entrances.
	if len(report.Fusion) != 0 {
		var evaluation fusion.Report
		if err := json.Unmarshal(report.Fusion, &evaluation); err != nil {
			return nil, err
		}
		evaluation.Truncated = true
		if evaluation.Status == "done" {
			evaluation.Status = "partial"
		}
		evaluation.Warnings = append(evaluation.Warnings, "组合报告超过 256 KiB，融合怪原始证据已缩减")
		for len(raw) > checkupReportLimit && len(evaluation.Output) > 0 {
			text := []rune(evaluation.Output)
			evaluation.Output = string(text[(len(text)+1)/2:])
			report.Fusion, err = json.Marshal(evaluation)
			if err != nil {
				return nil, err
			}
			raw, err = json.Marshal(report)
			if err != nil {
				return nil, err
			}
		}
		report.Fusion, err = json.Marshal(evaluation)
		if err != nil {
			return nil, err
		}
		raw, err = json.Marshal(report)
		if err != nil || len(raw) <= checkupReportLimit {
			return raw, err
		}
	}
	// Preserve full network evidence and the health verdict; cap free-form text
	// before retaining the largest entrance prefix that fits the report budget.
	if len(report.Summary) > 128 {
		report.Summary = report.Summary[:128]
	}
	for i := range report.Summary {
		report.Summary[i] = checkupText(report.Summary[i])
	}
	report.Summary = append(report.Summary, "报告超过 256 KiB，部分长文本、证书和入口详情已省略")
	report.Health.Error = checkupText(report.Health.Error)
	report.Health.Version = checkupText(report.Health.Version)
	report.Health.ObservedHash = checkupText(report.Health.ObservedHash)
	for i := range report.Health.Cores {
		core := &report.Health.Cores[i]
		core.Error, core.Version, core.ObservedHash = checkupText(core.Error), checkupText(core.Version), checkupText(core.ObservedHash)
	}
	if len(report.Health.Certificates) > 128 {
		report.Health.Certificates = report.Health.Certificates[:128]
	}
	for i := range report.Health.Certificates {
		cert := &report.Health.Certificates[i]
		cert.InboundName, cert.Path, cert.Error = checkupText(cert.InboundName), checkupText(cert.Path), checkupText(cert.Error)
	}
	for i := range report.Entrances {
		entry := &report.Entrances[i]
		entry.Name, entry.Error = checkupText(entry.Name), checkupText(entry.Error)
		for j := range entry.Results {
			r := &entry.Results[j]
			r.Reason, r.Location, r.Region, r.ExitIP = checkupText(r.Reason), checkupText(r.Location), checkupText(r.Region), checkupText(r.ExitIP)
		}
	}
	entries := report.Entrances
	report.Entrances = nil
	raw, err = json.Marshal(report)
	if err != nil {
		return nil, err
	}
	if len(raw) > checkupReportLimit-256 {
		report.Network, err = json.Marshal(diagnostics.Report{Position: "", Warnings: []string{"网络详情超过合并报告预算，未保存完整网络证据"}})
		if err != nil {
			return nil, err
		}
	}
	// Preserve structured Fusion measurements before retaining a large certificate
	// inventory; the earlier summary already identifies omitted evidence.
	raw, err = json.Marshal(report)
	if err != nil {
		return nil, err
	}
	for len(raw) > checkupReportLimit-256 && len(report.Health.Certificates) > 0 {
		report.Health.Certificates = report.Health.Certificates[:len(report.Health.Certificates)/2]
		raw, err = json.Marshal(report)
		if err != nil {
			return nil, err
		}
	}
	base := len(report.Summary)
	report.Summary = append(report.Summary, "")
	low, high := 0, len(entries)
	for low < high {
		mid := low + (high-low+1)/2
		report.Entrances = entries[:mid]
		report.Summary[base] = "省略入口详情数量: " + strconv.Itoa(len(entries)-mid)
		raw, err = json.Marshal(report)
		if err != nil {
			return nil, err
		}
		if len(raw) <= checkupReportLimit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	report.Entrances = entries[:low]
	report.Summary[base] = "省略入口详情数量: " + strconv.Itoa(len(entries)-low)
	raw, err = json.Marshal(report)
	if len(raw) > checkupReportLimit {
		return nil, fmt.Errorf("体检健康信息超过 256 KiB")
	}
	return raw, err
}

func checkupText(text string) string {
	if len(text) <= 512 {
		return text
	}
	text = text[:512]
	for !utf8.ValidString(text) {
		text = strings.TrimSuffix(text, text[len(text)-1:])
	}
	return text + "…[已截断]"
}
