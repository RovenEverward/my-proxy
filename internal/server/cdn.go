package server

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"time"

	"github.com/RovenEverward/my-proxy/internal/cdn"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// ---- UC-21 / UC-24:CDN IP 优选 ----
//
// 两段探测由境内探测点执行(粗扫 TCP:443、验证要 CF-RAY);控制面负责发
// 候选名单、收结果、维护优选池、把池内 IP 落成 Host。池的存储是 cdn_pool
// 表,优选出来的 Host 是 hosts(kind='cdn')行 —— 手工 Host 永远不动。
// UC-24 起名单分两组:recheck 是池内全体成员(探测端无条件核完、成败都
// 回报),scan 是从 IP 段新抽的样本(只报确认通过项);选优只取当轮复检
// 通过(misses=0)的成员 —— 失效 IP 立刻摘下入站,不再按旧延迟占位。

const (
	cdnInterval     = time.Hour // 优选周期:服务端节流,探测端每轮领名单
	cdnSweepWant    = 64        // 每轮粗扫拿到多少 TCP 合格候选后早停
	cdnVerifyWant   = 20        // 每轮验证凑够多少真边缘后停(池目标规模)
	cdnScanBatch    = 512       // 每轮新抽多少 IP 加入候选(渐进覆盖全段)
	cdnMaxMisses    = 2         // 池内 IP 连续几轮未通过后出池(保留一轮观察)
	cdnMaxPool      = 30        // 池软上限:复检成本随池线性涨,只留好的
	cdnPerInbound   = 3         // 每个 CDN 入站最多挂几个优选 Host
	cdnPingTimeout  = 800 * time.Millisecond
	cdnVerifyTimout = 5 * time.Second
)

func (s *Server) cdnRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/cdn-optimize/targets", s.handleCDNTargets)
	api.HandleFunc("POST /api/cdn-optimize/results", s.handleCDNResults)
	api.HandleFunc("GET /api/cdn-pool", s.handleCDNPool)
}

// handleCDNTargets 回答探测点「这轮扫什么」:recheck 是池内全体成员
// (必须逐个复检,成败都要回报 —— miss 语义只对实测结论计数),scan
// 是从 IP 段随机抽的新样本。ips 保留两者合集,旧探测端行为不变。
// 开关关着时返回空名单 —— 探测点按有没有名单决定干不干活,不需要知道
// 自己该不该开。名单发出即记 cdn_last_run(UC-24):探测端不回存的空轮
// 也占本轮配额,不再每分钟重扫。
func (s *Server) handleCDNTargets(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "设置")
		return
	}
	if !settings.CDNOptimize {
		writeJSON(w, http.StatusOK, map[string]any{"ips": []string{}})
		return
	}
	// 1 小时节流:探测点每分钟 tick 都来领,距上一轮不足一小时发空名单
	// —— 空名单探测端直接跳过,开销为零。
	if last, err := s.store.CDNLastRun(); err == nil && time.Since(last) < cdnInterval {
		writeJSON(w, http.StatusOK, map[string]any{"ips": []string{}, "retry_after_s": int(cdnInterval.Seconds() - time.Since(last).Seconds())})
		return
	}
	pool, err := s.store.ListCDNPool()
	if err != nil {
		s.writeStoreError(w, err, "优选池")
		return
	}
	// IP 段清单走内置 Cloudflare 段(cdn.Ranges);线上拉取清单的
	// 覆盖机制留作后续,先让开关语义简单:开=扫内置段。
	ips, err := cdn.Expand(cdn.Ranges)
	if err != nil {
		s.writeStoreError(w, err, "IP 段")
		return
	}
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	rnd.Shuffle(len(ips), func(i, j int) { ips[i], ips[j] = ips[j], ips[i] })
	inPool := map[string]bool{}
	recheck := make([]string, 0, len(pool))
	for _, e := range pool {
		inPool[e.IP] = true
		recheck = append(recheck, e.IP)
	}
	scan := make([]string, 0, cdnScanBatch)
	for _, ip := range ips {
		if len(scan) >= cdnScanBatch {
			break
		}
		if !inPool[ip] {
			scan = append(scan, ip)
		}
	}
	candidates := make([]string, 0, len(recheck)+len(scan))
	candidates = append(candidates, recheck...)
	candidates = append(candidates, scan...)
	if err := s.store.MarkCDNRun(time.Now()); err != nil {
		s.writeStoreError(w, err, "优选节流")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ips":             candidates,
		"recheck":         recheck,
		"scan":            scan,
		"port":            443,
		"sweep_want":      cdnSweepWant,
		"verify_want":     cdnVerifyWant,
		"ping_timeout_ms": int(cdnPingTimeout.Milliseconds()),
	})
}

