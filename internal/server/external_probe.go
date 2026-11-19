package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// 外部端点的降级探测(UC-17):没有 SSH 通道,控制面直连端点声明地址与第三方
// IP 数据服务。出口 IP 在这里实际是「端点地址解析出的 IP」——CDN 或域名的
// 话反映的是那个入口的信誉,不是机场后端出口。报告里 steps 写清楚每一步,
// 调用方与 UI 按同一份 QualityReport 展示。

var extProbeClient = &http.Client{Timeout: 8 * time.Second}
var extResolver = net.DefaultResolver

const extProbeTimeout = 20 * time.Second

// externalProber 让测试替换网络层:只包「解析 + 查第三方」这一步。
type externalProber interface {
	ProbeExternal(ctx context.Context, address string, abuseKey, ipapiKey string) model.QualityReport
}

// defaultExternalProber 是真实实现:域名先解析成 IP,再按 ipwho.is / ipapi.is /
// 可选 AbuseIPDB 的顺序查。
type defaultExternalProber struct{}

func (defaultExternalProber) ProbeExternal(ctx context.Context, address string, abuseKey, ipapiKey string) model.QualityReport {
	rep := model.QualityReport{Steps: []model.QualityStep{}}
	addr := strings.TrimSpace(address)
	if addr == "" {
		rep.Error = "端点没有地址"
		rep.Steps = append(rep.Steps, model.QualityStep{Name: "resolve", Status: "failed", Reason: "空地址"})
		return rep
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		ips, err := extResolver.LookupIP(ctx, "ip", addr)
		if err != nil || len(ips) == 0 {
			rep.Error = fmt.Sprintf("域名解析失败: %v", firstErr(err, "没有解析出 IP"))
			rep.Steps = append(rep.Steps, model.QualityStep{Name: "resolve", Status: "failed", Reason: rep.Error})
			return rep
		}
		ip = ips[0]
		rep.Steps = append(rep.Steps, model.QualityStep{Name: "resolve", Status: "ok"})
	} else {
		rep.Steps = append(rep.Steps, model.QualityStep{Name: "resolve", Status: "ok"})
	}
	rep.EgressIP = ip.String()

	geo, step := fetchGeo(ctx, rep.EgressIP)
	rep.Steps = append(rep.Steps, step)
	if geo != nil {
		rep.Region = strings.ToUpper(strings.TrimSpace(geo.CountryCode))
		rep.CityEn = strings.TrimSpace(geo.City)
		rep.City = model.CityZh(rep.CityEn)
	}
	flags, asn, org, step := fetchIPAPI(ctx, rep.EgressIP, ipapiKey)
	rep.Steps = append(rep.Steps, step)
	rep.Flags, rep.ASN, rep.Org = flags, asn, org
	if abuseKey != "" {
		score, step := fetchAbuse(ctx, rep.EgressIP, abuseKey)
		rep.Steps = append(rep.Steps, step)
		rep.AbuseScore = score
	}
	rep.Score, rep.Deductions = model.QualityScore(rep.Flags, rep.AbuseScore)
	return rep
}

func firstErr(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

// geoResult 只取 ipwho.is 要的两个字段。
type geoResult struct {
	CountryCode string `json:"country_code"`
	City        string `json:"city"`
}

func fetchGeo(ctx context.Context, ip string) (*geoResult, model.QualityStep) {
	step := model.QualityStep{Name: "ipwho.is", Status: "ok"}
	u := "https://ipwho.is/" + url.PathEscape(ip)
	var out struct {
		Success     bool   `json:"success"`
		CountryCode string `json:"country_code"`
		City        string `json:"city"`
	}
	if err := getJSON(ctx, u, &out); err != nil || !out.Success {
		step.Status = "failed"
		step.Reason = firstErr(err, "ipwho.is 返回失败")
		return nil, step
	}
	return &geoResult{CountryCode: out.CountryCode, City: out.City}, step
}

func fetchIPAPI(ctx context.Context, ip, key string) (map[string]bool, string, string, model.QualityStep) {
	step := model.QualityStep{Name: "ipapi.is", Status: "ok"}
	u := "https://api.ipapi.is/?q=" + url.QueryEscape(ip)
	if key != "" {
		u += "&key=" + url.QueryEscape(key)
	}
	var out struct {
		ASN struct {
			ASN  int    `json:"asn"`
			Org  string `json:"org"`
		} `json:"asn"`
		IsDatacenter bool `json:"is_datacenter"`
		IsVPN        bool `json:"is_vpn"`
		IsProxy      bool `json:"is_proxy"`
		IsTor        bool `json:"is_tor"`
		IsAbuser     bool `json:"is_abuser"`
		IsBogon      bool `json:"is_bogon"`
	}
	if err := getJSON(ctx, u, &out); err != nil {
		step.Status = "failed"
		step.Reason = err.Error()
		return nil, "", "", step
	}
	flags := map[string]bool{
		"is_datacenter": out.IsDatacenter,
		"is_vpn":        out.IsVPN,
		"is_proxy":      out.IsProxy,
		"is_tor":        out.IsTor,
		"is_abuser":     out.IsAbuser,
		"is_bogon":      out.IsBogon,
	}
	asn := ""
	if out.ASN.ASN > 0 {
		asn = fmt.Sprintf("AS%d", out.ASN.ASN)
	}
	return flags, asn, out.ASN.Org, step
}

func fetchAbuse(ctx context.Context, ip, key string) (*int, model.QualityStep) {
	step := model.QualityStep{Name: "abuseipdb", Status: "ok"}
	u := fmt.Sprintf("https://api.abuseipdb.com/api/v2/check?ipAddress=%s&maxAgeInDays=90", url.QueryEscape(ip))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		step.Status = "failed"
		step.Reason = err.Error()
		return nil, step
	}
	req.Header.Set("Key", key)
	req.Header.Set("Accept", "application/json")
	var out struct {
		Data struct {
			AbuseConfidenceScore int `json:"abuseConfidenceScore"`
		} `json:"data"`
	}
	if err := doJSON(extProbeClient, req, &out); err != nil {
		step.Status = "failed"
		step.Reason = err.Error()
		return nil, step
	}
	return &out.Data.AbuseConfidenceScore, step
}

func getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	return doJSON(extProbeClient, req, out)
}

func doJSON(c *http.Client, req *http.Request, out any) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// probeExternalEndpoint 对某个快照端点做降级探测并写 external_quality。
// prober 可换替身(测试里不打真实外网);真实实现是 defaultExternalProber。
func (s *Server) probeExternalEndpoint(ctx context.Context, src *model.SubscriptionSource, ep render.Endpoint) (model.ExternalQuality, error) {
	prober := s.extProber
	if prober == nil {
		prober = defaultExternalProber{}
	}
	rep := prober.ProbeExternal(ctx, ep.Address,
		os.Getenv("MPX_ABUSEIPDB_KEY"), os.Getenv("MPX_IPAPI_IS_KEY"))
	verdict := "untested"
	score := 0
	if rep.EgressIP != "" {
		score = rep.Score
		verdict = "ok"
		for _, st := range rep.Steps {
			if st.Status == "failed" {
				verdict = "partial"
				break
			}
		}
	}
	body, _ := json.Marshal(rep)
	q := model.ExternalQuality{
		SourceID: src.ID, SourceKey: ep.SourceKey,
		CheckedAt: time.Now().UTC(), Verdict: verdict, Score: score, Report: body,
	}
	if err := s.store.SaveExternalQuality(q); err != nil {
		return q, err
	}
	return q, nil
}

// handleProbeExternalQuality 对一个外部端点跑降级探测。与节点探测共用
// qualitySlot:两类探测打的是同一批第三方配额,不能各自放开并发。
func (s *Server) handleProbeExternalQuality(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sourceFromPath(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	ep, found := snapshotEndpoint(src, key)
	if !found {
		writeError(w, http.StatusNotFound, "这个端点不在当前快照里")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), extProbeTimeout+5*time.Second)
	defer cancel()
	select {
	case qualitySlot <- struct{}{}:
		defer func() { <-qualitySlot }()
	case <-ctx.Done():
		writeError(w, http.StatusServiceUnavailable, "质量探测队列已满,稍后再试")
		return
	}
	pctx, pcancel := context.WithTimeout(ctx, extProbeTimeout)
	q, err := s.probeExternalEndpoint(pctx, src, ep)
	pcancel()
	if err != nil {
		s.writeStoreError(w, err, "外部质量记录")
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) handleGetExternalQuality(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sourceFromPath(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	list, err := s.store.ListExternalQuality(src.ID)
	if err != nil {
		s.writeStoreError(w, err, "外部质量记录")
		return
	}
	for _, q := range list {
		if q.SourceKey == key {
			writeJSON(w, http.StatusOK, q)
			return
		}
	}
	writeError(w, http.StatusNotFound, "这个端点还没有质量探测记录")
}

// handleListExternalQuality 返回一个来源全部端点的质量记录,
// 形状与 handleListQuality 对齐:{endpoints:[…]}。
func (s *Server) handleListExternalQuality(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sourceFromPath(w, r)
	if !ok {
		return
	}
	list, err := s.store.ListExternalQuality(src.ID)
	if err != nil {
		s.writeStoreError(w, err, "外部质量记录")
		return
	}
	if list == nil {
		list = []model.ExternalQuality{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": list})
}

// snapshotEndpoint 按 source_key 在快照里找回端点;不存在时返回 false。
func snapshotEndpoint(src *model.SubscriptionSource, key string) (render.Endpoint, bool) {
	var eps []render.Endpoint
	if err := json.Unmarshal(src.Snapshot, &eps); err != nil {
		return render.Endpoint{}, false
	}
	for _, ep := range eps {
		if ep.SourceKey == key {
			ep.SourceID = src.ID
			return ep, true
		}
	}
	return render.Endpoint{}, false
}
