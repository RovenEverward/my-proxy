// Package probeagent 是探测编排的共享实现(UC-12):境内侧探测端(mpx probe
// run)与操作机常驻轮转(mpx ui 后台)跑的是同一份调度 —— 拉目标、按对象
// 算到期、一批一批探、把结果按(对象,侧)回存。
//
// 它只做编排,不持有连接:控制面调用经 Caller 抽象交给调用方,cli 用探测
// 点凭据,ui 用管理 token,两条路共用同一套到期与批大小规则。
package probeagent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/reach"
)

// Caller 是发一次控制面调用的抽象:method、路径、可选 JSON body,回原始响应体。
type Caller func(method, path string, body any) ([]byte, error)

// TargetsResponse 与 server 的 GET /api/reach/targets 响应同形。
type TargetsResponse struct {
	FetchedAt time.Time                  `json:"fetched_at"`
	Interval  int                        `json:"interval_minutes"`
	Exclude   bool                       `json:"exclude_enabled"`
	Targets   []reach.Target             `json:"targets"`
	Freshness map[string]reach.Freshness `json:"freshness"`
}

// tick 是调度粒度:一分钟看一次欠账,把到期对象摊进整个周期分批处理。
const tick = time.Minute

// Tick 跑一轮调度:拉目标 → 境内侧探一批到期对象 → 请控制面探它的欠账。
// 自动剔除关着时(Exclude=false)境内侧不探 —— 判定不为任何人服务,
// 白探只是给探测端与目标加无意义流量。控制面侧欠账照样清:
// 那批数据也供展示,而且成本在控制面不在探测端。
// logf 用来报非致命警告(如金丝雀判定路径被代理),nil 时静默。
func Tick(ctx context.Context, call Caller, logf func(string, ...any)) error {
	raw, err := call("GET", "/api/reach/targets", nil)
	if err != nil {
		return err
	}
	var rt TargetsResponse
	if err := json.Unmarshal(raw, &rt); err != nil {
		return err
	}
	interval := time.Duration(rt.Interval) * time.Minute
	if interval <= 0 {
		interval = time.Hour
	}
	now := time.Now()
	bySubject := map[string][]reach.Target{}
	for _, t := range rt.Targets {
		bySubject[t.Subject] = append(bySubject[t.Subject], t)
	}

	if rt.Exclude {
		due := reach.DueSubjects(rt.Targets, rt.Freshness, reach.SideDomestic, now, interval)
		batch := reach.RotationBatch(len(due), interval, tick)
		if batch > 0 && !reach.TrustedPath(ctx, 0) {
			// ISS-14:金丝雀能探通说明本机出机路径被代理接管,境内侧结果
			// 全是假的。整轮不写回,只告警 —— 静默产假数据比没数据更糟。
			if logf != nil {
				logf("境内探测路径被代理接管(金丝雀目标可达),这一轮境内结果不回存;检查本机代理/TUN")
			}
			batch = 0
		}
		var subjects []reach.SourceCheck
		for _, key := range due[:min(batch, len(due))] {
			res := reach.ProbeKeyed(ctx, bySubject[key], reach.Options{})
			subjects = append(subjects, reach.SourceCheck{
				SubjectKey: key, Scope: reach.ScopeDomestic,
				CheckedAt: now, Results: res,
			})
		}
		if len(subjects) > 0 {
			if _, err := call("POST", "/api/reach/results", map[string]any{
				"checked_at": now.UTC(), "scope": reach.ScopeDomestic, "subjects": subjects,
			}); err != nil {
				return err
			}
		}
	}

	// 控制面侧欠账:探测端把对象名单递过去,控制面自己探,探测端再按 cp 侧回存。
	dueCP := reach.DueSubjects(rt.Targets, rt.Freshness, reach.SideCP, now, interval)
	if n := reach.RotationBatch(len(dueCP), interval, tick); n > 0 {
		return cpRound(call, dueCP[:min(n, len(dueCP))])
	}
	return nil
}

// cpRound 请控制面对欠账对象探一轮,把返回的结果按对象摊平回存为 cp 侧数据。
func cpRound(call Caller, keys []string) error {
	want := map[string]bool{}
	var nodeIDs []int64
	var extKeys []string
	for _, key := range keys {
		want[key] = true
		if id, ok := model.ParseNodeSubjectKey(model.ProbeSubjectKey(key)); ok {
			nodeIDs = append(nodeIDs, id)
		} else {
			extKeys = append(extKeys, key)
		}
	}
	raw, err := call("POST", "/api/reach/probe", map[string]any{"node_ids": nodeIDs, "ext_keys": extKeys})
	if err != nil {
		return err
	}
	var pr struct {
		CheckedAt  time.Time                `json:"checked_at"`
		Targets    []reach.Target           `json:"targets"`
		Results    map[int64]*reach.Result  `json:"results"`
		ExtTargets []reach.Target           `json:"ext_targets"`
		ExtResults map[string]*reach.Result `json:"ext_results"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		return err
	}
	// 按对象归并:节点各入站摊到同一对象(键 inbound:<id>),外部端点一个
	// 对象一个目标(键就是它自己的 EndpointKey)。
	subjects := map[string]map[string]*reach.Result{}
	for _, t := range pr.Targets {
		k := t.Key()
		if res := pr.Results[t.InboundID]; res != nil {
			if subjects[t.Subject] == nil {
				subjects[t.Subject] = map[string]*reach.Result{}
			}
			subjects[t.Subject][k] = res
		}
	}
	for _, t := range pr.ExtTargets {
		k := t.Key()
		if res := pr.ExtResults[k]; res != nil {
			if subjects[t.Subject] == nil {
				subjects[t.Subject] = map[string]*reach.Result{}
			}
			subjects[t.Subject][k] = res
		}
	}
	var post []reach.SourceCheck
	for key, res := range subjects {
		if !want[key] || len(res) == 0 {
			continue
		}
		post = append(post, reach.SourceCheck{
			SubjectKey: key, Scope: reach.ScopeCP, CheckedAt: pr.CheckedAt, Results: res,
		})
	}
	if len(post) == 0 {
		return nil
	}
	_, err = call("POST", "/api/reach/results", map[string]any{
		"checked_at": pr.CheckedAt, "scope": reach.ScopeCP, "subjects": post,
	})
	return err
}

// Loop 是常驻轮转:先跑一轮,之后每 tick 一次,直到 ctx 结束。
// 单轮出错只记日志不退出 —— 探测端的价值在「一直在」,不在「一次都不能错」。
// logf 接 Tick 的非致命警告,nil 时静默。
func Loop(ctx context.Context, call Caller, logf func(string, ...any), onError func(error)) {
	for {
		if err := Tick(ctx, call, logf); err != nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tick):
		}
	}
}

// ---- 小工具 ----

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 包内无其他未用项;文件到此结束。
