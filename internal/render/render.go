package render

import "github.com/RovenEverward/my-proxy/internal/model"

// DefaultTemplate 返回某一格式的内置默认模板文本。links / base64-links /
// clash(非 meta)没有模板,返回空串。
//
// 导出给控制面:订阅设置里「取一份当前生效的模板」在没存自定义模板时要能拿到
// 内置默认的副本,而默认模板是 go:embed 编进本包二进制的。
func DefaultTemplate(format model.ClientFormat) string {
	switch format {
	case model.FormatClashMeta:
		return string(clashDefaultTemplate)
	case model.FormatSingBox:
		return string(singboxDefaultTemplate)
	case model.FormatXray:
		return string(xrayDefaultTemplate)
	default:
		return ""
	}
}
