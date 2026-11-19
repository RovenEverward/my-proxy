package server

import (
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
)

// 外部端点的订阅名只走这一层:清洗层(external_metadata)有地区就拼
// 「源名 · 旗帜 + 地区/城市 + 字母」;没清洗出来就回退「源名 · 上游原名」。
// 原始名字永远在快照里,清洗层只是展示身份(UC-17)。

// externalMetaKey 是查清洗层 map 的键,与 store 的拼接格式一致:
// source_id 十进制 + NUL + source_key。
func externalMetaKey(sourceID int64, sourceKey string) string {
	return fmt.Sprintf("%d\x00%s", sourceID, sourceKey)
}

// resolveExternalRemark 算一个外部端点的订阅显示名。
// meta 是 ListExternalMetadata(0) 的全量视图;没有记录时回退原名形态。
func resolveExternalRemark(sourceName string, meta map[string]model.ExternalMetadata, ep render.Endpoint) string {
	m, ok := meta[externalMetaKey(ep.SourceID, ep.SourceKey)]
	return model.ExternalRemark(sourceName, ep.Remark, m, ok)
}
