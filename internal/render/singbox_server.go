package render

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// SingBoxServerConfig 生成 QUIC 第二核心的配置。计量依赖官方 v1.14.3 源码
// with_v2ray_api 构建:用户 name 与 stats.users 同为控制面用户名。
func SingBoxServerConfig(n model.Node, d model.CoreDeployment, inbounds []model.Inbound, users []model.User, now time.Time) ([]byte, error) {
	if d.Core != "" && d.Core != model.CoreSingBox {
		return nil, fmt.Errorf("sing-box: 部署核心 %q 不匹配", d.Core)
	}
	if d.NodeID != 0 && d.NodeID != n.ID {
		return nil, fmt.Errorf("sing-box: 部署节点 %d 与节点 %d 不匹配", d.NodeID, n.ID)
	}
	apiPort := d.APIPortOrDefault()
	if apiPort < 1 || apiPort > 65535 {
		return nil, fmt.Errorf("sing-box: API 端口 %d 超出范围", apiPort)
	}
	rendered := make([]any, 0, len(inbounds))
	names := make(map[string]struct{})
	tags := make([]string, 0, len(inbounds))
	seenTags := make(map[string]struct{})
	for _, in := range inbounds {
		if !in.Protocol.QUIC() {
			continue
		}
		if in.NodeID != 0 && in.NodeID != n.ID {
			continue
		}
		item, err := singboxServerInbound(in, users, now)
		if err != nil {
			return nil, err
		}
		tag := item["tag"].(string)
		if _, exists := seenTags[tag]; exists {
			return nil, fmt.Errorf("sing-box: 入站 tag %q 重复", tag)
		}
		seenTags[tag] = struct{}{}
		tags = append(tags, tag)
		for _, u := range serverInboundUsers(users, in.ID, now) {
			names[u.Username] = struct{}{}
		}
		rendered = append(rendered, item)
	}
	statsUsers := make([]string, 0, len(names))
	for name := range names {
		statsUsers = append(statsUsers, name)
	}
	sort.Strings(statsUsers)
	cfg := map[string]any{
		"log":       map[string]any{"level": "warn"},
		"inbounds":  rendered,
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route":     map[string]any{"final": "direct", "rules": []any{map[string]any{"ip_is_private": true, "action": "reject"}}},
		"experimental": map[string]any{"v2ray_api": map[string]any{
			"listen": net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort)),
			"stats":  map[string]any{"enabled": true, "inbounds": tags, "users": statsUsers},
		}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func singboxServerInbound(in model.Inbound, users []model.User, now time.Time) (map[string]any, error) {
	tag := in.Tag
	if tag == "" {
		tag = fmt.Sprintf("%s-%d", in.Protocol, in.Port)
	}
	if in.Network != "" && in.Network != model.NetworkUDP {
		return nil, fmt.Errorf("sing-box: QUIC 入站 %q 必须使用 UDP", tag)
	}
	// 服务端要求明确声明 TLS,不把缺失的证书配置降级成不安全监听。
	if in.Security != model.SecurityTLS || in.CertFile == "" || in.KeyFile == "" {
		return nil, fmt.Errorf("sing-box: QUIC 入站 %q 必须显式配置 TLS 及证书/私钥路径", tag)
	}
	if in.Port < 1 || in.Port > 65535 {
		return nil, fmt.Errorf("sing-box: 入站 %q 端口 %d 超出范围", tag, in.Port)
	}
	tls := map[string]any{"enabled": true, "certificate_path": in.CertFile, "key_path": in.KeyFile}
	if in.SNI != "" {
		tls["server_name"] = in.SNI
	}
	if len(in.ALPN) > 0 {
		tls["alpn"] = in.ALPN
	}
	accounts := make([]any, 0)
	for _, u := range serverInboundUsers(users, in.ID, now) {
		account := map[string]any{"name": u.Username, "password": model.DerivePassword(u.Key)}
		if in.Protocol == model.ProtocolTUIC {
			account["uuid"] = model.DeriveUUID(u.Key)
		}
		accounts = append(accounts, account)
	}
	item := map[string]any{"type": string(in.Protocol), "tag": tag, "listen": "::", "listen_port": in.Port, "users": accounts, "tls": tls}
	if in.Protocol == model.ProtocolTUIC {
		cc, err := quicCongestion(in.CongestionControl)
		if err != nil {
			return nil, err
		}
		item["congestion_control"] = cc
	}
	return item, nil
}
