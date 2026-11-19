package server

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"time"

	"github.com/RovenEverward/my-proxy/internal/cdn"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// ---- UC-21:CDN IP 优选 ----
//
// 两段探测由境内探测点执行(粗扫 TCP:443、验证要 CF-RAY);控制面负责发
// 候选名单、收结果、维护优选池、把池内 IP 落成 Host。池的存储是 cdn_pool
// 表,优选出来的 Host 是 hosts(kind='cdn')行 —— 手工 Host 永远不动。

const (
	cdnInterval     = time.Hour // 优选周期:服务端节流,探测端每轮领名单
	cdnSweepWant    = 64        // 每轮粗扫拿到多少 TCP 合格候选后早停
	cdnVerifyWant   = 20  // 每轮验证凑够多少真边缘后停(池目标规模)
	cdnScanBatch    = 512 // 每轮新抽多少 IP 加入候选(渐进覆盖全段)
	cdnMaxMisses    = 2   // 池内 IP 连续几轮不在名单后出池(保留一轮观察)
	cdnPerInbound   = 3   // 每个 CDN 入站最多挂几个优选 Host
	cdnPingTimeout  = 800 * time.Millisecond
	cdnVerifyTimout = 5 * time.Second
)

func (s *Server) cdnRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/cdn-optimize/targets", s.handleCDNTargets)
	api.HandleFunc("POST /api/cdn-optimize/results", s.handleCDNResults)
	api.HandleFunc("GET /api/cdn-pool", s.handleCDNPool)
}

// handleCDNTargets 回答探测点「这轮扫什么」:池内现有 IP 全部重探,
// 外加从 IP 段里随机抽一批新的。开关关着时返回空名单 —— 探测点按
// 有没有名单决定干不干活,不需要知道自己该不该开。
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
	candidates := make([]string, 0, cdnScanBatch+len(pool))
	seen := map[string]bool{}
	for _, e := range pool {
		seen[e.IP] = true
		candidates = append(candidates, e.IP)
	}
	for _, ip := range ips {
		if len(candidates) >= cdnScanBatch+len(pool) {
			break
		}
		if !seen[ip] {
			seen[ip] = true
			candidates = append(candidates, ip)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ips":             candidates,
		"port":            443,
		"sweep_want":      cdnSweepWant,
		"verify_want":     cdnVerifyWant,
		"ping_timeout_ms": int(cdnPingTimeout.Milliseconds()),
	})
}

// handleCDNResults 收一轮优选结果:池落库(不在名单的 miss+1),然后
// 把池里的 IP 作为 kind='cdn' 的 Host 挂到各 CDN 入站。手工 Host 不动;
// 池内 IP 已有的 Host 复用,缺失的新建,不再池里的删掉。
func (s *Server) handleCDNResults(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Results []model.CDNResult `json:"results"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var winners []model.CDNResult
	for _, res := range body.Results {
		if res.Edge {
			winners = append(winners, res)
		}
	}
	now := time.Now()
	if err := s.store.MarkCDNRun(now); err != nil {
		s.writeStoreError(w, err, "优选节流")
		return
	}
	pool, err := s.store.SaveCDNResults(winners, now, cdnMaxMisses)
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

// applyCDNHosts 把池内前 cdnPerInbound 个 IP 同步到每个「CDN 适用」的入站:
// 入站适用 = 名下已有一条手工 Host 填了独立 address(这就是现网的
// 「· CDN」形状 —— 操作者造过 CDN 入站才给它挂优选,不自动发明)。
// 同步是幂等的:地址匹配的手工 Host 不重复建,池外 kind='cdn' 的删掉,
// 手工(kind=”)一律不碰。返回当前优选 Host 总数。
func (s *Server) applyCDNHosts(pool []model.CDNIP) (int, error) {
	top := pool
	if len(top) > cdnPerInbound {
		top = top[:cdnPerInbound]
	}
	if len(top) == 0 {
		return s.pruneCDNHosts(nil)
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
				total++
			}
		}
		// 删池外的优选 Host(同一入站内);手工与池内复用的不动。
		for _, h := range hosts {
			if h.Kind == "cdn" && !want[h.Address] {
				_ = s.store.DeleteHost(h.ID)
			}
		}
	}
	return total, nil
}

// pruneCDNHosts 池空时清掉全部优选 Host;返回 0(已无优选入口)。
func (s *Server) pruneCDNHosts(_ []model.CDNIP) (int, error) {
	inbounds, err := s.store.ListInbounds(0)
	if err != nil {
		return 0, err
	}
	for _, in := range inbounds {
		hosts, err := s.store.ListHosts(in.ID)
		if err != nil {
			return 0, err
		}
		for _, h := range hosts {
			if h.Kind == "cdn" {
				_ = s.store.DeleteHost(h.ID)
			}
		}
	}
	return 0, nil
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
