package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/ports"
)

// portProber 是 NodeOps 的可选扩展:在节点上列出监听端口(端口 → 占用者)。
type portProber interface {
	ListeningPorts(ctx context.Context, n model.Node) (tcp, udp map[int]string, err error)
}

// PortView 是 GET /api/nodes/{id}/ports 里的一项。v0.9.5 新增。
type PortView struct {
	Port  int    `json:"port"`
	Owner string `json:"owner"`          // 「nginx(进程 1234)」;ss 看不到进程时是「另一个程序」
	Core  bool   `json:"core,omitempty"` // 占着的是节点上的代理核心(xray / sing-box)自己
}

// coreOwner:占用者是不是代理核心自己(重装、重下发时它本来就占着入站端口)。
func coreOwner(owner string) bool {
	switch ports.Program(owner) {
	case "xray", "sing-box":
		return true
	}
	return false
}

func portViews(m map[int]string) []PortView {
	out := make([]PortView, 0, len(m))
	for p, o := range m {
		out = append(out, PortView{Port: p, Owner: o, Core: coreOwner(o)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// handleNodePorts:GET /api/nodes/{id}/ports,现场看节点上哪些端口在监听、被谁占着。
// 添加节点向导建入站前用它挑一个空闲端口。v0.9.5 新增,只读。
func (s *Server) handleNodePorts(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	n, err := s.store.GetNode(id)
	if err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	p, ok := s.nodeOps.(portProber)
	if !ok {
		writeError(w, http.StatusNotImplemented, "控制面没有接上节点执行器")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tcp, udp, err := p.ListeningPorts(ctx, *n)
	known := true
	if errors.Is(err, ports.ErrNoSS) {
		known, err = false, nil
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"node": n.Name, "known": known, "tcp": portViews(tcp), "udp": portViews(udp)})
}

// inboundPortConflicts 找出这台节点的入站里,端口被核心以外的程序占着的那些。
// 看不出来(没有 ss、SSH 失败)时返回空,不拦安装 —— 真起不来时安装本身会报错。
func (s *Server) inboundPortConflicts(ctx context.Context, n model.Node) []string {
	p, ok := s.nodeOps.(portProber)
	if !ok {
		return nil
	}
	inbs, err := s.store.ListInbounds(n.ID)
	if err != nil || len(inbs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tcp, udp, err := p.ListeningPorts(ctx, n)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, in := range inbs {
		proto, m := "TCP", tcp
		if in.Protocol.QUIC() || in.Protocol == "wireguard" {
			proto, m = "UDP", udp
		}
		key := fmt.Sprintf("%s/%d", proto, in.Port)
		if o, busy := m[in.Port]; busy && !coreOwner(o) && !seen[key] {
			seen[key] = true
			out = append(out, fmt.Sprintf("%d 端口(%s)已被 %s 占用", in.Port, proto, o))
		}
	}
	return out
}

// portConflictMessage 把冲突拼成一句中文说明。
func portConflictMessage(n model.Node, conflicts []string) (msg, hint string) {
	head := "节点 " + n.Name + " 上 " + strings.Join(conflicts, ";") + ",代理核心起不来。还没有改动这台节点。"
	msg = head + "换一个端口(mpx inbound edit <入站> --port <端口>),或先停掉占用它的程序,再重试。"
	hint = head + "在「入站与入口」里给这个入站换一个端口,或先停掉占用它的程序,再重试。"
	return msg, hint
}
