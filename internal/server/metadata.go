package server

import (
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/model"
	"net/http"
	"strings"
	"sync"
)

func (s *Server) metadataRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/nodes/{id}/metadata", s.handleGetMetadata)
	api.HandleFunc("PUT /api/nodes/{id}/metadata", s.handlePutMetadata)
	api.HandleFunc("POST /api/nodes/{id}/locate", s.handleLocateNode)
	api.HandleFunc("GET /api/hosts/{id}/metadata", s.handleGetMetadata)
	api.HandleFunc("PUT /api/hosts/{id}/metadata", s.handlePutMetadata)
}
func metadataKind(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/nodes/") {
		return "nodes"
	}
	return "hosts"
}
func (s *Server) handleGetMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetLocationMetadata(metadataKind(r), id)
	if err != nil {
		s.writeStoreError(w, err, "元数据")
		return
	}
	writeJSON(w, 200, m)
}
func (s *Server) handlePutMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	// city / letter 用指针判断「有没有带」:只认识 region 的老客户端整份 PUT 时,
	// 不能把已分配的城市和字母冲掉。老客户端带来的 line / purposes 字段会被忽略。
	var in struct {
		model.LocationMetadata
		City   *string `json:"city"`
		Letter *string `json:"letter"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	m := in.LocationMetadata
	if in.City != nil {
		m.City = strings.TrimSpace(*in.City)
	}
	if in.Letter != nil {
		m.Letter = strings.ToUpper(strings.TrimSpace(*in.Letter))
	}
	if err := m.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	kind := metadataKind(r)
	metadataMu.Lock()
	defer metadataMu.Unlock()
	if kind == "hosts" {
		if m.City != "" || m.Letter != "" {
			writeError(w, 400, "city / letter 只用于节点")
			return
		}
	} else {
		code, msg := s.fillNodeLetter(id, &m, in.City != nil, in.Letter != nil)
		if code != 0 {
			writeError(w, code, msg)
			return
		}
	}
	if err := s.store.SaveLocationMetadata(kind, id, m); err != nil {
		s.writeStoreError(w, err, "元数据")
		return
	}
	s.invalidateSubCache()
	writeJSON(w, 200, m)
}

// metadataMu 让「查已用字母 → 保存」成为一步,两个并发保存不会拿到同一个字母。
var metadataMu sync.Mutex

// fillNodeLetter 补齐节点元数据里省略的 city / letter,并按 (地区, 城市) 分配字母
// (docs/internal/node-naming-rfc.md):显式给的字母撞了就拒绝;沿用旧字母但换了分组
// 撞了就重新分配;没有字母就取该分组最小的空闲字母。删掉节点不让别人重排。
func (s *Server) fillNodeLetter(id int64, m *model.LocationMetadata, citySet, letterSet bool) (int, string) {
	all, err := s.store.ListLocationMetadata("nodes")
	if err != nil {
		return 500, err.Error()
	}
	old := all[id]
	if !citySet {
		m.City = old.City
	}
	if !letterSet {
		m.Letter = old.Letter
	}
	if strings.TrimSpace(m.Region) == "" {
		m.Letter = ""
		return 0, ""
	}
	used := map[string]int64{}
	usedSet := map[string]bool{}
	for other, om := range all {
		if other != id && om.Letter != "" && strings.TrimSpace(om.Region) != "" && model.LetterGroup(om) == model.LetterGroup(*m) {
			used[om.Letter] = other
			usedSet[om.Letter] = true
		}
	}
	if m.Letter != "" {
		if other, ok := used[m.Letter]; ok {
			if letterSet {
				return 409, fmt.Sprintf("字母 %s 已被节点 %d 占用", m.Letter, other)
			}
			m.Letter = ""
		}
	}
	if m.Letter == "" {
		if m.Letter = model.NextLetter(usedSet); m.Letter == "" {
			return 409, "该地区/城市的字母已用完"
		}
	}
	return 0, ""
}
