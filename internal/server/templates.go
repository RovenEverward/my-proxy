package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"gopkg.in/yaml.v3"
)

// templateFormats 是能存自定义模板的三种格式。links / base64-links 没有模板
// (纯文本拼行),clash(非 meta)刻意保持最小输出,都不接受模板。
var templateFormats = map[model.ClientFormat]bool{
	model.FormatClashMeta: true,
	model.FormatSingBox:   true,
	model.FormatXray:      true,
}

// handleGetTemplate 返回某一格式当前生效的模板正文:存了就返回存的,没存就返回
// 内置默认模板的副本。返回的是纯文本,不是 JSON 包装。
func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	format := model.ClientFormat(r.PathValue("format"))
	if !templateFormats[format] {
		writeError(w, http.StatusBadRequest, "不支持自定义模板的格式: "+string(format))
		return
	}

	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	body := settings.TemplateFor(format)
	if body == "" {
		body = render.DefaultTemplate(format)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

// handlePutTemplate 存一份自定义模板。正文就是模板本身(非 JSON 包装)。
// 写入前先做语法校验:坏模板在这里挡掉,免得渲染订阅时才炸。
func (s *Server) handlePutTemplate(w http.ResponseWriter, r *http.Request) {
	format := model.ClientFormat(r.PathValue("format"))
	if !templateFormats[format] {
		writeError(w, http.StatusBadRequest, "不支持自定义模板的格式: "+string(format))
		return
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读模板正文失败")
		return
	}
	if err := validateTemplate(format, data); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	if settings.Templates == nil {
		settings.Templates = map[string]string{}
	}
	settings.Templates[string(format)] = string(data)
	if err := s.store.SaveSettings(settings); err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	s.invalidateSubCache()
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteTemplate 删除某一格式的自定义模板,此后回落到内置默认。
func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	format := model.ClientFormat(r.PathValue("format"))
	if !templateFormats[format] {
		writeError(w, http.StatusBadRequest, "不支持自定义模板的格式: "+string(format))
		return
	}

	settings, err := s.store.GetSettings()
	if err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	delete(settings.Templates, string(format))
	if err := s.store.SaveSettings(settings); err != nil {
		s.writeStoreError(w, err, "订阅设置")
		return
	}
	s.invalidateSubCache()
	w.WriteHeader(http.StatusNoContent)
}

// validateTemplate 校验模板能通过渲染器这关:语法可解析、结构完整。
// Clash 模板还要有 rule-providers 段(proxies 的插入点)。
func validateTemplate(format model.ClientFormat, data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return fmt.Errorf("模板不能是空的")
	}
	switch format {
	case model.FormatClashMeta:
		var root yaml.Node
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("Clash 模板不是合法的 YAML: %w", err)
		}
		if root.Content == nil || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
			return fmt.Errorf("Clash 模板顶层必须是映射")
		}
		doc := root.Content[0]
		for i := 0; i+1 < len(doc.Content); i += 2 {
			if doc.Content[i].Value == "rule-providers" {
				return nil
			}
		}
		return fmt.Errorf("Clash 模板缺 rule-providers 段")
	case model.FormatSingBox, model.FormatXray:
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("%s 模板不是合法的 JSON: %w", format, err)
		}
		return nil
	default:
		return fmt.Errorf("不支持自定义模板的格式: %s", format)
	}
}
