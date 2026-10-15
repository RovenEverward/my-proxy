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

// autoLocateNode 在新建节点后识别地区并写入显示名(docs/internal/node-naming-rfc.md):
// 只在节点还没有地区时写,手动设过的不覆盖;地址是域名时不自动 SSH 探测(新节点
// 未必已经能连),提示用 node locate。返回写入后的显示名,或给人看的提示。
func (s *Server) autoLocateNode(ctx context.Context, n model.Node) (display, note string) {
	if !s.autoLocate {
		return "", ""
	}
	manual := "可以手动设:mpx node edit " + n.Name + " --region US --city 洛杉矶"
	ip := net.ParseIP(strings.Trim(n.Address, "[]"))
	if ip == nil {
		return "", "地址是域名,没有自动识别地区;节点能连上后跑 mpx node locate " + n.Name + " --apply," + manual
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	raw, err := s.fetchGeo(ctx, ip.String())
	var sg LocateSuggestion
	if err == nil {
		sg, err = parseLocate(raw)
	}
	if err != nil {
		s.logger.Warn("自动识别节点地区失败", "node", n.Name, "error", err)
		return "", "自动识别地区失败(" + err.Error() + ")," + manual
	}
	metadataMu.Lock()
	defer metadataMu.Unlock()
	m, err := s.store.GetPurposeMetadata("nodes", n.ID)
	if err != nil {
		return "", "自动识别后写入失败(" + err.Error() + ")," + manual
	}
	if strings.TrimSpace(m.Region) != "" {
		return model.NodeDisplay(m), "" // 已经有人设过,不覆盖
	}
	m.Region, m.City, m.Letter = sg.Region, sg.City, ""
	if code, msg := s.fillNodeLetter(n.ID, &m, true, true); code != 0 {
		return "", "自动识别后分配字母失败(" + msg + ")," + manual
	}
	if err := s.store.SavePurposeMetadata("nodes", n.ID, m); err != nil {
		return "", "自动识别后写入失败(" + err.Error() + ")," + manual
	}
	s.invalidateSubCache()
	display = model.NodeDisplay(m)
	if sg.City == "" && sg.CityEn != "" {
		note = "城市 " + sg.CityEn + " 不在对照表里,只用了国家;要补上:mpx node edit " + n.Name + " --city <城市>"
	}
	return display, note
}
