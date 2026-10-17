package server

import (
	"net/http"
	"reflect"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 入站改动要不要「应用到节点」:用户数上限只在控制面分配时用,客户端指纹只进订阅,
// 改它们不碰节点上的核心配置;其余字段(端口、协议、传输、安全层、证书、路径……)
// 都会改变节点上的 config,要应用才生效。
func inboundNeedsApply(old, cur model.Inbound) bool {
	old.MaxUsers, cur.MaxUsers = 0, 0
	old.Fingerprint, cur.Fingerprint = "", ""
	return !reflect.DeepEqual(old, cur)
}

// markNodePending / clearNodePending 失败只记日志:标记是给界面的提示,不该让
// 已经成功的入站写入或下发回错。
func (s *Server) markNodePending(nodeID int64) {
	if err := s.store.MarkNodePending(nodeID, time.Now()); err != nil {
		s.logger.Warn("记录未应用改动失败", "node_id", nodeID, "error", err)
	}
}

func (s *Server) clearNodePending(nodeID int64) {
	if err := s.store.ClearNodePending(nodeID); err != nil {
		s.logger.Warn("清除未应用标记失败", "node_id", nodeID, "error", err)
	}
}

// handleListNodePending: GET /api/nodes/pending,列出有未应用入站改动的节点。
func (s *Server) handleListNodePending(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListNodePending()
	if err != nil {
		s.writeStoreError(w, err, "未应用改动")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
