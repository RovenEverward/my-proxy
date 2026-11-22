package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// populateInboundCert 在响应里附带入站证书的展示信息。材料本身不进响应
// (json:"-"),所以只解析出 subject / issuer / 到期。
func populateInboundCert(in *model.Inbound) {
	in.Cert = nil
	if in.CertPEM != "" {
		if info, err := model.ParseInboundCert([]byte(in.CertPEM), time.Now()); err == nil {
			in.Cert = info
		}
	}
}

// ---- 入站 ----

func (s *Server) handleListInbounds(w http.ResponseWriter, r *http.Request) {
	var nodeID int64
	if v := r.URL.Query().Get("node_id"); v != "" {
		id, ok := parseInt(w, v)
		if !ok {
			return
		}
		nodeID = id
	}
	inbounds, err := s.store.ListInbounds(nodeID)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	// 显示用的名字算不出来时不影响列表本身:这两个字段是附加的,缺了就不带。
	names, err := s.subscriptionNames()
	if err != nil {
		slog.Warn("算订阅里的名字失败", "error", err)
	}
	views := make([]model.InboundView, 0, len(inbounds))
	for _, in := range inbounds {
		n := names[subNameKey{in.ID, 0}]
		populateInboundCert(&in)
		views = append(views, model.InboundView{Inbound: in, SubName: n.name, AutoName: n.auto})
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateInbound(w http.ResponseWriter, r *http.Request) {
	var in model.Inbound
	if !decodeBody(w, r, &in) {
		return
	}
	if err := ensureReality(&in, nil); err != nil {
		s.writeStoreError(w, err, "REALITY 密钥")
		return
	}
	if err := ensureWireguard(&in, nil); err != nil {
		s.writeStoreError(w, err, "wireguard 密钥")
		return
	}
	if msg := validateInbound(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// 默认 tag 落库,而不是每次用它的时候现算:节点上的增删用户、日志、渲染都按
	// tag 认入站,让它们各自去推同一个默认值迟早会推出分歧。
	in.Tag = in.TagOrDefault()
	if _, err := s.store.GetNode(in.NodeID); err != nil {
		s.writeStoreError(w, err, "节点")
		return
	}
	if err := s.store.CreateInbound(&in); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	s.markNodePending(in.NodeID)
	s.invalidateSubCache()
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) handleGetInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	in, err := s.store.GetInbound(id)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	populateInboundCert(in)
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleUpdateInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var in model.Inbound
	if !decodeBody(w, r, &in) {
		return
	}
	in.ID = id
	old, err := s.store.GetInbound(id)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	in.NodeID = old.NodeID
	// 证书材料走专用的 cert 端点,不进请求体:PUT 拿不到这两个值,沿用库里的,
	// 不然每次改入站都会把已存的证书抹掉(待遇同 wg_secret / reality_priv)。
	in.CertPEM, in.KeyPEM = old.CertPEM, old.KeyPEM
	// 没给 tag 就沿用原来的:换了 tag 等于在节点上换了一个入站,老的那个不会被删。
	// 归位放在校验之前:QUIC 的默认证书路径按最终 tag 分配(见 validateInbound),
	// 校验时 tag 必须是落库的那一份。
	if in.Tag == "" {
		in.Tag = old.TagOrDefault()
	}
	if err := ensureReality(&in, old); err != nil {
		s.writeStoreError(w, err, "REALITY 密钥")
		return
	}
	if err := ensureWireguard(&in, old); err != nil {
		s.writeStoreError(w, err, "wireguard 密钥")
		return
	}
	if msg := validateInbound(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateInbound(&in); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if inboundNeedsApply(*old, in) {
		s.markNodePending(in.NodeID)
	}
	s.invalidateSubCache()
	populateInboundCert(&in)
	writeJSON(w, http.StatusOK, in)
}

// handleUpdateInboundCert 单独写入/清除一条入站的托管证书材料。
//
// 不进主 PUT 的原因:整体替换语义下,一个拿不到 secret 的写方每次保存都会把
// 证书抹掉(同 reality_priv / wg_secret 的处理)。
//
// 请求体 {"cert_pem": "...", "key_pem": "..."};两者都为空表示清除托管,
// 回退控制面自签。只给一个或配对失败都报 400。
func (s *Server) handleUpdateInboundCert(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	in, err := s.store.GetInbound(id)
	if err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if in.Security != model.SecurityTLS {
		writeError(w, http.StatusBadRequest, "只有 TLS 入站能挂证书材料")
		return
	}
	switch {
	case body.CertPEM == "" && body.KeyPEM == "":
		// 清除托管:节点上现有文件留着,续命由控制面自签语义接手(下发
		// 不会主动去删已有文件)。
		if err := s.store.UpdateInboundCert(id, "", ""); err != nil {
			s.writeStoreError(w, err, "入站证书")
			return
		}
		in.CertPEM, in.KeyPEM = "", ""
	case body.CertPEM == "" || body.KeyPEM == "":
		writeError(w, http.StatusBadRequest, "证书与私钥必须一起给")
		return
	default:
		if err := model.ValidateCertPair([]byte(body.CertPEM), []byte(body.KeyPEM)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.UpdateInboundCert(id, body.CertPEM, body.KeyPEM); err != nil {
			s.writeStoreError(w, err, "入站证书")
			return
		}
		in.CertPEM, in.KeyPEM = body.CertPEM, body.KeyPEM
	}
	// 证书变化不进节点 config.json,但服务要重启才能拿到它:标待应用,
	// 让界面/CLI 提示跑 node sync。
	s.markNodePending(in.NodeID)
	s.invalidateSubCache()
	populateInboundCert(in)
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleDeleteInbound(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	gone, getErr := s.store.GetInbound(id)
	if err := s.store.DeleteInbound(id); err != nil {
		s.writeStoreError(w, err, "入站")
		return
	}
	if getErr == nil {
		s.markNodePending(gone.NodeID)
	}
	s.invalidateSubCache()
	writeJSON(w, http.StatusOK, nil)
}