// handleCDNResults 收一轮优选结论:池落库(不在通过名单的 miss+1,
// 连续超阈出池),然后把池里当轮可用(misses=0)的 IP 作为
// kind='cdn' 的 Host 挂到各 CDN 入站。手工 Host 不动;池内 IP 已有的
// Host 复用,缺失的新建,当轮不可用的摘掉。
func (s *Server) handleCDNResults(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Results []model.CDNResult `json:"results"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	pool, err := s.store.SaveCDNResults(body.Results, time.Now(), cdnMaxMisses, cdnMaxPool)
	if err != nil {
		s.writeStoreError(w, err, "优选池")
		return
	}
	hosts, err := s.applyCDNHosts(pool)
	if err != nil {
		s.writeStoreError(w, err, "优选入口")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pool": len(pool), "cdn_hosts": hosts,
	})
}

// applyCDNHosts 把池内「当轮可用」的前 cdnPerInbound 个 IP 同步到每个
// 「CDN 适用」的入站:可用 = misses=0(本轮复检通过)。失效 IP 留在池里
// 观察但立刻摘下入站 —— 不再按旧的低延迟占位(UC-24)。
// 入站适用 = 名下已有一条手工 Host 填了独立 address(这就是现网的
// 「· CDN」形状 —— 操作者造过 CDN 入站才给它挂优选,不自动发明)。
// 同步是幂等的:地址匹配的手工 Host 不重复建,不可用 kind='cdn' 的删掉,
// 手工(kind=”)一律不碰。当轮零可用时保留旧优选入口不作空(5a)。
// 返回当前优选 Host 总数。
func (s *Server) applyCDNHosts(pool []model.CDNIP) (int, error) {
	var eligible []model.CDNIP
	for _, e := range pool {
		if e.Misses == 0 {
			eligible = append(eligible, e)
		}
	}
	// 池按 latency 升序返回,eligible 继承该序。
	top := eligible
	if len(top) > cdnPerInbound {
		top = top[:cdnPerInbound]
	}
	nodes, err := s.store.ListNodes()
	if err != nil {
		return 0, err
	}
	nodeAddr := map[int64]string{}
	for _, n := range nodes {
		nodeAddr[n.ID] = n.Address
	}
	inbounds, err := s.store.ListInbounds(0)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, in := range inbounds {
		hosts, err := s.store.ListHosts(in.ID)
		if err != nil {
			return 0, err
		}
		var tpl *model.Host
		for i := range hosts {
			h := &hosts[i]
			if h.Kind == "" && h.Address != "" && h.Address != nodeAddr[in.NodeID] {
				tpl = h
				break
			}
		}
		if tpl == nil {
			continue
		}
		if len(top) == 0 {
			// 5a:当轮零可用,保留旧优选入口不作空,只清点现状。
			for _, h := range hosts {
				if h.Kind == "cdn" {
					total++
				}
			}
			continue
		}
		want := map[string]bool{}
		// 优选入口名字带 IP:同名 DedupeRemarks 编号会随名单漂,IP
		// 不漂 —— 「洛杉矶 A · CDN 104.16.77.108」一眼能看出是哪一个,
		// 订阅里出问题也好对。
		tplRemark := tpl.Remark
		if tplRemark == "" {
			tplRemark = "{NODE_DISPLAY} · CDN"
		}
		for _, e := range top {
			want[e.IP] = true
			wantRemark := tplRemark + " {HOST_IP}"
			var found bool
			for _, h := range hosts {
				if h.Address == e.IP {
					found = true
					if h.Kind == "cdn" && h.Remark != wantRemark {
						h.Remark = wantRemark
						_ = s.store.UpdateHost(&h)
					}
					break
				}
			}
			if !found {
				nh := &model.Host{
					InboundID: in.ID,
					Remark:    wantRemark, Address: e.IP, Port: tpl.Port,
					SNI: tpl.SNI, Host: tpl.Host, Path: tpl.Path,
					ALPN: tpl.ALPN, Kind: "cdn",
				}
				if err := s.store.CreateHost(nh); err != nil {
					return 0, err
				}
			}
		}
		// 删当轮不可用的优选 Host(同一入站内);手工与可用项的不动。
		for _, h := range hosts {
			if h.Kind == "cdn" {
				if !want[h.Address] {
					_ = s.store.DeleteHost(h.ID)
				} else {
					total++
				}
			}
		}
	}
	return total, nil
}

// handleCDNPool 是池的可视化:名单、延迟、colo、miss 计数与已挂 Host 数。
func (s *Server) handleCDNPool(w http.ResponseWriter, r *http.Request) {
	pool, err := s.store.ListCDNPool()
	if err != nil {
		s.writeStoreError(w, err, "优选池")
		return
	}
	settings, _ := s.store.GetSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": settings != nil && settings.CDNOptimize,
		"pool":    pool,
		"size":    len(pool),
		"target":  cdnVerifyWant,
		"updated": lastSeen(pool),
	})
}

func lastSeen(pool []model.CDNIP) string {
	var t time.Time
	for _, e := range pool {
		if e.LastSeen.After(t) {
			t = e.LastSeen
		}
	}
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
