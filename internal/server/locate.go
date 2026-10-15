package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// LocateSuggestion 是 POST /api/nodes/{id}/locate 的回答:只是建议,不写入。
type LocateSuggestion struct {
	Region  string `json:"region"`
	City    string `json:"city,omitempty"`    // 中文;对照表里没有时为空
	CityEn  string `json:"city_en,omitempty"` // ipwho.is 原文
	IP      string `json:"ip"`
	Source  string `json:"source"` // 固定 ipwho.is
	Via     string `json:"via"`    // control:控制面按节点 IP 查;node:在节点上探测出口
	Display string `json:"display"`
}

// egressProber 由 control 实现:在节点上请求 ipwho.is,返回原始 JSON。
// 用可选接口而不是加进 NodeOps,测试里的假实现不必跟着改。
type egressProber interface {
	ProbeEgressGeo(ctx context.Context, n model.Node) (string, error)
}

func (s *Server) handleLocateNode(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var raw, via string
	if ip := net.ParseIP(strings.Trim(n.Address, "[]")); ip != nil {
		via = "control"
		raw, err = s.fetchGeo(ctx, ip.String())
	} else {
		// 地址是域名(可能挂了 CDN):解析出来的未必是节点,改在节点上看出口。
		via = "node"
		p, ok := s.nodeOps.(egressProber)
		if !ok {
			err = fmt.Errorf("控制面没有接上节点执行器")
		} else {
			raw, err = p.ProbeEgressGeo(ctx, *n)
		}
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "识别失败("+err.Error()+");请手动填写:mpx node edit "+n.Name+" --region US --city 洛杉矶")
		return
	}
	sg, err := parseLocate(raw)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error()+";请手动填写:mpx node edit "+n.Name+" --region US --city 洛杉矶")
		return
	}
	sg.Via = via
	writeJSON(w, http.StatusOK, sg)
}

// geoBaseURL 可在测试里替换。
var geoBaseURL = "https://ipwho.is/"

func (s *Server) fetchGeo(ctx context.Context, ip string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", geoBaseURL+ip, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ipwho.is 返回 %d", resp.StatusCode)
	}
	return string(b), nil
}

func parseLocate(raw string) (LocateSuggestion, error) {
	var g struct {
		Success     bool   `json:"success"`
		Message     string `json:"message"`
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
		City        string `json:"city"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &g); err != nil {
		return LocateSuggestion{}, fmt.Errorf("ipwho.is 的回答不是 JSON")
	}
	if !g.Success || model.RegionFlag(g.CountryCode) == "" {
		msg := g.Message
		if msg == "" {
			msg = "没有国家信息"
		}
		return LocateSuggestion{}, fmt.Errorf("ipwho.is 没给出结果:%s", msg)
	}
	sg := LocateSuggestion{Region: strings.ToUpper(g.CountryCode), CityEn: g.City, City: model.CityZh(g.City), IP: g.IP, Source: "ipwho.is"}
	sg.Display = model.NodeDisplay(model.PurposeMetadata{Region: sg.Region, City: sg.City})
	return sg, nil
}
