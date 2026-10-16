package server

import (
	"context"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
)

// CoreInstaller 扩展节点安装通道,保留旧 Xray NodeOps 实现的兼容性。
type CoreInstaller interface {
	InstallNodeCore(ctx context.Context, n model.Node, core model.Core, version string) (string, error)
}

// CoreUpgrader 是节点操作通道的可选扩展:让节点从 GitHub 自己拉一份 xray 换上。
//
// 与 CoreInstaller 的安装不同,这条路控制面不推 65MB:它只把资产地址与 sha256
// 递给节点,下载、校验、替换、回滚都在节点上发生(见 node.UpgradeXray)。
type CoreUpgrader interface {
	// ResolveXray 问清节点架构,给出该平台该版本的下载地址与官方 sha256。
	ResolveXray(ctx context.Context, n model.Node, version string) (assetURL, sha256 string, err error)
	// UpgradeXray 让节点自己下载并原地换上指定版本,失败回滚(由 node 层负责)。
	UpgradeXray(ctx context.Context, n model.Node, version, assetURL, sha256 string) (node.UpgradeResult, error)
}

var coreServicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]*$`)

func validateQUICInbound(i *model.Inbound) string {
	if i.Network != model.NetworkUDP {
		return "hysteria2 / tuic 必须使用 udp 传输"
	}
	if i.Security != model.SecurityTLS {
		return "hysteria2 / tuic 必须使用 TLS"
	}
	if i.Flow != "" || i.Mux || i.Fragment {
		return "hysteria2 / tuic 不支持 flow / mux / fragment"
	}
	if i.WSPath != "" || i.GRPCService != "" || i.XHTTPPath != "" || i.XHTTPHost != "" || i.XHTTPMode != "" || i.RealityDest != "" || i.RealityPriv != "" || i.RealityPub != "" || len(i.ShortIDs) > 0 {
		return "hysteria2 / tuic 不支持 Xray 传输与 REALITY 参数"
	}
	if i.Protocol == model.ProtocolTUIC {
		if i.CongestionControl == "" {
			i.CongestionControl = "bbr"
		}
		switch i.CongestionControl {
		case "cubic", "new_reno", "bbr":
		default:
			return "TUIC 拥塞控制只能是 cubic / new_reno / bbr"
		}
	} else if i.CongestionControl != "" {
		return "拥塞控制只支持 TUIC"
	}
	return ""
}

func validateCoreDeployment(d *model.CoreDeployment, other model.CoreDeployment, inbounds []model.Inbound) string {
	if d.APIPort < 1 || d.APIPort > 65535 {
		return "核心 API 端口必须在 1-65535 之间"
	}
	if !strings.HasPrefix(d.ConfigPath, "/") || path.Clean(d.ConfigPath) != d.ConfigPath || d.ConfigPath == "/" || strings.ContainsAny(d.ConfigPath, " \t\r\n\"'\x00") {
		return "核心配置路径必须是规范的绝对路径,且不带空白和引号"
	}
	if !coreServicePattern.MatchString(d.ServiceName) {
		return "核心服务名只能用字母、数字、点、下划线、@ 和减号"
	}
	if d.APIPort == other.APIPort {
		return "两个核心的 API 端口不能相同"
	}
	if d.ConfigPath == other.ConfigPath {
		return "两个核心的配置路径不能相同"
	}
	if strings.TrimSuffix(d.ServiceName, ".service") == strings.TrimSuffix(other.ServiceName, ".service") {
		return "两个核心的服务名不能相同"
	}
	for _, in := range inbounds {
		if in.Port == d.APIPort {
			return "核心 API 端口不能与入站监听端口相同"
		}
	}
	return ""
}

func (s *Server) coreResource(w http.ResponseWriter, r *http.Request) (*model.CoreDeployment, bool) {
	id, ok := s.pathID(w, r)
	if !ok {
		return nil, false
	}
	core := model.Core(r.PathValue("core"))
	if !core.Valid() {
		writeError(w, http.StatusBadRequest, "核心只能是 xray / sing-box")
		return nil, false
	}
	d, err := s.store.GetCoreDeployment(id, core)
	if err != nil {
		s.writeStoreError(w, err, "节点核心")
		return nil, false
	}
	return d, true
}

func (s *Server) handleGetCoreDeployment(w http.ResponseWriter, r *http.Request) {
	d, ok := s.coreResource(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleSetCoreDeployment(w http.ResponseWriter, r *http.Request) {
	old, ok := s.coreResource(w, r)
	if !ok {
		return
	}
	var d model.CoreDeployment
	if !decodeBody(w, r, &d) {
		return
	}
	d.NodeID, d.Core = old.NodeID, old.Core
	// 下发基准只能由成功的部署写入,客户端 PUT 不得伪造。
	d.DeployedHash, d.DeployedAt = old.DeployedHash, old.DeployedAt
	if d.Core == model.CoreXray {
		n := model.Node{APIPort: d.APIPort, ConfigPath: d.ConfigPath, ServiceName: d.ServiceName}
		d.APIPort, d.ConfigPath, d.ServiceName = n.APIPortOrDefault(), n.ConfigPathOrDefault(), n.ServiceNameOrDefault()
	} else {
		d.APIPort, d.ConfigPath, d.ServiceName = d.APIPortOrDefault(), d.ConfigPathOrDefault(), d.ServiceNameOrDefault()
	}
	otherCore := model.CoreXray
	if d.Core == model.CoreXray {
		otherCore = model.CoreSingBox
	}
	other, err := s.store.GetCoreDeployment(d.NodeID, otherCore)
	if err != nil {
		s.writeStoreError(w, err, "节点核心")
		return
	}
	inbounds, err := s.store.ListInbounds(d.NodeID)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if msg := validateCoreDeployment(&d, *other, inbounds); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if d.Core == model.CoreSingBox && old.DeployedHash != "" && (d.APIPort != old.APIPort || d.ConfigPath != old.ConfigPath || d.ServiceName != old.ServiceName) {
		writeError(w, http.StatusConflict, "sing-box 已部署,不能修改 API 端口、配置路径或服务名;请在首次安装前配置")
		return
	}
	if err := s.store.SetCoreDeployment(d); err != nil {
		s.writeStoreError(w, err, "节点核心")
		return
	}
	writeJSON(w, http.StatusOK, d)
}
